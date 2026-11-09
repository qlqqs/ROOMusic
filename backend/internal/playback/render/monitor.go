package render

import (
	"math"
	"time"

	"github.com/qlqq/roomusic/backend/internal/zones"
)

// speedMonitor 只接收已排除预缓冲、名额、领先窗口与编码背压的处理时间。
type speedMonitor struct {
	audio      float64
	processing time.Duration
	reported   time.Time
	last       float64
}

func (m *speedMonitor) add(s *session, frames int, cost time.Duration) error {
	m.audio += float64(frames) / float64(s.pcm.SampleRate)
	m.processing += max(0, cost)
	now := s.owner.options.Clock()
	if m.reported.IsZero() {
		m.reported = now
		return nil
	}
	if now.Sub(m.reported) < time.Second || m.processing <= 0 {
		return nil
	}
	speed := math.Round(m.audio/m.processing.Seconds()*10) / 10
	if speed == m.last || math.IsNaN(speed) || math.IsInf(speed, 0) {
		return nil
	}
	m.reported = now
	m.last = speed
	return s.immediate(zones.Event{Kind: "processing_speed", Signal: speed})
}

// recordOutput 以量化块的起点登记削波，等待播放头而不是渲染进度。
func (s *session) recordOutput(frames, clipped int) error {
	s.mu.Lock()
	frame := s.written
	s.mu.Unlock()
	if s.spec.Settings.Headroom.ClipIndicator && clipped > 0 {
		if err := s.queueEvent(frame, zones.Event{Kind: "clip"}); err != nil {
			return err
		}
	}
	// 先登记块内事件，再发布播放头可到达的新上限，防止较晚分段抢先派发。
	s.mu.Lock()
	s.written += int64(frames)
	s.mu.Unlock()
	wake(s.eventWake)
	return nil
}

// signalMonitor 由派发协程独占。每次输出完整快照，避免实时字段覆盖路径。
type signalMonitor struct {
	path        signalPath
	havePath    bool
	clipEnabled bool
	clipping    bool
	lastClip    time.Time
	speed       *float64
}

func (m *signalMonitor) snapshot() signalPath {
	path := m.path
	if m.clipEnabled {
		clipping := m.clipping
		path.Clipping = &clipping
	}
	path.ProcessingSpeed = m.speed
	return path
}

func (m *signalMonitor) apply(event zones.Event, now time.Time) (zones.Event, bool) {
	switch event.Kind {
	case "segment_signal":
		m.path, m.havePath = event.Signal.(signalPath), true
		event.Kind = "signal"
	case "processing_speed":
		speed := event.Signal.(float64)
		m.speed = &speed
		if !m.havePath {
			return zones.Event{}, false
		}
		event.Kind = "signal"
	case "clip":
		m.lastClip = now
		if m.clipping || !m.clipEnabled {
			return zones.Event{}, false
		}
		m.clipping = true
		if !m.havePath {
			return zones.Event{}, false
		}
		event.Kind = "signal"
	default:
		return event, true
	}
	if m.havePath {
		event.Signal = m.snapshot()
	}
	return event, true
}

func (m *signalMonitor) reevaluate(now time.Time) (zones.Event, bool) {
	if !m.clipping || now.Sub(m.lastClip) < 450*time.Millisecond {
		return zones.Event{}, false
	}
	m.clipping = false
	if !m.havePath {
		return zones.Event{}, false
	}
	return zones.Event{Kind: "signal", Signal: m.snapshot(), At: now}, true
}
