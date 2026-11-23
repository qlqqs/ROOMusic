package governance

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

type TrackAutoSuppressionWriter interface {
	SuppressTrackValue(context.Context, *sql.Tx, string, string, catalog.CorrectionIntent, string, string) error
}

type trackAutoCorrectionKind struct{ suppressions TrackAutoSuppressionWriter }

func TrackAutoCorrectionOperations(w TrackAutoSuppressionWriter) operations.Kind {
	if w == nil {
		panic("注册曲目自动整理操作需要提供不再提出标记的写入器")
	}
	return trackAutoCorrectionKind{w}
}

func (trackAutoCorrectionKind) Type() string { return TypeTrackAutoCorrection }

func (k trackAutoCorrectionKind) Revert(ctx context.Context, tx *sql.Tx, id string, original operations.Record) (operations.Outcome, error) {
	var prior trackAutoRecord
	if err := json.Unmarshal(original.Before, &prior); err != nil {
		return operations.Outcome{}, err
	}
	locked, err := LockAutoTrackForRevert(ctx, tx, prior.TrackID)
	if err != nil {
		return operations.Outcome{}, err
	}
	next := make(map[string]*ownedAutoIntent, len(prior.Fields))
	for _, field := range catalog.TrackCorrectableFields {
		entry, ok := prior.Fields[field.Name]
		if !ok {
			continue
		}
		current, ok := locked.Current(field.Name)
		if !ok {
			return operations.Outcome{}, operations.Conflict("revert_conflict", field.Label+"的自动值已移除")
		}
		if current.OperationID != original.ID {
			return operations.Outcome{}, operations.Conflict("revert_conflict", field.Label+"已被操作 "+current.OperationID+" 改写")
		}
		next[field.Name] = entry.Auto
	}
	out, err := locked.replace(ctx, next)
	if err != nil {
		return out, err
	}
	for name := range next {
		current, _ := locked.Current(name)
		if err := k.suppressions.SuppressTrackValue(ctx, tx, prior.TrackID, name, current.Intent(), id, operations.ActorID(ctx)); err != nil {
			return operations.Outcome{}, err
		}
	}
	out.ResponseRevision = locked.EditRevision()
	return out, nil
}

func (trackAutoCorrectionKind) Describe(ctx context.Context, q operations.Queryer, records []operations.Record) (map[string]operations.Description, error) {
	infos, err := loadReleaseInfos(ctx, q, records)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(records))
	for _, r := range records {
		ids = append(ids, r.ID)
	}
	rows, err := q.QueryContext(ctx, `SELECT operation_id::text,track_id::text,field FROM track_auto_values WHERE operation_id=ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	owners := map[[3]string]bool{}
	for rows.Next() {
		var key [3]string
		if err := rows.Scan(&key[0], &key[1], &key[2]); err != nil {
			return nil, err
		}
		owners[key] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	descriptions := make(map[string]operations.Description, len(records))
	for _, record := range records {
		d := infos.describe(record)
		var before, after trackAutoRecord
		if err := json.Unmarshal(record.Before, &before); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(record.After, &after); err != nil {
			return nil, err
		}
		d.Revertible = record.Type != operations.TypeRevert && record.RevertedByID == "" && len(after.Fields) > 0
		var changes []map[string]any
		for _, field := range catalog.TrackCorrectableFields {
			if state, ok := after.Fields[field.Name]; ok {
				changes = append(changes, map[string]any{"field": field.Name, "before": before.Fields[field.Name], "after": state})
				d.Revertible = d.Revertible && owners[[3]string{record.ID, after.TrackID, field.Name}]
			}
		}
		d.Changes = map[string]any{"kind": TypeTrackAutoCorrection, "track": map[string]string{"id": after.TrackID, "title": after.TrackTitle}, "fields": changes}
		descriptions[record.ID] = d
	}
	return descriptions, nil
}

func (trackAutoCorrectionKind) PreviewRevert(ctx context.Context, q operations.Queryer, record operations.Record) ([]operations.PreviewChange, error) {
	var before trackAutoRecord
	if err := json.Unmarshal(record.Before, &before); err != nil {
		return nil, err
	}
	state, err := NewStore(q).loadTrackCorrectionState(ctx, before.TrackID, false)
	if err != nil {
		return nil, err
	}
	var changes []operations.PreviewChange
	for _, f := range state.Fields {
		prior, ok := before.Fields[f.Field]
		if !ok {
			continue
		}
		after := f.ScanValue
		if f.Correction != nil {
			after = f.Correction.Value
		} else if prior.Auto != nil {
			after = prior.Auto.Value
		}
		changes = append(changes, operations.PreviewChange{Field: f.Field, Before: f.EffectiveValue, After: after})
	}
	return changes, nil
}
