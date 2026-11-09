package render

import (
	"math"
	"sort"
	"time"

	"github.com/qlqq/roomusic/backend/internal/zones"
)

type timedEvent struct {
	frame  int64
	event  zones.Event
	source bool
}
type segment struct {
	frame, inputFrame int64
	transition        bool
	value             zones.Segment
}

// nextChangeState 是给 NextChanged 的不可变快照，避免渲染协程处理通知时丢失现场。
type nextChangeState struct {
	sample                              zones.PlayheadSample
	haveSample                          bool
	written, transition, inputSubmitted int64
	hasTransition                       bool
}

func (s *session) publishChangeStateLocked() {
	state := &nextChangeState{sample: s.sample, haveSample: s.haveSample, written: s.written, inputSubmitted: s.inputSubmitted}
	for i := len(s.segments) - 1; i >= 0; i-- {
		if s.segments[i].transition && s.segments[i].inputFrame < s.inputSubmitted {
			state.transition = s.segments[i].frame
			state.hasTransition = true
			break
		}
	}
	// 锁序固定为会话锁 → 通知锁；通知和消费不反向持有会话锁。
	s.changeMu.Lock()
	s.changeState.Store(state)
	// 通知可能先于本次输入提交，必须在新边界发布时补锁存。
	if s.changed && state.unheard(s.outputPCM.SampleRate, s.owner.options.Clock()) {
		s.changedTransition = true
	}
	s.changeMu.Unlock()
}

func (state *nextChangeState) unheard(rate int, now time.Time) bool {
	if !state.hasTransition {
		return false
	}
	head := sampleFrame(state.sample, state.haveSample, state.written, rate, now)
	return state.transition > head || state.transition == head && state.written <= head
}

func wake(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (s *session) Observe(sample zones.PlayheadSample) {
	if sample.StreamSeq != s.spec.Seq || math.IsNaN(sample.StreamSeconds) || math.IsInf(sample.StreamSeconds, 0) || sample.StreamSeconds < 0 {
		return
	}
	if sample.At.IsZero() {
		sample.At = s.owner.options.Clock()
	}
	s.mu.Lock()
	if !s.haveSample || !sample.At.Before(s.sample.At) {
		s.sample = sample
		s.haveSample = true
		s.publishChangeStateLocked()
		s.pruneEncodedLocked(s.playheadLocked(s.owner.options.Clock()))
	}
	s.mu.Unlock()
	if s.hls != nil {
		s.hls.prune()
	}
	wake(s.eventWake)
	wake(s.controlWake)
}

func (s *session) playheadLocked(now time.Time) int64 {
	return sampleFrame(s.sample, s.haveSample, s.written, s.outputPCM.SampleRate, now)
}

func sampleFrame(sample zones.PlayheadSample, haveSample bool, written int64, rate int, now time.Time) int64 {
	if !haveSample {
		return 0
	}
	seconds := sample.StreamSeconds
	if sample.Playing {
		seconds += max(0, now.Sub(sample.At).Seconds())
	}
	// 先钳秒数再转帧，避免异常远端位置的浮点转整数溢出。
	seconds = min(seconds, float64(written)/float64(rate))
	return int64(math.Floor(seconds*float64(rate) + 1e-7))
}
func (s *session) playhead() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.playheadLocked(s.owner.options.Clock())
}
func (s *session) hasUnheardTransition() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	head := s.playheadLocked(s.owner.options.Clock())
	for _, seg := range s.segments {
		if seg.transition && seg.inputFrame < s.inputSubmitted && (seg.frame > head || seg.frame == head && s.written <= head) {
			return true
		}
	}
	return false
}

func (s *session) queueEvent(frame int64, event zones.Event) error {
	return s.queueTimedEvent(timedEvent{frame: frame, event: event})
}

// eventBackpressureLocked 只计算已有输出的来源事件和普通输出域事件。
// 来源事件按帧排序，遇到前瞻区就停止；不遍历可能很长的延迟区间。
func (s *session) eventBackpressureLocked() bool {
	// 低字节长流仍须继续进料或冲刷到 EOF，不能让事件背压挡住客户端起播。
	if s.needsStreamBytesLocked() {
		return false
	}
	count := s.outputEvents
	if count >= 256 {
		return true
	}
	for _, item := range s.pending {
		if !s.eventHasOutputLocked(item.frame) {
			break
		}
		if item.source {
			count++
			if count >= 256 {
				return true
			}
		}
	}
	return false
}

func (s *session) eventHasOutputLocked(frame int64) bool {
	return frame < s.written || s.renderComplete && frame == s.written
}

// waitForEventSpace 在冲刷输出前执行背压；只等既有事件/控制通知，不轮询。
func (s *session) waitForEventSpace() error {
	for {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		if changed, transition := s.takeNextChange(); changed {
			if err := s.player.nextChanged(true, transition); err != nil {
				return err
			}
		}
		s.mu.Lock()
		full := s.eventBackpressureLocked()
		s.mu.Unlock()
		if !full {
			return nil
		}
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case <-s.eventSpace:
		case <-s.controlWake:
		}
	}
}

func (s *session) queueTimedEvent(item timedEvent) error {
	for {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		s.mu.Lock()
		// player.read 内的来源事件属于已预留的源域量子，不能在提交该量子前等输出。
		if item.source && !s.eventHasOutputLocked(item.frame) || !s.eventBackpressureLocked() {
			item.event.StreamSeconds = float64(item.frame) / float64(s.outputPCM.SampleRate)
			// 同帧维持登记顺序，混音窗口起点也按准确帧号登记。
			index := sort.Search(len(s.pending), func(i int) bool { return s.pending[i].frame > item.frame })
			s.pending = append(s.pending, timedEvent{})
			copy(s.pending[index+1:], s.pending[index:])
			s.pending[index] = item
			if !item.source {
				s.outputEvents++
			}
			s.mu.Unlock()
			wake(s.eventWake)
			return nil
		}
		s.mu.Unlock()
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case <-s.eventSpace:
		}
	}
}
func (s *session) immediate(event zones.Event) error {
	event.At = s.owner.options.Clock()
	select {
	case <-s.ctx.Done():
		return s.ctx.Err()
	case s.immediateEvents <- event:
		return nil
	}
}

// queueSourceEvent 是来源域事件进入公共输出时钟的唯一换算入口。
func (s *session) queueSourceEvent(inputFrame int64, event zones.Event) error {
	return s.queueTimedEvent(timedEvent{frame: s.chain.OutputFrame(inputFrame), event: event, source: true})
}

func (s *session) addSourceSegment(inputFrame int64, value zones.Segment, path signalPath, transition bool) error {
	frame := s.chain.OutputFrame(inputFrame)
	value.StreamStartSeconds = float64(frame) / float64(s.outputPCM.SampleRate)
	s.mu.Lock()
	s.segments = append(s.segments, segment{frame: frame, inputFrame: inputFrame, transition: transition, value: value})
	s.publishChangeStateLocked()
	s.mu.Unlock()
	return s.queueTimedEvent(timedEvent{frame: frame, event: zones.Event{Kind: "segment_signal", Segments: []zones.Segment{value}, Signal: path}, source: true})
}

func (s *session) segmentsLocked(frame int64) []zones.Segment {
	index := 0
	// 尚未提交或尚未产生一个可听输出帧的边界不能被映射舍入提前裁掉。
	for index+1 < len(s.segments) && s.segments[index+1].frame <= frame &&
		s.segments[index+1].inputFrame < s.inputSubmitted && s.segments[index+1].frame < s.written {
		index++
	}
	if index > 0 {
		s.segments = append(s.segments[:0], s.segments[index:]...)
	}
	values := make([]zones.Segment, len(s.segments))
	for i, seg := range s.segments {
		values[i] = seg.value
	}
	return values
}

// dueEvent 合并离散事件与 200 ms 网格，同帧先派发换轨，再派发 time。
func (s *session) dueEvent(now time.Time) (zones.Event, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	head := max(s.dispatched, s.playheadLocked(now))
	// 自然结束后仍由既有派发时钟修剪，不依赖渲染循环继续运行。
	s.pruneEncodedLocked(head)
	s.dispatched = head
	nextFrame := s.nextTime
	// 未完成时右端点仍可能是下一首的起点，不能抢在生产者登记换轨之前派发。
	hasTime := len(s.segments) > 0 && s.written > 0 && nextFrame <= head && (nextFrame < s.written || s.renderComplete && nextFrame == s.written)
	hasEvent := len(s.pending) > 0 && s.pending[0].frame <= head &&
		s.eventHasOutputLocked(s.pending[0].frame) &&
		(s.pending[0].event.Kind != "stream_ended" || s.renderComplete)
	if hasEvent && (!hasTime || s.pending[0].frame < nextFrame || s.pending[0].frame == nextFrame && s.pending[0].event.Kind != "stream_ended") {
		item := s.pending[0]
		s.pending = s.pending[1:]
		if !item.source {
			s.outputEvents--
		}
		event := item.event
		event.At = now
		if event.Kind == "segment_signal" || event.Kind == "track_started" {
			event.Segments = s.segmentsLocked(item.frame)
		}
		wake(s.eventSpace)
		return event, true
	}
	if !hasTime {
		return zones.Event{}, false
	}
	s.nextTime += int64(s.outputPCM.SampleRate / 5)
	selected := -1
	for i, seg := range s.segments {
		if seg.frame <= nextFrame {
			selected = i
		} else {
			break
		}
	}
	if selected < 0 {
		return zones.Event{}, false
	}
	seg := s.segments[selected]
	return zones.Event{Kind: "time", EntryID: seg.value.EntryID, Seconds: seg.value.TrackStartSeconds + float64(nextFrame-seg.frame)/float64(s.outputPCM.SampleRate), StreamSeconds: float64(nextFrame) / float64(s.outputPCM.SampleRate), At: now}, true
}

func (s *session) dispatch() {
	defer close(s.events)
	ticker := s.owner.options.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	monitor := signalMonitor{clipEnabled: s.spec.Settings.Headroom.ClipIndicator}
	var clipTicks <-chan time.Time
	if monitor.clipEnabled {
		clipTicker := s.owner.options.NewTicker(250 * time.Millisecond)
		defer clipTicker.Stop()
		clipTicks = clipTicker.C()
	}
	send := func(event zones.Event) bool {
		var ok bool
		event, ok = monitor.apply(event, s.owner.options.Clock())
		if !ok {
			return true
		}
		if s.ctx.Err() != nil {
			return false
		}
		select {
		case <-s.ctx.Done():
			return false
		case s.events <- event:
			return true
		}
	}
	reevaluate := func() bool {
		event, ok := monitor.reevaluate(s.owner.options.Clock())
		return !ok || send(event)
	}
	for {
		// 即使未来事件正在补发，也按独立的 250 ms 时钟复评。
		select {
		case <-clipTicks:
			if !reevaluate() {
				return
			}
		default:
		}
		if s.hls != nil {
			s.hls.prune()
		}
		// 优先处理立即事件，不受未来队列与播放头影响。
		select {
		case event := <-s.immediateEvents:
			if !send(event) {
				return
			}
			continue
		default:
		}
		event, ok := s.dueEvent(s.owner.options.Clock())
		if ok {
			if !send(event) {
				return
			}
			continue
		}
		select {
		case <-s.ctx.Done():
			return
		case event := <-s.immediateEvents:
			if !send(event) {
				return
			}
		case <-s.eventWake:
		case <-clipTicks:
			if !reevaluate() {
				return
			}
		case <-ticker.C():
		}
	}
}

func (s *session) removePendingEnd() {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.pending[:0]
	for _, item := range s.pending {
		if item.event.Kind != "stream_ended" {
			kept = append(kept, item)
		} else if !item.source {
			s.outputEvents--
		}
	}
	s.pending = kept
	wake(s.eventSpace)
}
