package upnp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/qlqq/roomusic/backend/internal/devices/airplay"
	"github.com/qlqq/roomusic/backend/internal/devices/cast"
	"github.com/qlqq/roomusic/backend/internal/devices/slimproto"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/ptp"
)

var (
	ErrDisabled        = errors.New("局域网设备开关已关闭")
	ErrAirPlayDisabled = errors.New("AirPlay 发现已关闭")
	ErrCastDisabled    = errors.New("Google Cast 发现已关闭")
	ErrUnknownDevice   = errors.New("找不到这个设备")
	ErrManualDevice    = errors.New("找不到手动添加的设备")
	ErrGrantUser       = errors.New("需要有效的账号列表")
	ErrProbe           = errors.New("无法读取渲染器信息")
	ErrDiscovery       = errors.New("无法启动设备发现")
)

type Settings struct {
	SlimProtoEnabled     bool    `json:"slimproto_enabled"`
	SlimProtoUnavailable *string `json:"slimproto_unavailable"`
	UPnPEnabled          bool    `json:"upnp_enabled"`
	CastEnabled          bool    `json:"cast_enabled"`
	AirPlayEnabled       bool    `json:"airplay_enabled"`
	Enabled              bool    `json:"enabled"`
	Shared               bool    `json:"shared"`
}

type AdminDevice struct {
	Entry
	Format  *DeviceFormat `json:"format"`
	UserIDs []string      `json:"user_ids"`
}

// Service 将管理员写入、持久开关与本进程的发现周期串行化。
type Service struct {
	lifecycle          context.Context
	db                 *sql.DB
	registry           *Registry
	mu                 sync.RWMutex
	protocolMu         sync.Mutex // 协议启停跨越输出收尾，期间允许查询与认领其他设备。
	slim               *slimproto.Registry
	cast               *cast.Registry
	protocols          map[string]*protocol
	output             *outputManager
	airplayCredentials *airplay.CredentialStore
	clock              ptp.Client
	pendingPIN         map[string]*pinChallenge
}

func NewService(ctx context.Context, db *sql.DB, registry *Registry, credentials *airplay.CredentialStore, ptpAddress string) *Service {
	receivers := cast.NewRegistryWithLogger(registry.logger)
	s := &Service{
		lifecycle: ctx, db: db, registry: registry, cast: receivers, airplayCredentials: credentials, clock: ptp.Client{Address: ptpAddress}, pendingPIN: make(map[string]*pinChallenge),
		protocols: map[string]*protocol{
			"upnp":    {registry: registry},
			"cast":    {registry: castRegistry{receivers}},
			"airplay": {registry: airplayRegistry{airplay.NewRegistryWithLogger(registry.logger)}},
		},
	}
	s.slim = slimproto.NewRegistry("", registry.logger, s.configureSlimPlayer)
	s.protocols["slimproto"] = &protocol{registry: slimRegistry{s.slim}}
	return s
}

// Restore 在装配时读取设置。默认关闭的实例不会建立网络监听。
func (s *Service) Restore(ctx context.Context) error {
	s.protocolMu.Lock()
	defer s.protocolMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	settings, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	_, err = s.startProtocols(ctx, settings)
	return err
}

func (v Settings) active(kind string) bool {
	return v.Enabled && (kind == "upnp" && v.UPnPEnabled || kind == "cast" && v.CastEnabled || kind == "airplay" && v.AirPlayEnabled || kind == "slimproto" && v.SlimProtoEnabled)
}
func (s *Service) running() bool {
	for _, p := range s.protocols {
		if p.ctx != nil {
			return true
		}
	}
	return false
}

// 恢复实例时保留成功启动的协议；管理员更新设置时可按列表撤回本次启动。
func (s *Service) startProtocols(ctx context.Context, settings Settings) ([]string, error) {
	var pending []string
	for _, kind := range []string{"upnp", "cast", "airplay", "slimproto"} {
		if settings.active(kind) && s.protocols[kind].ctx == nil {
			pending = append(pending, kind)
		}
	}
	if len(pending) == 0 {
		return nil, nil
	}
	devices, err := s.manualDevices(ctx)
	if err != nil {
		return nil, err
	}
	targets := make(map[string][]ManualDevice)
	for _, d := range devices {
		targets[d.Kind] = append(targets[d.Kind], ManualDevice{UDN: d.UDN, Address: d.Address})
	}
	var started []string
	type cycle struct {
		ctx    context.Context
		cancel context.CancelFunc
	}
	cycles := make(map[string]cycle, len(pending))
	var discoveryErr error
	// 在所有网络监听就绪后一次性发布周期，避免认领尚未提交的设置。
	s.mu.Unlock()
	for _, kind := range pending {
		run, cancel := context.WithCancel(s.lifecycle)
		if err := s.protocols[kind].registry.Start(run, targets[kind]); err != nil {
			cancel()
			discoveryErr = errors.Join(discoveryErr, fmt.Errorf("%w (%s): %w", ErrDiscovery, kind, err))
			continue
		}
		started = append(started, kind)
		cycles[kind] = cycle{run, cancel}
	}
	s.mu.Lock()
	for kind, cycle := range cycles {
		p := s.protocols[kind]
		p.ctx, p.cancel = cycle.ctx, cycle.cancel
	}
	return started, discoveryErr
}
func (s *Service) stopProtocols(kinds []string) {
	for _, kind := range kinds {
		s.protocols[kind].ctx = nil
	}
	stopped := s.cancelOutputs(func(o *deviceOutput) bool { return slices.Contains(kinds, deviceKind(o.udn)) })
	s.waitOutputsLocked(stopped)
	for _, kind := range kinds {
		p := s.protocols[kind]
		s.mu.Unlock()
		p.cancel()
		p.registry.Stop()
		p.probes.Wait()
		s.mu.Lock()
		p.cancel = nil
	}
}
func (s *Service) Close() {
	s.protocolMu.Lock()
	defer s.protocolMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	var stopping []string
	for kind, p := range s.protocols {
		if p.ctx != nil {
			stopping = append(stopping, kind)
		}
	}
	s.stopProtocols(stopping)
}

type SettingsUpdate struct {
	SlimProtoEnabled *bool `json:"slimproto_enabled"`
	Enabled          *bool `json:"enabled"`
	Shared           *bool `json:"shared"`
	UPnPEnabled      *bool `json:"upnp_enabled"`
	CastEnabled      *bool `json:"cast_enabled"`
	AirPlayEnabled   *bool `json:"airplay_enabled"`
}

func (s *Service) UpdateSettings(ctx context.Context, changes SettingsUpdate) (Settings, error) {
	s.protocolMu.Lock()
	defer s.protocolMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	before, err := s.Settings(ctx)
	if err != nil {
		return Settings{}, err
	}
	after := before
	if changes.SlimProtoEnabled != nil {
		after.SlimProtoEnabled = *changes.SlimProtoEnabled
	}
	if changes.Enabled != nil {
		after.Enabled = *changes.Enabled
	}
	if changes.Shared != nil {
		after.Shared = *changes.Shared
	}
	if changes.UPnPEnabled != nil {
		after.UPnPEnabled = *changes.UPnPEnabled
	}
	if changes.CastEnabled != nil {
		after.CastEnabled = *changes.CastEnabled
	}
	if changes.AirPlayEnabled != nil {
		after.AirPlayEnabled = *changes.AirPlayEnabled
	}
	started, err := s.startProtocols(ctx, after)
	if err == nil && after != before {
		_, err = s.db.ExecContext(ctx, `UPDATE upnp_settings SET enabled=$1, shared=$2, upnp_enabled=$3, cast_enabled=$4, airplay_enabled=$5, slimproto_enabled=$6 WHERE id=1`, after.Enabled, after.Shared, after.UPnPEnabled, after.CastEnabled, after.AirPlayEnabled, after.SlimProtoEnabled)
	}
	if err != nil {
		s.stopProtocols(started)
		return Settings{}, err
	}
	var stopping []string
	for kind, p := range s.protocols {
		if p.ctx != nil && !after.active(kind) {
			stopping = append(stopping, kind)
		}
	}
	s.stopProtocols(stopping)
	if before.Shared && !after.Shared {
		err = s.enforceGrants(ctx)
	}
	after.SlimProtoUnavailable = s.slimUnavailable()
	return after, err
}

func deviceKind(udn string) string {
	if strings.HasPrefix(udn, "slim:") {
		return "slimproto"
	}
	if strings.HasPrefix(udn, "airplay:") {
		return "airplay"
	}
	if strings.HasPrefix(udn, "cast:") {
		return "cast"
	}
	return "upnp"
}
func (s *Service) entries() []Entry {
	var items []Entry
	for _, p := range s.protocols {
		if p.ctx != nil {
			items = append(items, p.registry.List()...)
		}
	}
	return items
}

// 同一份快照中的所有设备共享一次本地时钟查询。
func (s *Service) deviceProjection(ctx context.Context, formats map[string]storedFormat) func(*Entry) *DeviceFormat {
	clock := sync.OnceValue(func() ptp.Status {
		status, err := s.clock.Status(ctx)
		if err != nil {
			return ptp.Status{Reason: "clock_unavailable"}
		}
		return status
	})
	return func(e *Entry) *DeviceFormat {
		if info := e.Capabilities.AirPlay; info != nil {
			projected := *info
			projected.ApplyTiming(e.Model, clock)
			e.Capabilities.AirPlay = &projected
		}
		return applyFormat(e, formats)
	}
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
	for _, e := range s.entries() {
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
	formats, err := s.formats(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]AdminDevice, 0, len(entries))
	project := s.deviceProjection(ctx, formats)
	for udn, e := range entries {
		format := project(&e)
		items = append(items, AdminDevice{Entry: e, Format: format, UserIDs: append([]string{}, grants[udn]...)})
	}
	slices.SortFunc(items, func(a, b AdminDevice) int { return strings.Compare(a.UDN, b.UDN) })
	return items, nil
}

func disabledProtocol(kind string) error {
	if kind == "airplay" {
		return ErrAirPlayDisabled
	}
	if kind == "cast" {
		return ErrCastDisabled
	}
	return ErrDisabled
}

func (s *Service) Add(ctx context.Context, kind, address string) (AdminDevice, error) {
	s.mu.RLock()
	p := s.protocols[kind]
	if p == nil || kind == "slimproto" {
		s.mu.RUnlock()
		return AdminDevice{}, ErrKind
	}
	run := p.ctx
	if run == nil {
		s.mu.RUnlock()
		return AdminDevice{}, disabledProtocol(kind)
	}
	p.probes.Add(1)
	s.mu.RUnlock()
	probe, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(run, cancel)
	defer stop()
	d, err := p.registry.Probe(probe, strings.TrimSpace(address))
	p.probes.Done()
	s.mu.Lock()
	defer s.mu.Unlock()
	// 关闭会取消正在进行的探测。即使又开启，也不能提交上一轮的结果。
	if p.ctx != run || run.Err() != nil {
		return AdminDevice{}, disabledProtocol(kind)
	}
	if err != nil {
		if errors.Is(err, ErrDeviceAddress) || errors.Is(err, cast.ErrAddress) || errors.Is(err, airplay.ErrAddress) {
			return AdminDevice{}, err
		}
		return AdminDevice{}, fmt.Errorf("%w: %w", ErrProbe, err)
	}
	grants, err := s.grants(ctx, " WHERE udn=$1", d.UDN)
	if err != nil {
		return AdminDevice{}, err
	}
	formats, err := s.formats(ctx)
	if err != nil {
		return AdminDevice{}, err
	}
	if err := s.saveManual(ctx, d); err != nil {
		return AdminDevice{}, err
	}
	p.registry.Remember(d)
	entry := Entry{Device: d, Online: true, Manual: true}
	format := s.deviceProjection(ctx, formats)(&entry)
	return AdminDevice{Entry: entry, Format: format, UserIDs: append([]string{}, grants[d.UDN]...)}, nil
}

func (s *Service) Remove(ctx context.Context, udn string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.deleteManual(ctx, udn); err != nil {
		return err
	}
	if flow := s.pendingPIN[udn]; flow != nil {
		flow.cancel()
	}
	s.protocols[deviceKind(udn)].registry.Forget(udn)
	return s.enforceGrants(ctx)
}

func (s *Service) SetGrants(ctx context.Context, udn string, ids []string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireDevice(ctx, udn); err != nil {
		return nil, err
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
	return normalized, s.enforceGrants(ctx)
}

// Visible 返回当前会话可见的在线设备，供播放区快照与控制入口使用。
func (s *Service) Visible(ctx context.Context, actor identity.User) ([]Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.visibleLocked(ctx, actor, "")
}

func (s *Service) visibleLocked(ctx context.Context, actor identity.User, udn string) ([]Entry, error) {
	items := []Entry{}
	if !s.running() {
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
	formats, err := s.formats(ctx)
	if err != nil {
		return nil, err
	}
	project := s.deviceProjection(ctx, formats)
	var entries []Entry
	if udn == "" {
		entries = s.entries()
	} else if p := s.protocols[deviceKind(udn)]; p.ctx != nil {
		if e, ok := p.registry.Lookup(udn); ok {
			entries = []Entry{e}
		}
	}
	for _, e := range entries {
		if e.Online && (grants == nil || len(grants[e.UDN]) > 0) {
			project(&e)
			items = append(items, e)
		}
	}
	return items, nil
}
