package airplay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/bits"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/airplay/bplist"
	"github.com/qlqq/roomusic/backend/internal/ptp"
)

var errPTPUnavailable = errors.New(PrecisionUnavailable)

func usableClock(s ptp.Status) bool {
	return s.Available && s.ClockID != 0 && s.GrandmasterID != 0
}

func (m *mediaSession) refreshClock(ctx context.Context) error {
	state, err := m.clock.PeerStatus(ctx, m.peer)
	if err != nil {
		return fmt.Errorf("%w: %w", errPTPUnavailable, err)
	}
	if !usableClock(state) {
		if state.Reason != "" {
			return fmt.Errorf("%w: %s", errPTPUnavailable, state.Reason)
		}
		return errPTPUnavailable
	}
	m.transport.SetPTPClock(state.GrandmasterID, state.OffsetNS)
	return nil
}

// 接收端可能在流级 SETUP 后才开始发送 Sync，等到所选对端时钟可用再交付 PCM。
func (m *mediaSession) waitClock(ctx context.Context) error {
	wait, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := m.refreshClock(wait)
		if err == nil {
			return nil
		}
		select {
		case <-wait.Done():
			return err
		case <-ticker.C:
		}
	}
}

func (s *Sender) TimingProtocol() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.timing
}

func (m *mediaSession) timingProtocol() string {
	if m.clock != nil {
		return "PTP"
	}
	return "NTP"
}

// 失锁后以新连接重新协商。只在时钟探测失败时进入这里，认证和会话被抢占沿用原错误路径。
func (s *Sender) clockLost(old *mediaSession, cause error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.media != old {
		return
	}
	if s.cfg.RequiredPTP {
		s.finish(cause, true)
		return
	}
	if err := s.replaceWithNTP(old, cause); err != nil {
		s.finish(err, false)
	}
}

func (s *Sender) replaceWithNTP(old *mediaSession, cause error) error {
	old.shutdown(true)
	s.media = nil
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	cfg := s.cfg
	cfg.PTP = nil
	m, err := openMedia(ctx, s.device, cfg)
	if err != nil {
		return errors.Join(cause, err)
	}
	if err = s.restoreControls(ctx, m); err != nil {
		m.shutdown(true)
		return err
	}
	s.media = m
	s.heartbeatFailures = 0
	s.protocol, s.timing, s.fallback = m.protocol, m.timingProtocol(), errors.Join(cause, m.fallback)
	s.next = time.Time{}
	s.flights = nil
	s.played = s.sent
	slog.Warn("AirPlay 播放中失去 PTP 时钟，已用 NTP 重建会话", "event", "airplay.sender.fallback", "module", "airplay", "udn", s.device.Key, "error", s.fallback)
	select {
	case s.fallbacks <- s.fallback:
	default:
	}
	s.signal()
	return nil
}

// rateAnchor 在首包与 FLUSH 后显式绑定首个 RTP 帧和主时钟呈现时刻。
// 行为参考 shairport-sync 5.6.3 与 saundo 的 HomePod 实测。
func (m *mediaSession) rateAnchor(ctx context.Context, at time.Time) error {
	if m.clock == nil || m.anchorUnsupported {
		return nil
	}
	stamp, gm, ns, first := m.transport.PresentationAnchor(at)
	if !first {
		return nil
	}
	fraction, _ := bits.Div64(uint64(ns%int64(time.Second)), 0, uint64(time.Second))
	body, err := bplist.Marshal(map[string]any{
		"rate": 1, "rtpTime": stamp, "networkTimeTimelineID": int64(gm),
		"networkTimeSecs": uint64(ns / int64(time.Second)), "networkTimeFrac": int64(fraction),
	})
	if err != nil {
		return err
	}
	h := m.headers()
	h.Set("Content-Type", "application/x-apple-binary-plist")
	resp, err := m.command(ctx, "SETRATEANCHORTIME", m.uri, h, body)
	if statusIs(err, 405, 451, 501) {
		m.anchorUnsupported = true
		return nil
	}
	if err != nil {
		return err
	}
	if len(resp.Body) == 0 {
		return nil
	}
	var result map[string]any
	if err = bplist.Unmarshal(resp.Body, &result); err != nil {
		return err
	}
	switch status := result["status"]; status {
	case nil, uint64(0):
		return nil
	case int64(-12782):
		m.anchorUnsupported = true
		return nil
	default:
		return fmt.Errorf("AirPlay 接收端拒绝播放锚点：%v", status)
	}
}
