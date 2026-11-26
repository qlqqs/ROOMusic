// Package curation 管理自动整理的开关、批次、写入与不再提出标记。
package curation

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/artwork"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

type Writer struct {
	artists  *governance.ArtistGovernance
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

// ArtworkExists 检查自动封面写入使用的同一个受管存储。
func (w *Writer) ArtworkExists(key string) bool { return w.artworks.Exists(key) }

type Enabler struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}
type Settings struct {
	ModelConcurrency int        `json:"model_concurrency"`
	Model            string     `json:"model"`
	DailyTokenLimit  *int64     `json:"daily_token_limit"`
	MaxRequestTokens int64      `json:"max_request_tokens"`
	Paused           bool       `json:"paused"`
	Enabled          bool       `json:"enabled"`
	EnabledBy        *Enabler   `json:"enabled_by"`
	EnabledAt        *time.Time `json:"enabled_at"`
	Active           bool       `json:"active"`
	InactiveReason   *string    `json:"inactive_reason"`
}

// 设置行与有效开启者的共享锁保持到自动写入或开批次的事务结束。
// 等待设置行锁后，外连接可能仍保留旧的空用户名，因此加锁读设置后再按开启者 ID 查询用户名。
func loadSettings(ctx context.Context, tx *sql.Tx) (Settings, error) {
	var s Settings
	var id *string
	err := tx.QueryRowContext(ctx, `SELECT paused,enabled,enabled_by::text,enabled_at,model,model_daily_token_limit,max_request_tokens,model_concurrency
 FROM curation_settings WHERE id=1 FOR SHARE`).
		Scan(&s.Paused, &s.Enabled, &id, &s.EnabledAt, &s.Model, &s.DailyTokenLimit, &s.MaxRequestTokens, &s.ModelConcurrency)
	if err != nil {
		return Settings{}, err
	}
	if id != nil {
		var username string
		if err := tx.QueryRowContext(ctx, `SELECT username FROM users WHERE id=$1::uuid`, *id).Scan(&username); err != nil {
			return Settings{}, err
		}
		s.EnabledBy = &Enabler{ID: *id, Username: username}
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
	return w.UpdateModelSettings(ctx, actorID, enabled, paused, ModelSettingsPatch{})
}

type ModelSettingsPatch struct {
	ModelConcurrency   *int
	MaxRequestTokens   *int64
	Model              *string
	SetDailyTokenLimit bool
	DailyTokenLimit    *int64
}

func invalidTokenLimit(limit *int64) bool {
	return limit != nil && (*limit < 1 || *limit > 1_000_000_000_000)
}

func (w *Writer) UpdateModelSettings(ctx context.Context, actorID string, enabled, paused *bool, patch ModelSettingsPatch) (Settings, error) {
	if patch.Model != nil && len(*patch.Model) > 200 || invalidTokenLimit(patch.DailyTokenLimit) || invalidTokenLimit(patch.MaxRequestTokens) {
		return Settings{}, operations.Invalid("invalid_input", "模型名称或每日用量上限无效")
	}
	if patch.ModelConcurrency != nil && (*patch.ModelConcurrency < 1 || *patch.ModelConcurrency > 16) {
		return Settings{}, operations.Invalid("invalid_input", "模型并发数必须是 1 到 16 的整数")
	}
	if enabled == nil && paused == nil && patch.ModelConcurrency == nil && patch.Model == nil && !patch.SetDailyTokenLimit && patch.MaxRequestTokens == nil {
		return Settings{}, operations.Invalid("invalid_input", "需要指定开关或暂停状态")
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return Settings{}, err
	}
	defer tx.Rollback()
	if patch.MaxRequestTokens != nil {
		var old int64
		if err := tx.QueryRowContext(ctx, `SELECT max_request_tokens FROM curation_settings WHERE id=1 FOR UPDATE`).Scan(&old); err != nil {
			return Settings{}, err
		}
		if *patch.MaxRequestTokens > old {
			if _, err := tx.ExecContext(ctx, `UPDATE curation_pending_items SET status='waiting_model',model_stop_code=NULL,shelved_reason=NULL,updated_at=now() WHERE status='shelved' AND model_stop_code='content_too_long'`); err != nil {
				return Settings{}, err
			}
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE curation_settings SET enabled=COALESCE($1::boolean,enabled),
 paused=CASE WHEN NOT enabled AND $1 THEN false ELSE COALESCE($3::boolean,paused) END,
 enabled_at=CASE WHEN $1 AND (NOT enabled OR enabled_by IS DISTINCT FROM $2::uuid) THEN now() ELSE enabled_at END,
 enabled_by=CASE WHEN $1 THEN $2::uuid ELSE enabled_by END,
 updated_at=CASE WHEN $1::boolean IS NOT NULL OR $3::boolean IS NOT NULL THEN now() ELSE updated_at END,
 model=COALESCE($4,model),model_daily_token_limit=CASE WHEN $5 THEN $6 ELSE model_daily_token_limit END,
 max_request_tokens=COALESCE($7::bigint,max_request_tokens),
 model_concurrency=COALESCE($8,model_concurrency),
 model_updated_at=CASE WHEN $4::text IS NOT NULL OR $5 OR $7::bigint IS NOT NULL OR $8::integer IS NOT NULL THEN now() ELSE model_updated_at END
 WHERE id=1`, enabled, actorID, paused, patch.Model, patch.SetDailyTokenLimit, patch.DailyTokenLimit, patch.MaxRequestTokens, patch.ModelConcurrency)
	if err != nil {
		return Settings{}, err
	}
	s, err := loadSettings(ctx, tx)
	if err != nil {
		return Settings{}, err
	}
	return s, tx.Commit()
}

// InsightSettings 只读实例开关与模型是否已指定，不取出模型名称。
func (w *Writer) InsightSettings(ctx context.Context) (enabled, modelConfigured bool, err error) {
	err = w.db.QueryRowContext(ctx, `SELECT enabled, model <> '' FROM curation_settings WHERE id=1`).Scan(&enabled, &modelConfigured)
	return
}
