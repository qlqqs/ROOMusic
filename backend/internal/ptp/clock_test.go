package ptp

import (
	"math"
	"net/netip"
	"testing"
	"time"
)

func decodedArrival(t *testing.T, b []byte, at time.Time) arrival {
	t.Helper()
	p, ok := parse(b, b[0]&15 < 8)
	if !ok {
		t.Fatalf("拒绝了 PTP 报文 %x", b)
	}
	return arrival{packet: p, at: at}
}

func TestPTPWireCodec(t *testing.T) {
	id := portID{0x0123456789abcdef, 4}
	for _, tc := range []struct {
		kind    byte
		size    int
		control byte
	}{
		{msgSync, 44, 0}, {msgDelayReq, 44, 1}, {msgFollowUp, 44, 2},
		{msgDelayResp, 54, 3}, {msgAnnounce, 64, 5}, {msgSignaling, 54, 5},
	} {
		b := message(id, tc.kind, tc.size, 65535, twoStep|unicast, -3)
		fixedCorrection := int64(-14 << 16)
		be.PutUint64(b[8:], uint64(fixedCorrection))
		ns := int64(5_000_000_000_123_456_789)
		stamp(b[34:], ns)
		p, ok := parse(b, tc.kind < 8)
		if !ok || p.kind != tc.kind || p.source != id || p.seq != 65535 || p.flags != 0x600 ||
			p.correction != -14 || p.b[32] != tc.control || int8(p.b[33]) != -3 {
			t.Fatalf("PTP 类型 %d 解码 = %+v", tc.kind, p)
		}
		if got, ok := readStamp(p.b[34:]); !ok || got != ns || be.Uint16(p.b[34:]) != 1 {
			t.Fatalf("48 位时间戳解码 = %d，valid=%t", got, ok)
		}
		if _, ok := parse(b, tc.kind >= 8); ok {
			t.Fatalf("类型 %d 被错误端口接受", tc.kind)
		}
		for n := range len(b) {
			if _, ok := parse(b[:n], tc.kind < 8); ok {
				t.Fatalf("类型 %d 接受了截断到 %d 字节的报文", tc.kind, n)
			}
		}
	}
	b := make([]byte, 10)
	stamp(b, math.MaxInt64)
	if got, ok := readStamp(b); !ok || got != math.MaxInt64 {
		t.Fatalf("最大时间戳 = %d，valid=%t", got, ok)
	}
	be.PutUint32(b[6:], 1_000_000_000)
	if _, ok := readStamp(b); ok {
		t.Fatal("接受了跨秒的小数部分")
	}
	b = message(portID{}, msgSync, 44, 1, 0, 0)
	if _, ok := parse(b, true); ok {
		t.Fatal("接受了零源标识")
	}
}

func TestPTPFollowerOffsetCorrectionsAndReorderedFollowUp(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	local := portID{9, 2}
	remote := portID{17, 1}
	for _, tc := range []struct {
		name                        string
		forward, backward, expected int64
		reverse                     bool
	}{
		{"symmetric", 100_000, 100_000, 500_000, false},
		{"asymmetric, follow-up arrives first", 300_000, 100_000, 400_000, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var f follower
			f.reset(remote)
			for i := range 3 {
				at := t0.Add(time.Duration(i) * 150 * time.Millisecond)
				seq := uint16(65534 + i)
				makeSync := func(kind byte, correction int64) []byte {
					b := message(remote, kind, 44, seq, twoStep, -3)
					be.PutUint64(b[8:], uint64(correction<<16))
					return b
				}
				sync := decodedArrival(t, makeSync(msgSync, 300), at)
				follow := makeSync(msgFollowUp, 700)
				stamp(follow[34:], at.UnixNano()+500_000-tc.forward-1000)
				up := decodedArrival(t, follow, at.Add(time.Millisecond))
				if tc.reverse {
					f.sync(up)
					f.sync(sync)
				} else {
					f.sync(sync)
					f.sync(up)
				}
				f.delaySeq = seq
				f.delaySent = at.Add(5 * time.Millisecond)
				response := message(remote, msgDelayResp, 54, seq, unicast, -2)
				be.PutUint64(response[8:], 2000<<16)
				stamp(response[34:], f.delaySent.UnixNano()+500_000+tc.backward+2000)
				at = at.Add(10 * time.Millisecond)
				wrong := portID{local.clock + 1, local.port}
				wrong.put(response[44:])
				f.measure(decodedArrival(t, response, at), local)
				if f.samples != i {
					t.Fatal("采纳了另一个客户端的 Delay_Resp")
				}
				local.put(response[44:])
				reply := decodedArrival(t, response, at)
				f.measure(reply, local)
				f.measure(reply, local)
				status := f.status(local.clock, at)
				if f.samples != i+1 || status.Available != (i == 2) || status.OffsetNS != tc.expected ||
					status.Role != "follower" || status.GrandmasterID != remote.clock {
					t.Fatalf("第 %d 次对时 = %+v，samples=%d", i+1, status, f.samples)
				}
			}
			if got := f.status(local.clock, t0.Add(4*time.Second)); got.Available || got.Reason != "clock_unlocked" {
				t.Fatalf("过期后时钟状态 = %+v", got)
			}
		})
	}
}

func TestPTPSelectsGrandmasterAndSwitchesOnExpiry(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	l := link{index: 1, ip: netip.MustParseAddr("192.168.44.1"), id: portID{9, 1}}
	e := engine{local: l.id.clock, peers: make(map[netip.Addr]*peerClock)}
	a, b := netip.MustParseAddr("192.168.44.2"), netip.MustParseAddr("192.168.44.3")
	for _, tc := range []struct {
		ip       netip.Addr
		id       uint64
		priority byte
	}{
		{a, 42, 120}, {b, 43, 100},
	} {
		packet := announce(portID{tc.id, 1}, 7, 0)
		packet[47] = tc.priority
		p := decodedArrival(t, packet, now)
		p.peer, p.link = tc.ip, l
		e.received(p)
	}
	e.peers[b].wakeClock = 43
	if _, got := e.primary(now); got.GrandmasterID != 42 {
		t.Fatalf("无 Sync 的唤醒公告不应改变主钟，状态 %+v", got)
	}
	f := &e.peers[b].follower
	f.lastSync, f.samples = now, 3
	f.firstSample, f.lastSample, f.offset = now.Add(-time.Second), now, -150_000
	for _, tc := range []struct {
		when      time.Time
		id        uint64
		available bool
		role      string
	}{
		{now, 43, true, "follower"},
		{now.Add(3500 * time.Millisecond), 43, false, "follower"},
		{now.Add(5 * time.Second), 9, true, "master"},
	} {
		status, _ := e.clocks(tc.when)
		if status.GrandmasterID != tc.id || status.Available != tc.available || status.Role != tc.role {
			t.Fatalf("%s 时的全局时钟 = %+v", tc.when.Sub(now), status)
		}
	}
	e.peers[b].remoteUntil = now.Add(time.Second)
	if _, got := e.primary(now.Add(2 * time.Second)); got.GrandmasterID != 42 || got.Available {
		t.Fatalf("次优主钟未接替失效主钟：%+v", got)
	}
}

func TestPTPFollowerLateFollowUpDoesNotReplaceNewerSync(t *testing.T) {
	remote := portID{42, 1}
	t0 := time.Unix(1_700_000_000, 0)
	var f follower
	f.reset(remote)
	old := message(remote, msgSync, 44, 100, twoStep, -3)
	f.sync(decodedArrival(t, old, t0))

	latestAt := t0.Add(125 * time.Millisecond)
	const latestForward = 600_000
	sync := message(remote, msgSync, 44, 101, 0, -3)
	stamp(sync[34:], latestAt.UnixNano()+latestForward)
	f.sync(decodedArrival(t, sync, latestAt))

	late := message(remote, msgFollowUp, 44, 100, 0, -3)
	stamp(late[34:], t0.UnixNano()+50_000)
	f.sync(decodedArrival(t, late, latestAt.Add(time.Millisecond)))
	if !f.lastSync.Equal(latestAt) || f.forward != latestForward {
		t.Fatalf("旧序号的 Follow_Up 覆盖了新 Sync：相对收包时间 = %s，正向时间差 = %d ns，期望 125ms / %d ns",
			f.lastSync.Sub(t0), f.forward, latestForward)
	}
}
