package airplay

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/airplay/rtp"
)

type flight struct {
	at          time.Time
	first, last uint64
}

type Sender struct {
	mu, writeMu      sync.Mutex
	device           Device
	cfg              SenderConfig
	media            *mediaSession
	protocol, timing string
	fallback         error
	err              error
	done             chan struct{}
	wake             chan struct{}
	paused, next     time.Time
	pending, silence []byte
	sent, played     uint64
	flights          []flight
	vol              *float64
	muted            bool
	metadata         *Metadata
}

func OpenSender(ctx context.Context, d Device, cfg SenderConfig) (*Sender, error) {
	if cfg.SampleRate == 0 {
		cfg.SampleRate = 44100
	}
	if cfg.BitDepth == 0 {
		cfg.BitDepth = 16
	}
	if !(cfg.SampleRate == 44100 && cfg.BitDepth == 16 || cfg.SampleRate == 48000 && cfg.BitDepth == 24) {
		return nil, errors.New("AirPlay PCM 格式不受支持")
	}
	cfg.RequiredPTP = cfg.RequiredPTP || RequiresPTP(d.Model)
	m, err := openMedia(ctx, d, cfg)
	if err != nil {
		return nil, err
	}
	n := rtp.FramesPerPacket * cfg.BitDepth / 4
	s := &Sender{
		device: d, cfg: cfg, media: m, protocol: m.protocol, timing: m.timingProtocol(), fallback: m.fallback,
		done: make(chan struct{}), wake: make(chan struct{}, 1),
		pending: make([]byte, 0, n), silence: make([]byte, n),
	}
	go s.maintain()
	return s, nil
}

func (s *Sender) Done() <-chan struct{} { return s.done }
func (s *Sender) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}
func (s *Sender) Protocol() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.protocol
}
func (s *Sender) FallbackReason() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fallback
}
func (s *Sender) FramesSent() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sent
}

// PlayedFrames 按已发送内容对应的 NTP 播放锚点和协商延迟估算接收端播放进度。
func (s *Sender) PlayedFrames() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updatePlayed(time.Now())
	return s.played
}

func (s *Sender) updatePlayed(now time.Time) {
	for len(s.flights) > 0 {
		f := s.flights[0]
		if now.Before(f.at) {
			break
		}
		elapsed := uint64(now.Sub(f.at)) * uint64(s.cfg.SampleRate) / uint64(time.Second)
		s.played = min(f.first+elapsed, f.last)
		if s.played < f.last {
			break
		}
		s.flights = s.flights[1:]
	}
}

func (s *Sender) check() error {
	select {
	case <-s.done:
		if s.err != nil {
			return s.err
		}
		return errors.New("AirPlay 会话已关闭")
	default:
		return nil
	}
}

func (s *Sender) finish(err error, teardown bool) error {
	if e := s.check(); e != nil {
		return s.err
	}
	s.err = err
	close(s.done)
	if s.media == nil {
		return nil
	}
	e := s.media.shutdown(teardown)
	s.media = nil
	return e
}

func (s *Sender) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finish(nil, true)
}

func (s *Sender) frameDuration(n int) time.Duration {
	return time.Second * time.Duration(n) / time.Duration(s.cfg.SampleRate)
}

func (s *Sender) transmit(ctx context.Context, pcm []byte, content int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	now := time.Now()
	if s.next.IsZero() || now.Sub(s.next) > s.frameDuration(rtp.FramesPerPacket)*2 {
		s.next = now
	}
	if delay := s.next.Sub(now); delay > 0 {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err := s.media.rateAnchor(ctx, s.next); err != nil {
		s.finish(err, false)
		return err
	}
	if err := s.media.transport.Send(pcm, s.next); err != nil {
		s.finish(err, false)
		return err
	}
	s.updatePlayed(s.next)
	if content > 0 {
		s.flights = append(s.flights, flight{at: s.next.Add(s.media.transport.Latency()), first: s.sent, last: s.sent + uint64(content)})
		s.sent += uint64(content)
	}
	s.next = s.next.Add(s.frameDuration(len(pcm) / (s.cfg.BitDepth / 4)))
	return nil
}

// WritePCM 保留不足一包的尾帧，下一首的输入可以紧接同一个 RTP 包。
func (s *Sender) WritePCM(ctx context.Context, pcm []byte) error {
	if len(pcm)%(s.cfg.BitDepth/4) != 0 {
		return errors.New("AirPlay PCM 未按立体声帧对齐")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	for len(pcm) > 0 {
		n, err := s.writePart(ctx, pcm)
		if err != nil {
			return err
		}
		pcm = pcm[n:]
	}
	return nil
}

func (s *Sender) writePart(ctx context.Context, pcm []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if !s.paused.IsZero() {
		return 0, errors.New("AirPlay 发送端已暂停")
	}
	n := min(cap(s.pending)-len(s.pending), len(pcm))
	s.pending = append(s.pending, pcm[:n]...)
	if len(s.pending) == cap(s.pending) {
		if err := s.transmit(ctx, s.pending, rtp.FramesPerPacket); err != nil {
			s.pending = s.pending[:len(s.pending)-n]
			return 0, err
		}
		s.pending = s.pending[:0]
	}
	return n, nil
}

func (s *Sender) sendTail(ctx context.Context) error {
	if len(s.pending) == 0 {
		return nil
	}
	content := len(s.pending) / (s.cfg.BitDepth / 4)
	s.pending = append(s.pending, s.silence[:cap(s.pending)-len(s.pending)]...)
	if err := s.transmit(ctx, s.pending, content); err != nil {
		s.pending = s.pending[:content*(s.cfg.BitDepth/4)]
		return err
	}
	s.pending = s.pending[:0]
	return nil
}

// Drain 在内容末尾继续计时并发送静音，待所有内容帧越过接收端播放锚点后返回。
func (s *Sender) Drain(ctx context.Context) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	for {
		s.mu.Lock()
		err := s.check()
		if err == nil {
			err = ctx.Err()
		}
		if err == nil && !s.paused.IsZero() {
			err = errors.New("已暂停的 AirPlay 会话无法排空")
		}
		if err == nil {
			err = s.sendTail(ctx)
		}
		s.updatePlayed(time.Now())
		done := s.played >= s.sent
		if err == nil && !done {
			err = s.transmit(ctx, s.silence, 0)
		}
		s.mu.Unlock()
		if err != nil || done {
			return err
		}
	}
}

func (s *Sender) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Sender) Pause(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return err
	}
	if !s.paused.IsZero() {
		return nil
	}
	if err := s.sendTail(ctx); err != nil {
		return err
	}
	s.paused = time.Now()
	s.signal()
	return nil
}

func (s *Sender) Resume(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return err
	}
	if s.media == nil {
		m, err := openMedia(ctx, s.device, s.cfg)
		if err != nil {
			return err
		}
		s.media = m
		s.protocol, s.timing, s.fallback = m.protocol, m.timingProtocol(), m.fallback
		if err = s.restoreControls(ctx, m); err != nil {
			m.shutdown(true)
			s.media = nil
			return err
		}
		s.next = time.Time{}
	}
	s.paused = time.Time{}
	s.signal()
	return nil
}

func (s *Sender) restoreControls(ctx context.Context, m *mediaSession) error {
	if s.vol != nil {
		if err := m.volume(ctx, *s.vol, s.muted); err != nil {
			return err
		}
	}
	if s.metadata != nil {
		return m.metadata(ctx, *s.metadata)
	}
	return nil
}

func (s *Sender) Flush(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return err
	}
	if s.media != nil {
		h := s.media.positionHeaders()
		if _, err := s.media.command(ctx, "FLUSH", s.media.uri, h, nil); err != nil {
			return err
		}
		s.media.transport.Flush()
	}
	s.pending = s.pending[:0]
	s.flights = nil
	s.played = s.sent
	s.next = time.Time{}
	return nil
}

// 暂停时保留时序，发送静音；长暂停释放套接字，Resume 重新握手。
func (s *Sender) maintain() {
	heartbeat := time.NewTicker(time.Second * 2)
	defer heartbeat.Stop()
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		s.mu.Lock()
		if s.check() != nil {
			s.mu.Unlock()
			return
		}
		var events <-chan error
		if s.media != nil {
			events = s.media.events
		}
		delay := time.Hour
		if !s.paused.IsZero() && s.media != nil {
			delay = max(time.Duration(0), time.Until(s.next))
		}
		timer.Reset(delay)
		s.mu.Unlock()
		select {
		case <-s.done:
			return
		case <-s.wake:
			continue
		case err := <-events:
			s.mu.Lock()
			if s.media != nil && s.media.events == events {
				s.finish(err, false)
			}
			s.mu.Unlock()
		case <-timer.C:
			s.mu.Lock()
			if !s.paused.IsZero() && s.media != nil {
				if time.Since(s.paused) >= 10*time.Minute {
					s.media.shutdown(true)
					s.media = nil
				} else {
					s.transmit(context.Background(), s.silence, 0)
				}
			}
			s.mu.Unlock()
		case <-heartbeat.C:
			s.heartbeat()
		}
	}
}

func (s *Sender) heartbeat() {
	s.mu.Lock()
	m := s.media
	s.mu.Unlock()
	if m == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	if m.clock != nil {
		s.mu.Lock()
		var err error
		if s.media == m {
			err = m.refreshClock(ctx)
		}
		s.mu.Unlock()
		if err != nil {
			cancel()
			s.clockLost(m, err)
			return
		}
	}
	method, target := "OPTIONS", "*"
	if m.protocol == "airplay2" {
		method, target = "POST", "/feedback"
	}
	_, err := m.command(ctx, method, target, nil, nil)
	cancel()
	if err != nil {
		s.mu.Lock()
		if s.media == m {
			s.finish(err, false)
		}
		s.mu.Unlock()
	}
}
