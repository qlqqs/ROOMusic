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

	"github.com/qlqq/roomusic/backend/internal/devices/airplay"
	"github.com/qlqq/roomusic/backend/internal/devices/cast"
	"github.com/qlqq/roomusic/backend/internal/devices/slimproto"
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
	handovers        map[string]zones.DeviceOccupant // 仅在 Service.mu 内读写。
	artwork          func(context.Context, string, bool) (string, []byte, error)
}

func (s *Service) AttachOutput(z *zones.Service, lanURL, httpAddr string, logger *slog.Logger) {
	s.output = &outputManager{zones: z, lanURL: strings.TrimRight(lanURL, "/"), httpAddr: httpAddr, logger: logger, owners: map[string]*deviceOutput{}, handovers: map[string]zones.DeviceOccupant{}}
}

func (s *Service) WithAirPlayArtwork(read func(context.Context, string, bool) (string, []byte, error)) {
	s.output.artwork = read
}

func (m *outputManager) baseURL(d Device) (string, error) {
	if m.lanURL != "" && (d.Kind != "slimproto" || m.httpAddr == "") {
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
		lossless := codec == "flac" || codec == "wav" || codec == "alac"
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
	items, err := s.visibleLocked(ctx, actor, "")
	if err != nil {
		return nil, err
	}
	out := make([]zones.ManagedDevice, 0, len(items))
	s.output.mu.Lock()
	defer s.output.mu.Unlock()
	for _, d := range items {
		row := zones.ManagedDevice{Kind: d.Kind, MaxSampleRate: d.Capabilities.MaxSampleRate, UDN: d.UDN, Name: d.Name, InitialVolume: d.initialVolume, Capabilities: playbackCaps(d.Capabilities), FormatDeclared: d.Capabilities.FormatDeclared}
		if info := d.Capabilities.AirPlay; info != nil {
			row.UnavailableReason = string(info.UnavailableReason)
		}
		if info := d.Capabilities.SlimProto; info != nil && info.Unsupported {
			row.UnavailableReason = "device_unsupported"
		}
		if pending, ok := s.output.handovers[d.UDN]; ok {
			row.OccupiedBy = &pending
		} else if owner := s.output.owners[d.UDN]; owner != nil {
			row.OccupiedBy = &zones.DeviceOccupant{UserID: owner.actor.ID, DisplayName: owner.actor.Username}
		}
		out = append(out, row)
	}
	return out, nil
}

func (s *Service) Acquire(ctx context.Context, userID, deviceID, udn string, caps zones.Capabilities, takeover bool) error {
	return s.acquire(ctx, userID, deviceID, udn, caps, takeover, "", nil)
}

func (s *Service) acquire(ctx context.Context, userID, deviceID, udn string, caps zones.Capabilities, takeover bool, leaderID string, rejoin *deviceOutput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.output
	protocol := s.protocols[deviceKind(udn)]
	run := protocol.ctx
	for {
		if protocol.ctx != run || run == nil || run.Err() != nil {
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
		device, base, err := s.outputDevice(ctx, actor, udn)
		if err != nil {
			return err
		}
		if pending, ok := m.handovers[udn]; ok {
			return &zones.DeviceBusyError{Occupant: pending}
		}
		m.mu.Lock()
		old := m.owners[udn]
		m.mu.Unlock()
		var leader *deviceOutput
		if leaderID != "" {
			leader = m.leader(userID, leaderID)
			if leader == nil || leader.udn == udn || (rejoin != nil && leader != rejoin) {
				return zones.ErrGroupUnsupported
			}
		}
		if old == nil {
			if leader != nil {
				if rejoin != nil {
					members := m.zones.GroupMembers(userID, leader.sessionID)
					if !slices.ContainsFunc(members, func(member zones.GroupMember) bool {
						return member.DeviceID == deviceID && member.UDN == udn
					}) {
						return zones.ErrNotFound
					}
				}
				return m.claimMember(leader, deviceID, device)
			}
			return s.claimOutput(ctx, actor, deviceID, device, base, caps)
		}
		if old.ctx.Err() == nil && old.device.slimPlayer == device.slimPlayer {
			if old.actor.ID == userID && old.deviceID == deviceID && old.leader == leader {
				return nil
			}
			if !takeover && !(leader != nil && rejoin == nil && old.leader == nil && old.actor.ID == userID) {
				return &zones.DeviceBusyError{Occupant: zones.DeviceOccupant{UserID: old.actor.ID, DisplayName: old.actor.Username}}
			}
		}
		if rejoin != nil {
			return &zones.DeviceBusyError{Occupant: zones.DeviceOccupant{UserID: old.actor.ID, DisplayName: old.actor.Username}}
		}
		m.handovers[udn] = zones.DeviceOccupant{UserID: actor.ID, DisplayName: actor.Username}
		old.cancel()
		s.mu.Unlock()
		<-old.done
		s.mu.Lock()
		delete(m.handovers, udn)
		if old.releaseErr != nil {
			return old.releaseErr
		}
		// 仍持有锁，从头核对本次协议周期、账号授权、在线状态和占用。
	}
}

func (s *Service) outputDevice(ctx context.Context, actor identity.User, udn string) (Device, string, error) {
	visible, err := s.visibleLocked(ctx, actor, udn)
	if err != nil {
		return Device{}, "", err
	}
	if len(visible) == 0 {
		return Device{}, "", zones.ErrNotFound
	}
	device := visible[0].Device
	if info := device.Capabilities.SlimProto; info != nil && info.Unsupported {
		return Device{}, "", zones.ErrDeviceUnsupported
	}
	if device.Kind != "airplay" {
		base, err := s.output.baseURL(device)
		return device, base, err
	}
	info := device.Capabilities.AirPlay
	if info != nil && info.UnavailableReason == airplay.PrecisionUnavailable {
		return Device{}, "", zones.ErrPrecisionUnavailable
	}
	if info == nil || info.UnavailableReason != "" {
		return Device{}, "", zones.ErrDeviceUnsupported
	}
	if (info.RequiresPIN || info.RequiresPassword) && !info.Paired {
		return Device{}, "", zones.ErrDevicePairingRequired
	}
	return device, "", nil
}

func (s *Service) claimOutput(ctx context.Context, actor identity.User, deviceID string, device Device, base string, caps zones.Capabilities) error {
	m := s.output
	userID, udn := actor.ID, device.UDN
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
	if device.Kind == "airplay" {
		receiver := airplay.Device{Key: device.UDN, Name: device.Name, Model: device.Model, Address: device.Address, LocalAddress: device.LocalAddress, Info: *device.Capabilities.AirPlay}
		p := airplayOutput{deviceOutput: o, receiver: receiver, clock: &s.clock, credentials: s.airplayCredentials, registry: s.protocols["airplay"].registry}
		go p.run(sub)
	} else if device.Kind == "slimproto" {
		output := slimproto.Output{Zones: m.zones, UserID: userID, DeviceID: deviceID, ZoneID: o.zoneID, SessionID: o.sessionID, BaseURL: base, Player: device.slimPlayer, Logger: m.logger, Offline: func() { s.slim.Offline(udn, device.slimPlayer) }}
		output.Member = func(ctx context.Context, member zones.GroupMember, rejoin bool) (slimproto.MemberLease, error) {
			return s.slimMember(ctx, o, member, rejoin)
		}
		go func() {
			position := output.Run(run, sub)
			o.cancel()
			m.finishMembers(o)
			o.release(position)
		}()
	} else if device.Kind == "cast" {
		output := cast.Output{Zones: m.zones, UserID: userID, DeviceID: deviceID, ZoneID: o.zoneID, SessionID: o.sessionID, BaseURL: base, Address: device.Address, Logger: m.logger, Online: func() bool { d, ok := s.cast.Lookup(udn); return ok && d.Online }, Offline: func() { s.cast.Offline(udn) }}
		go func() { position := output.Run(run, sub); o.cancel(); o.release(position) }()
	} else {
		go o.run(sub)
	}
	return nil
}

func (s *Service) cancelOutputs(match func(*deviceOutput) bool) []*deviceOutput {
	if s.output == nil {
		return nil
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
	return stopped
}

func (s *Service) waitOutputsLocked(stopped []*deviceOutput) {
	if len(stopped) == 0 {
		return
	}
	s.mu.Unlock()
	for _, o := range stopped {
		<-o.done
	}
	s.mu.Lock()
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
	stopped := s.cancelOutputs(func(o *deviceOutput) bool {
		d, exists := s.protocols[deviceKind(o.udn)].registry.Lookup(o.udn)
		return !exists || !d.Online || err != nil || (!settings.Shared && o.actor.Role != "admin" && !slices.Contains(grants[d.UDN], o.actor.ID))
	})
	s.waitOutputsLocked(stopped)
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
		stopped := s.cancelOutputs(func(o *deviceOutput) bool { return o.actor.ID == userID })
		s.waitOutputsLocked(stopped)
	}
}

type deviceOutput struct {
	leader *deviceOutput
	// 受 manager.mu 保护；协调器归还播放器后，成员的物理占用才能交接。
	borrowed                               chan struct{}
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
	s.waitOutputsLocked([]*deviceOutput{o})
	return o.releaseErr
}
