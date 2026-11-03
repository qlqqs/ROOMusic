package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

// 受治理操作的撤销与列表展示：字段修正与人工归组各实现一个 operations.Kind，由 app 注册给 operations/httpapi。
// 撤销是一次新的操作，恢复之前的意图（修正）或原来的组（归组），不把历史有效值写回扫描列；
// 只有目标上最近一次修改可以被撤销（人工修改版本号等于记录的结果版本），之后又有修改就拒绝，不覆盖新状态。

// errRevertConflict 表示目标在这条操作之后又被修改过。
func errRevertConflict() error {
	return operations.Conflict("revert_conflict", "这张专辑之后又被修改过，不能撤销这一条")
}

// CorrectionOperations 返回字段修正操作的撤销与展示能力。
func CorrectionOperations() operations.Kind { return correctionKind{} }

// GroupChangeOperations 返回人工归组操作的撤销与展示能力。
func GroupChangeOperations() operations.Kind { return groupChangeKind{} }

type correctionKind struct{}

func (correctionKind) Type() string { return operations.TypeReleaseCorrection }

// restoredIntent 校验记录里存下的前态意图并还原成写入需要的类型：JSON 里的数字是 float64，年份要还原成整数。
func restoredIntent(field correctableField, intent *CorrectionIntent) (*CorrectionIntent, error) {
	if intent == nil {
		return nil, nil
	}
	switch {
	case intent.Mode == CorrectionNull && intent.Value == nil:
		return &CorrectionIntent{Mode: CorrectionNull}, nil
	case intent.Mode != CorrectionSet:
	case field.year:
		if year, isNumber := intent.Value.(float64); isNumber && year == float64(int(year)) && year >= 1 && year <= 9999 {
			return &CorrectionIntent{Mode: CorrectionSet, Value: int(year)}, nil
		}
	default:
		if text, isText := intent.Value.(string); isText && text != "" {
			return &CorrectionIntent{Mode: CorrectionSet, Value: text}, nil
		}
	}
	return nil, fmt.Errorf("recorded correction for %s is malformed", field.name)
}

// Revert 把被这条修正改过的字段恢复成它之前的意图（没有修正、设值或设空），清除修正后显示的是当前的扫描值。
func (correctionKind) Revert(ctx context.Context, tx *sql.Tx, operationID string, original operations.Record) (operations.Outcome, error) {
	locked, err := lockVisibleRelease(ctx, tx, original.TargetID)
	if err != nil {
		return operations.Outcome{}, err
	}
	if locked.revision != original.ResultRevision {
		return operations.Outcome{}, errRevertConflict()
	}
	var recorded correctionRecordState
	if err := json.Unmarshal(original.Before, &recorded); err != nil {
		return operations.Outcome{}, fmt.Errorf("decode recorded correction state: %w", err)
	}
	current, err := loadCorrectionIntents(ctx, tx, original.TargetID)
	if err != nil {
		return operations.Outcome{}, err
	}
	effectiveBefore, err := loadFieldValues(ctx, tx, releasesql.ReleaseSource, original.TargetID)
	if err != nil {
		return operations.Outcome{}, err
	}

	before := correctionRecordState{Fields: map[string]*CorrectionIntent{}, Effective: map[string]any{}}
	after := correctionRecordState{Fields: map[string]*CorrectionIntent{}, Effective: map[string]any{}}
	for index, field := range correctableFields {
		intent, wasChanged := recorded.Fields[field.name]
		if !wasChanged {
			continue
		}
		restored, err := restoredIntent(field, intent)
		if err != nil {
			return operations.Outcome{}, err
		}
		if now, has := current[field.name]; has {
			before.Fields[field.name] = &now
		} else {
			before.Fields[field.name] = nil
		}
		before.Effective[field.name] = effectiveBefore[index]
		after.Fields[field.name] = restored
		if err := writeIntent(ctx, tx, original.TargetID, field, restored, operationID); err != nil {
			return operations.Outcome{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE releases SET edit_revision = edit_revision + 1 WHERE id=$1::uuid`, original.TargetID); err != nil {
		return operations.Outcome{}, err
	}
	effectiveAfter, err := loadFieldValues(ctx, tx, releasesql.ReleaseSource, original.TargetID)
	if err != nil {
		return operations.Outcome{}, err
	}
	for index, field := range correctableFields {
		if _, restoredField := after.Fields[field.name]; restoredField {
			after.Effective[field.name] = effectiveAfter[index]
		}
	}
	return operations.Outcome{
		TargetType: operations.TargetRelease, TargetID: original.TargetID,
		ExpectedRevision: locked.revision, ResultRevision: locked.revision + 1,
		Before: before, After: after,
	}, nil
}

// correctionFieldChange 是列表里一个字段的改动：改动前后的修正意图（null 表示没有修正）与当时的有效值。
type correctionFieldChange struct {
	Field  string               `json:"field"`
	Before correctionFieldState `json:"before"`
	After  correctionFieldState `json:"after"`
}

type correctionFieldState struct {
	Correction *CorrectionIntent `json:"correction"`
	Effective  any               `json:"effective"`
}

func (correctionKind) Describe(ctx context.Context, database operations.Queryer, records []operations.Record) (map[string]operations.Description, error) {
	infos, err := loadReleaseInfos(ctx, database, records)
	if err != nil {
		return nil, err
	}
	descriptions := make(map[string]operations.Description, len(records))
	for _, record := range records {
		description := infos.describe(record)
		var before, after correctionRecordState
		if json.Unmarshal(record.Before, &before) == nil && json.Unmarshal(record.After, &after) == nil {
			fields := []correctionFieldChange{}
			for _, field := range correctableFields {
				if _, changed := after.Fields[field.name]; changed {
					fields = append(fields, correctionFieldChange{
						Field:  field.name,
						Before: correctionFieldState{Correction: before.Fields[field.name], Effective: before.Effective[field.name]},
						After:  correctionFieldState{Correction: after.Fields[field.name], Effective: after.Effective[field.name]},
					})
				}
			}
			description.Changes = map[string]any{"kind": operations.TypeReleaseCorrection, "fields": fields}
		}
		descriptions[record.ID] = description
	}
	return descriptions, nil
}

type groupChangeKind struct{}

func (groupChangeKind) Type() string { return operations.TypeReleaseGroupChange }

// Revert 把版本移回它原来的组。组从不删除，所以原来的组总在；它可能已经没有别的可见版本，这个版本回去之后它再次可见。
func (groupChangeKind) Revert(ctx context.Context, tx *sql.Tx, operationID string, original operations.Record) (operations.Outcome, error) {
	locked, err := lockVisibleRelease(ctx, tx, original.TargetID)
	if err != nil {
		return operations.Outcome{}, err
	}
	if locked.revision != original.ResultRevision {
		return operations.Outcome{}, errRevertConflict()
	}
	var recordedBefore, recordedAfter groupRecordState
	if err := json.Unmarshal(original.Before, &recordedBefore); err != nil {
		return operations.Outcome{}, fmt.Errorf("decode recorded group state: %w", err)
	}
	if err := json.Unmarshal(original.After, &recordedAfter); err != nil {
		return operations.Outcome{}, fmt.Errorf("decode recorded group state: %w", err)
	}
	origin := strings.ToLower(recordedBefore.GroupID)
	if locked.groupID != strings.ToLower(recordedAfter.GroupID) {
		return operations.Outcome{}, errRevertConflict()
	}
	existing, err := lockGroups(ctx, tx, locked.groupID, origin)
	if err != nil {
		return operations.Outcome{}, err
	}
	if !existing[origin] {
		return operations.Outcome{}, errRevertConflict()
	}
	currentTitle, err := representativeTitle(ctx, tx, locked.groupID)
	if err != nil {
		return operations.Outcome{}, err
	}
	// 原来的组可能已经空了：回去的就是这个版本自己，用它的有效标题。
	originTitle, err := representativeTitle(ctx, tx, origin)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, `SELECT title FROM `+releasesql.ReleaseSource+` WHERE releases.id=$1::uuid`, original.TargetID).Scan(&originTitle)
	}
	if err != nil {
		return operations.Outcome{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE releases SET group_id=$2::uuid, edit_revision = edit_revision + 1 WHERE id=$1::uuid`, original.TargetID, origin); err != nil {
		return operations.Outcome{}, err
	}
	return operations.Outcome{
		TargetType: operations.TargetRelease, TargetID: original.TargetID,
		ExpectedRevision: locked.revision, ResultRevision: locked.revision + 1,
		Before: groupRecordState{GroupID: locked.groupID, GroupTitle: currentTitle},
		After:  groupRecordState{GroupID: origin, GroupTitle: originTitle},
	}, nil
}

func (groupChangeKind) Describe(ctx context.Context, database operations.Queryer, records []operations.Record) (map[string]operations.Description, error) {
	infos, err := loadReleaseInfos(ctx, database, records)
	if err != nil {
		return nil, err
	}
	descriptions := make(map[string]operations.Description, len(records))
	for _, record := range records {
		description := infos.describe(record)
		var before, after groupRecordState
		if json.Unmarshal(record.Before, &before) == nil && json.Unmarshal(record.After, &after) == nil {
			description.Changes = map[string]any{"kind": operations.TypeReleaseGroupChange, "from": before, "to": after}
		}
		descriptions[record.ID] = description
	}
	return descriptions, nil
}

// releaseInfos 是一批记录所指发行的当前状态，键是小写的发行 ID。
type releaseInfos map[string]struct {
	info     operations.ReleaseInfo
	revision int64
}

// loadReleaseInfos 用一条查询读取这些记录所指发行的当前有效标题、所在的组、是否可见与人工修改版本号。
func loadReleaseInfos(ctx context.Context, database operations.Queryer, records []operations.Record) (releaseInfos, error) {
	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.TargetID)
	}
	infos := releaseInfos{}
	if len(ids) == 0 {
		return infos, nil
	}
	rows, err := database.QueryContext(ctx, `SELECT releases.id::text, releases.title, releases.group_id::text, releases.edit_revision, `+releasesql.ReleaseVisiblePredicate+
		` FROM `+releasesql.ReleaseSource+` WHERE releases.id = ANY($1::text[]::uuid[])`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var entry struct {
			info     operations.ReleaseInfo
			revision int64
		}
		if err := rows.Scan(&entry.info.ID, &entry.info.Title, &entry.info.GroupID, &entry.revision, &entry.info.Visible); err != nil {
			return nil, err
		}
		infos[entry.info.ID] = entry
	}
	return infos, rows.Err()
}

// describe 给一条记录配上目标当前的展示信息；目标仍可见且人工修改版本号等于记录的结果版本时，这条记录可以被撤销。
func (infos releaseInfos) describe(record operations.Record) operations.Description {
	entry, exists := infos[strings.ToLower(record.TargetID)]
	if !exists {
		return operations.Description{}
	}
	info := entry.info
	return operations.Description{Release: &info, Revertible: info.Visible && entry.revision == record.ResultRevision}
}
