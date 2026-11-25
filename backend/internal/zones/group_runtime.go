package zones

import (
	"context"
	"slices"
	"strings"
)

// GroupMembers 返回当前编组，主设备排在第一位；单设备输出返回空集合。
func (s *Service) GroupMembers(userID, sessionID string) []GroupMember {
	out, err := s.findOutput(userID, sessionID)
	if err != nil || !out.managed {
		return nil
	}
	u, err := s.user(s.ctx, userID)
	if err != nil {
		return nil
	}
	u.mu.RLock()
	defer u.mu.RUnlock()
	z := out.zone
	z.mu.Lock()
	if outputError(out) != nil || len(z.group) == 0 {
		z.mu.Unlock()
		return nil
	}
	members := []GroupMember{groupMemberOf(z.device, "active")}
	slots := slices.Clone(z.group)
	z.mu.Unlock()
	for _, m := range slots {
		member := u.devices[m.DeviceID]
		member.mu.Lock()
		members = append(members, groupMemberOf(member.device, m.State))
		member.mu.Unlock()
	}
	return members
}
func groupMemberOf(d PersistedDevice, state string) GroupMember {
	return GroupMember{DeviceID: d.DeviceID, UDN: d.ExternalID, State: state, Volume: d.Volume, Muted: d.Muted, Fixed: d.Capabilities.Volume == "fixed", SyncDelayMS: d.SyncDelayMS}
}

func (s *Service) ReportGroupMember(ctx context.Context, userID, sessionID, deviceID, state string) error {
	switch state {
	case "active", "joining", "waiting", "dropped", "offline", "busy":
	default:
		return ErrInvalidInput
	}
	out, err := s.findOutput(userID, sessionID)
	if err != nil {
		return err
	}
	z := out.zone
	z.dispatch.Lock()
	defer z.dispatch.Unlock()
	z.mu.Lock()
	defer z.mu.Unlock()
	if err = outputError(out); err != nil {
		return err
	}
	if !out.managed || z.device.Kind != "slimproto" {
		return ErrGroupUnsupported
	}
	return s.setGroupStateLocked(ctx, out.userID, z, strings.ToLower(deviceID), state)
}
func (s *Service) setGroupStateLocked(ctx context.Context, userID string, z *zoneRuntime, id, state string) error {
	for i, m := range z.group {
		if m.DeviceID != id {
			continue
		}
		if m.State == state {
			return nil
		}
		slots := slices.Clone(z.group)
		slots[i].State = state
		return s.updateGroupProjection(ctx, userID, z, slots)
	}
	return ErrNotFound
}

// 成员状态仅在内存中保存，但区版本需要持久递增，客户端才会接收这次增量。
func (s *Service) updateGroupProjection(ctx context.Context, userID string, z *zoneRuntime, slots []groupSlot) error {
	p := z.persisted
	p.Revision++
	tracks, err := s.loadSummaries(ctx, p.Queue.Entries)
	if err != nil {
		return err
	}
	if err = s.transact(ctx, func(st *Store) error { return st.UpdateZone(ctx, userID, p) }); err != nil {
		return err
	}
	z.persisted = p
	s.applyGroupProjection(userID, z, slots)
	if z.visible() {
		s.publishZone(userID, s.zoneOf(z, tracks))
	}
	return nil
}

func (m groupSlot) expected() bool { return m.State != "offline" && m.State != "busy" }

// 注册表已完成本轮同步。用户写锁保护组关系，采样率和可见性使用本轮成员设备的快照。
func (s *Service) refreshGroupProjection(ctx context.Context, userID string, u *userRuntime) error {
	for _, leader := range u.order {
		leader.mu.Lock()
		group := []*zoneRuntime{leader}
		for _, m := range leader.group {
			group = append(group, u.devices[m.DeviceID])
		}
		leader.mu.Unlock()
		if len(group) == 1 {
			continue
		}
		unlock := lockGroupZones(group)
		slots := slices.Clone(leader.group)
		stateChanged := false
		for i, member := range group[1:] {
			m := &slots[i]
			m.maxSampleRate = member.device.MaxSampleRate
			switch {
			case !member.managedVisible:
				m.State = "offline"
			case member.occupiedBy != nil && !strings.EqualFold(member.occupiedBy.UserID, userID):
				m.State = "busy"
			case m.State == "offline" || m.State == "busy":
				m.State = "waiting"
			}
			stateChanged = stateChanged || m.State != leader.group[i].State
		}
		var err error
		if stateChanged {
			err = s.updateGroupProjection(ctx, userID, leader, slots)
		} else {
			s.applyGroupProjection(userID, leader, slots)
		}
		unlock()
		if err != nil {
			return err
		}
	}
	return nil
}

func (leader *zoneRuntime) removeMember(id string) {
	leader.group = slices.DeleteFunc(leader.group, func(m groupSlot) bool { return m.DeviceID == id })
	leader.removeMemberStreams(id)
}
func (leader *zoneRuntime) removeMemberStreams(id string) {
	for _, h := range []*renderHandle{leader.currentHandle, leader.nextHandle} {
		if h != nil {
			h.removeGroupMember(id)
		}
	}
}

func (z *zoneRuntime) zoneGroup() *ZoneGroup {
	if len(z.group) == 0 {
		return nil
	}
	members := make([]GroupMemberState, len(z.group))
	for i, m := range z.group {
		members[i] = m.GroupMemberState
	}
	return &ZoneGroup{Members: members}
}

// 只重协商尚待播放的接续流。已经打开的流冻结成员与格式，失去占用的成员立即失去原令牌。
func (s *Service) applyGroupProjection(userID string, leader *zoneRuntime, slots []groupSlot) {
	rebuild := false
	for i, next := range slots {
		prev := leader.group[i]
		if prev.expected() && !next.expected() {
			leader.removeMemberStreams(next.DeviceID)
		}
		if prev.expected() != next.expected() || next.expected() && prev.maxSampleRate != next.maxSampleRate {
			rebuild = true
		}
	}
	leader.group = slots
	if rebuild {
		s.regroupNext(userID, leader)
	}
}

func (s *Service) regroupNext(userID string, leader *zoneRuntime) {
	if h := leader.nextHandle; h != nil {
		s.scheduleNextRebuild(userID, leader, h, 0)
	}
}
