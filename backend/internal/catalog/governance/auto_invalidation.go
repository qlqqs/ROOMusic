package governance

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

const (
	TypeReleaseAutoInvalidation = "release_auto_invalidation"
	TypeTrackAutoInvalidation   = "track_auto_invalidation"
)

type lockedAutoFields interface {
	Current(string) (catalog.AutoValue, bool)
	EditRevision() int64
	scanBasis(context.Context) (map[string]json.RawMessage, error)
	replace(context.Context, map[string]*ownedAutoIntent) (operations.Outcome, error)
}

func sameScanValue(a, b json.RawMessage) (bool, error) {
	var left, right any
	if err := json.Unmarshal(a, &left); err != nil {
		return false, err
	}
	if err := json.Unmarshal(b, &right); err != nil {
		return false, err
	}
	return reflect.DeepEqual(left, right), nil
}

// InvalidateAutoFields 在与扫描相同的目录锁内复核快照，只删除仍归原写入者的过期字段。
func InvalidateAutoFields(ctx context.Context, tx *sql.Tx, releaseID string, candidate catalog.StaleAutoFields) (operations.Outcome, error) {
	var target lockedAutoFields
	if candidate.TrackID != "" {
		t, err := LockAutoTrack(ctx, tx, candidate.TrackID)
		if err != nil {
			return operations.Outcome{}, err
		}
		if t.ReleaseID() != releaseID {
			return operations.Outcome{}, operations.Conflict("item_stale", "曲目所属发行已变更")
		}
		target = t
	} else {
		r, err := LockAutoRelease(ctx, tx, releaseID)
		if err != nil {
			return operations.Outcome{}, err
		}
		target = r
	}
	scan, err := target.scanBasis(ctx)
	if err != nil {
		return operations.Outcome{}, err
	}
	remove := map[string]*ownedAutoIntent{}
	for name, owner := range candidate.Owners {
		current, ok := target.Current(name)
		if !ok || current.OperationID != owner || current.ScanBasis == nil {
			continue
		}
		same, err := sameScanValue(current.ScanBasis, scan[name])
		if err != nil {
			return operations.Outcome{}, err
		}
		if !same {
			remove[name] = nil
		}
	}
	if len(remove) == 0 {
		return operations.Outcome{}, operations.Conflict("nothing_to_apply", "扫描依据已经复核")
	}
	out, err := target.replace(ctx, remove)
	if err == nil {
		markInvalidation(out.Before, scan)
		markInvalidation(out.After, scan)
	}
	return out, err
}

func markInvalidation(snapshot any, scan map[string]json.RawMessage) {
	var fields map[string]autoFieldState
	switch record := snapshot.(type) {
	case autoRecordState:
		fields = record.Fields
	case trackAutoRecord:
		fields = record.Fields
	}
	for name, entry := range fields {
		entry.InvalidatingScan = scan[name]
		fields[name] = entry
	}
}

func isInvalidation(fields map[string]autoFieldState) bool {
	for _, field := range fields {
		return field.InvalidatingScan != nil
	}
	return false
}

// 不留自动行的字段用作废记录核对归属。列表和锁内撤销共用同一判断。
func revertibleInvalidations(ctx context.Context, q operations.Queryer, ids []string, track bool) (map[string]bool, error) {
	allowed := map[string]bool{}
	if len(ids) == 0 {
		return allowed, nil
	}
	target := `JOIN releases ON releases.id=o.target_id
	LEFT JOIN release_auto_values a ON a.release_id=releases.id AND a.field=f.key`
	current := `(` + releasesql.ScanFieldsJSON + `)->f.key`
	if track {
		target = `JOIN tracks t ON t.id=(o.after_state->>'track_id')::uuid
		LEFT JOIN track_auto_values a ON a.track_id=t.id AND a.field=f.key`
		current = `CASE f.key WHEN 'title' THEN to_jsonb(t.title) WHEN 'artist' THEN to_jsonb(t.artist) END`
	}
	rows, err := q.QueryContext(ctx, `SELECT o.id::text,bool_and(a.operation_id IS NULL AND (`+current+`)=f.value->'scan_value'
		AND NOT EXISTS (SELECT FROM operations later WHERE later.target_id=o.target_id
		AND later.operation_type=o.operation_type AND later.result_revision>o.result_revision
		AND later.after_state->>'track_id' IS NOT DISTINCT FROM o.after_state->>'track_id'
		AND (later.after_state->'fields'->f.key) ? 'scan_value'
		AND NOT EXISTS(SELECT FROM operations undo WHERE undo.reverts_operation_id=later.id)))
		FROM operations o CROSS JOIN LATERAL jsonb_each(o.after_state->'fields') f `+target+`
		WHERE o.id=ANY($1::uuid[]) GROUP BY o.id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var ok bool
		if err := rows.Scan(&id, &ok); err != nil {
			return nil, err
		}
		allowed[id] = ok
	}
	return allowed, rows.Err()
}

func revertInvalidation(ctx context.Context, tx *sql.Tx, target lockedAutoFields, original operations.Record, prior map[string]autoFieldState, track bool) (operations.Outcome, error) {
	allowed, err := revertibleInvalidations(ctx, tx, []string{original.ID}, track)
	if err != nil {
		return operations.Outcome{}, err
	}
	if !allowed[original.ID] {
		return operations.Outcome{}, operations.Conflict("revert_conflict", "字段已重新写入或扫描依据已变更")
	}
	next := make(map[string]*ownedAutoIntent, len(prior))
	scan := make(map[string]json.RawMessage, len(prior))
	for name, field := range prior {
		field.Auto.ScanBasis = field.InvalidatingScan
		next[name] = field.Auto
		scan[name] = field.InvalidatingScan
	}
	out, err := target.replace(ctx, next)
	if err == nil {
		markInvalidation(out.Before, scan)
		markInvalidation(out.After, scan)
		out.ResponseRevision = target.EditRevision()
	}
	return out, err
}

func describeInvalidations(ctx context.Context, q operations.Queryer, descriptions map[string]operations.Description, ids []string, track bool) (map[string]operations.Description, error) {
	allowed, err := revertibleInvalidations(ctx, q, ids, track)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		d := descriptions[id]
		d.Revertible = allowed[id]
		descriptions[id] = d
	}
	return descriptions, nil
}
