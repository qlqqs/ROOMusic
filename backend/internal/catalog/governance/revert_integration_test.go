package governance

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func (fixture *correctionFixture) revert(kind operations.Kind, originalID string) (operations.Result, error) {
	fixture.t.Helper()
	return operations.Run(context.Background(), fixture.database, operations.Spec{
		ActorID: fixture.actorID, Type: operations.TypeRevert, Key: uuid.New(),
		Fingerprint: operations.Fingerprint(map[string]any{"type": operations.TypeRevert, "operation_id": originalID}), RequestID: "test",
	}, operations.RevertExecutor(kind, originalID))
}

func (fixture *correctionFixture) mustRevert(kind operations.Kind, originalID string) operations.Result {
	fixture.t.Helper()
	result, err := fixture.revert(kind, originalID)
	if err != nil {
		fixture.t.Fatalf("revert %s: %v", originalID, err)
	}
	return result
}

func revertProblem(err error) (int, string) {
	status, code, _ := operations.Problem(err)
	return status, code
}

// intentOf 读取一个字段当前的修正意图的简写：nil 是没有修正，其余是 "set:值" 或 "null"。
func (fixture *correctionFixture) intentOf(releaseID, field string) string {
	fixture.t.Helper()
	intent, has := mustIntents(fixture, releaseID)[field]
	switch {
	case !has:
		return "none"
	case intent.Mode == catalog.CorrectionNull:
		return "null"
	default:
		return fmt.Sprintf("set:%v", intent.Value)
	}
}

func mustIntents(fixture *correctionFixture, releaseID string) map[string]catalog.CorrectionIntent {
	fixture.t.Helper()
	intents, err := catalog.LoadCorrectionIntents(context.Background(), fixture.database, releaseID)
	if err != nil {
		fixture.t.Fatalf("load correction intents: %v", err)
	}
	return intents
}

// 撤销把字段恢复成之前的意图——没有修正、设值或设空——文本与年份都一样，并且撤销之后版本号加一、撤销本身也有一条记录。
func TestPostgreSQLRevertRestoresEveryPreviousCorrectionIntent(t *testing.T) {
	fixture := newCorrectionFixture(t)
	kind := CorrectionOperations()
	type intent struct {
		name   string
		change CorrectionChange
	}
	for _, field := range []struct {
		name    string
		intents []intent
	}{
		{"genre", []intent{{"none", CorrectionChange{Mode: catalog.CorrectionClear}}, {"set:古典", CorrectionChange{Mode: catalog.CorrectionSet, Text: "古典"}}, {"set:爵士", CorrectionChange{Mode: catalog.CorrectionSet, Text: "爵士"}}, {"null", CorrectionChange{Mode: catalog.CorrectionNull}}}},
		{"year", []intent{{"none", CorrectionChange{Mode: catalog.CorrectionClear}}, {"set:1975", CorrectionChange{Mode: catalog.CorrectionSet, Year: 1975}}, {"set:1999", CorrectionChange{Mode: catalog.CorrectionSet, Year: 1999}}, {"null", CorrectionChange{Mode: catalog.CorrectionNull}}}},
	} {
		for _, from := range field.intents {
			for _, to := range field.intents {
				if from.name == to.name {
					continue
				}
				t.Run(field.name+" "+from.name+" to "+to.name, func(t *testing.T) {
					release := fixture.visibleRelease(field.name + from.name + to.name)
					fixture.exec(`UPDATE releases SET genre='扫描流派', year=2001 WHERE id=$1::uuid`, release.releaseID)
					revision := int64(1)
					if from.change.Mode != catalog.CorrectionClear {
						revision = fixture.mustCorrect(release.releaseID, revision, map[string]CorrectionChange{field.name: from.change}).Revision
					}
					before := fixture.intentOf(release.releaseID, field.name)
					applied := fixture.mustCorrect(release.releaseID, revision, map[string]CorrectionChange{field.name: to.change})

					reverted := fixture.mustRevert(kind, applied.OperationID)
					if got := fixture.intentOf(release.releaseID, field.name); got != before {
						t.Fatalf("after the revert the %s intent is %q, want %q (the state before the operation)", field.name, got, before)
					}
					if fixture.revisionOf(release.releaseID) != applied.Revision+1 {
						t.Fatalf("revision after the revert = %d, want %d", fixture.revisionOf(release.releaseID), applied.Revision+1)
					}
					// 撤销只能做一次，撤销记录本身不能再撤销。
					if _, err := fixture.revert(kind, applied.OperationID); err == nil {
						t.Fatal("the same operation was reverted twice")
					} else if status, code := revertProblem(err); status != 409 || code != "already_reverted" {
						t.Fatalf("second revert: error = %v (status %d, code %q), want 409 already_reverted", err, status, code)
					}
					if _, err := fixture.revert(kind, reverted.OperationID); err == nil {
						t.Fatal("a revert was reverted")
					} else if status, code := revertProblem(err); status != 409 || code != "not_revertible" {
						t.Fatalf("revert of the revert: error = %v (status %d, code %q), want 409 not_revertible", err, status, code)
					}
				})
			}
		}
	}
}

func TestPostgreSQLRevertOnlyTouchesTheFieldsTheOperationChanged(t *testing.T) {
	fixture := newCorrectionFixture(t)
	ctx := context.Background()
	kind := CorrectionOperations()
	release := fixture.visibleRelease("原标题")
	fixture.exec(`UPDATE releases SET genre='扫描流派', year=2001 WHERE id=$1::uuid`, release.releaseID)

	first := fixture.mustCorrect(release.releaseID, 1, map[string]CorrectionChange{"title": {Mode: catalog.CorrectionSet, Text: "新标题"}, "year": {Mode: catalog.CorrectionSet, Year: 1975}})
	second := fixture.mustCorrect(release.releaseID, first.Revision, map[string]CorrectionChange{"genre": {Mode: catalog.CorrectionNull}})

	// 只有最近一次修改可以撤销：先撤销第一次会被拒绝，不覆盖第二次。
	if _, err := fixture.revert(kind, first.OperationID); err == nil {
		t.Fatal("an operation that is not the latest edit was reverted")
	} else if status, code := revertProblem(err); status != 409 || code != "revert_conflict" {
		t.Fatalf("revert of the older operation: error = %v (status %d, code %q), want 409 revert_conflict", err, status, code)
	}
	if fixture.intentOf(release.releaseID, "title") != "set:新标题" || fixture.intentOf(release.releaseID, "genre") != "null" {
		t.Fatal("a refused revert changed the corrections")
	}
	// 撤销第二次：只有流派回到没有修正，标题与年份的修正不动。
	reverted := fixture.mustRevert(kind, second.OperationID)
	if fixture.intentOf(release.releaseID, "genre") != "none" || fixture.intentOf(release.releaseID, "title") != "set:新标题" || fixture.intentOf(release.releaseID, "year") != "set:1975" {
		t.Fatalf("after reverting the genre change: genre %s, title %s, year %s", fixture.intentOf(release.releaseID, "genre"), fixture.intentOf(release.releaseID, "title"), fixture.intentOf(release.releaseID, "year"))
	}
	// 撤销也是一次修改：第一次现在更不能撤销了（链式恢复不支持）。
	if _, err := fixture.revert(kind, first.OperationID); err == nil {
		t.Fatal("the older operation became revertible after a revert")
	}

	// 撤销记录：同一个目标，版本递增，前后状态记录这个字段当时的意图与有效值。
	record, err := operations.Load(ctx, fixture.database, reverted.OperationID)
	if err != nil || record.Type != operations.TypeRevert || record.RevertsID != second.OperationID || record.TargetID != release.releaseID || record.ExpectedRevision != 3 || record.ResultRevision != 4 {
		t.Fatalf("revert record = %+v, %v", record, err)
	}
	var before, after correctionRecordState
	if err := json.Unmarshal(record.Before, &before); err != nil {
		t.Fatalf("decode before: %v", err)
	}
	if err := json.Unmarshal(record.After, &after); err != nil {
		t.Fatalf("decode after: %v", err)
	}
	if len(before.Fields) != 1 || before.Fields["genre"] == nil || before.Fields["genre"].Mode != "null" || before.Effective["genre"] != nil ||
		len(after.Fields) != 1 || after.Fields["genre"] != nil || after.Effective["genre"] != "扫描流派" {
		t.Fatalf("revert states = before %+v, after %+v; want genre from empty back to the scanned value", before, after)
	}
}

func TestPostgreSQLRevertToNoCorrectionShowsTheCurrentScannedValue(t *testing.T) {
	fixture := newCorrectionFixture(t)
	kind := CorrectionOperations()
	release := fixture.visibleRelease("原标题")
	applied := fixture.mustCorrect(release.releaseID, 1, map[string]CorrectionChange{"title": {Mode: catalog.CorrectionSet, Text: "人工标题"}})
	fixture.exec(`UPDATE releases SET title='重扫后的标题' WHERE id=$1::uuid`, release.releaseID) // 模拟重扫
	fixture.mustRevert(kind, applied.OperationID)
	if got := mustState(t, fixture, release.releaseID); fieldOf(t, got, "title").EffectiveValue != "重扫后的标题" || fieldOf(t, got, "title").Correction != nil {
		t.Fatalf("title after the revert = %+v; want the current scanned value, not the one from before the correction", fieldOf(t, got, "title"))
	}
}

func TestPostgreSQLRevertIsRefusedAfterLaterEditsOrWhenTheTargetIsHidden(t *testing.T) {
	fixture := newCorrectionFixture(t)
	correction, groupChange := CorrectionOperations(), GroupChangeOperations()
	alpha, beta := fixture.versionOf("Alpha", 2000), fixture.versionOf("Beta", 2005)

	// 字段修正与归组共用版本号：归组之后不能撤销之前的修正；修正之后不能撤销之前的归组。
	corrected := fixture.mustCorrect(alpha.releaseID, 1, map[string]CorrectionChange{"genre": {Mode: catalog.CorrectionSet, Text: "古典"}})
	_, joined := fixture.mustChangeGroup(alpha.releaseID, corrected.Revision, GroupJoin, beta.groupID)
	if _, err := fixture.revert(correction, corrected.OperationID); err == nil {
		t.Fatal("a correction was reverted after a later group change")
	} else if status, code := revertProblem(err); status != 409 || code != "revert_conflict" {
		t.Fatalf("error = %v (status %d, code %q), want revert_conflict", err, status, code)
	}
	later := fixture.mustCorrect(alpha.releaseID, corrected.Revision+1, map[string]CorrectionChange{"genre": {Mode: catalog.CorrectionNull}})
	if _, err := fixture.revert(groupChange, joined.OperationID); err == nil {
		t.Fatal("a group change was reverted after a later correction")
	} else if status, code := revertProblem(err); status != 409 || code != "revert_conflict" {
		t.Fatalf("error = %v (status %d, code %q), want revert_conflict", err, status, code)
	}
	if fixture.groupOf(alpha.releaseID) != beta.groupID || fixture.intentOf(alpha.releaseID, "genre") != "null" {
		t.Fatal("refused reverts changed the release")
	}

	// 目标不可见（曲目都缺失了）：撤销是 404，什么都不改。
	fixture.exec(`UPDATE tracks SET source_status='missing' WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1::uuid)`, alpha.releaseID)
	records := fixture.operationCount()
	if _, err := fixture.revert(correction, later.OperationID); err == nil {
		t.Fatal("a hidden release was reverted")
	} else if status, code := revertProblem(err); status != 404 || code != "not_found" {
		t.Fatalf("error = %v (status %d, code %q), want 404 not_found", err, status, code)
	}
	if fixture.operationCount() != records || fixture.intentOf(alpha.releaseID, "genre") != "null" {
		t.Fatal("a refused revert wrote a record or changed the correction")
	}
}

func TestPostgreSQLRevertOfAGroupChangeMovesTheVersionBack(t *testing.T) {
	fixture := newCorrectionFixture(t)
	ctx := context.Background()
	kind := GroupChangeOperations()
	first, second := fixture.versionOf("First", 2000), fixture.versionOf("Second", 2005)

	// 归入后撤销：回到原来的组，原组重新可见，目标组恢复成一个版本。
	_, joined := fixture.mustChangeGroup(second.releaseID, 1, GroupJoin, first.groupID)
	if _, err := fixture.store.LoadReleaseGroup(ctx, second.groupID); err == nil {
		t.Fatal("the emptied group is still visible after the join")
	}
	reverted := fixture.mustRevert(kind, joined.OperationID)
	if fixture.groupOf(second.releaseID) != second.groupID || fixture.revisionOf(second.releaseID) != 3 {
		t.Fatalf("after the revert the release is in %s at revision %d, want its original group at revision 3", fixture.groupOf(second.releaseID), fixture.revisionOf(second.releaseID))
	}
	for _, groupID := range []string{first.groupID, second.groupID} {
		if detail, err := fixture.store.LoadReleaseGroup(ctx, groupID); err != nil || detail.ReleaseCount != 1 {
			t.Errorf("group %s after the revert = %+v, %v; want one version", groupID, detail, err)
		}
	}
	record, _ := operations.Load(ctx, fixture.database, reverted.OperationID)
	var before, after groupRecordState
	_ = json.Unmarshal(record.Before, &before)
	_ = json.Unmarshal(record.After, &after)
	// 撤销记录：从目标组（当时的代表版本标题）回到原组（只有这个版本，用它自己的标题）。
	if before != (groupRecordState{GroupID: first.groupID, GroupTitle: "First"}) || after != (groupRecordState{GroupID: second.groupID, GroupTitle: "Second"}) {
		t.Fatalf("revert states = before %+v, after %+v", before, after)
	}

	// 移出后撤销：回到原来的组，刚刚新建的组变空（不删除）。
	fixture.mustChangeGroup(second.releaseID, 3, GroupJoin, first.groupID)
	detachedResult, detachedOperation := fixture.mustChangeGroup(second.releaseID, 4, GroupDetach, "")
	fixture.mustRevert(kind, detachedOperation.OperationID)
	if fixture.groupOf(second.releaseID) != first.groupID {
		t.Fatalf("after reverting the detach the release is in %s, want the group it was detached from", fixture.groupOf(second.releaseID))
	}
	if _, err := fixture.store.LoadReleaseGroup(ctx, detachedResult.GroupID); err == nil {
		t.Fatal("the group created by the detach is still visible after the revert")
	}
	var groupRows int
	_ = fixture.database.QueryRow(`SELECT COUNT(*) FROM release_groups WHERE id=$1::uuid`, detachedResult.GroupID).Scan(&groupRows)
	if groupRows != 1 {
		t.Fatal("the group created by the detach was deleted")
	}

	// 原组之后又有了别的成员，撤销仍回到原组；别的成员自己的归组不受影响，也仍可撤销。
	third := fixture.versionOf("Third", 2010)
	_, thirdJoined := fixture.mustChangeGroup(third.releaseID, 1, GroupJoin, first.groupID)
	_, secondOut := fixture.mustChangeGroup(second.releaseID, 6, GroupDetach, "")
	fixture.mustRevert(kind, secondOut.OperationID)
	if fixture.groupOf(second.releaseID) != first.groupID {
		t.Fatal("the release did not return to its original group")
	}
	fixture.mustRevert(kind, thirdJoined.OperationID)
	if fixture.groupOf(third.releaseID) != third.groupID {
		t.Fatal("the third release did not return to its own group")
	}
}

func TestPostgreSQLDescribeReportsTargetsChangesAndRevertibility(t *testing.T) {
	fixture := newCorrectionFixture(t)
	ctx := context.Background()
	correctionKind, groupKind := CorrectionOperations(), GroupChangeOperations()
	alpha, beta := fixture.versionOf("Alpha", 2000), fixture.versionOf("Beta", 2005)

	corrected := fixture.mustCorrect(alpha.releaseID, 1, map[string]CorrectionChange{"title": {Mode: catalog.CorrectionSet, Text: "Alpha (corrected)"}, "year": {Mode: catalog.CorrectionNull}})
	_, joined := fixture.mustChangeGroup(alpha.releaseID, corrected.Revision, GroupJoin, beta.groupID)

	load := func(operationID string) operations.Record {
		t.Helper()
		record, err := operations.Load(ctx, fixture.database, operationID)
		if err != nil {
			t.Fatalf("load %s: %v", operationID, err)
		}
		return record
	}
	correctionRecord, joinedRecord := load(corrected.OperationID), load(joined.OperationID)

	described, err := correctionKind.Describe(ctx, fixture.database, []operations.Record{correctionRecord})
	if err != nil {
		t.Fatalf("Describe corrections: %v", err)
	}
	description := described[corrected.OperationID]
	// 目标是发行当前的状态：有效标题（修正后）、现在所在的组（归入之后）、可见；字段修正之后又归了组，所以这条修正已不能撤销。
	if description.Release == nil || *description.Release != (operations.ReleaseInfo{ID: alpha.releaseID, Title: "Alpha (corrected)", GroupID: beta.groupID, Visible: true}) || description.Revertible {
		t.Fatalf("correction description = %+v; want the release as it is now and not revertible", description)
	}
	encoded, _ := json.Marshal(description.Changes)
	var changes struct {
		Kind   string `json:"kind"`
		Fields []struct {
			Field  string `json:"field"`
			Before struct {
				Correction *catalog.CorrectionIntent `json:"correction"`
				Effective  any                       `json:"effective"`
			} `json:"before"`
			After struct {
				Correction *catalog.CorrectionIntent `json:"correction"`
				Effective  any                       `json:"effective"`
			} `json:"after"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(encoded, &changes); err != nil {
		t.Fatalf("decode the changes: %v: %s", err, encoded)
	}
	if changes.Kind != operations.TypeReleaseCorrection || len(changes.Fields) != 2 || changes.Fields[0].Field != "title" || changes.Fields[1].Field != "year" ||
		changes.Fields[0].Before.Correction != nil || changes.Fields[0].Before.Effective != "Alpha" ||
		changes.Fields[0].After.Correction == nil || changes.Fields[0].After.Correction.Value != "Alpha (corrected)" || changes.Fields[0].After.Effective != "Alpha (corrected)" ||
		changes.Fields[1].After.Correction == nil || changes.Fields[1].After.Correction.Mode != "null" || changes.Fields[1].After.Effective != nil || changes.Fields[1].Before.Effective != float64(2000) {
		t.Fatalf("correction changes = %s", encoded)
	}

	described, err = groupKind.Describe(ctx, fixture.database, []operations.Record{joinedRecord})
	if err != nil {
		t.Fatalf("Describe group changes: %v", err)
	}
	description = described[joined.OperationID]
	if description.Release == nil || description.Release.GroupID != beta.groupID || !description.Revertible {
		t.Fatalf("group change description = %+v; want the latest edit to be revertible", description)
	}
	encoded, _ = json.Marshal(description.Changes)
	var groupChanges struct {
		Kind string           `json:"kind"`
		From groupRecordState `json:"from"`
		To   groupRecordState `json:"to"`
	}
	if err := json.Unmarshal(encoded, &groupChanges); err != nil {
		t.Fatalf("decode the group changes: %v", err)
	}
	if groupChanges.Kind != operations.TypeReleaseGroupChange || groupChanges.From != (groupRecordState{GroupID: alpha.groupID, GroupTitle: "Alpha (corrected)"}) || groupChanges.To != (groupRecordState{GroupID: beta.groupID, GroupTitle: "Beta"}) {
		t.Fatalf("group changes = %s", encoded)
	}

	// 撤销记录按它撤销的类型解释；目标不可见之后 Release.Visible 为假，也不能撤销。
	reverted := fixture.mustRevert(groupKind, joined.OperationID)
	revertRecord := load(reverted.OperationID)
	described, err = groupKind.Describe(ctx, fixture.database, []operations.Record{revertRecord})
	if err != nil || described[reverted.OperationID].Changes == nil || described[reverted.OperationID].Release == nil || described[reverted.OperationID].Release.GroupID != alpha.groupID {
		t.Fatalf("revert description = %+v, %v", described[reverted.OperationID], err)
	}
	fixture.exec(`UPDATE tracks SET source_status='missing' WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1::uuid)`, alpha.releaseID)
	described, _ = correctionKind.Describe(ctx, fixture.database, []operations.Record{correctionRecord})
	if got := described[corrected.OperationID]; got.Release == nil || got.Release.Visible || got.Revertible {
		t.Fatalf("description of a hidden release = %+v; want visible=false and not revertible", got)
	}
	// 即使记录的结果版本仍是目标当前的版本（它是最近一次修改），目标不可见时也不能撤销。
	described, _ = groupKind.Describe(ctx, fixture.database, []operations.Record{revertRecord})
	if got := described[reverted.OperationID]; revertRecord.ResultRevision != fixture.revisionOf(alpha.releaseID) || got.Release == nil || got.Release.Visible || got.Revertible {
		t.Fatalf("description of the latest edit of a hidden release = %+v; want visible=false and not revertible", got)
	}
	// 目标行不存在（记录比目标活得久）：没有 Release，不可撤销。
	orphan := operations.Record{ID: uuid.New(), TargetType: operations.TargetRelease, TargetID: uuid.New(), Before: json.RawMessage(`{}`), After: json.RawMessage(`{}`)}
	described, err = groupKind.Describe(ctx, fixture.database, []operations.Record{orphan})
	if err != nil || described[orphan.ID].Release != nil || described[orphan.ID].Revertible {
		t.Fatalf("description of a record whose release is gone = %+v, %v", described[orphan.ID], err)
	}
	// 记录里的状态损坏时不让列表失败：改动摘要为空。
	broken := operations.Record{ID: uuid.New(), TargetID: alpha.releaseID, Before: json.RawMessage(`"not an object"`), After: json.RawMessage(`[]`)}
	described, err = correctionKind.Describe(ctx, fixture.database, []operations.Record{broken})
	if err != nil || described[broken.ID].Changes != nil {
		t.Fatalf("description of a record with unreadable states = %+v, %v", described[broken.ID], err)
	}
	if empty, err := correctionKind.Describe(ctx, fixture.database, nil); err != nil || len(empty) != 0 {
		t.Fatalf("Describe(nil) = %v, %v", empty, err)
	}
	if !reflect.DeepEqual(CorrectionOperations().Type(), operations.TypeReleaseCorrection) || GroupChangeOperations().Type() != operations.TypeReleaseGroupChange {
		t.Fatal("the kinds report the wrong operation types")
	}
}
