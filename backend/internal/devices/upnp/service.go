package upnp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

var (
	ErrDisabled      = errors.New("局域网设备开关已关闭")
	ErrUnknownDevice = errors.New("找不到这个设备")
	ErrManualDevice  = errors.New("找不到手动添加的设备")
	ErrGrantUser     = errors.New("需要有效的账号列表")
	ErrProbe         = errors.New("无法读取渲染器信息")
	ErrDiscovery     = errors.New("无法启动设备发现")
)

type Settings struct {
	Enabled bool `json:"enabled"`
	Shared  bool `json:"shared"`
}

type AdminDevice struct {
	Entry
	UserIDs []string `json:"user_ids"`
}

// Service 将管理员写入、持久开关与本进程的发现周期串行化。
type Service struct {
	lifecycle  context.Context
	db         *sql.DB
	registry   *Registry
	mu         sync.RWMutex
	running    bool
	generation uint64
}

func NewService(ctx context.Context, db *sql.DB, registry *Registry) *Service {
	return &Service{lifecycle: ctx, db: db, registry: registry}
}

// Restore 在装配时读取设置。默认关闭的实例不会建立网络监听。
func (s *Service) Restore(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings, err := s.Settings(ctx)
	if err != nil || !settings.Enabled || s.running {
		return err
	}
	return s.start(ctx)
}

func (s *Service) start(ctx context.Context) error {
	devices, err := s.manualDevices(ctx)
	if err != nil {
		return err
	}
	targets := make([]ManualDevice, 0, len(devices))
	for _, d := range devices {
		targets = append(targets, ManualDevice{UDN: d.UDN, Address: d.Address})
	}
	if err := s.registry.Start(s.lifecycle, targets); err != nil {
		return fmt.Errorf("%w: %w", ErrDiscovery, err)
	}
	s.generation++
	s.running = true
	return nil
}

func (s *Service) stopLocked() {
	if s.running {
		s.registry.Stop()
		s.running = false
	}
}

func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopLocked()
}

func (s *Service) UpdateSettings(ctx context.Context, enabled, shared *bool) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	before, err := s.Settings(ctx)
	if err != nil {
		return Settings{}, err
	}
	after := before
	if enabled != nil {
		after.Enabled = *enabled
	}
	if shared != nil {
		after.Shared = *shared
	}
	starting := after.Enabled && !s.running
	if starting {
		if err := s.start(ctx); err != nil {
			return Settings{}, err
		}
	}
	if after != before {
		_, err = s.db.ExecContext(ctx, `UPDATE upnp_settings SET enabled=$1,shared=$2 WHERE id=1`, after.Enabled, after.Shared)
		if err != nil {
			if starting {
				s.stopLocked()
			}
			return Settings{}, err
		}
	}
	if !after.Enabled {
		s.stopLocked()
	}
	return after, nil
}

func (s *Service) Devices(ctx context.Context) ([]AdminDevice, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	manual, err := s.manualDevices(ctx)
	if err != nil {
		return nil, err
	}
	grants, err := s.grants(ctx, "")
	if err != nil {
		return nil, err
	}
	entries := make(map[string]Entry, len(manual))
	for _, e := range s.registry.List() {
		entries[e.UDN] = e
	}
	for _, d := range manual {
		e := entries[d.UDN]
		if e.Name == "" {
			e.Device = d
		}
		e.Manual = true
		entries[d.UDN] = e
	}
	items := make([]AdminDevice, 0, len(entries))
	for udn, e := range entries {
		items = append(items, AdminDevice{Entry: e, UserIDs: append([]string{}, grants[udn]...)})
	}
	slices.SortFunc(items, func(a, b AdminDevice) int { return strings.Compare(a.UDN, b.UDN) })
	return items, nil
}

func (s *Service) Add(ctx context.Context, address string) (AdminDevice, error) {
	s.mu.RLock()
	generation, running := s.generation, s.running
	s.mu.RUnlock()
	if !running {
		return AdminDevice{}, ErrDisabled
	}
	d, err := s.registry.Probe(ctx, strings.TrimSpace(address))
	s.mu.Lock()
	defer s.mu.Unlock()
	// 关闭会取消正在进行的探测。即使又开启，也不能提交上一轮的结果。
	if !s.running || s.generation != generation {
		return AdminDevice{}, ErrDisabled
	}
	if err != nil {
		if errors.Is(err, ErrDeviceAddress) {
			return AdminDevice{}, err
		}
		return AdminDevice{}, fmt.Errorf("%w: %w", ErrProbe, err)
	}
	grants, err := s.grants(ctx, " WHERE udn=$1", d.UDN)
	if err != nil {
		return AdminDevice{}, err
	}
	if err := s.saveManual(ctx, d); err != nil {
		return AdminDevice{}, err
	}
	s.registry.Remember(d)
	return AdminDevice{Entry: Entry{Device: d, Online: true, Manual: true}, UserIDs: append([]string{}, grants[d.UDN]...)}, nil
}

func (s *Service) Remove(ctx context.Context, udn string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.deleteManual(ctx, udn); err != nil {
		return err
	}
	s.registry.Forget(udn)
	return nil
}

func (s *Service) SetGrants(ctx context.Context, udn string, ids []string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.registry.Lookup(udn); !ok {
		var exists bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM upnp_manual_devices WHERE udn=$1)`, udn).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, ErrUnknownDevice
		}
	}
	normalized := make([]string, 0, len(ids))
	for _, id := range ids {
		if !uuid.Valid(id) {
			return nil, ErrGrantUser
		}
		normalized = append(normalized, strings.ToLower(id))
	}
	slices.Sort(normalized)
	normalized = slices.Compact(normalized)
	if err := s.replaceGrants(ctx, udn, normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

// Visible 返回当前会话可见的在线设备，供播放区快照与控制入口使用。
func (s *Service) Visible(ctx context.Context, actor identity.User) ([]Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := []Entry{}
	if !s.running {
		return items, nil
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	if !settings.Enabled {
		return items, nil
	}
	var grants map[string][]string
	if !settings.Shared && actor.Role != "admin" {
		grants, err = s.grants(ctx, " WHERE user_id=$1", actor.ID)
		if err != nil {
			return nil, err
		}
	}
	for _, e := range s.registry.List() {
		if e.Online && (grants == nil || len(grants[e.UDN]) > 0) {
			items = append(items, e)
		}
	}
	return items, nil
}
