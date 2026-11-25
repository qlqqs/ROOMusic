package mdns

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"syscall"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/sys/unix"
)

func TestLookupMatchUsesEphemeralSourcePort(t *testing.T) {
	l, _ := testLANLink(t)
	server := testMDNSResponder(t, l.local, 5353)
	assertLegacyUnicastLookup(t, server, l.local)
}

func testLANLink(t *testing.T) (link, *net.Interface) {
	t.Helper()
	links, loop, err := lanLinks(netip.Addr{})
	if err != nil {
		t.Fatal(err)
	}
	if len(links) == 0 {
		t.Skip("测试需要局域网 IPv4 组播接口")
	}
	return links[0], loop
}

func testMDNSResponder(t *testing.T, ip netip.Addr, port uint16) *net.UDPConn {
	t.Helper()
	config := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
		var err error
		if controlErr := raw.Control(func(fd uintptr) {
			err = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
		}); controlErr != nil {
			return controlErr
		}
		return err
	}}
	server, err := config.ListenPacket(t.Context(), "udp4", netip.AddrPortFrom(ip, port).String())
	if port == 5353 && errors.Is(err, syscall.EADDRINUSE) {
		t.Skipf("默认 mDNS 端口已被占用：%v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	return server.(*net.UDPConn)
}

const lookupTestService = "_roomusic-unicast-test._tcp.local."

func assertLegacyUnicastLookup(t *testing.T, server *net.UDPConn, ip netip.Addr) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	deadline, _ := ctx.Deadline()
	if err := server.SetReadDeadline(deadline); err != nil {
		cancel()
		t.Fatal(err)
	}
	var got Entry
	var lookupErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		got, lookupErr = LookupMatch(ctx, lookupTestService, ip, func(Entry) bool { return true })
	}()
	defer func() { cancel(); <-done }()

	var query dnsmessage.Message
	var peer netip.AddrPort
	buf := make([]byte, 9000)
	for {
		n, addr, err := server.ReadFromUDPAddrPort(buf)
		if err != nil {
			t.Fatalf("假响应端未收到定向查询：%v", err)
		}
		if query.Unpack(buf[:n]) == nil && !query.Response && len(query.Questions) == 1 &&
			query.Questions[0].Name.String() == lookupTestService {
			peer = addr
			break
		}
	}
	sharedPort := server.LocalAddr().(*net.UDPAddr).AddrPort().Port()
	if peer.Port() == sharedPort {
		t.Fatalf("定向查询使用了共享源端口 UDP %d", sharedPort)
	}
	if peer.Addr() != ip || query.ID == 0 {
		t.Fatalf("定向查询来源 = %v，事务 ID = %d", peer, query.ID)
	}
	// 向同一个查询源地址依次发送完整的错误 ID 和正确 ID 应答。
	for _, reply := range []struct {
		id       uint16
		instance string
	}{
		{query.ID ^ 1, "wrong-id"},
		{query.ID, "matching-id"},
	} {
		msg, err := announcement(Entry{
			Service: lookupTestService, Instance: reply.instance, Host: "synthetic.local.", IP: ip, Port: 8009,
		}, 120)
		if err != nil {
			t.Fatal(err)
		}
		msg.ID, msg.Questions = reply.id, query.Questions
		data, err := msg.Pack()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := server.WriteToUDPAddrPort(data, peer); err != nil {
			t.Fatal(err)
		}
	}
	<-done
	if lookupErr != nil || got.Instance != "matching-id" || got.IP != ip || got.LocalIP != ip || got.Port != 8009 {
		t.Fatalf("定向查询结果 = %+v，错误 = %v", got, lookupErr)
	}
}
