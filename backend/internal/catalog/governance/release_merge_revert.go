package governance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/personal/albumfavorites"
)

type ReleaseMergeSuppressionWriter interface {
	SuppressReleaseMerge(ctx context.Context, tx *sql.Tx, targetID, sourceID, revertID, actorID string) error
}

type releaseMergeKind struct {
	operationType string
	suppressions  ReleaseMergeSuppressionWriter
}

func ReleaseMergeOperations(operationType string, suppressions ReleaseMergeSuppressionWriter) operations.Kind {
	return releaseMergeKind{operationType, suppressions}
}

func (k releaseMergeKind) Type() string { return k.operationType }

// m、o、s 分别是生效的合并、写入它的操作与来源发行。
// 自动撤销额外看两个发行在记录插入之后是否仍有生效的人工结构操作。
const mergeRevertibleSQL = `s.edit_revision=o.result_revision AND NOT EXISTS (
	SELECT 1 FROM media WHERE release_id=s.id
) AND (o.operation_type <> 'release_auto_merge' OR NOT EXISTS (
	SELECT 1 FROM operations_release_targets t JOIN operations later ON later.id=t.operation_id
	WHERE t.release_id IN (m.source_release_id,m.target_release_id)
	AND later.operation_type IN ('release_group_change','release_claim','release_merge')
	AND (later.operation_type <> 'release_group_change' OR (later.origin->>'source') IS DISTINCT FROM 'curation'
		OR later.origin->>'review_decision'='human')
	AND (later.created_at,later.id)>(o.created_at,o.id)
	AND NOT EXISTS (SELECT 1 FROM operations undone WHERE undone.reverts_operation_id=later.id)
))`

const currentMergeJoin = ` FROM release_merges m
	JOIN operations o ON o.id=m.operation_id JOIN releases s ON s.id=m.source_release_id `

func (k releaseMergeKind) Revert(ctx context.Context, tx *sql.Tx, id string, r operations.Record) (operations.Outcome, error) {
	var empty operations.Outcome
	if k.suppressions == nil {
		return empty, errors.New("撤销发行合并需要不再提出标记的写入器")
	}
	var before mergeRecordState
	if err := json.Unmarshal(r.Before, &before); err != nil {
		return empty, err
	}
	target, source := r.TargetID, before.Source.ID
	if err := validMergeIDs(target, source); err != nil {
		return empty, errRevertConflict()
	}
	if err := lockMergeReleases(ctx, tx, target, source); err != nil {
		return empty, err
	}
	var valid bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1`+currentMergeJoin+`
		WHERE m.operation_id=$1 AND m.target_release_id=$2 AND m.source_release_id=$3 AND `+mergeRevertibleSQL+`)`, r.ID, target, source).Scan(&valid); err != nil {
		return empty, err
	}
	if !valid {
		return empty, errRevertConflict()
	}

	// 按当前碟的来源还原，包含在合并期间由扫描新增的碟。
	rows, err := tx.QueryContext(ctx, `SELECT id::text,position,origin_position FROM media WHERE origin_release_id=$1 ORDER BY origin_position,id`, source)
	if err != nil {
		return empty, err
	}
	defer rows.Close()
	prior := mergeRecordState{Source: before.Source, Target: before.Target, Media: []mergeRecordedMedium{}}
	restored := prior
	prior.Source.Revision = r.ResultRevision
	restored.Source.Revision = r.ResultRevision + 1
	for rows.Next() {
		var mediumID string
		var display, original int
		if err := rows.Scan(&mediumID, &display, &original); err != nil {
			return empty, err
		}
		prior.Media = append(prior.Media, mergeRecordedMedium{mediumID, display})
		restored.Media = append(restored.Media, mergeRecordedMedium{mediumID, original})
	}
	if err := rows.Err(); err != nil {
		return empty, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE media SET release_id=origin_release_id,position=origin_position,
		origin_release_id=NULL,origin_position=NULL WHERE origin_release_id=$1`, source); err != nil {
		return empty, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM release_merges WHERE source_release_id=$1`, source); err != nil {
		return empty, err
	}
	if err := ingest.LayoutMergedMedia(ctx, tx, target); err != nil {
		return empty, err
	}
	if err := albumfavorites.RestoreMergedAlbumState(ctx, tx, before.Target.GroupID, before.Favorites, before.Ratings); err != nil {
		return empty, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE releases SET edit_revision=edit_revision+1 WHERE id=$1`, source); err != nil {
		return empty, err
	}
	if err := refreshMergeReleases(ctx, tx, target, source); err != nil {
		return empty, err
	}
	if err := k.suppressions.SuppressReleaseMerge(ctx, tx, target, source, id, operations.ActorID(ctx)); err != nil {
		return empty, err
	}
	var targetRevision int64
	if err := tx.QueryRowContext(ctx, `SELECT edit_revision FROM releases WHERE id=$1`, target).Scan(&targetRevision); err != nil {
		return empty, err
	}
	return operations.Outcome{TargetType: operations.TargetRelease, TargetID: target, Targets: releaseTargets(target, source),
		ExpectedRevision: r.ResultRevision, ResultRevision: r.ResultRevision + 1, ResponseRevision: targetRevision, Before: prior, After: restored}, nil
}

func (k releaseMergeKind) Describe(ctx context.Context, q operations.Queryer, records []operations.Record) (map[string]operations.Description, error) {
	infos, err := loadReleaseInfos(ctx, q, records)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(records))
	out := make(map[string]operations.Description, len(records))
	for _, r := range records {
		d := infos.describe(r)
		d.Revertible = false
		var state mergeRecordState
		if err := json.Unmarshal(r.After, &state); err != nil {
			return nil, err
		}
		d.Changes = map[string]any{"kind": r.ChangeType(), "source": map[string]string{"id": state.Source.ID, "title": state.Source.Title}, "medium_count": len(state.Media)}
		out[r.ID] = d
		if r.RevertedByID == "" && r.Type != operations.TypeRevert {
			ids = append(ids, r.ID)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT m.operation_id::text`+currentMergeJoin+`WHERE o.id=ANY($1::uuid[]) AND `+mergeRevertibleSQL, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		d := out[id]
		d.Revertible = true
		out[id] = d
	}
	return out, rows.Err()
}

type MergedRelease struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	OperationID string `json:"operation_id"`
	Revertible  bool   `json:"revertible"`
}

func (s *Store) LoadMergedReleases(ctx context.Context, targetID string) ([]MergedRelease, error) {
	rows, err := s.database.QueryContext(ctx, `SELECT s.id::text,e.title,m.operation_id::text,(`+mergeRevertibleSQL+`)`+currentMergeJoin+`
		JOIN release_effective e ON e.id=s.id WHERE m.target_release_id=$1 ORDER BY m.merged_at,m.source_release_id`, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []MergedRelease{}
	for rows.Next() {
		var m MergedRelease
		if err := rows.Scan(&m.ID, &m.Title, &m.OperationID, &m.Revertible); err != nil {
			return nil, err
		}
		items = append(items, m)
	}
	return items, rows.Err()
}

// PreviewRevert 显示撤销后恢复独立的发行，不把收藏前态暴露给治理工具。
func (releaseMergeKind) PreviewRevert(_ context.Context, _ operations.Queryer, r operations.Record) ([]operations.PreviewChange, error) {
	var state mergeRecordState
	if err := json.Unmarshal(r.After, &state); err != nil {
		return nil, err
	}
	// 管家确认对话框与记录列表共用来源与碟数，并明确恢复后的归属。
	source := map[string]string{"id": state.Source.ID, "title": state.Source.Title}
	return []operations.PreviewChange{{
		Field:  "release_merge",
		Before: map[string]any{"source": source, "medium_count": len(state.Media), "merged_into": state.Target.ID},
		After:  map[string]any{"source": source, "medium_count": len(state.Media), "merged_into": nil},
	}}, nil
}
