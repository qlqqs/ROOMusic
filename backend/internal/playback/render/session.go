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

type session struct {
	owner                              *Renderer
	ctx                                context.Context
	cancel                             context.CancelFunc
	spec                               zones.RenderSpec
	format                             zones.StreamFormat
	pcm                                chain.Format
	chain                              *chain.Chain
	player                             *player
	encoder                            *encoder
	hls                                *hlsMedia
	reader                             *io.PipeReader
	writer                             *io.PipeWriter
	ready                              chan struct{}
	readyOnce                          sync.Once
	closeOnce                          sync.Once
	closed                             atomic.Bool
	changed                            atomic.Bool
	controlWake, eventWake, eventSpace chan struct{}
	events, immediateEvents            chan zones.Event
	done                               chan struct{}
	mu                                 sync.Mutex
	sample                             zones.PlayheadSample
	haveSample                         bool
	renderComplete                     bool
	written, dispatched, nextTime      int64
	pending                            []timedEvent
	segments                           []segment
	failure                            error
}

func newSession(parent context.Context, owner *Renderer, spec zones.RenderSpec, format zones.StreamFormat, pcm chain.Format, track Track, prepareErr error, reservation *ffmpeg.Reservation) (*session, error) {
	var c *chain.Chain
	if prepareErr == nil {
		var err error
		c, err = chain.Build(chain.Spec{Format: pcm, OutputBits: format.BitDepth, Seed: 1})
		if err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithCancel(parent)
	reader, writer := io.Pipe()
	s := &session{owner: owner, ctx: ctx, cancel: cancel, spec: spec, format: format, pcm: pcm, chain: c, reader: reader, writer: writer, ready: make(chan struct{}),
		controlWake: make(chan struct{}, 1), eventWake: make(chan struct{}, 1), eventSpace: make(chan struct{}, 1), events: make(chan zones.Event, 64), immediateEvents: make(chan zones.Event, 64), done: make(chan struct{})}
	if format.Delivery == "hls" {
		s.hls = newHLSMedia(ctx, func() float64 {
			if s.pcm.SampleRate == 0 {
				return 0
			}
			return float64(s.playhead()) / float64(s.pcm.SampleRate)
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
	enc, err := newEncoder(ctx, reservation, owner.options.FFmpeg, format, pcm, writer, s.hls, s.fail)
	if err != nil {
		cancel()
		_ = reader.Close()
		_ = writer.Close()
		return nil, err
	}
	s.encoder = enc
	d, err := openDecoder(ctx, reservation, owner.options.FFmpeg, track, spec.Start, pcm, spec.StartSeconds, spec.Settings.Leveling)
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
func (s *session) NextChanged()               { s.changed.Store(true); wake(s.controlWake) }
func (s *session) markReady()                 { s.readyOnce.Do(func() { close(s.ready) }) }

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
	ticker := s.owner.options.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var speed speedMonitor
	var encoded []byte
	for {
		if err := s.waitToRender(ticker); err != nil {
			if s.ctx.Err() == nil {
				s.fail(err)
			}
			return
		}
		s.mu.Lock()
		available := int64(s.pcm.SampleRate)*20 + s.playheadLocked(s.owner.options.Clock()) - s.written
		s.mu.Unlock()
		if available <= 0 {
			continue
		}
		buffer, cost, err := s.player.read(min(s.pcm.SampleRate/5, int(available)))
		if err != nil {
			if s.ctx.Err() == nil {
				s.fail(err)
			}
			return
		}
		if buffer.Frames == 0 {
			break
		}
		s.markReady()
		begin := s.owner.options.Clock()
		buffer = s.chain.Process(buffer)
		encoded = s.encodePCM(encoded[:0], buffer)
		cost += s.owner.options.Clock().Sub(begin)
		if err = s.encoder.write(encoded); err != nil {
			if s.ctx.Err() == nil {
				s.fail(err)
			}
			return
		}
		s.mu.Lock()
		s.written += int64(buffer.Frames)
		s.mu.Unlock()
		wake(s.eventWake)
		if err = speed.add(s, buffer.Frames, cost); err != nil {
			return
		}
	}
	for {
		begin := s.owner.options.Clock()
		buffer := s.chain.Drain()
		if buffer == nil {
			break
		}
		encoded = s.encodePCM(encoded[:0], buffer)
		cost := s.owner.options.Clock().Sub(begin)
		s.markReady()
		if err := s.encoder.write(encoded); err != nil {
			if s.ctx.Err() == nil {
				s.fail(err)
			}
			return
		}
		s.mu.Lock()
		s.written += int64(buffer.Frames)
		s.mu.Unlock()
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
		if s.changed.Swap(false) {
			if err := s.player.nextChanged(true); err != nil {
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
	s.mu.Unlock()
	wake(s.eventWake)
	for {
		if s.changed.Swap(false) {
			if err := s.player.nextChanged(true); err != nil {
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

func (s *session) waitToRender(ticker Ticker) error {
	for {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		if s.changed.Swap(false) {
			if err := s.player.nextChanged(false); err != nil {
				return err
			}
		}
		s.mu.Lock()
		written := s.written
		head := s.playheadLocked(s.owner.options.Clock())
		s.mu.Unlock()
		if written-head < int64(s.pcm.SampleRate)*20 {
			return nil
		}
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case <-ticker.C():
		case <-s.controlWake:
		}
	}
}
func (s *session) encodePCM(dst []byte, b *chain.Buffer) []byte {
	if s.format.BitDepth != 0 {
		result, _ := s.chain.Quantize(dst, b)
		return result
	}
	needed := b.Frames * s.pcm.Channels * 4
	if cap(dst) < needed {
		dst = make([]byte, needed)
	} else {
		dst = dst[:needed]
	}
	for frame := range b.Frames {
		for channel := range s.pcm.Channels {
			binary.LittleEndian.PutUint32(dst[(frame*s.pcm.Channels+channel)*4:], math.Float32bits(float32(b.Samples[channel][frame])))
		}
	}
	return dst
}

var _ zones.RenderSession = (*session)(nil)
