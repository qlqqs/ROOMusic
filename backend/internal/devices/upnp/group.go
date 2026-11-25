package upnp

import (
	"context"

	"github.com/qlqq/roomusic/backend/internal/devices/slimproto"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

var _ zones.GroupOutputs = (*Service)(nil)

func (s *Service) AcquireMember(ctx context.Context, userID, leaderID, deviceID, udn string, takeover bool) error {
	if deviceKind(udn) != "slimproto" || leaderID == "" {
		return zones.ErrGroupUnsupported
	}
	return s.acquire(ctx, userID, deviceID, udn, zones.Capabilities{}, takeover, leaderID, nil)
}

func (m *outputManager) leader(userID, deviceID string) *deviceOutput {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, o := range m.owners {
		if o.leader == nil && o.actor.ID == userID && o.deviceID == deviceID && o.device.Kind == "slimproto" && o.ctx.Err() == nil {
			return o
		}
	}
	return nil
}

func (m *outputManager) claimMember(leader *deviceOutput, deviceID string, device Device) error {
	run, cancel := context.WithCancel(leader.ctx)
	o := &deviceOutput{leader: leader, manager: m, actor: leader.actor, device: device, deviceID: deviceID,
		udn: device.UDN, ctx: run, cancel: cancel, done: make(chan struct{})}
	m.mu.Lock()
	if run.Err() != nil {
		m.mu.Unlock()
		cancel()
		return zones.ErrGroupUnsupported
	}
	m.owners[o.udn] = o
	m.mu.Unlock()
	go func() {
		select {
		case <-run.Done():
		case <-device.slimPlayer.Done():
			cancel()
		}
		m.mu.Lock()
		borrowed := o.borrowed
		m.mu.Unlock()
		if borrowed != nil {
			<-borrowed
		}
		m.mu.Lock()
		delete(m.owners, o.udn)
		close(o.done)
		m.mu.Unlock()
	}()
	return nil
}

func (s *Service) ReleaseMember(ctx context.Context, userID, udn string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.output
	m.mu.Lock()
	o := m.owners[udn]
	m.mu.Unlock()
	if o != nil && o.leader != nil && o.actor.ID == userID {
		o.cancel()
		s.waitOutputsLocked([]*deviceOutput{o})
	}
	return nil
}

// 显式加入已经通过接管检查。恢复持久编组时只认领空闲播放器。
func (s *Service) slimMember(ctx context.Context, leader *deviceOutput, member zones.GroupMember, rejoin bool) (slimproto.MemberLease, error) {
	m := s.output
	if rejoin {
		if err := s.acquire(ctx, leader.actor.ID, member.DeviceID, member.UDN, zones.Capabilities{}, false, leader.deviceID, leader); err != nil {
			return slimproto.MemberLease{}, err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	o := m.owners[member.UDN]
	if o == nil || o.ctx.Err() != nil {
		return slimproto.MemberLease{}, zones.ErrNotFound
	}
	if o.leader != leader || o.borrowed != nil {
		return slimproto.MemberLease{}, &zones.DeviceBusyError{Occupant: zones.DeviceOccupant{UserID: o.actor.ID, DisplayName: o.actor.Username}}
	}
	o.borrowed = make(chan struct{})
	return slimproto.MemberLease{Player: o.device.slimPlayer, Done: o.ctx.Done(), Release: func() {
		o.cancel()
		close(o.borrowed)
	}}, nil
}

func (m *outputManager) finishMembers(leader *deviceOutput) {
	m.mu.Lock()
	var children []*deviceOutput
	for _, o := range m.owners {
		if o.leader == leader {
			o.cancel()
			children = append(children, o)
		}
	}
	m.mu.Unlock()
	for _, o := range children {
		<-o.done
	}
}
