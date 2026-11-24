package upnp

import (
	"context"
	"errors"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/airplay/hap"
	"github.com/qlqq/roomusic/backend/internal/devices/airplay/rtsp"
)

var (
	ErrPairingExpired  = errors.New("屏幕 PIN 配对已过期，请重新开始")
	ErrPairingBusy     = errors.New("此设备已在配对中")
	ErrPairingRejected = errors.New("接收端未接受配对，请重新开始并核对屏幕 PIN")
	ErrPairingInput    = errors.New("请输入四位数字 PIN")
)

type pinChallenge struct {
	ctx       context.Context
	cancel    context.CancelFunc
	conn      *rtsp.Client
	setup     *hap.PINSetup
	finishing bool
}

// 连接在等待管理员输入期间归设备协议实例持有。关闭协议、超时或移除设备会结束挑战。
func (s *Service) BeginAirPlayPIN(ctx context.Context, key string) error {
	s.mu.Lock()
	proto := s.protocols["airplay"]
	if proto.ctx == nil {
		s.mu.Unlock()
		return ErrAirPlayDisabled
	}
	e, ok := proto.registry.Lookup(key)
	if !ok || !e.Online {
		s.mu.Unlock()
		return ErrUnknownDevice
	}
	info := e.Capabilities.AirPlay
	if info == nil || info.AirPlayAddress == "" || info.UnavailableReason != "" {
		s.mu.Unlock()
		return ErrPairingRejected
	}
	if old := s.pendingPIN[key]; old != nil && old.ctx.Err() == nil {
		s.mu.Unlock()
		return ErrPairingBusy
	}
	lifetime, cancel := context.WithTimeout(proto.ctx, 2*time.Minute)
	flow := &pinChallenge{ctx: lifetime, cancel: cancel}
	s.pendingPIN[key] = flow
	s.mu.Unlock()
	context.AfterFunc(lifetime, func() {
		s.mu.Lock()
		if s.pendingPIN[key] == flow {
			delete(s.pendingPIN, key)
		}
		conn := flow.conn
		s.mu.Unlock()
		if conn != nil {
			conn.Close()
		}
	})

	// HTTP 请求取消仅中止发起挑战；成功返回后允许用户在另一请求中提交 PIN。
	req, stop := context.WithCancel(ctx)
	defer stop()
	unlink := context.AfterFunc(lifetime, stop)
	defer unlink()
	conn, err := rtsp.Dial(req, info.AirPlayAddress)
	if err != nil {
		cancel()
		return ErrProbe
	}
	setup, err := hap.BeginPIN(req, conn)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil || lifetime.Err() != nil || s.pendingPIN[key] != flow {
		conn.Close()
		cancel()
		return ErrPairingRejected
	}
	flow.conn, flow.setup = conn, setup
	return nil
}

func (s *Service) FinishAirPlayPIN(ctx context.Context, key, pin string) error {
	if len(pin) != 4 {
		return ErrPairingInput
	}
	for _, r := range pin {
		if r < '0' || r > '9' {
			return ErrPairingInput
		}
	}
	s.mu.Lock()
	flow := s.pendingPIN[key]
	if flow == nil || flow.ctx.Err() != nil {
		s.mu.Unlock()
		return ErrPairingExpired
	}
	if flow.finishing || flow.setup == nil {
		s.mu.Unlock()
		return ErrPairingBusy
	}
	flow.finishing = true
	s.mu.Unlock()
	defer flow.cancel()
	req, stop := context.WithCancel(ctx)
	defer stop()
	unlink := context.AfterFunc(flow.ctx, stop)
	defer unlink()
	creds, err := flow.setup.Finish(req, pin)
	if err != nil {
		return ErrPairingRejected
	}
	defer clear(creds.ControllerSeed)
	s.mu.Lock()
	defer s.mu.Unlock()
	if flow.ctx.Err() != nil || s.pendingPIN[key] != flow {
		return ErrPairingExpired
	}
	return s.airplayCredentials.Save(req, key, creds)
}
