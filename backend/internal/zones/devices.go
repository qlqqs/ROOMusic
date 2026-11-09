package zones

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/zones/queue"
)

func (s *Service) RegisterDevice(ctx context.Context, userID string, input RegisterDeviceInput) (RegisterDeviceResult, error) {
	input.Name = strings.TrimSpace(input.Name)
	if !validName(input.Name) || (input.Kind != "browser" && input.Kind != "native") || !finite(input.Volume) || input.Volume < 0 || input.Volume > 1 {
		return RegisterDeviceResult{}, ErrInvalidInput
	}
	if err := validateSettings(input.Settings); err != nil {
		return RegisterDeviceResult{}, err
	}
	u, err := s.user(ctx, userID)
	if err != nil {
		return RegisterDeviceResult{}, err
	}
	userID = strings.ToLower(userID)
	u.mu.Lock()
	defer u.mu.Unlock()
	names := map[string]bool{}
	for _, z := range u.order {
		names[z.device.Name] = true
	}
	name := input.Name
	for n := 2; names[name]; n++ {
		suffix := fmt.Sprintf(" %d", n)
		runes := []rune(input.Name)
		name = string(runes[:min(len(runes), 64-len([]rune(suffix)))]) + suffix
	}
	now := s.options.Now()
	paused := timestamp(now)
	d := PersistedDevice{DeviceID: s.newID(), Kind: input.Kind, Name: name, Capabilities: defaultCapabilities(), Volume: input.Volume, Muted: input.Muted, CreatedAt: now, LastSeenAt: now}
	p := PersistedZone{ZoneID: s.newID(), DeviceID: d.DeviceID, Queue: queue.EmptyQueue(), PositionSavedAt: now, Settings: applySettings(defaultSettings(), input.Settings)}
	if err := s.transact(ctx, func(store *Store) error { return store.CreateDevice(ctx, userID, d, p) }); err != nil {
		return RegisterDeviceResult{}, err
	}
	z := &zoneRuntime{persisted: p, device: d, intent: "pause", status: "idle", failures: []Failure{}, pausedAt: &paused, prepared: map[string]string{}}
	u.zones[p.ZoneID] = z
	u.devices[d.DeviceID] = z
	u.order = append(u.order, z)
	sortUser(u)
	result := RegisterDeviceResult{timestamp(now), deviceOf(z), s.zoneOf(z, nil)}
	s.publishDevice(userID, result.Device)
	s.publishZone(userID, result.Zone)
	return result, nil
}
func (s *Service) RenameDevice(ctx context.Context, userID, deviceID, name string) (DeviceResult, error) {
	name = strings.TrimSpace(name)
	if !validID(deviceID) || !validName(name) {
		return DeviceResult{}, ErrInvalidInput
	}
	return s.updateDevice(ctx, userID, deviceID, func(z *zoneRuntime, d *PersistedDevice) error { d.Name = name; return nil })
}
func (s *Service) SetVolume(ctx context.Context, userID, deviceID string, volume float64, muted bool) (DeviceResult, error) {
	if !validID(deviceID) || !finite(volume) || volume < 0 || volume > 1 {
		return DeviceResult{}, ErrInvalidInput
	}
	return s.updateDevice(ctx, userID, deviceID, func(z *zoneRuntime, d *PersistedDevice) error {
		if d.Capabilities.Volume == "fixed" {
			return ErrVolumeFixed
		}
		d.Volume = volume
		d.Muted = muted
		return nil
	})
}
func (s *Service) updateDevice(ctx context.Context, userID, deviceID string, update func(*zoneRuntime, *PersistedDevice) error) (DeviceResult, error) {
	u, err := s.user(ctx, userID)
	if err != nil {
		return DeviceResult{}, err
	}
	userID = strings.ToLower(userID)
	// 改名与登记共享用户注册表锁，避免同名后缀计算读取旧名称。
	u.mu.Lock()
	defer u.mu.Unlock()
	z := u.devices[strings.ToLower(deviceID)]
	if z == nil {
		return DeviceResult{}, ErrNotFound
	}
	z.dispatch.Lock()
	defer z.dispatch.Unlock()
	z.mu.Lock()
	d := z.device
	if err := update(z, &d); err != nil {
		z.mu.Unlock()
		return DeviceResult{}, err
	}
	if err := s.transact(ctx, func(store *Store) error { return store.UpdateDevice(ctx, userID, d) }); err != nil {
		z.mu.Unlock()
		return DeviceResult{}, err
	}
	z.device = d
	result := DeviceResult{timestamp(s.options.Now()), deviceOf(z)}
	z.mu.Unlock()
	s.publishDevice(userID, result.Device)
	return result, nil
}
func (s *Service) DeleteDevice(ctx context.Context, userID, deviceID string) error {
	if !validID(deviceID) {
		return ErrInvalidInput
	}
	return s.deleteDevice(ctx, userID, deviceID, nil)
}
func (s *Service) deleteDevice(ctx context.Context, userID, deviceID string, before *time.Time) error {
	u, err := s.user(ctx, userID)
	if err != nil {
		return err
	}
	userID = strings.ToLower(userID)
	u.mu.Lock()
	defer u.mu.Unlock()
	z := u.devices[strings.ToLower(deviceID)]
	if z == nil {
		if before != nil {
			return nil
		}
		return ErrNotFound
	}
	z.dispatch.Lock()
	defer z.dispatch.Unlock()
	z.mu.Lock()
	if z.online {
		z.mu.Unlock()
		if before != nil {
			return nil
		}
		return ErrDeviceOnline
	}
	if before != nil && !z.device.LastSeenAt.Before(*before) {
		z.mu.Unlock()
		return nil
	}
	var deleted bool
	err = s.transact(ctx, func(store *Store) error {
		var e error
		deleted, e = store.DeleteDevice(ctx, userID, z.device.DeviceID, before)
		return e
	})
	if err != nil {
		z.mu.Unlock()
		return err
	}
	if !deleted {
		z.mu.Unlock()
		if before != nil {
			return nil
		}
		return ErrNotFound
	}
	z.deleted = true
	s.cancelTimers(z)
	s.closeSession(z)
	delete(u.devices, z.device.DeviceID)
	delete(u.zones, z.persisted.ZoneID)
	for i, entry := range u.order {
		if entry == z {
			u.order = append(u.order[:i], u.order[i+1:]...)
			break
		}
	}
	message := struct {
		DeviceID string `json:"device_id"`
		ZoneID   string `json:"zone_id"`
	}{z.device.DeviceID, z.persisted.ZoneID}
	z.mu.Unlock()
	s.publish(userID, "device_removed", message)
	return nil
}

func validCapabilities(c Capabilities) bool {
	if c.MaxChannels < 1 || c.MaxChannels > 64 || (c.DSD != "none" && c.DSD != "dop" && c.DSD != "native") || (c.Volume != "number" && c.Volume != "fixed") {
		return false
	}
	for _, f := range c.StreamFormats {
		if strings.TrimSpace(f.Codec) == "" || strings.TrimSpace(f.MIMEType) == "" || (f.MaxSampleRate != nil && *f.MaxSampleRate <= 0) || (f.MaxBitDepth != nil && *f.MaxBitDepth <= 0) {
			return false
		}
	}
	return true
}

// SetOutputState 不分配输出会话，也不消费回报；B3 的会话管理器负责调用。
func (s *Service) SetOutputState(ctx context.Context, userID, deviceID string, state OutputState) (DeviceResult, error) {
	if !validID(deviceID) || (state.Capabilities != nil && !validCapabilities(*state.Capabilities)) {
		return DeviceResult{}, ErrInvalidInput
	}
	u, err := s.user(ctx, userID)
	if err != nil {
		return DeviceResult{}, err
	}
	userID = strings.ToLower(userID)
	u.mu.RLock()
	defer u.mu.RUnlock()
	z := u.devices[strings.ToLower(deviceID)]
	if z == nil {
		return DeviceResult{}, ErrNotFound
	}
	z.dispatch.Lock()
	defer z.dispatch.Unlock()
	z.mu.Lock()
	d, p := z.device, z.persisted
	if state.Capabilities != nil {
		d.Capabilities = *state.Capabilities
		d.Capabilities.StreamFormats = append([]StreamFormatCapability{}, state.Capabilities.StreamFormats...)
	}
	now := s.options.Now()
	d.LastSeenAt = now
	p.Revision++
	tracks, err := s.loadSummaries(ctx, p.Queue.Entries)
	if err != nil {
		z.mu.Unlock()
		return DeviceResult{}, err
	}
	p.PositionSeconds = positionAt(z, now, tracks)
	p.PositionSavedAt = now
	if err = s.transact(ctx, func(store *Store) error {
		if e := store.UpdateDevice(ctx, userID, d); e != nil {
			return e
		}
		return store.UpdateZone(ctx, userID, p)
	}); err != nil {
		z.mu.Unlock()
		return DeviceResult{}, err
	}
	z.device = d
	z.persisted = p
	z.online = state.Online
	if !state.Online {
		wasPlaying := z.intent == "play"
		s.cancelTimers(z)
		s.closeSession(z)
		s.pauseState(z, now)
		z.offlinePaused = wasPlaying
	}
	result := DeviceResult{timestamp(now), deviceOf(z)}
	zone := s.zoneOf(z, tracks)
	z.mu.Unlock()
	s.publishDevice(userID, result.Device)
	s.publishZone(userID, zone)
	return result, nil
}

// Cleanup 同时覆盖未加载的用户；删除前重新持锁核对在线状态与最后上线时间。
func (s *Service) Cleanup(ctx context.Context) error {
	if s.closed.Load() {
		return ErrClosed
	}
	if s.db == nil {
		return ErrDatabaseUnavailable
	}
	cutoff := s.options.Now().Add(-30 * 24 * time.Hour)
	rows, err := s.db.QueryContext(ctx, `SELECT user_id,id FROM playback_devices WHERE last_seen_at<$1 ORDER BY user_id,id`, cutoff)
	if err != nil {
		return databaseError(err)
	}
	type candidate struct{ userID, deviceID string }
	candidates := []candidate{}
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.userID, &c.deviceID); err != nil {
			rows.Close()
			return databaseError(err)
		}
		candidates = append(candidates, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return databaseError(err)
	}
	for _, c := range candidates {
		if err := s.deleteDevice(ctx, c.userID, c.deviceID, &cutoff); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) scheduleCleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed.Load() {
		s.cleanup = s.options.AfterFunc(time.Hour, func() {
			if err := s.Cleanup(s.ctx); err != nil {
				s.reportAsync("cleanup", "", "", err)
			}
			s.scheduleCleanup()
		})
	}
}
