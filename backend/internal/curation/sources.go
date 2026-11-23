package curation

import (
	"context"
	"database/sql"
	"errors"
	"slices"

	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

type SourceRegistration struct {
	Declaration  sources.Declaration
	Releases     sources.ReleaseFinder
	Recordings   sources.RecordingFinder
	Artists      sources.ArtistFinder
	Covers       sources.CoverFinder
	Lyrics       sources.LyricsFinder
	Fingerprints sources.FingerprintFinder
	Available    func() bool
}

type SourceSettings struct {
	ID             string               `json:"id"`
	Name           string               `json:"name"`
	Capabilities   []sources.Capability `json:"capabilities"`
	Enabled        bool                 `json:"enabled"`
	Active         bool                 `json:"active"`
	Keys           []SourceKeySettings  `json:"keys,omitempty"`
	InactiveReason *string              `json:"inactive_reason"`
}

type Sources struct {
	keys       KeyStore
	scanCheck  func(context.Context) (bool, error)
	db         *sql.DB
	registered []SourceRegistration
}

func NewSources(db *sql.DB, registrations ...SourceRegistration) *Sources {
	return &Sources{db: db, registered: slices.Clone(registrations)}
}

func (s *Sources) WithScanCheck(check func(context.Context) (bool, error)) *Sources {
	s.scanCheck = check
	return s
}

func (s *Sources) lookup(id string) (SourceRegistration, bool) {
	for _, r := range s.registered {
		if r.Declaration.ID == id {
			return r, true
		}
	}
	return SourceRegistration{}, false
}

func unknownSource() error { return operations.NotFound("not_found", "外部源不存在") }

func sourceState(ctx context.Context, tx *sql.Tx, r SourceRegistration, overall Settings) (SourceSettings, error) {
	d := r.Declaration
	item := SourceSettings{ID: d.ID, Name: d.Name, Capabilities: d.Capabilities, Enabled: d.DefaultEnabled}
	err := tx.QueryRowContext(ctx, `SELECT enabled FROM curation_source_settings WHERE source=$1`, d.ID).Scan(&item.Enabled)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return SourceSettings{}, err
	}
	item.Keys, err = loadKeySettings(ctx, tx, d)
	if err != nil {
		return SourceSettings{}, err
	}
	reason := ""
	switch {
	case !overall.Enabled:
		reason = "curation_disabled"
	case !overall.Active:
		reason = "enabler_unavailable"
	case !item.Enabled:
		reason = "source_disabled"
	case r.Available != nil && !r.Available():
		reason = "fingerprint_unavailable"
	case slices.ContainsFunc(item.Keys, func(k SourceKeySettings) bool { return !k.Configured }):
		reason = "key_missing"
	case slices.ContainsFunc(item.Keys, func(k SourceKeySettings) bool { return k.Status == "invalid" }):
		reason = "key_invalid"
	default:
		item.Active = true
	}
	if reason != "" {
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
	for _, r := range s.registered {
		item, err := sourceState(ctx, tx, r, settings)
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
	_, err = tx.ExecContext(ctx, `INSERT INTO curation_source_settings(source,enabled,updated_by) VALUES($1,$2,$3)
 ON CONFLICT(source) DO UPDATE SET enabled=excluded.enabled,updated_by=excluded.updated_by,updated_at=now()`, id, enabled, actorID)
	if err != nil {
		return SourceSettings{}, err
	}
	return committedSourceState(ctx, tx, r)
}

func committedSourceState(ctx context.Context, tx *sql.Tx, r SourceRegistration) (SourceSettings, error) {
	settings, err := loadSettings(ctx, tx)
	if err != nil {
		return SourceSettings{}, err
	}
	item, err := sourceState(ctx, tx, r, settings)
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
	if !settings.Enabled {
		return operations.Conflict("curation_disabled", "自动整理开关已关闭")
	}
	if !settings.Active {
		return operations.Conflict("curation_disabled", "自动整理开启者已不是启用状态的管理员")
	}
	if settings.Paused {
		return operations.Conflict("curation_paused", "自动整理已暂停")
	}
	item, err := sourceState(ctx, tx, r, settings)
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
	return nil
}

func disabledSource(reason string) error {
	message := "外部源开关已关闭"
	switch reason {
	case "fingerprint_unavailable":
		message = "声学指纹工具不可用"
	case "key_missing":
		message = "尚未填齐外部源密钥"
	case "key_invalid":
		message = "外部源密钥无效，请重新填写"
	}
	return operations.Conflict("source_disabled", message)
}
