package governance

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

// PreviewRevert 在恢复扫描值时使用当前扫描快照，执行时仍重新核对版本。
func (correctionKind) PreviewRevert(ctx context.Context, q operations.Queryer, record operations.Record) ([]operations.PreviewChange, error) {
	var before correctionRecordState
	if err := json.Unmarshal(record.Before, &before); err != nil {
		return nil, err
	}
	state, err := NewStore(q).LoadCorrectionState(ctx, record.TargetID)
	if err != nil {
		return nil, err
	}
	changes := []operations.PreviewChange{}
	for _, field := range state.Fields {
		intent, changed := before.Fields[field.Field]
		if !changed {
			continue
		}
		var after any = field.ScanValue
		if intent != nil {
			after = nil
			if intent.Mode == catalog.CorrectionSet {
				after = intent.Value
			}
		}
		changes = append(changes, operations.PreviewChange{Field: field.Field, Before: field.EffectiveValue, After: after})
	}
	return changes, nil
}

// PreviewRevert 只投影认领撤销会恢复的人工意图，不外发内部记录引用。
func (claimKind) PreviewRevert(_ context.Context, _ operations.Queryer, record operations.Record) ([]operations.PreviewChange, error) {
	var before, after claimRecordState
	if err := json.Unmarshal(record.After, &before); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(record.Before, &after); err != nil {
		return nil, err
	}
	changes := []operations.PreviewChange{}
	for _, pair := range [][2]claimSide{{before.Source, after.Source}, {before.Target, after.Target}} {
		from, to := pair[0], pair[1]
		prefix := to.Title + "."
		for _, field := range claimedFieldOrder {
			var old, next any
			if c, ok := from.Corrections[field]; ok {
				old = c.Intent
			}
			if c, ok := to.Corrections[field]; ok {
				next = c.Intent
			}
			x, _ := json.Marshal(old)
			y, _ := json.Marshal(next)
			if string(x) != string(y) {
				changes = append(changes, operations.PreviewChange{Field: prefix + field, Before: old, After: next})
			}
		}
		if from.GroupID != to.GroupID {
			changes = append(changes, operations.PreviewChange{Field: prefix + "group_id", Before: from.GroupID, After: to.GroupID})
		}
		for _, issue := range claimReviewOrder {
			_, old := from.Reviews[issue]
			_, next := to.Reviews[issue]
			if old != next {
				changes = append(changes, operations.PreviewChange{Field: prefix + issue, Before: old, After: next})
			}
		}
	}
	ids := []string{}
	for id := range after.Groups {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		old, next := before.Groups[id].DefaultReleaseID, after.Groups[id].DefaultReleaseID
		if old != next {
			changes = append(changes, operations.PreviewChange{Field: id + ".default_release_id", Before: old, After: next})
		}
	}
	return changes, nil
}
