package render

import (
	"math"
	"sort"
	"time"

	"github.com/qlqq/roomusic/backend/internal/zones"
)

type timedEvent struct {
	frame int64
	event zones.Event
}
type segment struct {
	frame int64
	value zones.Segment
}

// nextChangeState 是给 NextChanged 的不可变快照，避免渲染协程处理通知时丢失现场。
type nextChangeState struct {
	sample              zones.PlayheadSample
	haveSample          bool
	written, transition int64
}

func (s *session) publishChangeStateLocked() {
	state := &nextChangeState{sample: s.sample, haveSample: s.haveSample, written: s.written}
	for i := len(s.segments) - 1; i >= 0; i-- {
		if s.segments[i].frame < s.written {
			state.transition = s.segments[i].frame
			break
		}
	}
	s.changeState.Store(state)
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
	return sampleFrame(s.sample, s.haveSample, s.written, s.pcm.SampleRate, now)
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
		if seg.frame > head && seg.frame < s.written {
			return true
		}
	}
	return false
}

func (s *session) queueEvent(frame int64, event zones.Event) error {
	for {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		s.mu.Lock()
		if len(s.pending) < 256 {
			event.StreamSeconds = float64(frame) / float64(s.pcm.SampleRate)
			// 同帧维持登记顺序，混音窗口起点也按准确帧号登记。
			index := sort.Search(len(s.pending), func(i int) bool { return s.pending[i].frame > frame })
			s.pending = append(s.pending, timedEvent{})
			copy(s.pending[index+1:], s.pending[index:])
			s.pending[index] = timedEvent{frame, event}
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
func (s *session) addSegment(frame int64, value zones.Segment, path signalPath) error {
	s.mu.Lock()
	s.segments = append(s.segments, segment{frame, value})
	s.mu.Unlock()
	return s.queueEvent(frame, zones.Event{Kind: "segment_signal", Segments: []zones.Segment{value}, Signal: path})
}

func (s *session) segmentsLocked(frame int64) []zones.Segment {
	index := 0
	for index+1 < len(s.segments) && s.segments[index+1].frame <= frame {
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
	hasEvent := len(s.pending) > 0 && s.pending[0].frame <= head && (s.pending[0].event.Kind != "stream_ended" || s.renderComplete)
	if hasEvent && (!hasTime || s.pending[0].frame < nextFrame || s.pending[0].frame == nextFrame && s.pending[0].event.Kind != "stream_ended") {
		item := s.pending[0]
		s.pending = s.pending[1:]
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
	s.nextTime += int64(s.pcm.SampleRate / 5)
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
	return zones.Event{Kind: "time", EntryID: seg.value.EntryID, Seconds: seg.value.TrackStartSeconds + float64(nextFrame-seg.frame)/float64(s.pcm.SampleRate), StreamSeconds: float64(nextFrame) / float64(s.pcm.SampleRate), At: now}, true
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
		}
	}
	s.pending = kept
	wake(s.eventSpace)
}
