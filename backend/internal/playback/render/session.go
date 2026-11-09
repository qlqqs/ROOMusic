package render

import (
	"context"
	"encoding/binary"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qlqq/roomusic/backend/internal/dsp/chain"
	"github.com/qlqq/roomusic/backend/internal/playback/render/ffmpeg"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

const streamUnplayedBytes = 64 * 1024

type encodedCheckpoint struct {
	frame, bytes int64
}

type session struct {
	owner                              *Renderer
	ctx                                context.Context
	cancel                             context.CancelFunc
	spec                               zones.RenderSpec
	format                             zones.StreamFormat
	sourcePCM, outputPCM               chain.Format
	chain                              *chain.Chain
	pendingPlan                        *renderPlan
	player                             *player
	encoder                            *encoder
	hls                                *hlsMedia
	reader                             *io.PipeReader
	writer                             *io.PipeWriter
	ready                              chan struct{}
	readyOnce                          sync.Once
	closeOnce                          sync.Once
	closed                             atomic.Bool
	changeMu                           sync.Mutex
	changed, changedTransition         bool
	changeState                        atomic.Pointer[nextChangeState]
	controlWake, eventWake, eventSpace chan struct{}
	events, immediateEvents            chan zones.Event
	done                               chan struct{}
	mu                                 sync.Mutex
	sample                             zones.PlayheadSample
	haveSample                         bool
	renderComplete                     bool
	written, dispatched, nextTime      int64
	inputSubmitted                     int64
	pending                            []timedEvent
	outputEvents                       int
	segments                           []segment
	failure                            error
	encodedCheckpoints                 []encodedCheckpoint
	encodedTotal, encodedPlayed        int64
	encodedHead                        int64
}

func newSession(parent context.Context, owner *Renderer, spec zones.RenderSpec, format zones.StreamFormat, plan renderPlan, track Track, prepareErr error, reservation *ffmpeg.Reservation) (*session, error) {
	var c *chain.Chain
	if prepareErr == nil && len(plan.sampleRate.Stages) == 0 {
		var err error
		c, err = plan.buildChain(spec.Settings, format.BitDepth)
		if err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithCancel(parent)
	reader, writer := io.Pipe()
	s := &session{owner: owner, ctx: ctx, cancel: cancel, spec: spec, format: format, sourcePCM: plan.sourcePCM, outputPCM: plan.outputPCM, chain: c, reader: reader, writer: writer, ready: make(chan struct{}),
		controlWake: make(chan struct{}, 1), eventWake: make(chan struct{}, 1), eventSpace: make(chan struct{}, 1), events: make(chan zones.Event, 64), immediateEvents: make(chan zones.Event, 64), done: make(chan struct{})}
	if format.Delivery == "hls" {
		s.hls = newHLSMedia(ctx, func() float64 {
			if s.outputPCM.SampleRate == 0 {
				return 0
			}
			return float64(s.playhead()) / float64(s.outputPCM.SampleRate)
		}, owner.options.NewTicker)
		_ = writer.Close()
	}
	p := &player{session: s, anchor: track, anchorKnown: prepareErr == nil, last: spec.Start, firstErr: prepareErr}
	s.player = p
	if prepareErr != nil {
		// 没有源格式就没有音频流；查找接续条目不能阻塞 Body 或 Open。
		_ = writer.Close()
		s.markReady()
		return s, nil
	}
	if c == nil {
		s.pendingPlan = &plan
	}
	enc, err := newEncoder(ctx, reservation, owner.options.FFmpeg, format, plan.outputPCM, writer, s.hls, s.fail)
	if err != nil {
		cancel()
		_ = reader.Close()
		_ = writer.Close()
		return nil, err
	}
	s.encoder = enc
	d, err := openDecoder(ctx, reservation, owner.options.FFmpeg, track, spec.Start, plan.sourcePCM, spec.StartSeconds, spec.Settings.Leveling)
	if err != nil {
		_ = track.File.Close()
		p.firstErr = err
	} else {
		p.current = d
	}
	return s, nil
}

func (s *session) start() {
	if s.encoder == nil {
		go func() {
			defer close(s.done)
			if s.hls != nil {
				defer s.hls.close()
			}
			s.runFailure()
		}()
		return
	}
	stop := context.AfterFunc(s.ctx, func() {
		if s.hls != nil {
			s.hls.close()
		}
		s.mu.Lock()
		err := s.failure
		s.mu.Unlock()
		_ = s.writer.CloseWithError(err)
		s.markReady()
	})
	var workers sync.WaitGroup
	workers.Add(2)
	go func() { defer workers.Done(); s.dispatch() }()
	go func() { defer workers.Done(); s.run() }()
	go func() {
		workers.Wait()
		stop()
		if s.hls != nil {
			s.hls.close()
		}
		close(s.done)
	}()
}

func (s *session) MimeType() string           { return s.format.MimeType }
func (s *session) Format() zones.StreamFormat { return s.format }
func (s *session) Body() io.Reader            { return sessionBody{s} }
func (s *session) HLS() zones.HLSMedia {
	if s.hls == nil {
		return nil
	}
	return s.hls
}
func (s *session) Events() <-chan zones.Event { return s.events }
func (s *session) NextChanged() {
	// 通知锁只串行快照发布与通知交接，不持会话锁，也不调用 Cursor 或音频处理。
	s.changeMu.Lock()
	if state := s.changeState.Load(); state != nil && state.unheard(s.outputPCM.SampleRate, s.owner.options.Clock()) {
		s.changedTransition = true
	}
	// 污染状态必须先锁存，再发布可消费的通知。
	s.changed = true
	s.changeMu.Unlock()
	wake(s.controlWake)
}

func (s *session) takeNextChange() (changed, transition bool) {
	s.changeMu.Lock()
	defer s.changeMu.Unlock()
	changed, transition = s.changed, s.changedTransition
	s.changed, s.changedTransition = false, false
	return
}

func (s *session) markReady() { s.readyOnce.Do(func() { close(s.ready) }) }

// Close 不等待任何会话协程，尤其不能等待可能正在获取 zones 锁的 Cursor。
func (s *session) Close() {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		if s.hls != nil {
			s.hls.close()
		}
		s.cancel()
		_ = s.writer.Close()
		_ = s.reader.Close()
		s.markReady()
	})
}

func (s *session) fail(err error) {
	s.mu.Lock()
	if s.failure == nil {
		s.failure = err
	}
	s.pending = nil
	s.outputEvents = 0
	s.mu.Unlock()
	if s.hls != nil {
		s.hls.close()
	}
	_ = s.writer.CloseWithError(err)
	s.markReady()
	s.cancel()
}

type sessionBody struct{ s *session }

func (body sessionBody) Read(data []byte) (int, error) {
	s := body.s
	if len(data) == 0 {
		return 0, nil
	}
	if s.hls != nil {
		return 0, io.EOF
	}
	select {
	case <-s.ready:
	case <-s.ctx.Done():
	}
	if s.closed.Load() {
		return 0, io.EOF
	}
	if s.ctx.Err() != nil {
		s.mu.Lock()
		err := s.failure
		s.mu.Unlock()
		if err != nil {
			return 0, err
		}
		return 0, io.EOF
	}
	n, err := s.reader.Read(data)
	if s.closed.Load() {
		return 0, io.EOF
	}
	return n, err
}

// runFailure 只派发帧 0 的事件，不进入依赖 PCM 格式的渲染或播放头计算。
// 单一协程顺序写出，保证失败条目先于接续边界；Cursor 只在 Open 之外调用。
func (s *session) runFailure() {
	defer close(s.events)
	send := func(event zones.Event) bool {
		if s.ctx.Err() != nil {
			return false
		}
		event.At = s.owner.options.Clock()
		select {
		case <-s.ctx.Done():
			return false
		case s.events <- event:
			return true
		}
	}
	entry, err := s.spec.Start, s.player.firstErr
	for failures := 0; failures < 5; failures++ {
		if !send(zones.Event{Kind: "track_failed", EntryID: entry.EntryID, Reason: failureReason(err)}) {
			return
		}
		if failures == 4 || s.spec.Cursor == nil {
			break
		}
		var ok bool
		entry, ok = s.spec.Cursor.Next(entry.EntryID)
		if s.ctx.Err() != nil {
			return
		}
		if !ok {
			break
		}
		var track Track
		track, err = s.owner.options.Sources.Prepare(s.ctx, s.spec.UserID, entry.TrackID)
		if err == nil {
			err = validateTrack(track)
		}
		if track.File != nil {
			_ = track.File.Close()
		}
		if s.ctx.Err() != nil {
			return
		}
		if err == nil {
			if s.hls != nil {
				s.hls.finish()
			}
			if send(zones.Event{Kind: "format_boundary", NextEntryID: entry.EntryID, Reason: "format"}) {
				<-s.ctx.Done()
			}
			return
		}
		if failureReason(err) == "transient" {
			s.fail(err)
			return
		}
	}
	if s.hls != nil {
		s.hls.finish()
	}
	if send(zones.Event{Kind: "stream_ended"}) {
		<-s.ctx.Done()
	}
}

func (s *session) run() {
	defer s.player.close()
	defer s.encoder.close()
	if s.pendingPlan != nil {
		if s.ctx.Err() != nil {
			return
		}
		c, err := s.pendingPlan.buildChain(s.spec.Settings, s.format.BitDepth)
		if err != nil {
			s.fail(err)
			return
		}
		s.chain, s.pendingPlan = c, nil
		if s.ctx.Err() != nil {
			return
		}
	}
	ticker := s.owner.options.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var speed speedMonitor
	var encoded []byte
	var clipped int
	for {
		available, err := s.waitToRender(ticker)
		if err != nil {
			if s.ctx.Err() == nil {
				s.fail(err)
			}
			return
		}
		buffer, cost, err := s.player.read(available)
		if err != nil {
			if s.ctx.Err() == nil {
				s.fail(err)
			}
			return
		}
		if buffer.Frames == 0 {
			break
		}
		// 先发布真实输入提交边界，零输出、Process 内等待和编码背压均不能隐藏污染。
		s.mu.Lock()
		s.inputSubmitted += int64(buffer.Frames)
		s.publishChangeStateLocked()
		s.mu.Unlock()
		s.markReady()
		begin := s.owner.options.Clock()
		buffer = s.chain.Process(buffer)
		if buffer.Frames == 0 {
			cost += s.owner.options.Clock().Sub(begin)
			if err := speed.add(s, 0, cost); err != nil {
				return
			}
			continue
		}
		encoded, clipped = s.encodePCM(encoded[:0], buffer)
		cost += s.owner.options.Clock().Sub(begin)
		if err = s.encoder.write(encoded); err != nil {
			if s.ctx.Err() == nil {
				s.fail(err)
			}
			return
		}
		if err := s.recordOutput(buffer.Frames, clipped); err != nil {
			return
		}
		if err = speed.add(s, buffer.Frames, cost); err != nil {
			return
		}
	}
	for {
		if err := s.waitForEventSpace(); err != nil {
			return
		}
		begin := s.owner.options.Clock()
		buffer := s.chain.Drain()
		if buffer == nil {
			break
		}
		encoded, clipped = s.encodePCM(encoded[:0], buffer)
		cost := s.owner.options.Clock().Sub(begin)
		s.markReady()
		if err := s.encoder.write(encoded); err != nil {
			if s.ctx.Err() == nil {
				s.fail(err)
			}
			return
		}
		if err := s.recordOutput(buffer.Frames, clipped); err != nil {
			return
		}
		if err := speed.add(s, buffer.Frames, cost); err != nil {
			return
		}
	}
	s.markReady()
	if err := s.encoder.finish(); err != nil {
		if s.ctx.Err() == nil {
			s.fail(err)
		}
		return
	}
	// 进程读到 EOF 并完成 Wait 后才登记自然流尾；Body EOF 不等于 Close。
	draining := true
	for draining {
		if changed, transition := s.takeNextChange(); changed {
			if err := s.player.nextChanged(true, transition); err != nil {
				if s.ctx.Err() == nil {
					s.fail(err)
				}
				return
			}
		}
		select {
		case <-s.ctx.Done():
			return
		case <-s.encoder.done:
			draining = false
		case <-s.controlWake:
		}
	}
	if s.ctx.Err() != nil {
		return
	}
	if !s.player.boundary {
		s.mu.Lock()
		frame := s.written
		s.mu.Unlock()
		if err := s.queueEvent(frame, zones.Event{Kind: "stream_ended"}); err != nil {
			return
		}
	}
	s.mu.Lock()
	s.renderComplete = true
	// EOF 前最后一批输出可能晚于最后一次 PCM 提交，必须补采样。
	s.sampleEncodedLocked()
	s.pruneEncodedLocked(s.playheadLocked(s.owner.options.Clock()))
	s.mu.Unlock()
	wake(s.eventWake)
	for {
		if changed, transition := s.takeNextChange(); changed {
			if err := s.player.nextChanged(true, transition); err != nil {
				if s.ctx.Err() == nil {
					s.fail(err)
				}
				return
			}
		}
		select {
		case <-s.ctx.Done():
			return
		case <-s.controlWake:
		}
	}
}

// waitToRender 是唯一的领先额度判定；HLS 及未播放编码字节足量的长流不越过 20 秒。
func (s *session) waitToRender(ticker Ticker) (int, error) {
	for {
		if err := s.ctx.Err(); err != nil {
			return 0, err
		}
		if changed, transition := s.takeNextChange(); changed {
			if err := s.player.nextChanged(false, transition); err != nil {
				return 0, err
			}
		}
		s.mu.Lock()
		head := s.playheadLocked(s.owner.options.Clock())
		s.sampleEncodedLocked()
		s.pruneEncodedLocked(head)
		outputRate, sourceRate := int64(s.outputPCM.SampleRate), int64(s.sourcePCM.SampleRate)
		ahead := head + int64(s.chain.Latency())
		available := ahead/outputRate*sourceRate + ahead%outputRate*sourceRate/outputRate + sourceRate*20 - s.inputSubmitted
		if available <= 0 && s.needsStreamBytesLocked() {
			available = int64(s.sourcePCM.SampleRate / 5)
		}
		if s.eventBackpressureLocked() {
			available = 0
		}
		s.mu.Unlock()
		if available > 0 {
			return int(min(int64(s.sourcePCM.SampleRate/5), available)), nil
		}
		select {
		case <-s.ctx.Done():
			return 0, s.ctx.Err()
		case <-ticker.C():
		case <-s.controlWake:
		case <-s.eventSpace:
		}
	}
}

// needsStreamBytesLocked 统一长流的低字节例外，进料与事件背压使用同一阈值。
// 直接取编码累计值，冲刷期间也能看到新增字节；已播放量仍沿用 PCM 检查点近似。
func (s *session) needsStreamBytesLocked() bool {
	return s.encoder != nil && s.format.Delivery != "hls" &&
		s.encoder.encodedBytes.Load()-s.encodedPlayed < streamUnplayedBytes
}

// sampleEncodedLocked 只在 PCM 块提交之后（或 EOF）采样，不在写入中途采样。
// 检查点把此时已取出的编码字节近似归到最新提交块的末帧，不解析编码帧。
// 异步滞后的输出归到后续采样块，因而可能较晚扣除；同帧的新输出合并累计。
func (s *session) sampleEncodedLocked() {
	if s.encoder == nil || s.format.Delivery == "hls" {
		return
	}
	total := s.encoder.encodedBytes.Load()
	if total == s.encodedTotal {
		return
	}
	s.encodedTotal = total
	last := len(s.encodedCheckpoints) - 1
	if last >= 0 && s.encodedCheckpoints[last].frame == s.written {
		s.encodedCheckpoints[last].bytes = total
	} else {
		s.encodedCheckpoints = append(s.encodedCheckpoints, encodedCheckpoint{frame: s.written, bytes: total})
	}
}

// pruneEncodedLocked 只按播放头扣除完整检查点，保留累计基线而不保留历史块。
// 回退回报不能复活已扣除字节；零帧头部在播放真正前进前仍计入未播放量。
func (s *session) pruneEncodedLocked(head int64) {
	s.encodedHead = max(s.encodedHead, head)
	if s.encodedHead == 0 {
		return
	}
	count := 0
	for count < len(s.encodedCheckpoints) && s.encodedCheckpoints[count].frame <= s.encodedHead {
		s.encodedPlayed = s.encodedCheckpoints[count].bytes
		count++
	}
	if count > 0 {
		copy(s.encodedCheckpoints, s.encodedCheckpoints[count:])
		s.encodedCheckpoints = s.encodedCheckpoints[:len(s.encodedCheckpoints)-count]
	}
}

func (s *session) encodePCM(dst []byte, b *chain.Buffer) ([]byte, int) {
	if s.format.BitDepth != 0 {
		return s.chain.Quantize(dst, b)
	}
	needed := b.Frames * s.outputPCM.Channels * 4
	if cap(dst) < needed {
		dst = make([]byte, needed)
	} else {
		dst = dst[:needed]
	}
	clipped := 0
	for frame := range b.Frames {
		for channel := range s.outputPCM.Channels {
			sample := b.Samples[channel][frame]
			if s.spec.Settings.Headroom.ClipIndicator && math.Abs(sample) > 1 {
				clipped++
			}
			binary.LittleEndian.PutUint32(dst[(frame*s.outputPCM.Channels+channel)*4:], math.Float32bits(float32(sample)))
		}
	}
	return dst, clipped
}

var _ zones.RenderSession = (*session)(nil)
