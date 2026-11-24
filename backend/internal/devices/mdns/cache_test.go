package mdns

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func TestCastMDNSRecordResolutionAndExpiry(t *testing.T) {
	const service = "_googlecast._tcp.local."
	ip := netip.MustParseAddr("192.168.44.2")
	local := netip.MustParseAddr("192.168.44.1")
	now := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)
	message, err := announcement(Entry{
		Service: service, Instance: "SYNTHETIC", Host: "Cast-Host.local.", IP: ip, Port: 8009,
		TXT: map[string]string{"ID": "test-id", "fn": "合成客厅=音箱"},
	}, 120)
	if err != nil {
		t.Fatal(err)
	}
	message.Answers[3].Header.TTL = 2
	message.Additionals, message.Answers = message.Answers[1:], message.Answers[:1]
	wire, err := message.Pack()
	if err != nil {
		t.Fatal(err)
	}
	var decoded dnsmessage.Message
	if err := decoded.Unpack(wire); err != nil {
		t.Fatal(err)
	}
	cache := newPeerCache()
	l := link{iface: net.Interface{Index: 8}, local: local}
	var entries []Entry
	receive := func(e Entry) { entries = append(entries, e) }

	cache.ingest(service, ip, decoded.Answers, now)
	q := cache.resolve(service, l, now, receive)
	if len(entries) != 0 || len(q) != 2 || q[0].Type != dnsmessage.TypeTXT || q[1].Type != dnsmessage.TypeSRV {
		t.Fatalf("仅收到 PTR 后结果 = %+v，补查 = %+v", entries, q)
	}
	cache.ingest(service, ip, decoded.Additionals[:2], now)
	q = cache.resolve(service, l, now, receive)
	if len(entries) != 0 || len(q) != 1 || q[0].Type != dnsmessage.TypeA {
		t.Fatalf("缺少 A 记录时结果 = %+v，补查 = %+v", entries, q)
	}
	cache.ingest(service, netip.MustParseAddr("192.168.44.3"), decoded.Additionals[2:], now)
	cache.resolve(service, l, now, receive)
	if len(entries) != 0 {
		t.Fatalf("A 记录地址与来源不符仍发布了 %+v", entries)
	}
	cache.ingest(service, ip, decoded.Additionals[2:], now)
	cache.resolve(service, l, now, receive)
	if len(entries) != 1 {
		t.Fatalf("完整应答发布了 %d 条记录", len(entries))
	}
	got := entries[0]
	if got.Service != service || got.Instance != "SYNTHETIC" || got.Host != "Cast-Host.local." ||
		got.Port != 8009 || got.IP != ip || got.LocalIP != local || got.InterfaceIndex != 8 ||
		got.TXT["id"] != "test-id" || got.TXT["fn"] != "合成客厅=音箱" || !got.Expires.Equal(now.Add(2*time.Second)) {
		t.Fatalf("完整应答 = %+v", got)
	}
	cache.resolve(service, l, now, receive)
	if len(entries) != 1 {
		t.Fatal("相同发现结果被重复发布")
	}

	t.Run("multi_interface", func(t *testing.T) {
		other := newPeerCache()
		otherLocal := netip.MustParseAddr("10.22.0.1")
		b := browser{service: service, socket: &socket{links: map[int]link{
			8: l, 12: {iface: net.Interface{Index: 12}, local: otherLocal},
		}}}
		other.ingest(service, ip, decoded.Answers, now)
		other.ingest(service, ip, decoded.Additionals, now)
		var result Entry
		b.resolve(other, source{ip: ip, idx: 12}, now, func(e Entry) { result = e })
		if result.InterfaceIndex != 12 || result.LocalIP != otherLocal || result.IP != ip {
			t.Fatalf("第二张网卡的发现结果 = %+v", result)
		}
		goodbye := decoded.Answers[0]
		goodbye.Header.TTL = 0
		other.ingest(service, ip, []dnsmessage.Resource{goodbye}, now)
		b.resolve(other, source{ip: ip, idx: 12}, now, func(e Entry) { result = e })
		if !result.Expires.Equal(now) {
			t.Fatalf("TTL 0 后设备仍到 %v 才离线", result.Expires)
		}
		cache.resolve(service, l, now, receive)
		if len(entries) != 1 {
			t.Fatal("第二张网卡下线影响了第一张网卡的缓存")
		}
	})

	expiry := now.Add(2 * time.Second)
	cache.prune(expiry)
	cache.resolve(service, l, expiry, receive)
	if len(entries) != 2 || entries[1].IP != ip || !entries[1].Expires.Equal(expiry) {
		t.Fatalf("A 记录过期后的发布 = %+v", entries)
	}
}

func TestCastMDNSIgnoresMalformedDatagrams(t *testing.T) {
	links, loop, err := lanLinks(netip.Addr{})
	if err != nil {
		t.Fatal(err)
	}
	if len(links) == 0 {
		t.Skip("测试需要局域网 IPv4 组播接口")
	}
	l := links[0]
	s, err := openSocket(l, loop)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	const service = "_roomusic-dns-test._tcp.local."
	b, err := prepare(service, netip.Addr{})
	if err != nil {
		t.Fatal(err)
	}
	b.socket = s
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	entries := make(chan Entry, 1)
	done := make(chan error, 1)
	go func() {
		done <- b.run(ctx, func(e Entry) {
			select {
			case entries <- e:
			default:
			}
		}, time.Minute)
	}()
	defer func() { cancel(); <-done }()
	msg, err := announcement(Entry{Service: service, Instance: "synthetic", Host: "synthetic.local.", IP: l.local, Port: 8009}, 120)
	if err != nil {
		t.Fatal(err)
	}
	valid, err := msg.Pack()
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{{0, 0, 0x80}, valid[:len(valid)-1], valid} {
		if err := s.multicast(data, l); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case e := <-entries:
		if e.Instance != "synthetic" || e.IP != l.local || e.InterfaceIndex != l.iface.Index {
			t.Fatalf("畸形包后的有效发现 = %+v", e)
		}
	case <-ctx.Done():
		t.Fatal("畸形包后未收到有效的 mDNS 发现")
	}
}
