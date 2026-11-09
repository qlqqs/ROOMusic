package zonestest

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qlqq/roomusic/backend/internal/zones"
)

type action struct {
	event   *zones.Event
	elapsed time.Duration
	ack     chan struct{}
}

// Session 的 Emit/Advance 可用于确定性脚本；不能从持有播放区锁的代码中调用。
// Observe、NextChanged、Close 只更新短临界区或发信号，不等待 Cursor 或事件消费者。
type Session struct {
	spec        zones.RenderSpec
	options     Options
	format      zones.StreamFormat
	body        *silenceWAV
	events      chan zones.Event
	actions     chan action
	done        chan struct{}
	stopOnce    sync.Once
	wake        chan struct{}
	sampleMu    sync.Mutex
	sample      zones.PlayheadSample
	nextChanges atomic.Int64

	// 以下字段只由 run 协程访问。
	current         zones.Entry
	position        float64
	streamSeconds   float64
	gapRemaining    float64
	next            *zones.Entry
	preparedVersion int64
	ended           bool
}

func newSession(ctx context.Context, spec zones.RenderSpec, options Options) *Session {
	s := &Session{
		spec: spec, options: options,
		format: zones.StreamFormat{Codec: "wav", MimeType: "audio/wav", Quality: "lossless", SampleRate: 48000, BitDepth: 16, Channels: 2},
		events: make(chan zones.Event, 64), actions: make(chan action, 64), done: make(chan struct{}), wake: make(chan struct{}, 1),
		current: spec.Start, position: spec.StartSeconds,
	}
	s.body = newSilenceWAV(s.done, s.format)
	go s.run(ctx)
	return s
}

func (s *Session) Spec() zones.RenderSpec     { return s.spec }
func (s *Session) MimeType() string           { return s.format.MimeType }
func (s *Session) Format() zones.StreamFormat { return s.format }
func (s *Session) Body() io.Reader            { return s.body }
func (s *Session) HLS() zones.HLSMedia        { return nil }
func (s *Session) Events() <-chan zones.Event { return s.events }
func (s *Session) Closed() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}
func (s *Session) Close()             { s.stopOnce.Do(func() { close(s.done) }) }
func (s *Session) NextChanged()       { s.nextChanges.Add(1) }
func (s *Session) NextChanges() int64 { return s.nextChanges.Load() }
func (s *Session) Observe(sample zones.PlayheadSample) {
	s.sampleMu.Lock()
	s.sample = sample
	s.sampleMu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Emit 按脚本发送一个事件；通道满时等待消费者或 Close，不悄悄丢事件。
func (s *Session) Emit(event zones.Event) error {
	return s.send(action{event: &event})
}

// Advance 按曲目时长前进，自动生成 time、后续条目的 track_started 与 stream_ended。
// 零时长只检查边界；起始条目不发 track_started。
func (s *Session) Advance(elapsed time.Duration) error {
	return s.send(action{elapsed: elapsed})
}

func (s *Session) send(a action) error {
	a.ack = make(chan struct{})
	select {
	case <-s.done:
		return ErrClosed
	case s.actions <- a:
	}
	select {
	case <-s.done:
		return ErrClosed
	case <-a.ack:
		return nil
	}
}

func (s *Session) run(ctx context.Context) {
	defer close(s.events)
	defer s.Close()
	stopContext := context.AfterFunc(ctx, s.Close)
	defer stopContext()
	var ticks <-chan time.Time
	if s.options.Realtime {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		ticks = ticker.C
	}
	lastTick := time.Now()
	for {
		select {
		case <-s.done:
			return
		case <-ctx.Done():
			return
		case a := <-s.actions:
			if a.event != nil {
				if !s.publish(*a.event) {
					return
				}
				if a.event.Kind == "stream_ended" {
					s.ended = true
					s.body.end()
				}
			} else if !s.advance(max(0, a.elapsed.Seconds())) {
				return
			}
			close(a.ack)
		case <-s.wake:
			s.sampleMu.Lock()
			sample := s.sample
			s.sampleMu.Unlock()
			if sample.StreamSeconds > s.streamSeconds && !s.advance(sample.StreamSeconds-s.streamSeconds) {
				return
			}
			lastTick = time.Now()
		case now := <-ticks:
			s.sampleMu.Lock()
			playing := s.sample.Playing
			s.sampleMu.Unlock()
			elapsed := now.Sub(lastTick).Seconds()
			lastTick = now
			if playing && !s.advance(max(0, elapsed)) {
				return
			}
		}
	}
}

func (s *Session) publish(event zones.Event) bool {
	select {
	case <-s.done:
		return false
	case s.events <- event:
		return true
	}
}

func (s *Session) duration() float64 {
	duration := s.options.Durations[s.current.TrackID]
	if duration <= 0 {
		duration = s.options.DefaultDuration
	}
	return duration.Seconds()
}

func (s *Session) advance(seconds float64) bool {
	if s.ended {
		return true
	}
	for {
		if s.gapRemaining > 0 {
			step := min(seconds, s.gapRemaining)
			s.streamSeconds += step
			s.gapRemaining -= step
			seconds -= step
			if s.gapRemaining > 0 {
				return true
			}
			if s.preparedVersion != s.nextChanges.Load() {
				next, ok := s.spec.Cursor.Next(s.current.EntryID)
				if !ok {
					return s.endStream()
				}
				s.next = &next
			}
			if !s.startNext("gap") {
				return false
			}
		}
		remaining := max(0, s.duration()-s.position)
		if remaining > 0 && seconds > 0 {
			step := min(seconds, remaining, 0.2)
			s.position += step
			s.streamSeconds += step
			seconds -= step
			if !s.publish(zones.Event{Kind: "time", EntryID: s.current.EntryID, Seconds: s.position, StreamSeconds: s.streamSeconds, At: s.options.Now().UTC()}) {
				return false
			}
			remaining = max(0, s.duration()-s.position)
		}
		if remaining > 0 {
			if seconds <= 0 {
				return true
			}
			continue
		}
		s.preparedVersion = s.nextChanges.Load()
		var next zones.Entry
		var ok bool
		if s.spec.Cursor != nil {
			next, ok = s.spec.Cursor.Next(s.current.EntryID)
		}
		if !ok {
			return s.endStream()
		}
		s.next = &next
		if !s.spec.Settings.Gapless {
			s.gapRemaining = 0.5
		} else if !s.startNext("gapless") {
			return false
		}
		if seconds <= 0 {
			return true
		}
	}
}

func (s *Session) endStream() bool {
	s.ended = true
	s.body.end()
	return s.publish(zones.Event{Kind: "stream_ended", EntryID: s.current.EntryID, StreamSeconds: s.streamSeconds})
}

func (s *Session) startNext(transition string) bool {
	s.current = *s.next
	s.next = nil
	s.position = 0
	return s.publish(zones.Event{
		Kind: "track_started", EntryID: s.current.EntryID, Transition: transition,
		StreamSeconds: s.streamSeconds, At: s.options.Now().UTC(),
		Segments: []zones.Segment{{EntryID: s.current.EntryID, TrackID: s.current.TrackID, StreamStartSeconds: s.streamSeconds}},
	})
}

var _ zones.RenderSession = (*Session)(nil)
