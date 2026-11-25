package airplay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/textproto"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/airplay/rtp"
	"github.com/qlqq/roomusic/backend/internal/devices/airplay/rtsp"
	"github.com/qlqq/roomusic/backend/internal/devices/lan"
)

type classicFixture struct {
	listener       net.Listener
	audio, control *net.UDPConn
	commands       chan string
	heartbeatCodes []int
	heartbeats     int
}

func newClassicFixture(t *testing.T, heartbeatCodes ...int) *classicFixture {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, addr := range addrs {
		prefix, err := netip.ParsePrefix(addr.String())
		if err != nil || !lan.IsLANAddress(prefix.Addr()) {
			continue
		}
		ap := netip.AddrPortFrom(prefix.Addr(), 0)
		listener, err := net.Listen("tcp4", ap.String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { listener.Close() })
		openUDP := func() *net.UDPConn {
			c, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(ap))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { c.Close() })
			return c
		}
		f := &classicFixture{listener: listener, audio: openUDP(), control: openUDP(), commands: make(chan string, 16), heartbeatCodes: heartbeatCodes}
		var worker sync.WaitGroup
		worker.Go(func() {
			for {
				c, err := listener.Accept()
				if err != nil {
					return
				}
				f.serve(c, t)
			}
		})
		t.Cleanup(func() { listener.Close(); worker.Wait() })
		return f
	}
	t.Skip("RAOP 发送需要本机局域网 IPv4 地址")
	return nil
}

func (f *classicFixture) serve(conn net.Conn, t *testing.T) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	r := textproto.NewReader(bufio.NewReader(conn))
	setup := true
	for {
		line, err := r.ReadLine()
		if err != nil {
			if err != io.EOF {
				t.Error(err)
			}
			return
		}
		parts := strings.Fields(line)
		if len(parts) != 3 || parts[2] != "RTSP/1.0" {
			t.Errorf("无效 RTSP 请求 %q", line)
			return
		}
		h, err := r.ReadMIMEHeader()
		if err != nil {
			t.Error(err)
			return
		}
		extra := ""
		status := http.StatusOK
		switch parts[0] {
		case "OPTIONS":
			if !setup && f.heartbeats < len(f.heartbeatCodes) {
				status = f.heartbeatCodes[f.heartbeats]
				f.heartbeats++
			}
			setup = false
			if status == 0 {
				// 故意不回复这一次心跳，接收端对后续请求仍可正常响应。
				continue
			}
		case "ANNOUNCE":
			n, err := strconv.Atoi(h.Get("Content-Length"))
			if err != nil || n <= 0 || n > 4096 {
				t.Errorf("ANNOUNCE Content-Length = %q", h.Get("Content-Length"))
				return
			}
			body := make([]byte, n)
			if _, err := io.ReadFull(r.R, body); err != nil {
				t.Error(err)
				return
			}
			lines := strings.Split(string(body), "\r\n")
			if h.Get("Content-Type") != "application/sdp" ||
				!slices.Contains(lines, "m=audio 0 RTP/AVP 96") ||
				!slices.Contains(lines, "a=rtpmap:96 AppleLossless") ||
				!slices.Contains(lines, "a=fmtp:96 352 0 16 40 10 14 2 255 0 0 44100") {
				t.Errorf("RAOP SDP = %q", body)
			}
		case "SETUP":
			for _, field := range []string{"RTP/AVP/UDP;unicast;mode=record;", ";control_port=", ";timing_port="} {
				if !strings.Contains(h.Get("Transport"), field) {
					t.Errorf("SETUP Transport = %q", h.Get("Transport"))
				}
			}
			extra = fmt.Sprintf("Session: fixture;timeout=30\r\nTransport: RTP/AVP/UDP;server_port=%d;control_port=%d\r\n",
				f.audio.LocalAddr().(*net.UDPAddr).Port, f.control.LocalAddr().(*net.UDPAddr).Port)
		case "RECORD":
			if h.Get("Session") != "fixture" || h.Get("Range") != "npt=0-" || !strings.HasPrefix(h.Get("RTP-Info"), "seq=") {
				t.Errorf("RECORD 请求头 = %v", h)
			}
			extra = "Audio-Latency: 11025\r\n"
		}
		if _, err := fmt.Fprintf(conn, "RTSP/1.0 %d %s\r\nCSeq: %s\r\n%sContent-Length: 0\r\n\r\n", status, http.StatusText(status), h.Get("CSeq"), extra); err != nil {
			t.Error(err)
			return
		}
		f.commands <- parts[0]
		if parts[0] == "TEARDOWN" {
			return
		}
	}
}

func newHeartbeatTestSender(t *testing.T, codes ...int) (*Sender, *classicFixture) {
	t.Helper()
	f := newClassicFixture(t, codes...)
	device := Device{Info: Info{RAOPAddress: f.listener.Addr().String()}}
	cfg := SenderConfig{SampleRate: 44100, BitDepth: 16}
	media, err := openMedia(t.Context(), device, cfg)
	if err != nil {
		t.Fatal(err)
	}
	// 手动触发心跳，避免后台计时器插入额外请求。
	s := &Sender{device: device, cfg: cfg, media: media, done: make(chan struct{}), wake: make(chan struct{}, 1)}
	t.Cleanup(func() { _ = s.Close() })
	for _, method := range []string{"OPTIONS", "ANNOUNCE", "SETUP", "RECORD"} {
		f.expect(t, method)
	}
	return s, f
}

func TestAirPlayHeartbeatConsecutiveFailures(t *testing.T) {
	const failure = http.StatusServiceUnavailable
	codes := []int{failure, failure, http.StatusOK, failure, failure, failure}
	s, f := newHeartbeatTestSender(t, codes...)
	for i := range codes {
		s.heartbeat()
		f.expect(t, "OPTIONS")
		select {
		case <-s.Done():
			if i != len(codes)-1 {
				t.Fatalf("第 %d 次心跳提前结束会话：%v", i+1, s.Err())
			}
			var status *rtsp.StatusError
			if !errors.As(s.Err(), &status) || status.Code != failure {
				t.Fatalf("会话结束错误 = %v，期望 RTSP %d", s.Err(), failure)
			}
		default:
			if i == len(codes)-1 {
				t.Fatal("连续第三次心跳失败后会话仍然存活")
			}
		}
	}
}

func TestAirPlayHeartbeatRecoversAfterTimeout(t *testing.T) {
	s, f := newHeartbeatTestSender(t, 0, http.StatusOK)
	s.heartbeat()
	select {
	case <-s.Done():
		t.Fatalf("一次心跳超时就结束了会话：%v", s.Err())
	default:
	}
	s.heartbeat()
	select {
	case method := <-f.commands:
		if method != "OPTIONS" {
			t.Fatalf("超时后收到 %s，期望 OPTIONS", method)
		}
	case <-time.After(time.Second):
		t.Fatal("一次心跳超时后，接收端没有收到下一次 OPTIONS")
	}
}

func (f *classicFixture) expect(t *testing.T, method string) {
	t.Helper()
	select {
	case got := <-f.commands:
		if got != method {
			t.Fatalf("收到 %s，期望 %s", got, method)
		}
	case <-time.After(time.Second):
		t.Fatalf("未收到 %s", method)
	}
}

func (f *classicFixture) packet(t *testing.T) []byte {
	t.Helper()
	f.audio.SetReadDeadline(time.Now().Add(time.Second))
	packet := make([]byte, 2048)
	n, _, err := f.audio.ReadFromUDP(packet)
	if err != nil {
		t.Fatal(err)
	}
	if n <= 12 {
		t.Fatalf("RTP 包过短，长度 %d", n)
	}
	return packet[:n]
}

func TestAirPlayClassicSenderPacingAndPauseTimeout(t *testing.T) {
	f := newClassicFixture(t)
	d := Device{Info: Info{RAOPAddress: f.listener.Addr().String()}}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	s, err := OpenSender(ctx, d, SenderConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, method := range []string{"OPTIONS", "ANNOUNCE", "SETUP", "RECORD"} {
		f.expect(t, method)
	}
	if s.Protocol() != "raop" || s.TimingProtocol() != "NTP" || s.media.transport.Latency() != 1250*time.Millisecond {
		t.Fatal("RAOP 默认协商格式、NTP 计时或输出延迟错误")
	}

	pcm1 := bytes.Repeat([]byte{1, 2, 3, 4}, 100)
	pcm2 := bytes.Repeat([]byte{5, 6, 7, 8}, rtp.FramesPerPacket-100)
	if err := s.WritePCM(ctx, pcm1); err != nil || s.FramesSent() != 0 {
		t.Fatalf("首段不足一包时发送了 PCM，错误 %v", err)
	}
	const packets = 12
	content := append(bytes.Clone(pcm2), bytes.Repeat([]byte{6, 7, 8, 9}, (packets-1)*rtp.FramesPerPacket)...)
	start := time.Now()
	if err := s.WritePCM(ctx, content); err != nil {
		t.Fatal(err)
	}
	wantFrames := uint64(packets * rtp.FramesPerPacket)
	if elapsed := time.Since(start); elapsed < 75*time.Millisecond || s.FramesSent() != wantFrames || s.PlayedFrames() != 0 {
		t.Fatalf("连续发送耗时 %s，已发送 %d 帧，已播放 %d 帧", elapsed, s.FramesSent(), s.PlayedFrames())
	}
	first := f.packet(t)
	want, _ := rtp.ALAC(append(bytes.Clone(pcm1), pcm2...), 16)
	if !bytes.Equal(first[12:], want) {
		t.Fatal("两段 PCM 未无缝拼入同一 ALAC 帧")
	}
	seq, stamp := binary.BigEndian.Uint16(first[2:]), binary.BigEndian.Uint32(first[4:])
	for i := 1; i < packets; i++ {
		p := f.packet(t)
		if binary.BigEndian.Uint16(p[2:]) != seq+uint16(i) || binary.BigEndian.Uint32(p[4:]) != stamp+uint32(i*rtp.FramesPerPacket) {
			t.Fatalf("第 %d 个包的序号或时间戳错误", i+1)
		}
	}
	if err := s.Pause(ctx); err != nil {
		t.Fatal(err)
	}
	silence, _ := rtp.ALAC(make([]byte, rtp.FramesPerPacket*4), 16)
	if got := f.packet(t); !bytes.Equal(got[12:], silence) || s.FramesSent() != wantFrames {
		t.Fatal("暂停时仍推进内容计数或输出了非静音")
	}

	// 推进暂停计时，保留真实维护循环和 TEARDOWN，无需等待十分钟。
	s.mu.Lock()
	s.paused = time.Now().Add(-10 * time.Minute)
	s.signal()
	s.mu.Unlock()
	f.expect(t, "TEARDOWN")
	if err := s.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"OPTIONS", "ANNOUNCE", "SETUP", "RECORD"} {
		f.expect(t, method)
	}
	if err := s.WritePCM(ctx, append(bytes.Clone(pcm1), pcm2...)); err != nil {
		t.Fatal(err)
	}
	// 暂停计时切换前维护循环可能已经发送过一个旧会话的静音包。
	for {
		p := f.packet(t)
		if p[1]&0x80 != 0 {
			if !bytes.Equal(p[12:], want) {
				t.Fatal("续播首包的 ALAC 内容错误")
			}
			break
		}
	}
}
