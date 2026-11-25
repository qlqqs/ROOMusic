package zones

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
)

const maxGroupDevices = 8

// AcquireMember 返回前须结束旧占用及其输出会话；ReleaseMember 仅释放本人的成员占用。
type GroupOutputs interface {
	AcquireMember(ctx context.Context, userID, leaderDeviceID, memberDeviceID, udn string, takeover bool) error
	ReleaseMember(ctx context.Context, userID, udn string) error
}

type DeviceGroup struct {
	ZoneID string `json:"zone_id"`
	Role   string `json:"role"`
}
type GroupMemberState struct {
	DeviceID string `json:"device_id"`
	State    string `json:"state"`
}
type ZoneGroup struct {
	Members []GroupMemberState `json:"members"`
}
type groupSlot struct {
	GroupMemberState
	maxSampleRate int
}
type GroupMember struct {
	DeviceID, UDN, State string
	Volume               float64
	Muted, Fixed         bool
	SyncDelayMS          int
}
type GroupVolumeResult struct {
	ServerTime Timestamp `json:"server_time"`
	Devices    []Device  `json:"devices"`
}

func (s *Service) AddGroupMember(ctx context.Context, userID, zoneID, deviceID string, takeover bool) (RegisterDeviceResult, error) {
	return s.changeGroup(ctx, userID, zoneID, deviceID, true, takeover)
}
func (s *Service) RemoveGroupMember(ctx context.Context, userID, zoneID, deviceID string) (RegisterDeviceResult, error) {
	return s.changeGroup(ctx, userID, zoneID, deviceID, false, false)
}

func checkGroupChange(leader, member *zoneRuntime, add bool) error {
	if leader.deleted || member.deleted {
		return ErrNotFound
	}
	if leader.device.Kind != "slimproto" || member.device.Kind != "slimproto" || leader == member {
		return ErrGroupUnsupported
	}
	if !leader.visible() {
		return ErrZoneOffline
	}
	if !add {
		if member.leaderZoneID != leader.persisted.ZoneID {
			return ErrNotFound
		}
		return nil
	}
	if !member.visible() {
		return ErrNotFound
	}
	if leader.leaderZoneID != "" || len(member.group) > 0 || member.leaderZoneID != "" {
		return ErrDeviceGrouped
	}
	if len(leader.group)+1 >= maxGroupDevices {
		return ErrGroupFull
	}
	if leader.output == nil || !leader.output.managed || outputError(leader.output) != nil {
		return ErrGroupUnsupported
	}
	if !leader.online {
		return ErrZoneOffline
	}
	return nil
}

// 调用方持有用户注册表锁；多个区依次按 ID 取 dispatch，再按同一顺序取状态锁。
func lockGroupZones(zones []*zoneRuntime) func() {
	ids := make(map[*zoneRuntime]string, len(zones))
	for _, z := range zones {
		z.mu.Lock()
		ids[z] = z.persisted.ZoneID
		z.mu.Unlock()
	}
	ordered := slices.Clone(zones)
	sort.Slice(ordered, func(i, j int) bool { return ids[ordered[i]] < ids[ordered[j]] })
	for _, z := range ordered {
		z.dispatch.Lock()
	}
	for _, z := range ordered {
		z.mu.Lock()
	}
	return func() {
		for i := len(ordered) - 1; i >= 0; i-- {
			ordered[i].mu.Unlock()
		}
		for i := len(ordered) - 1; i >= 0; i-- {
			ordered[i].dispatch.Unlock()
		}
	}
}

func (s *Service) changeGroup(ctx context.Context, userID, zoneID, deviceID string, add, takeover bool) (RegisterDeviceResult, error) {
	var result RegisterDeviceResult
	if !validID(zoneID) || !validID(deviceID) {
		return result, ErrInvalidInput
	}
	userID, zoneID, deviceID = strings.ToLower(userID), strings.ToLower(zoneID), strings.ToLower(deviceID)
	u, err := s.user(ctx, userID)
	if err != nil {
		return result, err
	}
	if err = s.syncManaged(ctx, userID, u); err != nil {
		return result, err
	}
	u.mu.RLock()
	leader, member := u.zones[zoneID], u.devices[deviceID]
	u.mu.RUnlock()
	if leader == nil || member == nil {
		return result, ErrNotFound
	}
	if leader == member {
		return result, ErrGroupUnsupported
	}

	finish, err := member.reserveAcquisition(ctx)
	if err != nil {
		return result, err
	}
	defer finish()
	u.mu.Lock()
	unlock := lockGroupZones([]*zoneRuntime{leader, member})
	err = checkGroupChange(leader, member, add)
	leaderID, udn, output := leader.device.DeviceID, member.device.ExternalID, leader.output
	unlock()
	u.mu.Unlock()
	if err != nil {
		return result, err
	}
	provider, ok := s.managedOutputs().(GroupOutputs)
	if !ok {
		return result, ErrGroupUnsupported
	}
	if add {
		err = provider.AcquireMember(ctx, userID, leaderID, deviceID, udn, takeover)
	} else {
		err = provider.ReleaseMember(ctx, userID, udn)
	}
	if err != nil {
		return result, err
	}

	u.mu.Lock()
	unlock = lockGroupZones([]*zoneRuntime{leader, member})
	err = checkGroupChange(leader, member, add)
	if add && err == nil && leader.output != output {
		err = ErrGroupUnsupported
	}
	if err == nil {
		result, err = s.commitGroupChange(ctx, userID, leader, member, add)
	}
	if err != nil && !add && member.leaderZoneID == zoneID {
		err = errors.Join(err, s.setGroupStateLocked(context.WithoutCancel(ctx), userID, leader, deviceID, "waiting"))
	}
	unlock()
	u.mu.Unlock()
	if err != nil && add {
		return result, errors.Join(err, provider.ReleaseMember(context.WithoutCancel(ctx), userID, udn))
	}
	return result, err
}

func (s *Service) commitGroupChange(ctx context.Context, userID string, leader, member *zoneRuntime, add bool) (RegisterDeviceResult, error) {
	var result RegisterDeviceResult
	zoneID, id := leader.persisted.ZoneID, member.device.DeviceID
	tracks, err := s.loadSummaries(ctx, leader.persisted.Queue.Entries)
	if err != nil {
		return result, err
	}
	p := leader.persisted
	p.Revision++
	if err = s.transact(ctx, func(st *Store) error {
		if err := st.changeGroupMember(ctx, userID, zoneID, id, add); err != nil {
			return err
		}
		return st.UpdateZone(ctx, userID, p)
	}); err != nil {
		return result, err
	}
	leader.persisted = p
	if add {
		member.leaderZoneID = zoneID
		member.offlinePaused = false
		leader.group = append(leader.group, groupSlot{GroupMemberState: GroupMemberState{id, "waiting"}, maxSampleRate: member.device.MaxSampleRate})
	} else {
		member.leaderZoneID = ""
		leader.removeMember(id)
	}
	s.regroupNext(userID, leader)
	result = RegisterDeviceResult{timestamp(s.Now()), deviceOf(member), s.zoneOf(leader, tracks)}
	s.publishDevice(userID, deviceOf(leader))
	if member.visible() {
		s.publishDevice(userID, result.Device)
	}
	s.publishZone(userID, result.Zone)
	return result, nil
}

func (s *Service) SetSyncDelay(ctx context.Context, userID, deviceID string, ms int) (DeviceResult, error) {
	if !validID(deviceID) || ms < -500 || ms > 500 {
		return DeviceResult{}, ErrInvalidInput
	}
	return s.updateDevice(ctx, userID, deviceID, func(_ *zoneRuntime, d *PersistedDevice) error {
		if d.Kind != "slimproto" {
			return ErrGroupUnsupported
		}
		d.SyncDelayMS = ms
		return nil
	})
}

func (s *Service) SetGroupVolume(ctx context.Context, userID, zoneID string, volume float64, expectedRevision ...int64) (GroupVolumeResult, error) {
	var result GroupVolumeResult
	if !validID(zoneID) || !finite(volume) || volume < 0 || volume > 1 {
		return result, ErrInvalidInput
	}
	u, err := s.user(ctx, userID)
	if err != nil {
		return result, err
	}
	if err = s.syncManaged(ctx, userID, u); err != nil {
		return result, err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	leader := u.zones[strings.ToLower(zoneID)]
	if leader == nil {
		return result, ErrNotFound
	}
	// 成员集合只在用户写锁内增删；状态由主区锁保护。
	leader.mu.Lock()
	group := []*zoneRuntime{leader}
	for _, m := range leader.group {
		group = append(group, u.devices[m.DeviceID])
	}
	leader.mu.Unlock()
	unlock := lockGroupZones(group)
	defer unlock()
	if len(expectedRevision) != 0 && leader.persisted.Revision != expectedRevision[0] {
		return result, ErrRevisionConflict
	}
	if !leader.visible() {
		return result, ErrZoneOffline
	}
	if len(group) == 1 {
		return result, ErrGroupUnsupported
	}
	var peak float64
	for _, z := range group {
		if z.device.Capabilities.Volume != "fixed" {
			peak = max(peak, z.device.Volume)
		}
	}
	updates := make(map[*zoneRuntime]PersistedDevice, len(group))
	if err = s.transact(ctx, func(st *Store) error {
		for _, z := range group {
			d := z.device
			if d.Capabilities.Volume == "fixed" {
				continue
			}
			d.Volume = volume
			if peak > 0 {
				d.Volume = volume * (z.device.Volume / peak)
			}
			d.VolumeDisplayOnly = false
			if err := st.UpdateDevice(ctx, userID, d); err != nil {
				return err
			}
			updates[z] = d
		}
		return nil
	}); err != nil {
		return result, err
	}
	result.ServerTime = timestamp(s.Now())
	for _, z := range group {
		if d, ok := updates[z]; ok {
			z.device = d
			if z.visible() {
				s.publishDevice(userID, deviceOf(z))
			}
		}
		result.Devices = append(result.Devices, deviceOf(z))
	}
	return result, nil
}

// 物理操作的预约按设备串行。等待、适配器回调与网络交接期间均不持运行时锁。
func (z *zoneRuntime) reserveAcquisition(ctx context.Context) (func(), error) {
	for {
		z.mu.Lock()
		pending := z.acquisition
		if pending == nil {
			pending = make(chan struct{})
			z.acquisition = pending
			z.mu.Unlock()
			return func() {
				z.mu.Lock()
				z.acquisition = nil
				close(pending)
				z.mu.Unlock()
			}, nil
		}
		z.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-pending:
		}
	}
}
