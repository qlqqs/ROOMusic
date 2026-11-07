package governance

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func (fixture *correctionFixture) changeGroup(releaseID string, expectedRevision int64, action, groupID string) (operations.Result, error) {
	fixture.t.Helper()
	request := GroupChangeRequest{ReleaseID: releaseID, ExpectedRevision: expectedRevision, Action: action, GroupID: groupID}
	return operations.Run(context.Background(), fixture.database, operations.Spec{
		ActorID: fixture.actorID, Type: operations.TypeReleaseGroupChange, Key: uuid.New(),
		Fingerprint: request.Fingerprint(), RequestID: "test",
	}, GroupChangeExecutor(request))
}

func (fixture *correctionFixture) mustChangeGroup(releaseID string, expectedRevision int64, action, groupID string) (GroupChangeResult, operations.Result) {
	fixture.t.Helper()
	result, err := fixture.changeGroup(releaseID, expectedRevision, action, groupID)
	if err != nil {
		fixture.t.Fatalf("change group (%s): %v", action, err)
	}
	var decoded GroupChangeResult
	if err := json.Unmarshal(result.Response, &decoded); err != nil {
		fixture.t.Fatalf("decode group change result: %v", err)
	}
	if decoded.OperationID != result.OperationID || decoded.ReleaseID != releaseID {
		fixture.t.Fatalf("group change response = %+v, want operation %s for release %s", decoded, result.OperationID, releaseID)
	}
	return decoded, result
}

// versionOf 造一个可见的版本：独立的组、给定的标题与年份。
func (fixture *correctionFixture) versionOf(title string, year int) placementRelease {
	fixture.t.Helper()
	release := fixture.visibleRelease(title)
	fixture.exec(`UPDATE releases SET year=$2 WHERE id=$1::uuid`, release.releaseID, year)
	return release
}

func (fixture *correctionFixture) groupOf(releaseID string) string {
	fixture.t.Helper()
	var groupID string
	if err := fixture.database.QueryRow(`SELECT group_id::text FROM releases WHERE id=$1::uuid`, releaseID).Scan(&groupID); err != nil {
		fixture.t.Fatalf("read group: %v", err)
	}
	return groupID
}

func TestPostgreSQLJoinMovesAVersionIntoAnotherAlbum(t *testing.T) {
	fixture := newCorrectionFixture(t)
	ctx := context.Background()
	moved, target := fixture.versionOf("Moved", 2000), fixture.versionOf("Target", 2005)
	fixture.decision(moved.releaseID, "year", "uncertain_apply")
	fixture.decision(target.releaseID, "year", "uncertain_apply")
	fixture.decision(target.releaseID, "label", "uncertain_apply")

	result, operation := fixture.mustChangeGroup(moved.releaseID, 1, GroupJoin, target.groupID)
	if result.GroupID != target.groupID || result.PreviousGroupID != moved.groupID || result.Revision != 2 || fixture.groupOf(moved.releaseID) != target.groupID || fixture.revisionOf(moved.releaseID) != 2 {
		t.Fatalf("result = %+v; want the release in the target group at revision 2", result)
	}
	// 目标组现在有两个版本；代表版本规则不变（年份最早的在前），attention 之和包含新成员。
	detail, err := fixture.store.LoadReleaseGroup(ctx, target.groupID)
	if err != nil || detail.ReleaseCount != 2 || detail.Representative.ID != moved.releaseID || len(detail.Releases) != 2 ||
		detail.Releases[0].ID != moved.releaseID || detail.Releases[1].ID != target.releaseID || detail.AttentionCount != 3 {
		t.Fatalf("target group = %+v, %v; want two versions, the earlier year representative and attention 3", detail, err)
	}
	// 原组没有可见版本：从列表消失、详情找不到；组行仍在（撤销时回到它）。
	if _, err := fixture.store.LoadReleaseGroup(ctx, moved.groupID); !errors.Is(err, catalog.ErrReleaseGroupNotFound) {
		t.Fatalf("the emptied group is still readable: %v", err)
	}
	groups, err := fixture.store.ListReleaseGroups(ctx, catalog.ListParams{Page: 1, PageSize: 10})
	if err != nil || groups.Total != 1 || groups.Items[0].ID != target.groupID {
		t.Fatalf("group list = %+v, %v; want only the target group", groups, err)
	}
	var groupRows int
	_ = fixture.database.QueryRow(`SELECT COUNT(*) FROM release_groups WHERE id=$1::uuid`, moved.groupID).Scan(&groupRows)
	if groupRows != 1 {
		t.Fatalf("the emptied group was deleted")
	}
	// 搜索按组内任一成员命中；/releases 列表不受影响（每个版本仍是一行）。
	releases, err := fixture.store.ListReleases(ctx, catalog.ListParams{Page: 1, PageSize: 10})
	if err != nil || releases.Total != 2 {
		t.Fatalf("release list = %+v, %v; want both versions", releases, err)
	}

	// 操作记录：类型、目标、前后版本与前后状态（组 ID 与当时的标题快照）。
	record, err := operations.Load(ctx, fixture.database, operation.OperationID)
	if err != nil || record.Type != operations.TypeReleaseGroupChange || record.TargetID != moved.releaseID || record.ExpectedRevision != 1 || record.ResultRevision != 2 {
		t.Fatalf("operation record = %+v, %v", record, err)
	}
	var before, after groupRecordState
	if err := json.Unmarshal(record.Before, &before); err != nil {
		t.Fatalf("decode before: %v", err)
	}
	if err := json.Unmarshal(record.After, &after); err != nil {
		t.Fatalf("decode after: %v", err)
	}
	if before != (groupRecordState{GroupID: moved.groupID, GroupTitle: "Moved"}) || after != (groupRecordState{GroupID: target.groupID, GroupTitle: "Target"}) {
		t.Fatalf("recorded states = before %+v, after %+v", before, after)
	}
}

func TestPostgreSQLDetachMakesAVersionItsOwnAlbum(t *testing.T) {
	fixture := newCorrectionFixture(t)
	ctx := context.Background()
	first, second := fixture.versionOf("First", 2000), fixture.versionOf("Second", 2005)
	fixture.mustChangeGroup(second.releaseID, 1, GroupJoin, first.groupID)
	if second.groupID == first.groupID {
		t.Fatal("fixture error: the versions started in the same group")
	}

	result, operation := fixture.mustChangeGroup(second.releaseID, 2, GroupDetach, "")
	if result.PreviousGroupID != first.groupID || result.GroupID == first.groupID || result.GroupID == second.groupID || result.Revision != 3 {
		t.Fatalf("detach result = %+v; want a brand new group at revision 3", result)
	}
	groups, err := fixture.store.ListReleaseGroups(ctx, catalog.ListParams{Page: 1, PageSize: 10})
	if err != nil || groups.Total != 2 {
		t.Fatalf("group list = %+v, %v; want two albums again", groups, err)
	}
	for _, groupID := range []string{first.groupID, result.GroupID} {
		detail, err := fixture.store.LoadReleaseGroup(ctx, groupID)
		if err != nil || detail.ReleaseCount != 1 {
			t.Errorf("group %s = %+v, %v; want one version", groupID, detail, err)
		}
	}
	record, _ := operations.Load(ctx, fixture.database, operation.OperationID)
	var before, after groupRecordState
	_ = json.Unmarshal(record.Before, &before)
	_ = json.Unmarshal(record.After, &after)
	// 前态的标题是移动之前原组的代表版本（年份最早的 First）；后态的标题是这个版本自己的标题。
	if before != (groupRecordState{GroupID: first.groupID, GroupTitle: "First"}) || after != (groupRecordState{GroupID: result.GroupID, GroupTitle: "Second"}) {
		t.Fatalf("recorded states = before %+v, after %+v", before, after)
	}

	// 已经是独立专辑的版本不能再移出（不会产生空组或重复的组）。
	var groupsBefore int
	_ = fixture.database.QueryRow(`SELECT COUNT(*) FROM release_groups`).Scan(&groupsBefore)
	_, err = fixture.changeGroup(second.releaseID, 3, GroupDetach, "")
	if status, code, _ := operations.Problem(err); status != 409 || code != "already_alone" {
		t.Fatalf("detach of an album that is already alone: error = %v (status %d, code %q), want 409 already_alone", err, status, code)
	}
	var groupsAfter int
	_ = fixture.database.QueryRow(`SELECT COUNT(*) FROM release_groups`).Scan(&groupsAfter)
	if groupsAfter != groupsBefore || fixture.revisionOf(second.releaseID) != 3 {
		t.Fatalf("a rejected detach changed groups %d→%d or the revision %d", groupsBefore, groupsAfter, fixture.revisionOf(second.releaseID))
	}
}

func TestPostgreSQLGroupChangeRejectsInvalidRequestsWithoutSideEffects(t *testing.T) {
	fixture := newCorrectionFixture(t)
	alpha, beta := fixture.versionOf("Alpha", 2000), fixture.versionOf("Beta", 2005)
	hidden := fixture.release("只有缺失曲目")
	fixture.track(hidden, "缺失", "missing", 10.0)
	hiddenOnly := fixture.release("目标组里只有缺失曲目")
	fixture.track(hiddenOnly, "缺失", "missing", 10.0)

	for _, testCase := range []struct {
		name     string
		release  string
		revision int64
		action   string
		group    string
		status   int
		code     string
	}{
		{"join the current group", alpha.releaseID, 1, GroupJoin, alpha.groupID, 409, "same_group"},
		{"join a group that does not exist", alpha.releaseID, 1, GroupJoin, uuid.New(), 404, "group_not_found"},
		{"join a group without visible versions", alpha.releaseID, 1, GroupJoin, hiddenOnly.groupID, 404, "group_not_found"},
		{"stale revision", alpha.releaseID, 5, GroupJoin, beta.groupID, 409, "revision_conflict"},
		{"stale revision of zero", alpha.releaseID, 0, GroupJoin, beta.groupID, 409, "revision_conflict"},
		{"release without visible tracks", hidden.releaseID, 1, GroupJoin, beta.groupID, 404, "not_found"},
		{"unknown release", uuid.New(), 1, GroupDetach, "", 404, "not_found"},
		{"unknown action", alpha.releaseID, 1, "merge", beta.groupID, 400, "invalid_input"},
		{"detach an album that is already alone", alpha.releaseID, 1, GroupDetach, "", 409, "already_alone"},
	} {
		records := fixture.operationCount()
		_, err := fixture.changeGroup(testCase.release, testCase.revision, testCase.action, testCase.group)
		if status, code, _ := operations.Problem(err); status != testCase.status || code != testCase.code {
			t.Errorf("%s: error = %v (status %d, code %q), want %d %s", testCase.name, err, status, code, testCase.status, testCase.code)
		}
		if fixture.operationCount() != records {
			t.Errorf("%s: a rejected request wrote an operation record", testCase.name)
		}
	}
	if fixture.groupOf(alpha.releaseID) != alpha.groupID || fixture.revisionOf(alpha.releaseID) != 1 || fixture.groupOf(beta.releaseID) != beta.groupID {
		t.Fatal("rejected requests moved a release or changed a revision")
	}

	// 字段修正与归组共用同一个版本号：修正之后用旧版本号归组被拒绝，用新版本号成功。
	fixture.mustCorrect(alpha.releaseID, 1, map[string]CorrectionChange{"genre": {Mode: "set", Genres: []string{"古典"}}})
	if _, err := fixture.changeGroup(alpha.releaseID, 1, GroupJoin, beta.groupID); err == nil {
		t.Fatal("a group change with the revision from before a correction was accepted")
	}
	fixture.mustChangeGroup(alpha.releaseID, 2, GroupJoin, beta.groupID)
	// 反过来：归组之后用旧版本号修正被拒绝。
	if _, err := fixture.correctRelease(alpha.releaseID, 2, map[string]CorrectionChange{"genre": {Mode: "null"}}); err == nil {
		t.Fatal("a correction with the revision from before a group change was accepted")
	}
	fixture.mustCorrect(alpha.releaseID, 3, map[string]CorrectionChange{"genre": {Mode: "null"}})
}

// 两个版本同时归入对方所在的组：发行行先锁，组行按 UUID 顺序锁，所以不会死锁；结果一致（一个成功，另一个发现目标组已空）。
func TestPostgreSQLConcurrentGroupChangesDoNotDeadlock(t *testing.T) {
	fixture := newCorrectionFixture(t)
	alpha, beta := fixture.versionOf("Alpha", 2000), fixture.versionOf("Beta", 2005)

	holder, err := fixture.database.Begin()
	if err != nil {
		t.Fatalf("begin lock holder: %v", err)
	}
	t.Cleanup(func() { _ = holder.Rollback() })
	// 测试事务先锁住两个组行：两个归组请求各自先锁到自己的发行行，再一起卡在组行上，
	// 释放之后它们对组行的争用正好是死锁的经典交错——只有统一的加锁顺序才能避免。
	if _, err := holder.Exec(`SELECT id FROM release_groups WHERE id = ANY($1::text[]::uuid[]) FOR UPDATE`, []string{alpha.groupID, beta.groupID}); err != nil {
		t.Fatalf("lock the groups: %v", err)
	}
	type outcome struct {
		result operations.Result
		err    error
	}
	outcomes := make(chan outcome, 2)
	var started sync.WaitGroup
	for _, move := range []struct{ release, group string }{{alpha.releaseID, beta.groupID}, {beta.releaseID, alpha.groupID}} {
		started.Add(1)
		go func() {
			started.Done()
			result, err := fixture.changeGroup(move.release, 1, GroupJoin, move.group)
			outcomes <- outcome{result, err}
		}()
	}
	started.Wait()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := fixture.database.QueryRow(`SELECT COUNT(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND pid<>pg_backend_pid()`).Scan(&waiting); err != nil {
			t.Fatalf("count lock waiters: %v", err)
		}
		if waiting >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d group changes are waiting on the group locks", waiting)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := holder.Rollback(); err != nil {
		t.Fatalf("release the group locks: %v", err)
	}

	succeeded, notFound := 0, 0
	for range 2 {
		select {
		case result := <-outcomes:
			switch status, code, _ := operations.Problem(result.err); {
			case result.err == nil:
				succeeded++
			case status == 404 && code == "group_not_found":
				notFound++
			default:
				t.Fatalf("a concurrent group change failed with %v (status %d, code %q); a deadlock or lost update?", result.err, status, code)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("the concurrent group changes did not finish")
		}
	}
	if succeeded != 1 || notFound != 1 {
		t.Fatalf("%d succeeded and %d found the target group already empty; want exactly one of each", succeeded, notFound)
	}
	// 结果一致：所有版本都在可见的组里，没有版本丢失。
	groups, err := fixture.store.ListReleaseGroups(context.Background(), catalog.ListParams{Page: 1, PageSize: 10})
	releases, _ := fixture.store.ListReleases(context.Background(), catalog.ListParams{Page: 1, PageSize: 10})
	if err != nil || groups.Total != 1 || releases.Total != 2 {
		t.Fatalf("after the race: groups %+v (%v), releases %d; want one group holding both versions", groups, err, releases.Total)
	}
}
