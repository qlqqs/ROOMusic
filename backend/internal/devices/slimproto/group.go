package slimproto

import (
	"errors"
	"sync"
	"time"

	"github.com/qlqq/roomusic/backend/internal/zones"
)

type groupCoordinator struct {
	*outputSession
	leader, reference                        *groupPeer
	peers                                    map[string]*groupPeer
	buffering, resumePending                 bool
	releasing                                sync.WaitGroup
	readyBy, startAfter, decodeBy, nextCheck time.Time
}

const (
	groupTimeout  = 8 * time.Second
	warmInterval  = 250 * time.Millisecond
	startLead     = 300 * time.Millisecond
	pointLifetime = 3 * time.Second
)

func newGroup(s *outputSession) *groupCoordinator {
	now := time.Now()
	s.Player.groupControl(true)
	p := &groupPeer{GroupMember: zones.GroupMember{DeviceID: s.DeviceID}, MemberLease: MemberLease{Player: s.Player},
		current: s.current, next: s.next, awaiting: s.awaitingConnect, attached: now, warmUntil: now.Add(2 * time.Second)}
	return &groupCoordinator{outputSession: s, leader: p, reference: p, peers: map[string]*groupPeer{s.DeviceID: p}}
}

func (g *groupCoordinator) streamInfo(seq int64) (zones.GroupStreamInfo, error) {
	info, err := g.Zones.ManagedGroupStream(g.UserID, g.SessionID, seq)
	if !errors.Is(err, zones.ErrStreamGone) {
		return info, err
	}
	// 正在播放时首次编组，原句柄只有主设备的令牌。
	single, err := g.Zones.ManagedStream(g.UserID, g.SessionID, seq)
	if err != nil {
		return zones.GroupStreamInfo{}, err
	}
	return zones.GroupStreamInfo{Members: []zones.GroupMemberStream{{DeviceID: g.DeviceID, Path: single.Path, Delivered: single.Delivered}}, DurationSeconds: single.DurationSeconds}, nil
}

func (g *groupCoordinator) reset(opening map[string]bool) error {
	g.current, g.next = nil, nil
	for _, p := range g.peers {
		if p.current != nil && !opening[p.DeviceID] {
			if err := g.setState(p, "waiting"); err != nil {
				return err
			}
			if err := p.Player.command("strm", control('q')); err != nil {
				if err = g.drop(p, "offline"); err != nil {
					return err
				}
			}
		}
		p.current, p.next = nil, nil
		p.ready, p.ended, p.paused = false, false, false
		p.joinBy, p.scheduled = time.Time{}, time.Time{}
		p.point, p.corrected, p.elapsed = time.Time{}, time.Time{}, 0
	}
	g.seconds, g.elapsedOffset, g.anchor = 0, 0, time.Time{}
	g.buffering, g.resumePending = false, false
	g.controlIntent, g.state = "", ""
	g.decodeBy = time.Time{}
	g.reference = g.leader
	return nil
}

func (g *groupCoordinator) open(stream *zones.Stream, prefetch bool) error {
	info, err := g.streamInfo(stream.Seq)
	if err != nil {
		return err
	}
	now := time.Now()
	if !prefetch {
		opening := make(map[string]bool, len(info.Members))
		for _, r := range info.Members {
			opening[r.DeviceID] = !r.Dropped
		}
		if err := g.reset(opening); err != nil {
			return err
		}
		g.buffering, g.readyBy = true, now.Add(groupTimeout)
		g.state, g.controlIntent = "loading", "play"
		g.startAfter = now.Add(warmInterval)
	}
	rate := 0
	if stream.Format != nil {
		rate = stream.Format.SampleRate
	}
	for _, reader := range info.Members {
		p := g.peers[reader.DeviceID]
		if p == nil {
			continue
		}
		if p.Player == nil {
			if err := g.bind(p, true); err != nil {
				return err
			}
			if p.Player == nil {
				continue
			}
		}
		if reader.Dropped {
			if err := g.drop(p, "dropped"); err != nil {
				return err
			}
			continue
		}
		auto := byte('1')
		joining := prefetch && p.current == nil
		if !prefetch || joining {
			auto = '0'
			p.warmUntil = now.Add(2 * time.Second)
			if err := g.poll(p, now); err != nil {
				return err
			}
			if p.Player == nil {
				continue
			}
			if err := p.Player.command("strm", control('q')); err != nil {
				if err = g.drop(p, "offline"); err != nil {
					return err
				}
				continue
			}
		}
		frame, err := startStream(p.Player.local, g.port, reader.Path, rate, auto, 255)
		if err != nil {
			return err
		}
		if err = p.Player.send(frame); err != nil {
			if err = g.drop(p, "offline"); err != nil {
				return err
			}
			continue
		}
		p.awaiting++
		opened := &outputStream{seq: stream.Seq, path: reader.Path}
		if prefetch && !joining {
			p.next = opened
		} else {
			p.current = opened
			p.ready, p.paused, p.ended = false, false, false
			p.scheduled, p.point = time.Time{}, time.Time{}
			if joining {
				p.joinBy = now.Add(groupTimeout)
			}
			if err := g.setState(p, "joining"); err != nil {
				return err
			}
		}
	}
	if g.reference.current == nil {
		for _, p := range g.peers {
			if p.current != nil {
				g.reference = p
				break
			}
		}
	}
	g.current, g.next = g.reference.current, g.reference.next
	if g.current == nil {
		return errors.New("编组没有可用的流")
	}
	if prefetch {
		return nil
	}
	return g.report("status", "loading", nil, g.seq(), 0)
}

func (g *groupCoordinator) apply() error {
	if err := g.members(); err != nil {
		return err
	}
	z := g.zone
	if z.Stream == nil {
		return g.reset(nil)
	}
	if g.current == nil || g.current.seq != z.Stream.Seq {
		if z.Intent != "play" {
			return g.reset(nil)
		}
		return g.open(z.Stream, false)
	}
	if g.next != nil && (z.NextStream == nil || g.next.seq != z.NextStream.Seq) {
		canceled := g.next.seq
		for _, p := range g.peers {
			if p.current != nil && p.current.seq == canceled {
				if err := p.Player.command("strm", control('q')); err != nil {
					if err = g.drop(p, "offline"); err != nil {
						return err
					}
				}
				p.current, p.joinBy = nil, time.Time{}
			}
			if p.next != nil {
				if err := p.Player.command("strm", control('f')); err != nil {
					if err = g.drop(p, "offline"); err != nil {
						return err
					}
				}
				p.next = nil
			}
		}
		g.next = nil
		g.decodeBy = time.Time{}
	}
	if z.Intent != g.controlIntent {
		g.controlIntent = z.Intent
		g.resumePending = false
		if z.Intent == "play" && !g.buffering {
			for _, p := range g.peers {
				joining := p.current == nil && p.State != "offline" && p.State != "busy"
				if joining || (p.current != nil && p.current.seq != g.current.seq) {
					return g.restart()
				}
			}
			return g.schedule(time.Now(), true)
		}
		if z.Intent == "pause" {
			g.decodeBy = time.Time{}
			for _, p := range g.peers {
				if p.current == nil {
					continue
				}
				p.paused = false
				if err := p.Player.command("strm", controlMS('p', 0)); err != nil {
					if err = g.drop(p, "offline"); err != nil {
						return err
					}
				}
			}
		}
	}
	return g.advance(time.Now())
}

func (g *groupCoordinator) advance(now time.Time) error {
	if g.current == nil || g.controlIntent != "play" {
		return nil
	}
	if g.resumePending {
		return g.schedule(now, true)
	}
	if g.buffering {
		for _, p := range g.peers {
			if p.current == nil || p.ready {
				continue
			}
			if now.Before(g.readyBy) {
				return nil
			}
			if err := g.drop(p, "dropped"); err != nil {
				return err
			}
		}
		if now.Before(g.startAfter) {
			return nil
		}
		return g.schedule(now, false)
	}
	allEnded := true
	for _, p := range g.peers {
		if p.current != nil && (!p.ended || p.current.seq != g.current.seq) {
			allEnded = false
		}
	}
	for _, p := range g.peers {
		if p.current != nil && !p.joinBy.IsZero() {
			if err := g.joinNext(p, now); err != nil {
				return err
			}
		}
	}
	if g.next != nil {
		return nil
	}
	next := g.zone.NextStream
	if next == nil {
		if allEnded && g.state != "ended" {
			pos := g.position()
			g.seconds, g.state = pos, "ended"
			return g.report("ended", "paused", nil, g.seq(), pos)
		}
		g.decodeBy = time.Time{}
		return nil
	}
	info, err := g.streamInfo(g.current.seq)
	if err != nil {
		return err
	}
	g.current.duration = info.DurationSeconds
	readers := make(map[string]zones.GroupMemberStream, len(info.Members))
	for _, reader := range info.Members {
		readers[reader.DeviceID] = reader
	}
	if g.decodeBy.IsZero() {
		allDelivered := true
		for _, p := range g.peers {
			if p.current != nil && !readers[p.DeviceID].Delivered {
				allDelivered = false
				break
			}
		}
		if allDelivered {
			// HTTP 可比播放头领先约二十秒，全部交付时解码器可能仍在等待输出缓冲腾出空间。
			remaining := max(0, info.DurationSeconds-g.position())
			g.decodeBy = now.Add(time.Duration(remaining*float64(time.Second)) + groupTimeout)
		}
	}
	pending := false
	for _, p := range g.peers {
		if p.current == nil {
			continue
		}
		if p.current.seq != g.current.seq {
			return nil
		}
		reader := readers[p.DeviceID]
		if reader.Dropped || reader.Path == "" {
			if err := g.drop(p, "dropped"); err != nil {
				return err
			}
			continue
		}
		if p.current.decoded && reader.Delivered {
			if deadline := now.Add(groupTimeout); g.decodeBy.IsZero() || deadline.Before(g.decodeBy) {
				g.decodeBy = deadline
			}
			continue
		}
		if !g.decodeBy.IsZero() && !now.Before(g.decodeBy) {
			if err := g.drop(p, "dropped"); err != nil {
				return err
			}
			continue
		}
		pending = true
	}
	if pending {
		return nil
	}
	g.decodeBy = time.Time{}
	return g.open(next, true)
}

func (g *groupCoordinator) event(p *groupPeer, ev playerEvent) error {
	if ev.Event == "STMc" {
		if p.awaiting > 0 {
			p.awaiting--
		}
		return nil
	}
	if p.current == nil {
		return nil
	}
	if p.awaiting > 0 {
		if p.next == nil {
			return nil
		}
		switch ev.Event {
		case "STMt", "STMp", "STMr", "STMo":
		case "STMs":
			if p.current.started {
				return nil
			}
		default:
			return nil
		}
	}
	kind := ""
	switch ev.Event {
	case "STMl":
		p.ready = true
		return g.advance(time.Now())
	case "STMd":
		target := p.current
		if p.next != nil {
			target = p.next
		}
		target.decoded = true
		return g.advance(time.Now())
	case "STMn", "STMo", "DSCO":
		return g.drop(p, "dropped")
	case "STMs":
		if p.current.started && p.next != nil {
			p.current, p.next = p.next, nil
		}
		p.ended, p.paused, p.current.started = false, false, true
		p.elapsed, p.point = 0, ev.at
		kind = "playing"
	case "STMt", "STMr", "STMp", "STMu":
		if ev.Event == "STMt" && (!p.current.started || p.ended || p.paused || ev.at.Before(p.scheduled)) {
			return nil
		}
		ms := int64(ev.ElapsedMS)
		if ms/1000 < int64(ev.ElapsedSeconds) {
			ms = int64(ev.ElapsedSeconds)*1000 + ms%1000
		}
		p.elapsed, p.point = time.Duration(ms)*time.Millisecond, ev.at
		switch ev.Event {
		case "STMp":
			p.paused = true
			kind = "paused"
		case "STMr":
			// 定时 u 的确认先于实际开始出声，起播以 STMs 为准。
			if !p.current.started {
				return nil
			}
			p.paused = false
			if p.point.Before(p.scheduled) {
				p.point = p.scheduled
			}
			kind = "playing"
		case "STMu":
			p.ended = true
		}
	default:
		return nil
	}
	if kind == "playing" && g.leader.current != nil && g.leader.current.started && g.leader.current.seq == p.current.seq && (p == g.leader || p == g.reference) {
		g.reference, p = g.leader, g.leader
	}
	if p == g.reference {
		g.current, g.next = p.current, p.next
		g.seconds, g.anchor = p.elapsed.Seconds(), p.point
		if p.ended {
			g.anchor = time.Time{}
		}
		if kind != "" {
			g.state = kind
			return g.report("status", kind, nil, g.seq(), g.position())
		}
	}
	if ev.Event == "STMu" {
		return g.advance(time.Now())
	}
	return nil
}
