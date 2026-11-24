// Package insights 管理实例的匿名统计设置、预览与每日发送。
package insights

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/qlqq/roomusic/backend/internal/insights/schema"
	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

// Inputs 在装配时绑定到各模块的聚合查询。
type Inputs struct {
	Accounts interface {
		EnabledAccountCounts(context.Context) (members, admins int, err error)
	}
	Library interface {
		LibraryCounts(context.Context) (tracks, releases int, err error)
	}
	Models interface {
		HasConfiguredModel(context.Context) (bool, error)
	}
	Tokens interface {
		HasUnrevokedToken(context.Context) (bool, error)
	}
	Curation interface {
		InsightSettings(context.Context) (enabled, modelConfigured bool, err error)
	}
	Plugins interface {
		EnabledPluginCount(context.Context) (int, error)
	}
	Zones interface {
		DeviceUsage(context.Context, time.Time) (browser, upnp, cast, airplay int, dsp bool, err error)
	}
	LANEnabled    func(context.Context) (bool, error)
	ScheduledScan func(context.Context) (bool, error)
}

type Options struct {
	Endpoint    string
	Version     string
	ForcedOff   bool
	ExplicitURL bool
	MockURL     string
}

type Service struct {
	db         *sql.DB
	inputs     Inputs
	options    Options
	client     *http.Client
	endpoint   *url.URL
	publicPage string
	container  bool
	wake       chan struct{}
	mu         sync.Mutex
	cancel     context.CancelFunc
}

func New(db *sql.DB, inputs Inputs, opts Options) *Service {
	// 地址已在配置加载时校验。
	endpoint, err := url.Parse(opts.Endpoint)
	if err != nil {
		panic(err)
	}
	page := (&url.URL{Scheme: endpoint.Scheme, Host: endpoint.Host, Path: "/"}).String()
	if opts.MockURL != "" {
		endpoint, err = url.Parse(opts.MockURL + "/insights/api/v1/heartbeat")
		if err != nil {
			panic(err)
		}
	}
	mock := sources.NewMockHost(opts.MockURL)
	client, err := egress.NewClient(mock.EgressOptions(egress.Options{Policy: egress.PublicOnly, Timeout: 10 * time.Second}))
	if err != nil {
		panic(err)
	}
	_, docker := os.Stat("/.dockerenv")
	_, podman := os.Stat("/run/.containerenv")
	return &Service{db: db, inputs: inputs, options: opts, client: client, endpoint: endpoint, publicPage: page, container: docker == nil || podman == nil, wake: make(chan struct{}, 1)}
}

type Status struct {
	Enabled            bool            `json:"enabled"`
	ForcedOff          bool            `json:"forced_off"`
	InstanceID         string          `json:"instance_id"`
	NoticeAcknowledged bool            `json:"notice_acknowledged"`
	FirstSendAfter     time.Time       `json:"first_send_after"`
	LastAttemptAt      *time.Time      `json:"last_attempt_at"`
	LastSuccessAt      *time.Time      `json:"last_success_at"`
	LastError          *string         `json:"last_error"`
	EndpointHost       string          `json:"endpoint_host"`
	PublicPageURL      string          `json:"public_page_url"`
	Payload            json.RawMessage `json:"payload"`
}

func (s *Service) state(ctx context.Context) (Status, error) {
	state := Status{ForcedOff: s.options.ForcedOff, EndpointHost: s.endpoint.Host, PublicPageURL: s.publicPage}
	err := s.db.QueryRowContext(ctx, `SELECT enabled, instance_id::text, notice_acknowledged_at IS NOT NULL,
        created_at + interval '24 hours', last_attempt_at, last_success_at, last_error FROM instance_insights WHERE id`).
		Scan(&state.Enabled, &state.InstanceID, &state.NoticeAcknowledged, &state.FirstSendAfter, &state.LastAttemptAt, &state.LastSuccessAt, &state.LastError)
	state.Enabled = state.Enabled && !state.ForcedOff
	return state, err
}

func (s *Service) Status(ctx context.Context) (Status, error) {
	state, err := s.state(ctx)
	if err != nil {
		return Status{}, err
	}
	members, _, err := s.inputs.Accounts.EnabledAccountCounts(ctx)
	if err != nil {
		return Status{}, err
	}
	state.Payload, err = s.payload(ctx, state.InstanceID, members)
	return state, err
}

var ErrForcedOff = errors.New("部署配置已关闭匿名统计")

func (s *Service) SetEnabled(ctx context.Context, enabled bool) error {
	if s.options.ForcedOff {
		return ErrForcedOff
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.ExecContext(ctx, `UPDATE instance_insights SET enabled=$1 WHERE id`, enabled); err != nil {
		return err
	}
	if !enabled && s.cancel != nil {
		s.cancel()
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return nil
}

func (s *Service) Acknowledge(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE instance_insights SET notice_acknowledged_at=now() WHERE id AND notice_acknowledged_at IS NULL`)
	return err
}

func (s *Service) payload(ctx context.Context, id string, members int) (json.RawMessage, error) {
	p := schema.Payload{Schema: schema.Version, InstanceID: id, Version: s.options.Version, OS: runtime.GOOS,
		Arch: runtime.GOARCH, Container: s.container, Members: schema.MembersBucket(members)}
	if p.Version == "" {
		p.Version = schema.DevVersion
	}
	tracks, releases, err := s.inputs.Library.LibraryCounts(ctx)
	if err != nil {
		return nil, err
	}
	p.Library = schema.Library{Tracks: schema.LibraryBucket(tracks), Releases: schema.LibraryBucket(releases)}
	plugins, err := s.inputs.Plugins.EnabledPluginCount(ctx)
	if err != nil {
		return nil, err
	}
	p.Features.Plugins = schema.DeviceBucket(plugins)
	browser, upnp, cast, airplay, dsp, err := s.inputs.Zones.DeviceUsage(ctx, time.Now().Add(-30*24*time.Hour))
	if err != nil {
		return nil, err
	}
	p.Devices30d = schema.Devices{Browser: schema.DeviceBucket(browser), UPnP: schema.DeviceBucket(upnp), Cast: schema.DeviceBucket(cast), AirPlay: schema.DeviceBucket(airplay)}
	p.Features.DSP = dsp
	if p.Features.AutoCuration, p.Features.StewardModel, err = s.inputs.Curation.InsightSettings(ctx); err != nil {
		return nil, err
	}
	if !p.Features.StewardModel {
		if p.Features.StewardModel, err = s.inputs.Models.HasConfiguredModel(ctx); err != nil {
			return nil, err
		}
	}
	if p.Features.MCPToken, err = s.inputs.Tokens.HasUnrevokedToken(ctx); err != nil {
		return nil, err
	}
	if p.Features.LANDevices, err = s.inputs.LANEnabled(ctx); err != nil {
		return nil, err
	}
	if p.Features.ScheduledScan, err = s.inputs.ScheduledScan(ctx); err != nil {
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(p)
}
