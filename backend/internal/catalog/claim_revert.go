package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/operations"
)

// 认领的撤销与展示（阶段 M4 第三项）。撤销把两个发行的修正与核对（连同原来的 operation_id，核对人仍是原来的人）、所在的专辑和
// 各专辑的默认版本一次恢复成认领之前；只有新版本、旧版本与涉及的专辑都停在这次认领之后的版本上时才能撤销，任何一方之后又被修改过
// 就拒绝（revert_conflict），不做部分撤销。撤销不要求新版本可见：认领之后新版本又被整张替换、变得不可见时，撤销仍能把人工状态还给旧版本，
// 这正是撤销的目的。这一点与其他类型不同，只在这里实现。

// ClaimOperations 返回认领操作的撤销与展示能力。
func ClaimOperations() operations.Kind { return claimKind{} }

type claimKind struct{}

func (claimKind) Type() string { return operations.TypeReleaseClaim }

// restoreClaimSide 把一个发行的人工状态写成 side：先清掉现有的修正与核对行，再原样写回（连同原来的 operation_id），
// 并恢复它所在的组。调用方已经锁住发行行与涉及的记录。
func restoreClaimSide(ctx context.Context, tx *sql.Tx, side claimSide) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM release_corrections WHERE release_id=$1::uuid`, side.ReleaseID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM release_issue_reviews WHERE release_id=$1::uuid`, side.ReleaseID); err != nil {
		return err
	}
	for _, name := range claimedFieldOrder {
		correction, ok := side.Corrections[name]
		if !ok {
			continue
		}
		field, _ := correctableFieldByName(name)
		intent, err := restoredIntent(field, &correction.Intent)
		if err != nil {
			return err
		}
		if err := writeIntent(ctx, tx, side.ReleaseID, field, intent, correction.OperationID); err != nil {
			return err
		}
	}
	for _, issue := range claimReviewOrder {
		review, ok := side.Reviews[issue]
		if !ok {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO release_issue_reviews(release_id,issue,basis,operation_id) VALUES($1::uuid,$2,$3::jsonb,$4::uuid)`,
			side.ReleaseID, issue, string(review.Basis), review.OperationID); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `UPDATE releases SET group_id=$2::uuid, edit_revision = edit_revision + 1 WHERE id=$1::uuid`, side.ReleaseID, side.GroupID)
	return err
}

// recordedOperationIDs 返回状态里引用到的全部操作记录 ID，升序且无重复：修正行与核对行写回时的外键检查要给这些记录加 KEY SHARE 锁。
func (state claimRecordState) recordedOperationIDs() []string {
	seen := map[string]bool{}
	for _, side := range []claimSide{state.Source, state.Target} {
		for _, correction := range side.Corrections {
			seen[correction.OperationID] = true
		}
		for _, review := range side.Reviews {
			seen[review.OperationID] = true
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Revert 恢复认领之前的状态。加锁顺序与认领一致：先对前态引用的操作记录加 KEY SHARE（写回的行引用它们，而撤销那些更早的记录会先
// FOR UPDATE 锁住记录、再锁发行行），再按 UUID 顺序锁两个发行行，最后按 UUID 顺序锁前态里的各个组。然后核对：新版本的版本号等于这条记录的
// 结果版本，旧版本与各组的版本号等于后态里的值，否则 revert_conflict。
func (claimKind) Revert(ctx context.Context, tx *sql.Tx, operationID string, original operations.Record) (operations.Outcome, error) {
	var before, after claimRecordState
	if err := json.Unmarshal(original.Before, &before); err != nil {
		return operations.Outcome{}, fmt.Errorf("decode recorded claim state: %w", err)
	}
	if err := json.Unmarshal(original.After, &after); err != nil {
		return operations.Outcome{}, fmt.Errorf("decode recorded claim state: %w", err)
	}
	sourceID, targetID := strings.ToLower(before.Source.ReleaseID), strings.ToLower(before.Target.ReleaseID)
	if sourceID == "" || targetID == "" || targetID != strings.ToLower(original.TargetID) {
		return operations.Outcome{}, fmt.Errorf("recorded claim state is malformed")
	}

	if err := lockOperationRecords(ctx, tx, before.recordedOperationIDs()); err != nil {
		return operations.Outcome{}, err
	}
	firstID, secondID := sourceID, targetID
	if secondID < firstID {
		firstID, secondID = secondID, firstID
	}
	for _, id := range []string{firstID, secondID} {
		if _, _, exists, err := lockReleaseRow(ctx, tx, id); err != nil {
			return operations.Outcome{}, err
		} else if !exists {
			return operations.Outcome{}, operations.NotFound("not_found", "发行版本不存在")
		}
	}
	groupIDs := make([]string, 0, len(before.Groups))
	for id := range before.Groups {
		groupIDs = append(groupIDs, id)
	}
	sort.Strings(groupIDs)
	existing, err := lockGroups(ctx, tx, groupIDs...)
	if err != nil {
		return operations.Outcome{}, err
	}

	// 版本核对：之后任何一方又被修改过（含别人改了专辑的默认版本）就拒绝。
	currentTarget, err := loadClaimSide(ctx, tx, targetID, false)
	if err != nil {
		return operations.Outcome{}, err
	}
	currentSource, err := loadClaimSide(ctx, tx, sourceID, false)
	if err != nil {
		return operations.Outcome{}, err
	}
	if currentTarget.Revision != original.ResultRevision || currentSource.Revision != after.Source.Revision {
		return operations.Outcome{}, errRevertConflict()
	}
	currentGroups := map[string]claimGroupState{}
	for _, id := range groupIDs {
		if !existing[id] {
			return operations.Outcome{}, errRevertConflict()
		}
		var state claimGroupState
		var stored sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT edit_revision, default_release_id::text FROM release_groups WHERE id=$1::uuid`, id).Scan(&state.Revision, &stored); err != nil {
			return operations.Outcome{}, err
		}
		state.DefaultReleaseID = stored.String
		currentGroups[id] = state
		if recorded, ok := after.Groups[id]; !ok || recorded.Revision != state.Revision {
			return operations.Outcome{}, errRevertConflict()
		}
	}

	// 恢复前态。
	if err := restoreClaimSide(ctx, tx, before.Source); err != nil {
		return operations.Outcome{}, err
	}
	if err := restoreClaimSide(ctx, tx, before.Target); err != nil {
		return operations.Outcome{}, err
	}
	for _, id := range groupIDs {
		if want := before.Groups[id].DefaultReleaseID; want != currentGroups[id].DefaultReleaseID {
			if err := writeGroupDefault(ctx, tx, id, want); err != nil {
				return operations.Outcome{}, err
			}
		}
	}

	restored, err := loadClaimRecordState(ctx, tx, claimSnapshot{Source: before.Source, Target: before.Target, Groups: before.Groups})
	if err != nil {
		return operations.Outcome{}, err
	}
	return operations.Outcome{
		TargetType: operations.TargetRelease, TargetID: targetID,
		ExpectedRevision: currentTarget.Revision, ResultRevision: currentTarget.Revision + 1,
		Before: claimRecordState{Source: currentSource, Target: currentTarget, Groups: currentGroups},
		After:  restored,
	}, nil
}

// lockOperationRecords 对这些操作记录加 FOR KEY SHARE（按 ID 升序）。
func lockOperationRecords(ctx context.Context, tx *sql.Tx, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT id::text FROM operations WHERE id = ANY($1::text[]::uuid[]) ORDER BY id FOR KEY SHARE`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	locked := 0
	for rows.Next() {
		locked++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if locked != len(ids) {
		return fmt.Errorf("lock the recorded operations: %d of %d found", locked, len(ids))
	}
	return nil
}

// claimChanges 是改动摘要：来源版本的信息，以及迁移与保留的各类（撤销记录只带来源）。
// PreviousGroupID 是认领之前新版本所在的组：从记录撤销认领时，页面要跟着新版本回到那里（撤销记录不带）。
type claimChanges struct {
	Kind            string         `json:"kind"`
	Source          claimSourceRef `json:"source"`
	PreviousGroupID string         `json:"previous_group_id,omitempty"`
	Moved           *claimSummary  `json:"moved,omitempty"`
	Skipped         *claimSummary  `json:"skipped,omitempty"`
}

type claimSourceRef struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Visible bool   `json:"visible"`
}

// claimSummary 是某一类去向的摘要：修正的字段、是否涉及专辑、接任默认版本的专辑数、核对的问题。
type claimSummary struct {
	Corrections []string `json:"corrections"`
	Album       bool     `json:"album"`
	Defaults    int      `json:"defaults"`
	Reviews     []string `json:"reviews"`
}

// summarizeClaim 由记录的前后状态推导迁移与保留的各类：新版本上操作号是这条记录的修正是迁移来的，其余修正被跳过；
// 核对按依据与操作号是否原样出现在新版本上判断；专辑看新版本是否进了来源原来的组；默认版本看各组的默认版本是否指向新版本。
func summarizeClaim(recordID string, before, after claimRecordState) (moved, skipped claimSummary) {
	moved, skipped = claimSummary{Corrections: []string{}, Reviews: []string{}}, claimSummary{Corrections: []string{}, Reviews: []string{}}
	for _, name := range claimedFieldOrder {
		if _, had := before.Source.Corrections[name]; !had {
			continue
		}
		if now, ok := after.Target.Corrections[name]; ok && now.OperationID == recordID {
			moved.Corrections = append(moved.Corrections, name)
		} else {
			skipped.Corrections = append(skipped.Corrections, name)
		}
	}
	for _, issue := range claimReviewOrder {
		old, had := before.Source.Reviews[issue]
		if !had {
			continue
		}
		now, ok := after.Target.Reviews[issue]
		if _, existed := before.Target.Reviews[issue]; ok && !existed && now.OperationID == old.OperationID && string(now.Basis) == string(old.Basis) {
			moved.Reviews = append(moved.Reviews, issue)
		} else {
			skipped.Reviews = append(skipped.Reviews, issue)
		}
	}
	if before.Source.GroupID != after.Source.GroupID {
		if after.Target.GroupID == before.Source.GroupID {
			moved.Album = true
		} else {
			skipped.Album = true
		}
	}
	for id, group := range before.Groups {
		if group.DefaultReleaseID == before.Source.ReleaseID && after.Groups[id].DefaultReleaseID == before.Target.ReleaseID {
			moved.Defaults++
		}
	}
	return moved, skipped
}

// Describe 给记录配上新版本当前的标题、所在的组、是否可见与能否撤销，以及改动摘要。能否撤销只看版本号：新版本停在这条记录的结果版本，
// 旧版本与各组停在后态里的版本；不要求新版本可见。
func (claimKind) Describe(ctx context.Context, database operations.Queryer, records []operations.Record) (map[string]operations.Description, error) {
	infos, err := loadReleaseInfos(ctx, database, records)
	if err != nil {
		return nil, err
	}
	descriptions := make(map[string]operations.Description, len(records))
	for _, record := range records {
		description := operations.Description{}
		if entry, exists := infos[strings.ToLower(record.TargetID)]; exists {
			info := entry.info
			description.Release = &info
		}
		var before, after claimRecordState
		if json.Unmarshal(record.Before, &before) != nil || json.Unmarshal(record.After, &after) != nil || after.Source.ReleaseID == "" {
			descriptions[record.ID] = description
			continue
		}
		sourceID := strings.ToLower(after.Source.ReleaseID)
		// 来源的名字是记录里的：用户认领时认得的那个名字（认领之前的有效标题；撤销记录取恢复之后的）。认领会把旧版本的修正清走，
		// 现在的有效标题已是扫描值，页面上的名字就和用户认领时看到的不一样了；是否可见才是现在的状态。
		source := claimSourceRef{ID: sourceID, Title: after.Source.Title}
		if record.Type == operations.TypeReleaseClaim {
			source.Title = before.Source.Title
		}
		var sourceRevision int64
		sourceExists := true
		err := database.QueryRowContext(ctx, `SELECT releases.edit_revision, `+releaseVisiblePredicate+` FROM `+releaseSource+` WHERE releases.id=$1::uuid`, sourceID).
			Scan(&sourceRevision, &source.Visible)
		if errors.Is(err, sql.ErrNoRows) {
			sourceExists = false
		} else if err != nil {
			return nil, err
		}
		changes := claimChanges{Kind: operations.TypeReleaseClaim, Source: source}
		if record.Type == operations.TypeReleaseClaim {
			moved, skipped := summarizeClaim(record.ID, before, after)
			changes.Moved, changes.Skipped, changes.PreviousGroupID = &moved, &skipped, before.Target.GroupID
		}
		description.Changes = changes

		// 能否撤销：只看版本号，不要求可见。
		revertible := sourceExists && sourceRevision == after.Source.Revision
		if entry, exists := infos[strings.ToLower(record.TargetID)]; revertible && (!exists || entry.revision != record.ResultRevision) {
			revertible = false
		}
		for id, recorded := range after.Groups {
			if !revertible {
				break
			}
			if _, tracked := before.Groups[id]; !tracked {
				continue
			}
			var revision int64
			if err := database.QueryRowContext(ctx, `SELECT edit_revision FROM release_groups WHERE id=$1::uuid`, id).Scan(&revision); err != nil || revision != recorded.Revision {
				revertible = false
			}
		}
		description.Revertible = revertible
		descriptions[record.ID] = description
	}
	return descriptions, nil
}
