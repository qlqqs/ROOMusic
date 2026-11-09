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
	return s.immediate(zones.Event{Kind: "signal", Signal: map[string]any{"processing_speed": speed}})
}
