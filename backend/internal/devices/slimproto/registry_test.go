package slimproto

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/slimproto/slimtest"
)

const registryTestKey = "slim:02:ab:cd:ef:00:01"

func useSlimProtoPort(t *testing.T, port uint16, links []link) {
	t.Helper()
	oldPort, oldLinks := listenPort, listLinks
	t.Cleanup(func() { listenPort, listLinks = oldPort, oldLinks })
	listenPort = port
	listLinks = func() ([]link, error) { return links, nil }
}

func newTestRegistry(t *testing.T) (*Registry, string) {
	t.Helper()
	probe, err := net.ListenTCP("tcp4", &net.TCPAddr{})
	if err != nil {
		t.Fatal(err)
	}
	port := uint16(probe.Addr().(*net.TCPAddr).Port)
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	useSlimProtoPort(t, port, nil)
	r := NewRegistry("ROOMusic 测试", slog.New(slog.DiscardHandler), nil)
	if err := r.Start(t.Context()); err != nil || r.PortInUse() {
		t.Fatalf("启动 SlimProto 注册表，端口占用 %t，错误 %v", r.PortInUse(), err)
	}
	t.Cleanup(r.Stop)
	return r, (&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(port)}).String()
}

func sendRegistryTestHello(t *testing.T, p *slimtest.Player, channels uint16) {
	t.Helper()
	p.Send(t, "HELO", helloPayload(channels, "Model=squeezelite,ModelName=SqueezeLite,flc"))
	if err := p.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, want := range [][]byte{
		{'a', 'u', 'd', 'e', 1, 1},
		{'a', 'u', 'd', 'g', 0, 0, 0, 128, 0, 0, 0, 128, 1, 255, 0, 1, 0, 0, 0, 1, 0, 0},
		{'s', 'e', 't', 'd', 0},
		append([]byte("strmq"), make([]byte, 23)...),
	} {
		got, err := p.ReadFrame()
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("握手响应 = %x %v，期望 %x", got, err, want)
		}
	}
}

func waitRegistryPlayerEOF(t *testing.T, p *slimtest.Player, timeout time.Duration) {
	t.Helper()
	if err := p.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatal(err)
	}
	for {
		got, err := p.ReadFrame()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil || len(got) != 28 || string(got[:5]) != "strmt" {
			t.Fatalf("等待连接关闭时读到 %x，错误 %v", got, err)
		}
	}
}

func TestSlimProtoRegistryIgnoresUnknownFrameAndClosesOversizedFrame(t *testing.T) {
	r, address := newTestRegistry(t)
	p := slimtest.Dial(t, address)
	sendRegistryTestHello(t, p, 0)
	p.WritePacket(t, append(upstreamFrame("MISC", []byte("SETD\x00\x00")), upstreamFrame("SETD", []byte("\x00书房\x00"))...))
	waitRegistry(t, func() bool {
		d, ok := r.Lookup(registryTestKey)
		return ok && d.Name == "书房"
	})
	// 只发长度超限的帧头，服务端应立即关闭连接。
	header := []byte{'R', 'E', 'S', 'P', 0, 1, 0, 1}
	p.WritePacket(t, header)
	waitRegistryPlayerEOF(t, p, 2*time.Second)
	waitRegistry(t, func() bool { _, online := r.Lookup(registryTestKey); return !online })
}

func TestSlimProtoRegistryReconnectReplacesPlayer(t *testing.T) {
	r, address := newTestRegistry(t)
	first := slimtest.Dial(t, address)
	sendRegistryTestHello(t, first, 0)
	previous, ok := r.Lookup(registryTestKey)
	if !ok {
		t.Fatal("首次握手后未登记")
	}
	second := slimtest.Dial(t, address)
	sendRegistryTestHello(t, second, 0x4000)
	waitRegistryPlayerEOF(t, first, 2*time.Second)
	if d, ok := r.Lookup(registryTestKey); !ok || d.Player == previous.Player || !d.Reconnect || len(r.List()) != 1 {
		t.Fatalf("同 MAC 重连后的设备 = %+v，在线 %t", d, ok)
	}
}

func TestSlimProtoRegistryReadDeadlines(t *testing.T) {
	r, address := newTestRegistry(t)
	started := time.Now()
	withoutHello := slimtest.Dial(t, address)
	withoutStatus := slimtest.Dial(t, address)
	sendRegistryTestHello(t, withoutStatus, 0)
	helloAt := time.Now()
	t.Run("no_HELO", func(t *testing.T) {
		waitRegistryPlayerEOF(t, withoutHello, 7*time.Second)
		if elapsed := time.Since(started); elapsed < 4500*time.Millisecond {
			t.Fatalf("未发送 HELO 的连接在 %v 提前关闭", elapsed)
		}
	})
	t.Run("no_STAT", func(t *testing.T) {
		waitRegistryPlayerEOF(t, withoutStatus, 13*time.Second)
		if elapsed := time.Since(helloAt); elapsed < 14*time.Second {
			t.Fatalf("未发送 STAT 的播放器在握手后 %v 提前关闭", elapsed)
		}
		waitRegistry(t, func() bool { _, online := r.Lookup(registryTestKey); return !online })
	})
}

func waitRegistry(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for !ready() {
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("等待 SlimProto 注册表更新超时")
		}
	}
}

func TestSlimProtoRegistryRetriesBusyPort(t *testing.T) {
	occupied, err := net.ListenTCP("tcp4", &net.TCPAddr{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { occupied.Close() })
	port := uint16(occupied.Addr().(*net.TCPAddr).Port)
	useSlimProtoPort(t, port, nil)
	oldInterval := refreshInterval
	t.Cleanup(func() { refreshInterval = oldInterval })
	refreshInterval = 20 * time.Millisecond

	r := NewRegistry("", slog.New(slog.DiscardHandler), nil)
	if err := r.Start(t.Context()); err != nil || !r.PortInUse() {
		t.Fatalf("端口占用时启动，标记 %t，错误 %v", r.PortInUse(), err)
	}
	t.Cleanup(r.Stop)
	if err := occupied.Close(); err != nil {
		t.Fatal(err)
	}
	waitRegistry(t, func() bool { return !r.PortInUse() })
	p := slimtest.Dial(t, (&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(port)}).String())
	sendRegistryTestHello(t, p, 0)
}
