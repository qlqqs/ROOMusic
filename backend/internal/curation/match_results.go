package curation

import (
	"context"
	"encoding/json"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type ReleaseMatchResult struct {
	Source    string     `json:"source"`
	Status    string     `json:"status"`
	CheckedAt *time.Time `json:"checked_at"`
	Reason    string     `json:"reason"`
	Conflicts []string   `json:"conflicts"`
}

// MatchModelConfigured 使用整理指定的模型，未单独指定时沿用开启者的管家模型。
func MatchModelConfigured(ctx context.Context, q operations.Queryer) (bool, error) {
	var configured bool
	err := q.QueryRowContext(ctx, `SELECT coalesce(nullif(c.model,''),u.model,'')<>''
FROM curation_settings c LEFT JOIN steward_user_settings u ON u.user_id=c.enabled_by WHERE c.id=1`).Scan(&configured)
	return configured, err
}

func SaveReleaseMatchResult(ctx context.Context, q Execer, id string, r ReleaseMatchResult) error {
	_, err := q.ExecContext(ctx, `INSERT INTO curation_release_match_results
 (release_id,source,status,checked_at,reason,conflicts) VALUES ($1,$2,$3,$4,$5,coalesce($6::text[],'{}'))
 ON CONFLICT(release_id,source) DO UPDATE SET status=excluded.status,
 checked_at=COALESCE(excluded.checked_at,curation_release_match_results.checked_at),reason=excluded.reason,conflicts=excluded.conflicts`,
		id, r.Source, r.Status, r.CheckedAt, r.Reason, r.Conflicts)
	return err
}

func (w *Writer) ReleaseMatchResults(ctx context.Context, id string) ([]ReleaseMatchResult, error) {
	if !uuid.Valid(id) {
		return nil, operations.Invalid("invalid_input", "发行编号无效")
	}
	var data []byte
	var visible bool
	err := w.db.QueryRowContext(ctx, `WITH visible AS (`+catalog.VisibleReleaseIDsSQL+`)
 SELECT EXISTS(SELECT FROM visible WHERE id=$1),COALESCE((SELECT jsonb_agg(to_jsonb(r)-'release_id' ORDER BY source)
 FROM curation_release_match_results r WHERE release_id=$1),'[]'::jsonb)`, id).Scan(&visible, &data)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, operations.NotFound("not_found", "发行不存在")
	}
	var rows []ReleaseMatchResult
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, err
	}
	for i, r := range rows {
		if r.CheckedAt != nil {
			utc := r.CheckedAt.UTC()
			rows[i].CheckedAt = &utc
		}
	}
	return rows, nil
}
