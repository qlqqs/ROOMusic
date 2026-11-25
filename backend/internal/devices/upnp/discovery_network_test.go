package upnp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/fakerenderer"
	"golang.org/x/sys/unix"
)

func TestRegistryDiscoversAfterLANInterfaceAppears(t *testing.T) {
	index, ip := testSSDPInterface(t)
	const udn = "uuid:roomusic-ssdp-rescan"
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	location := serveSSDPTestDescription(t, ip, udn)
	group := netip.MustParseAddrPort(ssdpGroup)
	addr := netip.AddrPortFrom(ip, group.Port())
	config := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
		var err error
		if controlErr := raw.Control(func(fd uintptr) {
			err = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
		}); controlErr != nil {
			return controlErr
		}
		return err
	}}
	socket, err := config.ListenPacket(ctx, "udp4", addr.String())
	if errors.Is(err, syscall.EADDRINUSE) {
		t.Skipf("SSDP 测试端口已被占用：%v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	conn := socket.(*net.UDPConn)
	defer conn.Close()

	oldList, oldInterval := listLinks, searchInterval
	t.Cleanup(func() { listLinks, searchInterval = oldList, oldInterval })
	searchInterval = 100 * time.Millisecond
	var available atomic.Bool
	listLinks = func() (map[int]netip.Addr, error) {
		if available.Load() {
			return map[int]netip.Addr{index: ip}, nil
		}
		return nil, nil
	}
	r := NewRegistry()
	if err := r.Start(ctx, nil); err != nil {
		t.Fatalf("无网卡时启动 SSDP 失败：%v", err)
	}
	defer r.Stop()

	buf := make([]byte, 8192)
	readSearch := func(deadline time.Time) (netip.AddrPort, error) {
		t.Helper()
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatal(err)
		}
		for {
			n, peer, err := conn.ReadFromUDPAddrPort(buf)
			if err != nil {
				return netip.AddrPort{}, err
			}
			if peer.Addr() == ip && strings.Contains(string(buf[:n]), "HOST: "+addr.String()+"\r\n") &&
				strings.HasPrefix(string(buf[:n]), "M-SEARCH * HTTP/1.1\r\n") {
				return peer, nil
			}
		}
	}
	if peer, err := readSearch(time.Now().Add(2 * searchInterval)); err == nil {
		t.Fatalf("无网卡时收到 M-SEARCH，来源 %v", peer)
	} else {
		var timeout net.Error
		if !errors.As(err, &timeout) || !timeout.Timeout() {
			t.Fatal(err)
		}
	}
	available.Store(true)
	deadline, _ := ctx.Deadline()
	peer, err := readSearch(deadline)
	if err != nil {
		t.Fatalf("网卡出现后未收到 M-SEARCH：%v", err)
	}
	if _, err := conn.WriteToUDPAddrPort(ssdpTestReply(udn, location), peer); err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if entry, ok := r.Lookup(udn); ok && entry.Online {
			if entry.Device.LocalAddress != ip.String() {
				t.Fatalf("发现设备的本机地址 = %q，期望 %v", entry.Device.LocalAddress, ip)
			}
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("收到 M-SEARCH 应答后，设备 %s 未上线", udn)
		}
	}
}

func TestSSDPRescanReplacesSocketsAfterAddressChangeAndReadFailure(t *testing.T) {
	const udn = "uuid:roomusic-ssdp-reopen"
	index, addr := testSSDPInterface(t)
	oldList := listLinks
	t.Cleanup(func() { listLinks = oldList })
	listLinks = func() (map[int]netip.Addr, error) { return map[int]netip.Addr{index: addr}, nil }
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	received := make(chan announcement, 1)
	network := ssdpNetwork{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		links:  make(map[int]*ssdpLink),
		receive: func(a announcement) {
			if a.udn == udn {
				select {
				case received <- a:
				default:
				}
			}
		},
	}
	defer func() {
		for _, l := range network.links {
			l.close()
		}
	}()
	rescan := func() *ssdpLink {
		t.Helper()
		if err := network.rescan(ctx); err != nil {
			t.Fatalf("扫描 SSDP 网卡失败：%v", err)
		}
		l := network.links[index]
		if l == nil || l.search.LocalAddr().(*net.UDPAddr).AddrPort().Addr() != addr {
			t.Fatalf("搜索套接字未绑定到 %v", addr)
		}
		return l
	}
	assertClosed := func(l *ssdpLink) {
		t.Helper()
		for _, conn := range []*net.UDPConn{l.notify, l.search} {
			if err := conn.SetReadDeadline(time.Now()); !errors.Is(err, net.ErrClosed) {
				t.Fatalf("旧 SSDP 套接字仍可使用：%v", err)
			}
		}
	}

	first := rescan()
	ip := addr
	addr = netip.MustParseAddr("127.0.0.1")
	changed := rescan()
	assertClosed(first)
	if err := changed.notify.Close(); err != nil {
		t.Fatalf("模拟 SSDP 通知读失败：%v", err)
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for !changed.stopped.Load() {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("SSDP 读循环未报告套接字关闭")
		}
	}
	reopened := rescan()
	assertClosed(changed)
	notify, err := reopened.notify.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	if err := notify.Control(func(uintptr) {}); err != nil {
		t.Fatalf("重开的 SSDP 通知套接字不可用：%v", err)
	}

	// 把合成应答发给重开的搜索套接字，确认其读协程也已恢复。
	peer, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(ip, 0)))
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	location := "http://" + ip.String() + "/description.xml"
	if _, err := peer.WriteToUDP(ssdpTestReply(udn, location), reopened.search.LocalAddr().(*net.UDPAddr)); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-received:
		if got.local != addr || got.source != ip || got.location != location {
			t.Fatalf("重开后的 SSDP 应答 = %+v", got)
		}
	case <-ctx.Done():
		t.Fatal("重开后未收到 SSDP 应答")
	}
}

func testSSDPInterface(t *testing.T) (int, netip.Addr) {
	t.Helper()
	links, err := lanLinks()
	if err != nil {
		t.Fatal(err)
	}
	first := 0
	for index := range links {
		if first == 0 || index < first {
			first = index
		}
	}
	if first == 0 {
		t.Skip("测试需要局域网 IPv4 组播接口")
	}
	return first, links[first]
}

func serveSSDPTestDescription(t *testing.T, ip netip.Addr, udn string) string {
	t.Helper()
	renderer, err := fakerenderer.New(fakerenderer.Options{UDN: udn})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(renderer.Close)
	listener, err := net.Listen("tcp4", netip.AddrPortFrom(ip, 0).String())
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: renderer.Handler()}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Close()
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("合成设备描述服务器：%v", err)
		}
	})
	return "http://" + listener.Addr().String() + "/description.xml"
}

func ssdpTestReply(udn, location string) []byte {
	return []byte(fmt.Sprintf("HTTP/1.1 200 OK\r\nST: %s\r\nUSN: %s::%s\r\nLOCATION: %s\r\nCache-Control: max-age=30\r\n\r\n",
		testRendererTarget, udn, testRendererTarget, location))
}
