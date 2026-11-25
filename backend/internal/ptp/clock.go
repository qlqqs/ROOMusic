package ptp

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"maps"
	"math"
	"net/netip"
	"sync"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/mdns"
)

var linkScanInterval = 30 * time.Second
var errLinksChanged = errors.New("PTP 局域网网卡已变化")

type peerClock struct {
	link                              link
	expires, remoteUntil, subscribeAt time.Time
	expectsMaster                     bool
	wakeClock                         uint64
	follower                          follower
}

// 唤醒请求与主钟公告都用 Announce。同一地址真正提供 Sync 后仍可成为上游。
func (p *peerClock) awaitingWakeSync() bool {
	f := &p.follower
	return p.wakeClock != 0 && f.source.clock == p.wakeClock && f.grandmaster == p.wakeClock && f.lastSync.IsZero()
}

type engine struct {
	*wire
	local          uint64
	peers          map[netip.Addr]*peerClock
	seq, signalSeq uint16
}

func (e *engine) peer(ip netip.Addr, l link) *peerClock {
	if p := e.peers[ip]; p != nil {
		return p
	}
	if len(e.peers) >= 1024 {
		return nil
	}
	p := &peerClock{link: l}
	e.peers[ip] = p
	return p
}

func (e *engine) discovered(d mdns.Entry) {
	l, ok := e.links[d.InterfaceIndex]
	if !ok || !d.Expires.After(time.Now()) {
		delete(e.peers, d.IP)
		return
	}
	p := e.peer(d.IP, l)
	if p == nil {
		return
	}
	p.link, p.expires = l, d.Expires
	// 行为参考 saundo 的真机协议记录：单台 HomePod 和 Mac 提供主钟，组合音箱接收主钟。
	profile := ReceiverProfile(d.TXT["model"])
	p.expectsMaster = profile.ProvidesClock && d.TXT["tsid"] == ""
	p.wakeClock = profile.wakeClockID(d.TXT["deviceid"])
}

func (e *engine) localStatus() Status {
	return Status{Available: true, Role: "master", ClockID: e.local, GrandmasterID: e.local}
}

// 尚未收到第一组完整测量的优先主钟也参与选择，锁定前停止发布旧时间轴。
func (e *engine) primary(now time.Time) (*peerClock, Status) {
	var best *peerClock
	for _, p := range e.peers {
		if !now.Before(p.expires) || !now.Before(p.remoteUntil) || p.follower.grandmaster == 0 || p.awaitingWakeSync() {
			continue
		}
		if best == nil || precedes(&p.follower, &best.follower) {
			best = p
		}
	}
	if best == nil {
		return nil, e.localStatus()
	}
	return best, best.follower.status(e.local, now)
}

func precedes(a, b *follower) bool {
	if d := bytes.Compare(a.rank[:], b.rank[:]); d != 0 {
		return d < 0
	}
	if a.steps != b.steps {
		return a.steps < b.steps
	}
	return a.source.clock < b.source.clock
}
func (e *engine) clocks(now time.Time) (Status, map[netip.Addr]Status) {
	_, global := e.primary(now)
	statuses := make(map[netip.Addr]Status, len(e.peers))
	for ip, p := range e.peers {
		if now.After(p.expires) {
			delete(e.peers, ip)
			continue
		}
		s := global
		if p.expectsMaster || now.Before(p.remoteUntil) && !p.awaitingWakeSync() {
			s = p.follower.status(e.local, now)
		}
		statuses[ip] = s
	}
	return global, statuses
}

func (e *engine) signal(p arrival, c *peerClock) {
	target := readID(p.b[34:])
	if target.clock != math.MaxUint64 && target.clock != e.local {
		return
	}
	for rest := p.b[44:]; len(rest) >= 4; {
		kind, n := be.Uint16(rest), int(be.Uint16(rest[2:]))
		if n > len(rest)-4 {
			return
		}
		v := rest[4 : 4+n]
		rest = rest[4+n:]
		switch kind {
		case 4:
			if n != 6 || (v[0]>>4 != msgAnnounce && v[0]>>4 != msgSync && v[0]>>4 != msgDelayResp) {
				continue
			}
			reply := message(p.link.id, msgSignaling, 56, p.seq, unicast, 0x7f)
			p.source.put(reply[34:])
			be.PutUint16(reply[44:], 5)
			be.PutUint16(reply[46:], 8)
			copy(reply[48:], v)
			if v[0]>>4 == msgSync {
				reply[49] = 0xfd
			} else if v[0]>>4 == msgAnnounce {
				reply[49] = 0
			}
			reply[55] = 1
			e.send(p.link, p.peer, reply)
		case 5:
			if n == 8 && be.Uint32(v[2:]) > 0 && (v[0]>>4 == msgSync || v[0]>>4 == msgAnnounce) {
				c.remoteUntil = p.at.Add(4 * time.Second)
				if c.follower.source != p.source {
					c.follower.reset(p.source)
				}
			}
		}
	}
}

func (e *engine) received(p arrival) {
	c := e.peer(p.peer, p.link)
	if c == nil {
		return
	}
	if until := p.at.Add(12 * time.Second); until.After(c.expires) {
		c.expires = until
	}
	clock := &c.follower
	switch p.kind {
	case msgAnnounce:
		// 跨网卡比较优先级、时钟质量及主钟标识；本机以自由运行等级参选。
		rank := p.b[47:61]
		local := announce(p.link.id, 0, 0)
		if be.Uint64(rank[6:]) == 0 || be.Uint64(rank[6:]) == e.local || be.Uint16(p.b[61:]) >= 255 || (!c.expectsMaster && bytes.Compare(rank, local[47:61]) >= 0) {
			return
		}
		if clock.source != p.source || clock.grandmaster != be.Uint64(rank[6:]) {
			clock.reset(p.source)
		}
		copy(clock.rank[:], rank)
		clock.grandmaster = be.Uint64(rank[6:])
		c.remoteUntil = p.at.Add(4 * time.Second)
		clock.steps = be.Uint16(p.b[61:])
		clock.timeSource = p.b[63]
	case msgSync, msgFollowUp:
		if clock.source != p.source {
			if !c.expectsMaster && p.at.After(c.remoteUntil) {
				return
			}
			if clock.source.clock != 0 {
				return
			}
			clock.reset(p.source)
		}
		clock.sync(p)
		c.remoteUntil = p.at.Add(4 * time.Second)
	case msgDelayResp:
		if clock.source == p.source {
			clock.measure(p, p.link.id)
		}
	case msgDelayReq:
		_, domain := e.primary(p.at)
		if !domain.Available {
			return
		}
		reply := message(p.link.id, msgDelayResp, 54, p.seq, p.flags&unicast, 0)
		stamp(reply[34:], p.at.UnixNano()+domain.OffsetNS)
		p.source.put(reply[44:])
		copy(reply[8:16], p.b[8:16])
		e.send(p.link, p.peer, reply)
	case msgPdelayReq:
		reply := message(p.link.id, msgPdelayResp, 54, p.seq, unicast|twoStep, 0x7f)
		stamp(reply[34:], p.at.UnixNano())
		p.source.put(reply[44:])
		if e.send(p.link, p.peer, reply) == nil {
			at := time.Now().UnixNano()
			follow := message(p.link.id, msgPdelayFollowUp, 54, p.seq, unicast, 0x7f)
			stamp(follow[34:], at)
			p.source.put(follow[44:])
			e.send(p.link, p.peer, follow)
		}
	case msgSignaling:
		e.signal(p, c)
	}
}

func (e *engine) transmitClock(l link, addr netip.Addr, flags uint16, upstream *peerClock, domain Status) {
	if !domain.Available {
		return
	}
	if e.seq%8 == 0 {
		adv := announce(l.id, e.seq/8, flags)
		if upstream != nil {
			f := &upstream.follower
			if f.timeSource != 0 {
				copy(adv[47:61], f.rank[:])
				adv[63] = f.timeSource
			}
			be.PutUint64(adv[53:], f.grandmaster)
			be.PutUint16(adv[61:], f.steps+1)
		}
		e.send(l, addr, adv)
	}
	sync := message(l.id, msgSync, 44, e.seq, flags|twoStep, -3)
	if e.send(l, addr, sync) != nil {
		return
	}
	ts := time.Now().UnixNano()
	follow := message(l.id, msgFollowUp, 44, e.seq, flags, -3)
	stamp(follow[34:], ts+domain.OffsetNS)
	e.send(l, addr, follow)
}

func (e *engine) tick(now time.Time) {
	e.seq++
	primary, domain := e.primary(now)
	for addr, p := range e.peers {
		if !p.expires.After(now) {
			delete(e.peers, addr)
			continue
		}
		f := &p.follower
		remote := p.expectsMaster || now.Before(p.remoteUntil)
		if remote {
			if !now.Before(p.subscribeAt) {
				for _, sub := range []struct {
					kind     byte
					interval int8
				}{{msgAnnounce, 0}, {msgSync, -3}, {msgDelayResp, -2}} {
					e.signalSeq++
					target := f.source
					if target.clock == 0 {
						target = portID{math.MaxUint64, math.MaxUint16}
					}
					e.send(p.link, addr, subscription(p.link.id, target, e.signalSeq, sub.kind, sub.interval))
				}
				p.subscribeAt = now.Add(time.Second)
				if f.status(e.local, now).Available {
					p.subscribeAt = now.Add(20 * time.Second)
				}
			}
			if !f.lastSync.IsZero() && now.Sub(f.lastSync) < 3*time.Second && e.seq%2 == 0 {
				f.delaySeq++
				req := message(p.link.id, msgDelayReq, 44, f.delaySeq, unicast, 0x7f)
				f.delaySent = time.Now()
				e.send(p.link, addr, req)
			}
		}
		if !remote || p.awaitingWakeSync() {
			e.transmitClock(p.link, addr, unicast, primary, domain)
		}
	}
	for index, l := range e.links {
		if primary == nil || primary.link.index != index {
			e.transmitClock(l, multicast, 0, primary, domain)
		}
	}
}

func (e *engine) serve(parent context.Context, state *state, logger *slog.Logger) error {
	ctx, cancel := context.WithCancel(parent)
	packets := make(chan arrival, 64)
	discovery := make(chan mdns.Entry, 32)
	failures := make(chan error, 2)
	var readers sync.WaitGroup
	e.listen(ctx, &readers, packets, failures)
	defer func() { cancel(); e.close(); readers.Wait() }()
	stop, err := mdns.Start(ctx, "_airplay._tcp.local.", logger, func(d mdns.Entry) {
		select {
		case discovery <- d:
		case <-ctx.Done():
		}
	})
	if err == nil {
		defer func() { cancel(); stop() }()
	}
	scan := time.NewTicker(linkScanInterval)
	defer scan.Stop()
	tick := time.NewTicker(125 * time.Millisecond)
	defer tick.Stop()
	state.publish(e.localStatus(), nil)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-failures:
			return err
		case <-scan.C:
			interfaces, err := listLinks()
			if err == nil && !maps.Equal(interfaces, e.interfaces) {
				return errLinksChanged
			}
		case p := <-packets:
			e.received(p)
		case d := <-discovery:
			e.discovered(d)
		case now := <-tick.C:
			e.tick(now)
			status, peers := e.clocks(now)
			state.publish(status, peers)
		}
	}
}
