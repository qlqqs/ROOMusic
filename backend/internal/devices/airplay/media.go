package airplay

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/airplay/bplist"
	"github.com/qlqq/roomusic/backend/internal/devices/airplay/hap"
	"github.com/qlqq/roomusic/backend/internal/devices/airplay/raop"
	"github.com/qlqq/roomusic/backend/internal/devices/airplay/rtp"
	"github.com/qlqq/roomusic/backend/internal/devices/airplay/rtsp"
	"github.com/qlqq/roomusic/backend/internal/ptp"
	"golang.org/x/crypto/chacha20poly1305"
)

type mediaSession struct {
	client, event     *rtsp.Client
	events            chan error
	transport         *rtp.Transport
	protocol, uri, id string
	fallback          error
	rate, depth       int
	clock             *ptp.Client
	peer              netip.Addr
	anchorUnsupported bool
}

type SenderConfig struct {
	SampleRate, BitDepth int
	Credentials          *CredentialStore
	PTP                  *ptp.Client
	RequiredPTP          bool
}

func openMedia(ctx context.Context, d Device, cfg SenderConfig) (*mediaSession, error) {
	if d.UnavailableReason != "" {
		return nil, errors.New(string(d.UnavailableReason))
	}
	if cfg.RequiredPTP && d.AirPlayAddress == "" {
		return nil, errPTPUnavailable
	}
	var fallback error
	if d.AirPlayAddress != "" {
		var clock ptp.Status
		var err error
		// 发现的 SupportsPTP 位和型号约束共同决定是否尝试原生 PTP。
		if cfg.PTP != nil && (d.Info.PrefersPTP(d.Model) || cfg.RequiredPTP) {
			clock, err = cfg.PTP.Status(ctx)
		}
		if cfg.RequiredPTP && (!clock.CanNegotiate() || err != nil) {
			return nil, errors.Join(errPTPUnavailable, err)
		}
		if clock.CanNegotiate() && err == nil {
			m, canRetry, e := openNative(ctx, d, cfg, &clock)
			if e == nil {
				return m, nil
			}
			if !canRetry || cfg.RequiredPTP || ctx.Err() != nil {
				return nil, e
			}
			fallback = e
		}
		m, canRetry, e := openNative(ctx, d, cfg, nil)
		if e == nil {
			m.fallback = fallback
			return m, nil
		}
		fallback = errors.Join(fallback, e)
		if !canRetry || d.RAOPAddress == "" || ctx.Err() != nil || d.RequiresPIN || d.RequiresPassword || cfg.SampleRate != 44100 {
			return nil, fallback
		}
	}
	if cfg.SampleRate != 44100 || cfg.BitDepth != 16 {
		return nil, errors.Join(fallback, errors.New("RAOP 仅支持 44100 Hz 16 位"))
	}
	m, err := newMedia(ctx, d.RAOPAddress, "raop", cfg)
	if err != nil {
		return nil, errors.Join(fallback, err)
	}
	endpoint, _ := netip.ParseAddrPort(d.RAOPAddress)
	s, err := raop.Setup(ctx, m.client, endpoint.Addr(), d.RAOPEncryption, m.transport)
	m.uri, m.id = s.URI, s.ID
	if err != nil {
		m.shutdown(s.ID != "")
		return nil, errors.Join(fallback, err)
	}
	m.fallback = fallback
	return m, nil
}

func openNative(ctx context.Context, d Device, cfg SenderConfig, clock *ptp.Status) (*mediaSession, bool, error) {
	m, err := newMedia(ctx, d.AirPlayAddress, "airplay2", cfg)
	if err != nil {
		return nil, false, err
	}
	canRetry, err := m.native(ctx, d, cfg, clock)
	if err != nil {
		m.shutdown(m.uri != "")
		return nil, canRetry, err
	}
	return m, false, nil
}

func newMedia(ctx context.Context, address, protocol string, cfg SenderConfig) (*mediaSession, error) {
	c, err := rtsp.Dial(ctx, address)
	if err != nil {
		return nil, err
	}
	endpoint, _ := netip.ParseAddrPort(address)
	t, err := rtp.New(c.LocalAddr(), endpoint.Addr(), cfg.SampleRate, cfg.BitDepth)
	if err != nil {
		c.Close()
		return nil, err
	}
	return &mediaSession{client: c, transport: t, peer: endpoint.Addr(), protocol: protocol, rate: cfg.SampleRate, depth: cfg.BitDepth}, nil
}

func (m *mediaSession) headers() http.Header {
	h := make(http.Header)
	if m.id != "" {
		h.Set("Session", m.id)
	}
	return h
}

func (m *mediaSession) command(ctx context.Context, method, target string, h http.Header, body []byte) (*rtsp.Response, error) {
	if h == nil {
		h = m.headers()
	}
	resp, err := m.client.Do(ctx, method, target, h, body)
	if err != nil {
		return nil, err
	}
	if err = resp.CheckStatus(); err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	return resp, nil
}

// native 按实时流顺序打开事件、RECORD、再协商数据端口。行为参考 airplay2-sender-cpp（Apache-2.0）。
func (m *mediaSession) native(ctx context.Context, d Device, cfg SenderConfig, clock *ptp.Status) (bool, error) {
	var auth *hap.Session
	var err error
	if d.RequiresPIN || d.RequiresPassword {
		if cfg.Credentials == nil {
			return false, errors.New("AirPlay 接收端需要配对凭据")
		}
		auth, err = cfg.Credentials.Verify(ctx, d.Key, m.client)
	} else {
		auth, err = hap.PairTransient(ctx, m.client)
	}
	if err != nil {
		return !d.RequiresPIN && !d.RequiresPassword && statusIs(err, 404, 405, 501), err
	}
	info, err := m.command(ctx, "GET", "/info", nil, nil)
	if err != nil {
		return statusIs(err, 404, 405, 501), err
	}
	audioFormat := uint64(0x40000)
	if m.depth == 24 {
		audioFormat = 0x200000
	}
	if err = checkAudioFormat(info.Body, audioFormat); err != nil {
		return errors.Is(err, errAudioFormat), err
	}
	ctl, ntp := m.transport.Ports()
	var mac [6]byte
	rand.Read(mac[:])
	mac[0] = mac[0]&0xfe | 2
	timing := Timing{Protocol: "NTP", Port: ntp}
	if clock != nil {
		timing = Timing{Protocol: "PTP", ClockID: clock.ClockID}
	}
	setup, err := SetupSession(ctx, m.client, net.HardwareAddr(mac[:]).String(), timing)
	m.uri = setup.URI
	if err != nil {
		return statusIs(err, 400, 405, 415, 451, 461, 501), fmt.Errorf("AirPlay 2 %s 会话 SETUP: %w", timing.Protocol, err)
	}
	peer, _ := netip.ParseAddrPort(d.AirPlayAddress)
	event, err := dialEvent(ctx, netip.AddrPortFrom(peer.Addr(), setup.EventPort).String())
	if err != nil {
		return false, err
	}
	m.event = event
	read, err := auth.DeriveKey("Events-Salt", "Events-Write-Encryption-Key")
	if err != nil {
		return false, err
	}
	write, err := auth.DeriveKey("Events-Salt", "Events-Read-Encryption-Key")
	if err != nil {
		return false, err
	}
	if err = event.EnableEncryption(write, read); err != nil {
		return false, err
	}
	m.events = make(chan error, 1)
	go func() { m.events <- event.ServeEvents() }()
	if _, err = m.command(ctx, "RECORD", m.uri, nil, nil); err != nil {
		return statusIs(err, 405, 455, 501), err
	}
	if clock != nil {
		m.clock = cfg.PTP
		if ptp.ReceiverProfile(d.Model).ProvidesClock {
			// 行为参考 saundo：这些机型在 RECORD 之后开始提供主钟。
			if err = m.waitClock(ctx); err != nil {
				return true, err
			}
		}
		// SETPEERS 在 RECORD 和流级 SETUP 之间发送；行为参考 OwnTone 的线上会话。
		body, err := bplist.Marshal([]any{m.peer.String(), m.client.LocalAddr().String()})
		if err != nil {
			return false, err
		}
		if _, err = m.command(ctx, "SETPEERS", "/peer-list-changed", http.Header{"Content-Type": {"application/x-apple-binary-plist"}}, body); err != nil {
			return statusIs(err, 405, 451, 461, 501), err
		}
	}
	_, sid, _ := strings.Cut(strings.TrimPrefix(m.uri, "rtsp://"), "/")
	id, _ := strconv.ParseUint(sid, 10, 32)
	audioKey := auth.AudioKey()
	stream := map[string]any{
		"type": 96, "ct": 2, "audioFormat": audioFormat, "audioMode": "default",
		"isMedia": true, "spf": rtp.FramesPerPacket, "sr": m.rate,
		"controlPort": ctl, "shk": audioKey, "latencyMin": m.rate / 4, "latencyMax": m.rate * 2,
		"supportsDynamicStreamID": false, "streamConnectionID": id,
	}
	body, err := bplist.Marshal(map[string]any{"streams": []any{stream}})
	if err != nil {
		return false, err
	}
	h := m.headers()
	h.Set("Content-Type", "application/x-apple-binary-plist")
	resp, err := m.command(ctx, "SETUP", m.uri, h, body)
	if err != nil {
		return statusIs(err, 400, 405, 415, 451, 461, 501), fmt.Errorf("AirPlay 2 %s 流 SETUP: %w", timing.Protocol, err)
	}
	var result map[string]any
	if err = bplist.Unmarshal(resp.Body, &result); err != nil {
		return false, err
	}
	streams, _ := result["streams"].([]any)
	if len(streams) != 1 {
		return true, errors.New("AirPlay 2 SETUP 未返回实时流")
	}
	stream, _ = streams[0].(map[string]any)
	if kind, exists := stream["type"]; exists && kind != uint64(96) {
		return true, errors.New("AirPlay 2 SETUP 返回其他流类型")
	}
	if mask, exists := stream["audioFormat"]; exists && mask != audioFormat {
		return true, errAudioFormat
	}
	audio, _ := stream["dataPort"].(uint64)
	control, _ := stream["controlPort"].(uint64)
	if audio < 1 || audio > 65535 || control < 1 || control > 65535 {
		return true, errors.New("AirPlay 2 SETUP 未返回实时流端口")
	}
	aead, err := chacha20poly1305.New(audioKey)
	clear(audioKey)
	if err != nil {
		return false, err
	}
	m.transport.Configure(uint16(audio), uint16(control), rtp.ChaCha(aead))
	if clock != nil {
		if err = m.waitClock(ctx); err != nil {
			return true, err
		}
	}
	return false, nil
}

func (m *mediaSession) shutdown(teardown bool) error {
	var err error
	if teardown && m.uri != "" {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err = m.command(ctx, "TEARDOWN", m.uri, nil, nil)
		cancel()
	}
	if m.event != nil {
		m.event.Close()
	}
	m.client.Close()
	m.transport.Close()
	return err
}

// 行为参考 Vespertine PR 397 的格式记录。只使用实时流面向接收输入的位图。
func checkAudioFormat(body []byte, wanted uint64) error {
	var info map[string]any
	if err := bplist.Unmarshal(body, &info); err != nil {
		return fmt.Errorf("AirPlay /info: %w", err)
	}
	formats, _ := info["audioFormats"].([]any)
	var declared bool
	for _, entry := range formats {
		format, _ := entry.(map[string]any)
		if format["type"] != uint64(96) {
			continue
		}
		if mask, ok := format["audioInputFormats"].(uint64); ok {
			declared = true
			if mask&wanted != 0 {
				return nil
			}
		}
	}
	if declared {
		return errAudioFormat
	}
	return nil
}

// 行为参考 pyatv（MIT）：仅在接收端尚未监听已公布的事件端口时短时等待。
func dialEvent(ctx context.Context, address string) (*rtsp.Client, error) {
	for backoff := 20 * time.Millisecond; ; backoff *= 2 {
		c, err := rtsp.Dial(ctx, address)
		if !errors.Is(err, syscall.ECONNREFUSED) || backoff > 320*time.Millisecond {
			return c, err
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

var errAudioFormat = errors.New("AirPlay 接收端不支持所选实时 ALAC 格式")

func statusIs(err error, codes ...int) bool {
	var status *rtsp.StatusError
	return errors.As(err, &status) && slices.Contains(codes, status.Code)
}
