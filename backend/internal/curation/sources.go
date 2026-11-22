package curation

import (
	"context"
	"database/sql"
	"slices"

	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

type SourceRegistration struct {
	Declaration sources.Declaration
	Releases    sources.ReleaseFinder
	Recordings  sources.RecordingFinder
	Artists     sources.ArtistFinder
	Covers      sources.CoverFinder
	Lyrics      sources.LyricsFinder
}

type SourceSettings struct {
	ID             string               `json:"id"`
	Name           string               `json:"name"`
	Capabilities   []sources.Capability `json:"capabilities"`
	Enabled        bool                 `json:"enabled"`
	Active         bool                 `json:"active"`
	InactiveReason *string              `json:"inactive_reason"`
}

type Sources struct {
	db         *sql.DB
	registered []SourceRegistration
}

func NewSources(db *sql.DB, registrations ...SourceRegistration) *Sources {
	return &Sources{db: db, registered: slices.Clone(registrations)}
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

func sourceState(ctx context.Context, tx *sql.Tx, d sources.Declaration, overall Settings) (SourceSettings, error) {
	item := SourceSettings{ID: d.ID, Name: d.Name, Capabilities: d.Capabilities}
	err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT enabled FROM curation_source_settings WHERE source=$1),$2::boolean)`, d.ID, d.DefaultEnabled).Scan(&item.Enabled)
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
		item, err := sourceState(ctx, tx, r.Declaration, settings)
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
	settings, err := loadSettings(ctx, tx)
	if err != nil {
		return SourceSettings{}, err
	}
	item, err := sourceState(ctx, tx, r.Declaration, settings)
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
	item, err := sourceState(ctx, tx, r.Declaration, settings)
	if err != nil {
		return err
	}
	if !item.Enabled {
		return operations.Conflict("source_disabled", "外部源开关已关闭")
	}
	return tx.Commit()
}
