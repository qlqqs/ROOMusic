package zones

import (
	"context"
	"reflect"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/zones/queue"
)

// ManagedOutputs 由应用装配，设备访问及物理占用归适配器管理。
type ManagedOutputs interface {
	VisibleDevices(ctx context.Context, userID string) ([]ManagedDevice, error)
	Acquire(ctx context.Context, userID, deviceID, udn string, caps Capabilities, takeover bool) error
	ReleaseIdle(ctx context.Context, userID, udn, sessionID string) error
}

type ManagedDevice struct {
	Kind, UDN, Name   string
	MaxSampleRate     int
	InitialVolume     *float64
	Capabilities      Capabilities
	FormatDeclared    bool
	UnavailableReason string
	OccupiedBy        *DeviceOccupant
}

type DeviceOccupant struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
}

type DeviceBusyError struct {
	Occupant DeviceOccupant
}

func (e *DeviceBusyError) Error() string { return "device_busy" }

// SetManagedOutputs 在启动时、接收控制请求前调用。
func (s *Service) SetManagedOutputs(outputs ManagedOutputs) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.managed = outputs
}

func (s *Service) managedOutputs() ManagedOutputs {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.managed
}

func managedKind(kind string) bool {
	return kind == "upnp" || kind == "cast" || kind == "airplay" || kind == "slimproto"
}

func (z *zoneRuntime) visible() bool { return !managedKind(z.device.Kind) || z.managedVisible }

func limitSampleRate(c Capabilities, rate int) Capabilities {
	c = cloneCapabilities(c)
	for i := range c.StreamFormats {
		c.StreamFormats[i].MaxSampleRate = &rate
	}
	return c
}

// RefreshManagedDevices 把管理员保存的新格式交给正在运行的输出，在下一条流协商时生效。
func (s *Service) RefreshManagedDevices(ctx context.Context, userID string) error {
	u, err := s.user(ctx, userID)
	if err != nil {
		return err
	}
	return s.syncManaged(ctx, userID, u)
}

// 查询注册表时不持设备或用户锁，适配器在另一请求中可能正在认领输出。
func (s *Service) syncManaged(ctx context.Context, userID string, u *userRuntime) error {
	provider := s.managedOutputs()
	if provider == nil {
		return nil
	}
	u.projection.Lock()
	defer u.projection.Unlock()
	items, err := provider.VisibleDevices(ctx, userID)
	if err != nil {
		return err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	existing := make(map[string]*zoneRuntime)
	for _, z := range u.order {
		z.mu.Lock()
		if managedKind(z.device.Kind) {
			existing[z.device.ExternalID] = z
		}
		z.mu.Unlock()
	}
	for _, item := range items {
		z := existing[item.UDN]
		if z == nil {
			if err := s.createManaged(ctx, userID, u, item); err != nil {
				return err
			}
			continue
		}
		delete(existing, item.UDN)
		if err := s.refreshManaged(ctx, userID, z, item); err != nil {
			return err
		}
	}
	for _, z := range existing {
		z.dispatch.Lock()
		z.mu.Lock()
		if z.managedVisible {
			z.managedVisible, z.occupiedBy = false, nil
			s.publish(userID, "device_removed", map[string]string{"device_id": z.device.DeviceID, "zone_id": z.persisted.ZoneID})
		}
		z.mu.Unlock()
		z.dispatch.Unlock()
	}
	return nil
}

func managedName(raw string) string {
	name := []rune(strings.TrimSpace(raw))
	return string(name[:min(len(name), 64)])
}

func (s *Service) createManaged(ctx context.Context, userID string, u *userRuntime, item ManagedDevice) error {
	now := s.Now()
	volume := 0.5
	if item.InitialVolume != nil {
		volume = *item.InitialVolume
	}
	d := PersistedDevice{DeviceID: s.newID(), ExternalID: item.UDN, Kind: item.Kind, Name: managedName(item.Name), MaxSampleRate: item.MaxSampleRate, Capabilities: limitSampleRate(item.Capabilities, item.MaxSampleRate), Volume: volume, VolumeDisplayOnly: item.InitialVolume == nil, CreatedAt: now, LastSeenAt: now}
	p := PersistedZone{ZoneID: s.newID(), DeviceID: d.DeviceID, Queue: queue.EmptyQueue(), PositionSavedAt: now, Settings: defaultSettings()}
	if err := s.transact(ctx, func(st *Store) error { return st.CreateDevice(ctx, userID, d, p) }); err != nil {
		return err
	}
	at := timestamp(now)
	z := &zoneRuntime{device: d, persisted: p, managedVisible: true, unavailableReason: item.UnavailableReason, formatDeclared: item.FormatDeclared, occupiedBy: cloneValue(item.OccupiedBy), intent: "pause", status: "idle", failures: []Failure{}, pausedAt: &at, prepared: map[string]string{}}
	u.devices[d.DeviceID], u.zones[p.ZoneID] = z, z
	u.order = append(u.order, z)
	sortUser(u)
	s.publishDevice(userID, deviceOf(z))
	s.publishZone(userID, s.zoneOf(z, nil))
	return nil
}

func (s *Service) refreshManaged(ctx context.Context, userID string, z *zoneRuntime, item ManagedDevice) error {
	z.dispatch.Lock()
	defer z.dispatch.Unlock()
	z.mu.Lock()
	defer z.mu.Unlock()
	d := z.device
	d.Name = managedName(item.Name)
	if item.Kind == "cast" || item.Kind == "airplay" || item.Kind == "slimproto" {
		d.MaxSampleRate = item.MaxSampleRate
	}
	d.Capabilities = limitSampleRate(item.Capabilities, d.MaxSampleRate)
	formatChanged := !reflect.DeepEqual(d.Capabilities.StreamFormats, z.device.Capabilities.StreamFormats)
	changed := d.Name != z.device.Name || !reflect.DeepEqual(d.Capabilities, z.device.Capabilities)
	newlyVisible := !z.managedVisible
	if newlyVisible || changed || s.Now().Sub(d.LastSeenAt) >= DeviceSeenSaveInterval {
		d.LastSeenAt = s.Now()
		if err := s.transact(ctx, func(st *Store) error { return st.UpdateDevice(ctx, userID, d) }); err != nil {
			return err
		}
	}
	if newlyVisible || changed || z.unavailableReason != item.UnavailableReason || z.formatDeclared != item.FormatDeclared || !reflect.DeepEqual(z.occupiedBy, item.OccupiedBy) {
		z.unavailableReason = item.UnavailableReason
		z.device, z.formatDeclared, z.occupiedBy, z.managedVisible = d, item.FormatDeclared, cloneValue(item.OccupiedBy), true
		s.publishDevice(userID, deviceOf(z))
		if formatChanged && z.session != nil {
			if item.Kind == "slimproto" {
				if z.nextHandle != nil {
					s.scheduleNextRebuild(userID, z, z.nextHandle, 0)
				}
			} else {
				s.scheduleRebuild(userID, z)
			}
		}
		if newlyVisible {
			tracks, err := s.loadSummaries(ctx, z.persisted.Queue.Entries)
			if err != nil {
				return err
			}
			s.publishZone(userID, s.zoneOf(z, tracks))
		}
	} else {
		z.device = d
	}
	return nil
}

func (s *Service) acquireManaged(ctx context.Context, userID string, z *zoneRuntime, takeover bool) error {
	z.mu.Lock()
	d := z.device
	visible := z.visible() && !z.deleted
	z.mu.Unlock()
	if !visible {
		return ErrNotFound
	}
	if !managedKind(d.Kind) {
		return nil
	}
	return s.managedOutputs().Acquire(ctx, userID, d.DeviceID, d.ExternalID, cloneCapabilities(d.Capabilities), takeover)
}

func (s *Service) ActivateDevice(ctx context.Context, userID, deviceID string, takeover bool) (DeviceResult, error) {
	if !validID(deviceID) {
		return DeviceResult{}, ErrInvalidInput
	}
	u, err := s.user(ctx, userID)
	if err != nil {
		return DeviceResult{}, err
	}
	if err = s.syncManaged(ctx, userID, u); err != nil {
		return DeviceResult{}, err
	}
	u.mu.RLock()
	z := u.devices[strings.ToLower(deviceID)]
	u.mu.RUnlock()
	if z == nil {
		return DeviceResult{}, ErrNotFound
	}
	z.mu.Lock()
	managed := managedKind(z.device.Kind)
	z.mu.Unlock()
	if !managed {
		return DeviceResult{}, ErrInvalidInput
	}
	if err = s.acquireManaged(ctx, userID, z, takeover); err != nil {
		return DeviceResult{}, err
	}
	if err = s.syncManaged(ctx, userID, u); err != nil {
		return DeviceResult{}, err
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	if !z.visible() {
		return DeviceResult{}, ErrNotFound
	}
	return DeviceResult{timestamp(s.Now()), deviceOf(z)}, nil
}

func (s *Service) SetDeviceSampleRate(ctx context.Context, userID, deviceID string, rate int) (DeviceResult, error) {
	if !validID(deviceID) || (rate != 44100 && rate != 48000 && rate != 96000 && rate != 192000) {
		return DeviceResult{}, ErrInvalidInput
	}
	return s.updateDevice(ctx, userID, deviceID, func(z *zoneRuntime, d *PersistedDevice) error {
		if d.Kind != "upnp" {
			return ErrInvalidInput
		}
		d.MaxSampleRate = rate
		d.Capabilities = limitSampleRate(d.Capabilities, rate)
		return nil
	})
}

// ManagedOutputVolume 在同一区锁内取得可下发的音量，避免旧设备事件覆盖成员最新设置。
func (s *Service) ManagedOutputVolume(userID, sessionID string) (float64, bool) {
	out, err := s.findOutput(userID, sessionID)
	if err != nil || !out.managed {
		return 0, false
	}
	z := out.zone
	z.mu.Lock()
	defer z.mu.Unlock()
	if outputError(out) != nil || z.device.VolumeDisplayOnly {
		return 0, false
	}
	if z.device.Muted {
		return 0, true
	}
	return z.device.Volume, true
}
