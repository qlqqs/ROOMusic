package upnp

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"maps"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/cast"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

// 启停、授权修改、占用交接均由 Service.mu 串行化。退出的输出协程可独立注销。
type outputManager struct {
	zones            *zones.Service
	logger           *slog.Logger
	lanURL, httpAddr string
	mu               sync.Mutex
	owners           map[string]*deviceOutput
}

func (s *Service) AttachOutput(z *zones.Service, lanURL, httpAddr string, logger *slog.Logger) {
	s.output = &outputManager{zones: z, lanURL: strings.TrimRight(lanURL, "/"), httpAddr: httpAddr, logger: logger, owners: map[string]*deviceOutput{}}
}

func (m *outputManager) baseURL(d Device) (string, error) {
	if m.lanURL != "" {
		return m.lanURL, nil
	}
	_, port, err := net.SplitHostPort(m.httpAddr)
	if err != nil {
		return "", err
	}
	return "http://" + net.JoinHostPort(d.LocalAddress, port), nil
}

func (s *Service) account(ctx context.Context, id string) (identity.User, error) {
	u := identity.User{ID: id}
	err := s.db.QueryRowContext(ctx, "SELECT username,role FROM users WHERE id=$1 AND disabled_at IS NULL", id).Scan(&u.Username, &u.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return u, zones.ErrNotFound
	}
	return u, err
}

func playbackCaps(c Capabilities) zones.Capabilities {
	out := zones.Capabilities{MaxChannels: 2, DSD: "none", Volume: c.Volume}
	for _, codec := range c.StreamFormats {
		mime := "audio/" + codec
		if codec == "mp3" {
			mime = "audio/mpeg"
		}
		lossless := codec == "flac" || codec == "wav"
		f := zones.StreamFormatCapability{Codec: codec, MIMEType: mime, Lossless: lossless, MaxSampleRate: &c.MaxSampleRate}
		if lossless {
			f.MaxBitDepth = &c.MaxBitDepth
		}
		out.StreamFormats = append(out.StreamFormats, f)
	}
	return out
}

func (s *Service) VisibleDevices(ctx context.Context, userID string) ([]zones.ManagedDevice, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.running() {
		return nil, nil
	}
	actor, err := s.account(ctx, userID)
	if errors.Is(err, zones.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	items, err := s.visibleLocked(ctx, actor)
	if err != nil {
		return nil, err
	}
	out := make([]zones.ManagedDevice, 0, len(items))
	s.output.mu.Lock()
	defer s.output.mu.Unlock()
	for _, d := range items {
		row := zones.ManagedDevice{Kind: d.Kind, MaxSampleRate: d.Capabilities.MaxSampleRate, UDN: d.UDN, Name: d.Name, InitialVolume: d.initialVolume, Capabilities: playbackCaps(d.Capabilities), FormatDeclared: d.Capabilities.FormatDeclared}
		if owner := s.output.owners[d.UDN]; owner != nil {
			row.OccupiedBy = &zones.DeviceOccupant{UserID: owner.actor.ID, DisplayName: owner.actor.Username}
		}
		out = append(out, row)
	}
	return out, nil
}

func (s *Service) Acquire(ctx context.Context, userID, deviceID, udn string, caps zones.Capabilities, takeover bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running() {
		return zones.ErrNotFound
	}
	actor, err := s.account(ctx, userID)
	if err != nil {
		return err
	}
	active, err := identity.HasActiveSession(ctx, s.db, userID)
	if err != nil {
		return err
	}
	if !active {
		return zones.ErrNotFound
	}
	visible, err := s.visibleLocked(ctx, actor)
	if err != nil {
		return err
	}
	index := slices.IndexFunc(visible, func(d Entry) bool { return d.UDN == udn })
	if index < 0 {
		return zones.ErrNotFound
	}
	device := visible[index].Device
	m := s.output
	base, err := m.baseURL(device)
	if err != nil {
		return err
	}
	m.mu.Lock()
	old := m.owners[udn]
	m.mu.Unlock()
	if old != nil {
		if old.actor.ID == userID && old.deviceID == deviceID {
			return nil
		}
		if !takeover {
			return &zones.DeviceBusyError{Occupant: zones.DeviceOccupant{UserID: old.actor.ID, DisplayName: old.actor.Username}}
		}
		old.cancel()
		<-old.done
		if old.releaseErr != nil {
			return old.releaseErr
		}
	}
	claim, err := m.zones.ClaimOutput(ctx, userID, deviceID, zones.ClaimOutputInput{Protocol: 1, Capabilities: caps, Managed: true})
	if err != nil {
		return err
	}
	sub, err := m.zones.SubscribeOutput(ctx, userID, claim.SessionID)
	if err != nil {
		return errors.Join(err, m.zones.ReleaseOutput(context.WithoutCancel(ctx), userID, claim.SessionID, zones.ReleaseOutputInput{}))
	}
	run, cancel := context.WithCancel(s.lifecycle)
	o := &deviceOutput{manager: m, registry: s.registry, device: device, udn: udn, base: base, actor: actor, deviceID: deviceID, sessionID: claim.SessionID, zoneID: claim.Zone.ZoneID, ctx: run, cancel: cancel, done: make(chan struct{}), volume: -1}
	m.mu.Lock()
	m.owners[udn] = o
	m.mu.Unlock()
	if device.Kind == "cast" {
		output := cast.Output{Zones: m.zones, UserID: userID, DeviceID: deviceID, ZoneID: o.zoneID, SessionID: o.sessionID, BaseURL: base, Address: device.Address, Logger: m.logger, Online: func() bool { d, ok := s.cast.Lookup(udn); return ok && d.Online }, Offline: func() { s.cast.Offline(udn) }}
		go func() { position := output.Run(run, sub); sub.Close(); o.cancel(); o.release(position) }()
	} else {
		go o.run(sub)
	}
	return nil
}

func (s *Service) stopOutputs(match func(*deviceOutput) bool) {
	if s.output == nil {
		return
	}
	m := s.output
	m.mu.Lock()
	owners := slices.Collect(maps.Values(m.owners))
	m.mu.Unlock()
	var stopped []*deviceOutput
	for _, o := range owners {
		if match(o) {
			o.cancel()
			stopped = append(stopped, o)
		}
	}
	for _, o := range stopped {
		<-o.done
	}
}

func (s *Service) enforceGrants(ctx context.Context) error {
	if s.output == nil {
		return nil
	}
	settings, err := s.Settings(ctx)
	var grants map[string][]string
	if err == nil && !settings.Shared {
		grants, err = s.grants(ctx, "")
	}
	s.stopOutputs(func(o *deviceOutput) bool {
		d, exists := s.protocols[deviceKind(o.udn)].registry.Lookup(o.udn)
		return !exists || !d.Online || err != nil || (!settings.Shared && o.actor.Role != "admin" && !slices.Contains(grants[d.UDN], o.actor.ID))
	})
	return err
}

// 登出只释放已经没有有效会话的账号；与同一时刻的新认领串行。
func (s *Service) SessionRevoked(userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running() || s.output == nil {
		return
	}
	active, err := identity.HasActiveSession(s.lifecycle, s.db, userID)
	if err != nil {
		s.output.logger.Error("查询设备占用者会话失败", "module", "upnp", "user_id", userID, "error", err)
	}
	if !active || err != nil {
		s.stopOutputs(func(o *deviceOutput) bool { return o.actor.ID == userID })
	}
}

type deviceOutput struct {
	manager                                *outputManager
	registry                               *Registry
	actor                                  identity.User
	device                                 Device
	udn, deviceID, zoneID, sessionID, base string
	ctx                                    context.Context
	cancel                                 context.CancelFunc
	done                                   chan struct{}
	releaseErr                             error

	zone        zones.Zone
	seq         *int64
	path        string
	state       string
	volume      int
	seconds     float64
	started     time.Time
	lastRel     time.Duration
	useClock    bool
	needsStream bool
}

// 队列成功转移后检查原会话，避免释放已经被其他命令重新使用的设备。
func (s *Service) ReleaseIdle(ctx context.Context, userID, udn, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.output
	m.mu.Lock()
	o := m.owners[udn]
	m.mu.Unlock()
	if o == nil || o.actor.ID != userID || o.sessionID != sessionID || !m.zones.ManagedOutputIdle(userID, sessionID) {
		return nil
	}
	o.cancel()
	<-o.done
	return o.releaseErr
}
