package slimproto

import (
	"errors"
	"net"
	"time"

	"github.com/qlqq/roomusic/backend/internal/zones"
)

// MemberLease 的归还发生在取消订阅和停止旧播放器之后。
type MemberLease struct {
	Player  *Player
	Done    <-chan struct{}
	Release func()
}

type groupPeer struct {
	zones.GroupMember
	MemberLease
	stopRelay                                       chan struct{}
	subscription                                    chan playerEvent
	current, next                                   *outputStream
	awaiting                                        int
	ready, ended, paused                            bool
	point, attached, nextPoll, warmUntil, corrected time.Time
	joinBy, scheduled                               time.Time
	elapsed                                         time.Duration
	ppm                                             float64
	driftSamples                                    []lagSample
	rateUpdated                                     time.Time
}

type groupEvent struct {
	peer  *groupPeer
	lease chan struct{}
	event playerEvent
	lost  string
}

func (g *groupCoordinator) close() {
	for _, p := range g.peers {
		if p != g.leader {
			g.detach(p)
		}
	}
	g.releasing.Wait()
	g.leader.Player.groupControl(false)
	g.awaitingConnect = g.leader.awaiting
}

func (g *groupCoordinator) detach(p *groupPeer) {
	if p.Player == nil {
		return
	}
	lease := p.MemberLease
	close(p.stopRelay)
	p.stopRelay = nil
	lease.Player.unsubscribe(p.subscription)
	lease.Player.groupControl(false)
	if p.current != nil {
		ack := lease.Player.requestStop()
		g.releasing.Go(func() {
			lease.Player.waitStop(ack)
			lease.Release()
		})
	} else {
		lease.Release()
	}
	p.ppm = 0
	p.resetDriftSamples()
	p.MemberLease = MemberLease{}
	p.current, p.next = nil, nil
}

func (g *groupCoordinator) attach(p *groupPeer, acquire bool) error {
	lease, err := g.Member(g.ctx, p.GroupMember, acquire)
	if err != nil {
		return err
	}
	p.MemberLease = lease
	now := time.Now()
	p.attached, p.nextPoll, p.warmUntil = now, now, now.Add(2*time.Second)
	p.awaiting = 0
	stop := make(chan struct{})
	p.stopRelay = stop
	lease.Player.groupControl(true)
	ch := lease.Player.subscribe()
	p.subscription = ch
	// 转发协程只读状态；接管等待协调器归还租约，已入队的旧事件按租约过滤。
	go func() {
		for {
			ev := groupEvent{peer: p, lease: stop}
			select {
			case <-stop:
				return
			case <-lease.Done:
				ev.lost = "waiting"
				if lease.Player.ctx.Err() != nil {
					ev.lost = "offline"
				}
			case <-lease.Player.ctx.Done():
				ev.lost = "offline"
			case ev.event = <-ch:
			}
			select {
			case <-stop:
				return
			case g.memberEvents <- ev:
			}
			if ev.lost != "" {
				return
			}
		}
	}()
	return nil
}

func (g *groupCoordinator) setState(p *groupPeer, state string) error {
	if p == g.leader || p.State == state {
		return nil
	}
	err := g.Zones.ReportGroupMember(g.ctx, g.UserID, g.SessionID, p.DeviceID, state)
	if err == nil || errors.Is(err, zones.ErrNotFound) {
		p.State = state
		return nil
	}
	return err
}

func (p *groupPeer) volume() float64 {
	if p.Muted {
		return 0
	}
	return p.Volume
}

func (g *groupCoordinator) bind(p *groupPeer, acquire bool) error {
	err := g.attach(p, acquire)
	if err == nil {
		if err := p.Player.setVolume(p.volume()); err != nil {
			return g.drop(p, "offline")
		}
		return nil
	}
	var busy *zones.DeviceBusyError
	switch {
	case errors.As(err, &busy):
		if !acquire {
			return nil
		}
		return g.setState(p, "busy")
	case errors.Is(err, zones.ErrNotFound), errors.Is(err, zones.ErrDeviceUnsupported):
		if !acquire {
			return nil
		}
		return g.setState(p, "offline")
	default:
		return err
	}
}

func (g *groupCoordinator) members() error {
	roster := g.Zones.GroupMembers(g.UserID, g.SessionID)
	present := map[string]bool{g.DeviceID: true}
	for _, m := range roster {
		present[m.DeviceID] = true
		p := g.peers[m.DeviceID]
		if p == nil {
			p = &groupPeer{GroupMember: m}
			g.peers[m.DeviceID] = p
		}
		if p.Player == nil && m.State == "waiting" {
			if err := g.bind(p, false); err != nil {
				return err
			}
		}
		if p.SyncDelayMS != m.SyncDelayMS {
			p.resetDriftSamples()
		}
		previous := p.State
		p.GroupMember = m
		if p.current != nil && (m.State == "busy" || m.State == "offline" || (m.State == "dropped" && previous != m.State)) {
			if err := g.drop(p, m.State); err != nil {
				return err
			}
		}
		if p.Player != nil {
			if err := p.Player.setVolume(p.volume()); err != nil {
				if err = g.drop(p, "offline"); err != nil {
					return err
				}
			}
		}
	}
	for id, p := range g.peers {
		if !present[id] {
			if err := g.drop(p, "waiting"); err != nil {
				return err
			}
			delete(g.peers, id)
		}
	}
	return nil
}

func (g *groupCoordinator) drop(p *groupPeer, state string) error {
	if p == g.leader {
		if state == "offline" {
			p.Player.cancel()
			return net.ErrClosed
		}
		if err := p.Player.command("strm", control('q')); err != nil {
			return err
		}
		p.current, p.next = nil, nil
	} else {
		g.detach(p)
		if err := g.setState(p, state); err != nil {
			return err
		}
	}
	if p == g.reference && g.current != nil {
		for _, other := range g.peers {
			if other.current != nil {
				g.reference = other
				g.current, g.next = other.current, other.next
				g.seconds, g.anchor = other.sourceElapsed().Seconds(), other.point
				return nil
			}
		}
		return errors.New("编组没有可用的播放设备")
	}
	return nil
}
