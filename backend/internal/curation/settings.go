// Package curation 管理自动整理的开关、批次、写入与不再提出标记。
package curation

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/artwork"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

type Writer struct {
	db       *sql.DB
	artworks *artwork.Store
	logger   *slog.Logger
}

func NewWriter(db *sql.DB, dataDirectory string, logger *slog.Logger) *Writer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Writer{db: db, artworks: artwork.NewStore(dataDirectory), logger: logger}
}

type Enabler struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}
type Settings struct {
	Paused         bool       `json:"paused"`
	Enabled        bool       `json:"enabled"`
	EnabledBy      *Enabler   `json:"enabled_by"`
	EnabledAt      *time.Time `json:"enabled_at"`
	Active         bool       `json:"active"`
	InactiveReason *string    `json:"inactive_reason"`
}

// 设置行与有效开启者的共享锁保持到自动写入或开批次的事务结束。
func loadSettings(ctx context.Context, tx *sql.Tx) (Settings, error) {
	var s Settings
	var id, username *string
	err := tx.QueryRowContext(ctx, `SELECT s.paused,s.enabled,s.enabled_by::text,u.username,s.enabled_at
 FROM curation_settings s LEFT JOIN users u ON u.id=s.enabled_by WHERE s.id=1 FOR SHARE OF s`).
		Scan(&s.Paused, &s.Enabled, &id, &username, &s.EnabledAt)
	if err != nil {
		return Settings{}, err
	}
	if id != nil {
		s.EnabledBy = &Enabler{ID: *id, Username: *username}
	}
	switch {
	case !s.Enabled:
		reason := "disabled"
		s.InactiveReason = &reason
	default:
		if id != nil {
			s.Active, err = identity.IsEnabledAdmin(ctx, tx, *id)
			if err != nil {
				return Settings{}, err
			}
		}
		if !s.Active {
			reason := "enabler_unavailable"
			s.InactiveReason = &reason
		}
	}
	return s, nil
}

func activeEnabler(ctx context.Context, tx *sql.Tx) (string, error) {
	s, err := loadSettings(ctx, tx)
	if err != nil {
		return "", err
	}
	if !s.Enabled {
		return "", operations.Conflict("curation_disabled", "自动整理开关已关闭")
	}
	if !s.Active {
		return "", operations.Conflict("curation_disabled", "自动整理开启者已不是启用状态的管理员")
	}
	return s.EnabledBy.ID, nil
}

func (w *Writer) Settings(ctx context.Context) (Settings, error) {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return Settings{}, err
	}
	defer tx.Rollback()
	s, err := loadSettings(ctx, tx)
	if err != nil {
		return Settings{}, err
	}
	return s, tx.Commit()
}

func (w *Writer) SetSettings(ctx context.Context, actorID string, enabled bool) (Settings, error) {
	return w.UpdateSettings(ctx, actorID, &enabled, nil)
}

func (w *Writer) SetPaused(ctx context.Context, actorID string, paused bool) (Settings, error) {
	return w.UpdateSettings(ctx, actorID, nil, &paused)
}

func (w *Writer) UpdateSettings(ctx context.Context, actorID string, enabled, paused *bool) (Settings, error) {
	if enabled == nil && paused == nil {
		return Settings{}, operations.Invalid("invalid_input", "需要指定开关或暂停状态")
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return Settings{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `UPDATE curation_settings SET enabled=COALESCE($1::boolean,enabled),
 paused=CASE WHEN NOT enabled AND $1 THEN false ELSE COALESCE($3::boolean,paused) END,
 enabled_at=CASE WHEN $1 AND (NOT enabled OR enabled_by IS DISTINCT FROM $2::uuid) THEN now() ELSE enabled_at END,
 enabled_by=CASE WHEN $1 THEN $2::uuid ELSE enabled_by END,updated_at=now() WHERE id=1`, enabled, actorID, paused)
	if err != nil {
		return Settings{}, err
	}
	s, err := loadSettings(ctx, tx)
	if err != nil {
		return Settings{}, err
	}
	return s, tx.Commit()
}
