package mdns

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

func TestBrowseRescansLinksAndReopensAfterReadFailure(t *testing.T) {
	l, loop := testLANLink(t)
	reserved := testMDNSResponder(t, netip.IPv4Unspecified(), 0)
	port := reserved.LocalAddr().(*net.UDPAddr).Port
	reserved.Close()
	oldList, oldOpen, oldInterval, oldPort := listLinks, openBrowseSocket, searchInterval, mdnsPort
	t.Cleanup(func() {
		listLinks, openBrowseSocket, searchInterval, mdnsPort = oldList, oldOpen, oldInterval, oldPort
	})
	searchInterval, mdnsPort = 100*time.Millisecond, port
	var active atomic.Pointer[link]
	emptyScan := make(chan struct{}, 2)
	listLinks = func(local netip.Addr) ([]link, *net.Interface, error) {
		if local == l.local {
			return []link{l}, loop, nil
		}
		if current := active.Load(); current != nil {
			return []link{*current}, loop, nil
		}
		select {
		case emptyScan <- struct{}{}:
		default:
		}
		return nil, loop, nil
	}
	opened := make(chan *socket, 4)
	openBrowseSocket = func(l link, loop *net.Interface) (*socket, error) {
		s, err := oldOpen(l, loop)
		if err == nil {
			opened <- s
		}
		return s, err
	}
	const service = "_roomusic-browse-test._tcp.local."
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	entries := make(chan Entry, 8)
	stop, err := Start(ctx, service, logger, func(e Entry) {
		select {
		case entries <- e:
		case <-ctx.Done():
		}
	})
	if err != nil {
		t.Fatalf("无网卡时启动浏览失败：%v", err)
	}
	defer stop()

	for range 2 {
		select {
		case <-emptyScan:
		case <-ctx.Done():
			t.Fatal("无网卡时没有继续扫描")
		}
	}
	select {
	case <-opened:
		t.Fatal("无网卡时打开了浏览套接字")
	default:
	}

	advertise := func(instance string) {
		done := make(chan error, 1)
		go func() {
			done <- Advertise(ctx, Entry{
				Service: service, Instance: instance, Host: "synthetic.local.", IP: l.local, Port: 8009,
			})
		}()
		t.Cleanup(func() {
			if err := <-done; err != nil {
				t.Errorf("测试服务 %s 公告失败：%v", instance, err)
			}
		})
	}
	waitSocket := func(want link) *socket {
		t.Helper()
		select {
		case s := <-opened:
			if s.link.local != want.local || s.link.iface.Index != want.iface.Index {
				t.Fatalf("新浏览套接字网卡 = %+v，期望 %+v", s.link, want)
			}
			return s
		case <-ctx.Done():
			t.Fatalf("下一轮未打开网卡 %d 的浏览套接字", want.iface.Index)
			return nil
		}
	}
	waitEntry := func(instance string) {
		t.Helper()
		for {
			select {
			case e := <-entries:
				if e.Instance != instance {
					continue
				}
				if e.IP != l.local || e.LocalIP != l.local || e.InterfaceIndex != l.iface.Index {
					t.Fatalf("浏览结果 = %+v", e)
				}
				return
			case <-ctx.Done():
				t.Fatalf("未发现测试服务 %s", instance)
			}
		}
	}

	advertise("synthetic-before-close")
	active.Store(&l)
	first := waitSocket(l)
	waitEntry("synthetic-before-close")
	if err := first.Close(); err != nil {
		t.Fatalf("模拟浏览读失败：%v", err)
	}
	reopened := waitSocket(l)
	advertise("synthetic-after-close")
	waitEntry("synthetic-after-close")

	changed := l
	changed.local = netip.MustParseAddr("192.168.255.254")
	if changed.local == l.local {
		changed.local = changed.local.Next()
	}
	active.Store(&changed)
	waitSocket(changed)
	if err := reopened.SetReadDeadline(time.Now()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("地址变化后旧浏览套接字仍可使用：%v", err)
	}
}

func TestLookupMatchReceivesMatchingReplyWithBrowserRunning(t *testing.T) {
	l, loop := testLANLink(t)
	server := testMDNSResponder(t, l.local, 0)
	oldList, oldPort := listLinks, mdnsPort
	t.Cleanup(func() { listLinks, mdnsPort = oldList, oldPort })
	listLinks = func(netip.Addr) ([]link, *net.Interface, error) { return []link{l}, loop, nil }
	mdnsPort = server.LocalAddr().(*net.UDPAddr).Port
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	stop, err := Start(t.Context(), lookupTestService, logger, func(Entry) {})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	assertLegacyUnicastLookup(t, server, l.local)
}

func TestLinkForTargetPrefersMostSpecificSubnet(t *testing.T) {
	links := []link{
		{local: netip.MustParseAddr("192.168.1.1"), prefix: netip.MustParsePrefix("192.168.1.0/24")},
		{local: netip.MustParseAddr("10.0.0.1"), prefix: netip.MustParsePrefix("10.0.0.0/8")},
		{local: netip.MustParseAddr("10.20.30.1"), prefix: netip.MustParsePrefix("10.20.30.0/24")},
	}
	target := netip.MustParseAddr("10.20.30.44")
	if got := linkForTarget(links, target); got.local != links[2].local {
		t.Fatalf("目标 %v 使用本机地址 %v，期望 %v", target, got.local, links[2].local)
	}
}
