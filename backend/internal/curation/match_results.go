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
	SnapshotID string          `json:"snapshot_id,omitempty"`
	Candidates json.RawMessage `json:"candidates"`
	Selection  *MatchSelection `json:"selection"`
	Source     string          `json:"source"`
	Status     string          `json:"status"`
	CheckedAt  *time.Time      `json:"checked_at"`
	Reason     string          `json:"reason"`
	Conflicts  []string        `json:"conflicts"`
}

// MatchModelConfigured 使用整理指定的模型，未单独指定时沿用开启者的管家模型。
func MatchModelConfigured(ctx context.Context, q operations.Queryer) (bool, error) {
	var configured bool
	err := q.QueryRowContext(ctx, `SELECT coalesce(nullif(c.model,''),u.model,'')<>''
FROM curation_settings c LEFT JOIN steward_user_settings u ON u.user_id=c.enabled_by WHERE c.id=1`).Scan(&configured)
	return configured, err
}

func SaveReleaseMatchResult(ctx context.Context, q Execer, id string, r ReleaseMatchResult) error {
	if len(r.Candidates) == 0 {
		r.Candidates = json.RawMessage("[]")
	}
	_, err := q.ExecContext(ctx, `INSERT INTO curation_release_match_results
 (release_id,source,status,checked_at,reason,conflicts,snapshot_id,candidates,selection) VALUES ($1,$2,$3,$4,$5,coalesce($6::text[],'{}'),nullif($7,'')::uuid,$8,NULL)
 ON CONFLICT(release_id,source) DO UPDATE SET status=excluded.status,
 checked_at=COALESCE(excluded.checked_at,curation_release_match_results.checked_at),reason=excluded.reason,conflicts=excluded.conflicts,snapshot_id=excluded.snapshot_id,candidates=excluded.candidates,selection=CASE WHEN excluded.status='matched' AND curation_release_match_results.status='matched' THEN curation_release_match_results.selection END`,
		id, r.Source, r.Status, r.CheckedAt, r.Reason, r.Conflicts, r.SnapshotID, r.Candidates)
	return err
}

func (w *Writer) ReleaseMatchResults(ctx context.Context, id string) ([]ReleaseMatchResult, error) {
	if !uuid.Valid(id) {
		return nil, operations.Invalid("invalid_input", "发行编号无效")
	}
	var data []byte
	var visible bool
	err := w.db.QueryRowContext(ctx, `WITH visible AS (`+catalog.VisibleReleaseIDsSQL+`)
 SELECT EXISTS(SELECT FROM visible WHERE id=$1),COALESCE((SELECT jsonb_agg((to_jsonb(r)-'release_id'-'selection') || jsonb_build_object('selection',CASE WHEN r.status='matched' AND r.selection ? 'selected_at' AND EXISTS (SELECT FROM external_links l WHERE l.source=r.source AND l.external_id=r.selection->>'external_id' AND (l.object_type='release' AND l.object_id=r.release_id OR l.object_type='track' AND EXISTS(SELECT FROM tracks t JOIN media m ON m.id=t.medium_id WHERE t.id=l.object_id AND m.release_id=r.release_id))) THEN r.selection-'key'-'snapshot_id' END) ORDER BY source)
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

type MatchSelection struct {
	ExternalID   string    `json:"external_id"`
	SelectedBy   string    `json:"selected_by"`
	SelectedAt   time.Time `json:"selected_at"`
	OperationIDs []string  `json:"operation_ids"`
}

type MatchChoice struct {
	Source     string `json:"source"`
	SnapshotID string `json:"snapshot_id"`
	ExternalID string `json:"external_id"`
}

func (w *Writer) MatchConflicts(ctx context.Context, page, size int) (json.RawMessage, error) {
	var data []byte
	err := w.db.QueryRowContext(ctx, `WITH visible AS (`+catalog.VisibleReleaseIDsSQL+`), conflicts AS (
 SELECT m.*,r.title FROM curation_release_match_results m JOIN visible v ON v.id=m.release_id JOIN release_effective r ON r.id=v.id
 JOIN curation_eligible_releases eligible ON eligible.id=v.id WHERE m.status='conflict')
 SELECT jsonb_build_object('items',coalesce((SELECT jsonb_agg((to_jsonb(p) || jsonb_build_object('selection',NULL)) ORDER BY checked_at DESC,release_id) FROM
 (SELECT * FROM conflicts ORDER BY checked_at DESC,release_id LIMIT $2 OFFSET ($1::int-1)*$2) p),'[]'),
 'pagination',jsonb_build_object('page',$1::int,'page_size',$2::int,'total',(SELECT count(*) FROM conflicts)))`, page, size).Scan(&data)
	return data, err
}
