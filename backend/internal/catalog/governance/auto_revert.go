package governance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

// AutoSuppressionWriter 由 curation 实现，在撤销事务中记录被撤掉的规范化意图与本次撤销者。
type AutoSuppressionWriter interface {
	SuppressReleaseValue(ctx context.Context, tx *sql.Tx, releaseID, field string, value catalog.CorrectionIntent, revertID, actorID string) error
}

// 本地拆分按段和连接词抑制，同一个显示名仍可采用别的署名结构。
type splitSuppressionWriter interface {
	SuppressCreditParts(ctx context.Context, tx *sql.Tx, owner, target, targetID, field, revertID, actorID string) (bool, error)
}

func suppressCreditParts(ctx context.Context, tx *sql.Tx, w any, owner, target, id, field, revertID string) (bool, error) {
	if field != "artist" && field != "album_artist" {
		return false, nil
	}
	if splits, ok := w.(splitSuppressionWriter); ok {
		return splits.SuppressCreditParts(ctx, tx, owner, target, id, field, revertID, operations.ActorID(ctx))
	}
	return false, nil
}

type autoCorrectionKind struct{ suppressions AutoSuppressionWriter }

// AutoCorrectionOperations 在装配时要求提供“不再提出”的写入能力。
func AutoCorrectionOperations(suppressions AutoSuppressionWriter) operations.Kind {
	if suppressions == nil {
		panic("注册自动整理操作需要提供不再提出标记的写入器")
	}
	return autoCorrectionKind{suppressions}
}

func (autoCorrectionKind) Type() string { return operations.TypeReleaseAutoCorrection }

func readAutoState(raw json.RawMessage) (autoRecordState, error) {
	var state autoRecordState
	if err := json.Unmarshal(raw, &state); err != nil {
		return autoRecordState{}, err
	}
	if len(state.Fields) == 0 {
		return autoRecordState{}, errors.New("自动操作记录缺少字段")
	}
	for name, snapshot := range state.Fields {
		field, ok := correctableFieldNamed(name)
		if !ok {
			return autoRecordState{}, fmt.Errorf("自动操作记录含有未知字段 %q", name)
		}
		if snapshot.Auto != nil {
			intent, err := restoredIntent(field, &snapshot.Auto.CorrectionIntent)
			if err != nil {
				return autoRecordState{}, err
			}
			snapshot.Auto.CorrectionIntent = *intent
		}
	}
	return state, nil
}

func (k autoCorrectionKind) Revert(ctx context.Context, tx *sql.Tx, operationID string, original operations.Record) (operations.Outcome, error) {
	locked, err := lockAutoRelease(ctx, tx, original.TargetID, false)
	if err != nil {
		return operations.Outcome{}, err
	}
	prior, err := readAutoState(original.Before)
	if err != nil {
		return operations.Outcome{}, err
	}
	if isInvalidation(prior.Fields) {
		return revertInvalidation(ctx, tx, locked, original, prior.Fields, false)
	}
	next := make(map[string]*ownedAutoIntent, len(prior.Fields))
	for _, field := range catalog.CorrectableFields {
		entry, changed := prior.Fields[field.Name]
		if !changed {
			continue
		}
		current, exists := locked.Current(field.Name)
		if !exists {
			return operations.Outcome{}, operations.Conflict("revert_conflict", field.Label+"的自动值已移除")
		}
		if current.OperationID != original.ID {
			return operations.Outcome{}, operations.Conflict("revert_conflict", field.Label+"已被操作 "+current.OperationID+" 改写")
		}
		next[field.Name] = entry.Auto
	}
	outcome, err := locked.replace(ctx, next)
	if err != nil {
		return operations.Outcome{}, err
	}
	for name := range next {
		handled, err := suppressCreditParts(ctx, tx, k.suppressions, original.ID, "release", locked.id, name, operationID)
		if err != nil {
			return operations.Outcome{}, err
		}
		if handled {
			continue
		}
		value := locked.current[name]
		if err := k.suppressions.SuppressReleaseValue(ctx, tx, locked.id, name, value.Intent(), operationID, operations.ActorID(ctx)); err != nil {
			return operations.Outcome{}, err
		}
	}
	outcome.ResponseRevision = locked.editRevision
	return outcome, nil
}

func (autoCorrectionKind) Describe(ctx context.Context, q operations.Queryer, records []operations.Record) (map[string]operations.Description, error) {
	infos, err := loadReleaseInfos(ctx, q, records)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(infos))
	for id := range infos {
		ids = append(ids, id)
	}
	rows, err := q.QueryContext(ctx, `SELECT release_id::text, field, operation_id::text FROM release_auto_values WHERE release_id=ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	owners := map[[2]string]string{}
	for rows.Next() {
		var id, field, owner string
		if err := rows.Scan(&id, &field, &owner); err != nil {
			return nil, err
		}
		owners[[2]string{id, field}] = owner
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var invalidations []string
	descriptions := make(map[string]operations.Description, len(records))
	for _, record := range records {
		var d operations.Description
		if entry, ok := infos[record.TargetID]; ok {
			d.Release = &entry.info
		}
		before, bErr := readAutoState(record.Before)
		after, aErr := readAutoState(record.After)
		if bErr == nil && aErr == nil && len(before.Fields) == len(after.Fields) {
			changes := make([]map[string]any, 0, len(before.Fields))
			d.Revertible = record.Type != operations.TypeRevert && record.RevertedByID == ""
			for _, field := range catalog.CorrectableFields {
				prior, ok := before.Fields[field.Name]
				if !ok {
					continue
				}
				next, ok := after.Fields[field.Name]
				if !ok {
					break
				}
				changes = append(changes, map[string]any{"field": field.Name, "before": prior.display(), "after": next.display()})
				d.Revertible = d.Revertible && owners[[2]string{record.TargetID, field.Name}] == record.ID
			}
			if len(changes) == len(before.Fields) {
				kind := operations.TypeReleaseAutoCorrection
				if isInvalidation(after.Fields) {
					kind = TypeReleaseAutoInvalidation
					if record.Type != operations.TypeRevert && record.RevertedByID == "" {
						invalidations = append(invalidations, record.ID)
					}
				}
				d.Changes = map[string]any{"kind": kind, "fields": changes}
			} else {
				d.Revertible = false
			}
		}
		descriptions[record.ID] = d
	}
	return describeInvalidations(ctx, q, descriptions, invalidations, false)
}

func (autoCorrectionKind) PreviewRevert(ctx context.Context, q operations.Queryer, record operations.Record) ([]operations.PreviewChange, error) {
	before, err := readAutoState(record.Before)
	if err != nil {
		return nil, err
	}
	scan, err := loadFieldValues(ctx, q, "releases", record.TargetID)
	if err != nil {
		return nil, err
	}
	effective, err := loadFieldValues(ctx, q, releasesql.ReleaseSource, record.TargetID)
	if err != nil {
		return nil, err
	}
	corrections, err := catalog.LoadCorrectionIntents(ctx, q, record.TargetID)
	if err != nil {
		return nil, err
	}
	changes := make([]operations.PreviewChange, 0, len(before.Fields))
	for i, field := range catalog.CorrectableFields {
		prior, ok := before.Fields[field.Name]
		if !ok {
			continue
		}
		after := scan[i]
		if correction, ok := corrections[field.Name]; ok {
			after = correction.Value
		} else if prior.Auto != nil {
			after = prior.Auto.Value
		}
		changes = append(changes, operations.PreviewChange{Field: field.Name, Before: effective[i], After: after})
	}
	return changes, nil
}
