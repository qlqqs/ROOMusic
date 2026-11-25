package slimproto

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"
)

func TestSlimProtoDiscoveryAllowedPeer(t *testing.T) {
	for _, tc := range []struct {
		ip   string
		want bool
	}{
		{"10.0.0.12", true}, {"172.16.2.18", true}, {"192.168.0.4", true},
		{"169.254.1.1", true}, {"127.0.0.2", true},
		{"8.8.8.8", false}, {"100.64.0.1", false}, {"224.0.0.251", false},
		{"255.255.255.255", false}, {"0.0.0.0", false}, {"::1", false}, {"fd00::1", false},
	} {
		if got := allowedPeer(netip.MustParseAddr(tc.ip)); got != tc.want {
			t.Errorf("发现来源 %s 的许可 = %t，期望 %t", tc.ip, got, tc.want)
		}
	}
}

func TestSlimProtoDiscoveryRepliesOnIncomingLink(t *testing.T) {
	client, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	probe, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		t.Fatal(err)
	}
	port := uint16(probe.LocalAddr().(*net.UDPAddr).Port)
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	first := link{index: 1, prefix: netip.MustParsePrefix("127.0.0.1/8")}
	second := link{index: 2, prefix: netip.MustParsePrefix("127.0.0.2/8")}
	useSlimProtoPort(t, port, []link{first, second})
	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	d := newDiscovery(ctx, "ROOMusic é库", slog.New(slog.DiscardHandler), &wg)
	t.Cleanup(func() { cancel(); d.close(); wg.Wait() })
	if err := d.refresh(); err != nil {
		t.Fatal(err)
	}
	peer := client.LocalAddr().(*net.UDPAddr).AddrPort()
	local := netip.AddrPortFrom(second.prefix.Addr(), port)
	checkReply := func(want []byte) {
		t.Helper()
		if err := client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatal(err)
		}
		var buf [32]byte
		n, from, err := client.ReadFromUDPAddrPort(buf[:])
		if err != nil || from != local || !bytes.Equal(buf[:n], want) {
			t.Fatalf("发现应答 = %x，来源 %s，错误 %v，期望 %x，来源 %s", buf[:n], from, err, want, local)
		}
	}
	if _, err := client.WriteToUDPAddrPort([]byte{'e'}, local); err != nil {
		t.Fatal(err)
	}
	select {
	case packet := <-d.packets:
		d.respond(packet)
	case <-time.After(2 * time.Second):
		t.Fatal("未收到环回发现请求")
	}
	checkReply([]byte{'E'})

	// 注入广播接收得到的网卡索引，使用真实 UDP 套接字核对回复的源地址。
	d.respond(discoveryPacket{socket: d.broadcast, iface: second.index, kind: 'd', peer: peer})
	want := make([]byte, 18)
	copy(want, "DROOMusic \xe9?")
	checkReply(want)
}
