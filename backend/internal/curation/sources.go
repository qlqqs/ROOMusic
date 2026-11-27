package curation

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

type SourceRegistration struct {
	PluginHash      string
	Declaration     sources.Declaration
	Releases        sources.ReleaseFinder
	Recordings      sources.RecordingFinder
	Artists         sources.ArtistFinder
	WikidataArtists sources.WikidataArtistFinder
	ArtistLinks     sources.ArtistLinkFinder
	ArtistImages    sources.ArtistImageFinder
	ArtistBios      sources.ArtistBioFinder
	Covers          sources.CoverFinder
	Lyrics          sources.LyricsFinder
	Fingerprints    sources.FingerprintFinder
	Unavailable     func(context.Context, string) string
	Requirements    func(context.Context, *sql.Tx, string) ([]SourceRequirement, error)
}

type SourceRequirement struct {
	Kind  string `json:"kind"`
	Ready bool   `json:"ready"`
}

type SourceSettings struct {
	Requirements   []SourceRequirement  `json:"requirements,omitempty"`
	Upstreams      []string             `json:"upstreams,omitempty"`
	ID             string               `json:"id"`
	Name           string               `json:"name"`
	Capabilities   []sources.Capability `json:"capabilities"`
	Enabled        bool                 `json:"enabled"`
	Active         bool                 `json:"active"`
	Keys           []SourceKeySettings  `json:"keys,omitempty"`
	InactiveReason *string              `json:"inactive_reason"`
}

type Sources struct {
	mu               sync.RWMutex
	plugins          []SourceRegistration
	rejectedDefaults sync.Map // 键为来源与被拒绝的内置密钥名。
	keys             KeyStore
	scanCheck        func(context.Context) (bool, error)
	db               *sql.DB
	registered       []SourceRegistration
}

func NewSources(db *sql.DB, registrations ...SourceRegistration) *Sources {
	return &Sources{db: db, registered: slices.Clone(registrations)}
}

func (s *Sources) WithScanCheck(check func(context.Context) (bool, error)) *Sources {
	s.scanCheck = check
	return s
}

func (s *Sources) ReplacePlugins(reg []SourceRegistration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plugins = reg
}
func (s *Sources) registrations() []SourceRegistration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Concat(s.registered, s.plugins)
}
func (s *Sources) PluginIDs(cap sources.Capability) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var ids []string
	for _, r := range s.plugins {
		if slices.Contains(r.Declaration.Capabilities, cap) {
			ids = append(ids, r.Declaration.ID)
		}
	}
	return ids
}
func (s *Sources) lookup(id string) (SourceRegistration, bool) {
	for _, r := range s.registrations() {
		if r.Declaration.ID == id {
			return r, true
		}
	}
	return SourceRegistration{}, false
}

func unknownSource() error { return operations.NotFound("not_found", "外部源不存在") }

type manualSourceActorKey struct{}

// WithManualSourceActor 允许管理员点名单项查询，来源自身的设置与扫描闸门仍由 Check 核对。
func WithManualSourceActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, manualSourceActorKey{}, actor)
}

func manualSourceActor(ctx context.Context) string {
	if actor, _ := ctx.Value(manualSourceActorKey{}).(string); actor != "" {
		return actor
	}
	return ArtistWebActor(ctx)
}

func (s *Sources) sourceState(ctx context.Context, tx *sql.Tx, r SourceRegistration, overall Settings) (SourceSettings, error) {
	d := r.Declaration
	item := SourceSettings{Upstreams: d.Upstreams, ID: d.ID, Name: d.Name, Capabilities: d.Capabilities, Enabled: d.DefaultEnabled}
	err := tx.QueryRowContext(ctx, `SELECT enabled FROM curation_source_settings WHERE source=$1`, d.ID).Scan(&item.Enabled)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return SourceSettings{}, err
	}
	item.Keys, err = loadKeySettings(ctx, tx, d)
	if err != nil {
		return SourceSettings{}, err
	}
	enabler := ""
	manualActor := manualSourceActor(ctx)
	if manualActor != "" {
		enabler = manualActor
	} else if overall.EnabledBy != nil {
		enabler = overall.EnabledBy.ID
	}
	if r.Requirements != nil {
		item.Requirements, err = r.Requirements(ctx, tx, enabler)
		if err != nil {
			return SourceSettings{}, err
		}
	}
	reason := ""
	switch {
	case manualActor == "" && !overall.Enabled:
		reason = "curation_disabled"
	case manualActor == "" && !overall.Active:
		reason = "enabler_unavailable"
	case !item.Enabled:
		reason = "source_disabled"
	case r.Unavailable != nil:
		reason = r.Unavailable(ctx, enabler)
	case slices.ContainsFunc(item.Requirements, func(r SourceRequirement) bool { return r.Kind == "search" && !r.Ready }):
		reason = "search_unconfigured"
	}
	if reason == "" {
		switch {
		case slices.ContainsFunc(item.Keys, func(k SourceKeySettings) bool { return !k.Optional && !k.Configured }):
			reason = "key_missing"
		case slices.ContainsFunc(item.Keys, func(k SourceKeySettings) bool { return k.Status == "invalid" }):
			reason = "key_invalid"
		case slices.ContainsFunc(item.Keys, func(k SourceKeySettings) bool {
			_, rejected := s.rejectedDefaults.Load(sourceKeyPurpose(d.ID, k.Name))
			return !k.Configured && k.Optional && rejected
		}):
			reason = "bundled_key_rejected"
		}
	}
	item.Active = reason == ""
	if !item.Active {
		item.InactiveReason = &reason
	}
	return item, nil
}

func (s *Sources) List(ctx context.Context) ([]SourceSettings, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	settings, err := loadSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	items := make([]SourceSettings, 0, len(s.registered))
	for _, r := range s.registrations() {
		item, err := s.sourceState(ctx, tx, r, settings)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, tx.Commit()
}

func (s *Sources) SetEnabled(ctx context.Context, id, actorID string, enabled bool) (SourceSettings, error) {
	r, ok := s.lookup(id)
	if !ok {
		return SourceSettings{}, unknownSource()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SourceSettings{}, err
	}
	defer tx.Rollback()
	if err := lockPluginSettings(ctx, tx, r, enabled); err != nil {
		return SourceSettings{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO curation_source_settings(source,enabled,updated_by) VALUES($1,$2,$3)
 ON CONFLICT(source) DO UPDATE SET enabled=excluded.enabled,updated_by=excluded.updated_by,updated_at=now()`, id, enabled, actorID)
	if err != nil {
		return SourceSettings{}, err
	}
	return s.committedSourceState(ctx, tx, r)
}

// 管理员提交的开关和密钥只对当前确认的版本有效。行锁与升级、删除串行。
func lockPluginSettings(ctx context.Context, tx *sql.Tx, r SourceRegistration, enabling bool) error {
	id, ok := strings.CutPrefix(r.Declaration.ID, "plugin:")
	if !ok {
		return nil
	}
	var digest, reason string
	err := tx.QueryRowContext(ctx, "SELECT sha256,disabled_reason FROM source_plugins WHERE id=$1 FOR UPDATE", id).Scan(&digest, &reason)
	if errors.Is(err, sql.ErrNoRows) {
		return unknownSource()
	}
	if err != nil {
		return err
	}
	if digest != r.PluginHash {
		return operations.Conflict("plugin_changed", "插件已更新，请刷新设置")
	}
	if enabling && reason != "" {
		return operations.Conflict(reason, "插件文件已变化，请重新预检安装")
	}
	return nil
}

func (s *Sources) committedSourceState(ctx context.Context, tx *sql.Tx, r SourceRegistration) (SourceSettings, error) {
	settings, err := loadSettings(ctx, tx)
	if err != nil {
		return SourceSettings{}, err
	}
	item, err := s.sourceState(ctx, tx, r, settings)
	if err != nil {
		return SourceSettings{}, err
	}
	return item, tx.Commit()
}

func (s *Sources) Check(ctx context.Context, id string) error {
	r, ok := s.lookup(id)
	if !ok {
		return unknownSource()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	settings, err := loadSettings(ctx, tx)
	if err != nil {
		return err
	}
	if manualSourceActor(ctx) == "" && !settings.Enabled {
		return operations.Conflict("curation_disabled", "自动整理开关已关闭")
	}
	if manualSourceActor(ctx) == "" && !settings.Active {
		return operations.Conflict("curation_disabled", "自动整理开启者已不是启用状态的管理员")
	}
	if manualSourceActor(ctx) == "" && settings.Paused {
		return operations.Conflict("curation_paused", "自动整理已暂停")
	}
	item, err := s.sourceState(ctx, tx, r, settings)
	if err != nil {
		return err
	}
	// 匹配用指纹闸门区分暂时中止与不可用；其余源沿用关闭后直接返回的顺序。
	if !item.Enabled && !slices.Contains(r.Declaration.Capabilities, sources.FingerprintLookup) {
		return disabledSource("source_disabled")
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if s.scanCheck != nil {
		running, err := s.scanCheck(ctx)
		if err != nil {
			return err
		}
		if running {
			return operations.Conflict("scan_running", "曲库扫描进行中")
		}
	}
	if item.InactiveReason != nil {
		return disabledSource(*item.InactiveReason)
	}
	for _, r := range item.Requirements {
		if r.Kind == "model" && !r.Ready {
			return operations.Conflict("source_disabled", "开启者尚未配置可用的管家模型")
		}
	}
	return nil
}

func disabledSource(reason string) error {
	message := "外部源开关已关闭"
	switch reason {
	case "search_unconfigured":
		message = "开启者尚未配置可用的搜索来源"
	case "fingerprint_unavailable":
		message = "声学指纹工具不可用"
	case "bundled_key_rejected":
		message = "外部源拒绝了内置密钥"
	case "key_missing":
		message = "尚未填齐外部源密钥"
	case "key_invalid":
		message = "外部源密钥无效，请重新填写"
	}
	return operations.Conflict("source_disabled", message)
}

func (s *Sources) EnabledPluginCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM curation_source_settings
        WHERE enabled AND source LIKE 'plugin:%'`).Scan(&n)
	return n, err
}

// MatchFallbackReadiness 与联网来源共用开启者的模型和搜索配置，不检查模型预算。
func (s *Sources) MatchFallbackReadiness(ctx context.Context) (model, web bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, false, err
	}
	defer tx.Rollback()
	settings, err := loadSettings(ctx, tx)
	if err != nil {
		return false, false, err
	}
	model = settings.Model != ""
	if r, ok := s.lookup("web_search"); ok {
		item, err := s.sourceState(ctx, tx, r, settings)
		if err != nil {
			return false, false, err
		}
		for _, req := range item.Requirements {
			if req.Kind == "model" {
				model = req.Ready
			}
		}
		web = item.Active && model
	}
	return model, web, tx.Commit()
}

// ScanRunning 让无需外部来源的模型核对共用扫描闸门。
func (s *Sources) ScanRunning(ctx context.Context) (bool, error) {
	if s.scanCheck == nil {
		return false, nil
	}
	return s.scanCheck(ctx)
}

// ArtistWebConfiguration 读取点击按钮的管理员在管家中的搜索和模型配置。
func (s *Sources) ArtistWebConfiguration(ctx context.Context, actor string) (bool, string, error) {
	reg, ok := s.lookup("web_search")
	if !ok {
		return false, "search_unconfigured", nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, "", err
	}
	defer tx.Rollback()
	item, err := s.sourceState(WithArtistWebActor(ctx, actor), tx, reg, Settings{})
	if err != nil {
		return false, "", err
	}
	for _, r := range item.Requirements {
		if r.Kind == "search" && !r.Ready {
			return false, "search_unconfigured", nil
		}
	}
	for _, r := range item.Requirements {
		if r.Kind == "model" && !r.Ready {
			return false, "model_not_configured", nil
		}
	}
	if !item.Enabled {
		return true, "source_disabled", nil
	}
	return true, "", tx.Commit()
}
