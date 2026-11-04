package governance

import (
	"context"
	"database/sql"
	"encoding/json"
	"math/rand"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// setDefault 在专辑上执行真正的设定流程（operations.Run + GroupDefaultExecutor）；releaseID 为空表示改回自动选择。
func (fixture *correctionFixture) setDefault(groupID string, expectedRevision int64, releaseID string) (operations.Result, error) {
	fixture.t.Helper()
	request := GroupDefaultRequest{GroupID: groupID, ExpectedRevision: expectedRevision, ReleaseID: releaseID}
	return operations.Run(context.Background(), fixture.database, operations.Spec{
		ActorID: fixture.actorID, Type: operations.TypeReleaseGroupDefault, Key: uuid.New(),
		Fingerprint: request.Fingerprint(), RequestID: "test",
	}, GroupDefaultExecutor(request))
}

// mustSetDefault 执行一次应该成功的设定，核对响应：专辑、现在已存的默认版本、新的版本号与操作 ID。
func (fixture *correctionFixture) mustSetDefault(groupID string, expectedRevision int64, releaseID string) (GroupDefaultResult, operations.Result) {
	fixture.t.Helper()
	result, err := fixture.setDefault(groupID, expectedRevision, releaseID)
	if err != nil {
		fixture.t.Fatalf("set default (%q): %v", releaseID, err)
	}
	var decoded GroupDefaultResult
	if err := json.Unmarshal(result.Response, &decoded); err != nil {
		fixture.t.Fatalf("decode the default-version result: %v", err)
	}
	wantDefault := optionalDefaultID(releaseID)
	if decoded.OperationID != result.OperationID || decoded.GroupID != groupID || decoded.Revision != expectedRevision+1 ||
		(wantDefault == nil) != (decoded.DefaultReleaseID == nil) || (wantDefault != nil && *wantDefault != *decoded.DefaultReleaseID) {
		fixture.t.Fatalf("default-version response = %+v, want operation %s on group %s at revision %d with default %v", decoded, result.OperationID, groupID, expectedRevision+1, wantDefault)
	}
	return decoded, result
}

func (fixture *correctionFixture) groupRevision(groupID string) int64 {
	fixture.t.Helper()
	var revision int64
	if err := fixture.database.QueryRow(`SELECT edit_revision FROM release_groups WHERE id=$1::uuid`, groupID).Scan(&revision); err != nil {
		fixture.t.Fatalf("read group revision: %v", err)
	}
	return revision
}

// storedDefault 读取已存的默认版本（包括失效中的）；没有指定时为空。
func (fixture *correctionFixture) storedDefault(groupID string) string {
	fixture.t.Helper()
	var stored sql.NullString
	if err := fixture.database.QueryRow(`SELECT default_release_id::text FROM release_groups WHERE id=$1::uuid`, groupID).Scan(&stored); err != nil {
		fixture.t.Fatalf("read the stored default: %v", err)
	}
	return stored.String
}

// setVisible 让发行有或没有可见曲目：把它唯一的曲目标成 present 或 missing。
func (fixture *correctionFixture) setVisible(release placementRelease, visible bool) {
	fixture.t.Helper()
	status := "missing"
	if visible {
		status = "present"
	}
	fixture.exec(`UPDATE tracks SET source_status=$2 WHERE medium_id=$1::uuid`, release.mediumID, status)
}

// membersOf 读取组详情里的成员顺序与生效中的默认版本。
func (fixture *correctionFixture) membersOf(groupID string) (order []string, detail catalog.ReleaseGroupDetail) {
	fixture.t.Helper()
	detail, err := fixture.store.LoadReleaseGroup(context.Background(), groupID)
	if err != nil {
		fixture.t.Fatalf("load group %s: %v", groupID, err)
	}
	for _, member := range detail.Releases {
		order = append(order, member.ID)
	}
	return order, detail
}

func (fixture *correctionFixture) requireOrder(groupID string, want ...string) catalog.ReleaseGroupDetail {
	fixture.t.Helper()
	order, detail := fixture.membersOf(groupID)
	if !slices.Equal(order, want) {
		fixture.t.Fatalf("members of %s = %v, want %v", groupID, order, want)
	}
	if detail.Representative.ID != want[0] {
		fixture.t.Fatalf("representative of %s = %s, want the first member %s", groupID, detail.Representative.ID, want[0])
	}
	return detail
}

func (fixture *correctionFixture) requireDefault(detail catalog.ReleaseGroupDetail, want string) {
	fixture.t.Helper()
	switch {
	case want == "" && detail.DefaultReleaseID != nil:
		fixture.t.Fatalf("the group reports an effective default %s, want none", *detail.DefaultReleaseID)
	case want != "" && (detail.DefaultReleaseID == nil || *detail.DefaultReleaseID != want):
		fixture.t.Fatalf("the group's effective default = %v, want %s", detail.DefaultReleaseID, want)
	}
}

func (fixture *correctionFixture) groupIDs() []string {
	fixture.t.Helper()
	rows, err := fixture.database.Query(`SELECT id::text FROM release_groups ORDER BY id`)
	if err != nil {
		fixture.t.Fatalf("list groups: %v", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			fixture.t.Fatalf("scan group id: %v", err)
		}
		ids = append(ids, id)
	}
	return ids
}

func requireProblem(t *testing.T, err error, status int, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("no error, want %d %s", status, code)
	}
	if gotStatus, gotCode := revertProblem(err); gotStatus != status || gotCode != code {
		t.Fatalf("error = %v (status %d, code %q), want %d %s", err, gotStatus, gotCode, status, code)
	}
}

// 默认版本成为专辑的代表版本：专辑卡片、列表排序、成员顺序、归组记录的标题快照都跟着变；改回自动选择后恢复。
func TestPostgreSQLDefaultVersionBecomesTheRepresentative(t *testing.T) {
	fixture := newCorrectionFixture(t)
	ctx := context.Background()
	original := fixture.versionOf("Alpha", 2000)
	reissue := fixture.versionOf("Alpha Remaster", 2015)
	hires := fixture.versionOf("Zulu Hi-Res", 2020)
	standalone := fixture.versionOf("Middle", 2001)
	group := original.groupID
	fixture.mustChangeGroup(reissue.releaseID, 1, GroupJoin, group)
	fixture.mustChangeGroup(hires.releaseID, 1, GroupJoin, group)

	// 没有指定：年份最早的在前，版本号 1，没有生效中的默认版本；列表里 Alpha 排在 Middle 之前。
	detail := fixture.requireOrder(group, original.releaseID, reissue.releaseID, hires.releaseID)
	fixture.requireDefault(detail, "")
	if detail.Revision != 1 || detail.Representative.Title != "Alpha" {
		t.Fatalf("group without a default = revision %d, representative %q; want 1 and Alpha", detail.Revision, detail.Representative.Title)
	}
	groups, err := fixture.store.ListReleaseGroups(ctx, catalog.ListParams{Page: 1, PageSize: 10})
	if err != nil || len(groups.Items) != 2 || groups.Items[0].ID != group || groups.Items[1].ID != standalone.groupID {
		t.Fatalf("group list without a default = %+v, %v; want the album first", groups, err)
	}

	hiresRevision := fixture.revisionOf(hires.releaseID)
	result, _ := fixture.mustSetDefault(group, 1, hires.releaseID)
	if result.Revision != 2 || fixture.groupRevision(group) != 2 || fixture.storedDefault(group) != hires.releaseID {
		t.Fatalf("after setting the default: result %+v, group revision %d, stored %q", result, fixture.groupRevision(group), fixture.storedDefault(group))
	}
	// 默认版本是专辑的状态：版本自己的人工修改版本号不变，其余成员仍按年份最早排。
	if fixture.revisionOf(hires.releaseID) != hiresRevision {
		t.Fatalf("setting the default changed the version's own revision: %d → %d", hiresRevision, fixture.revisionOf(hires.releaseID))
	}
	detail = fixture.requireOrder(group, hires.releaseID, original.releaseID, reissue.releaseID)
	fixture.requireDefault(detail, hires.releaseID)
	if detail.Revision != 2 || detail.Representative.Title != "Zulu Hi-Res" || detail.ReleaseCount != 3 {
		t.Fatalf("group with a default = revision %d, representative %q, %d versions", detail.Revision, detail.Representative.Title, detail.ReleaseCount)
	}
	// 专辑卡片显示默认版本，列表按它的标题排序（Zulu 排在 Middle 之后）。
	groups, err = fixture.store.ListReleaseGroups(ctx, catalog.ListParams{Page: 1, PageSize: 10})
	if err != nil || len(groups.Items) != 2 || groups.Items[0].ID != standalone.groupID || groups.Items[1].ID != group || groups.Items[1].Representative.ID != hires.releaseID {
		t.Fatalf("group list with a default = %+v, %v; want Middle first and the album shown as its default", groups, err)
	}
	if groups.Items[1].ReleaseCount != 3 {
		t.Fatalf("the album card counts %d versions, want 3", groups.Items[1].ReleaseCount)
	}

	// 归组记录里的标题快照跟着代表版本：把原版移出时，原组的前态标题是默认版本的标题。
	_, operation := fixture.mustChangeGroup(original.releaseID, fixture.revisionOf(original.releaseID), GroupDetach, "")
	record, err := operations.Load(ctx, fixture.database, operation.OperationID)
	if err != nil {
		t.Fatalf("load the detach record: %v", err)
	}
	var before groupRecordState
	if err := json.Unmarshal(record.Before, &before); err != nil || before.GroupTitle != "Zulu Hi-Res" {
		t.Fatalf("the detach record's previous title = %+v (%v); want the default version's title", before, err)
	}
	// 归组不改专辑的默认版本与版本号。
	if fixture.storedDefault(group) != hires.releaseID || fixture.groupRevision(group) != 2 {
		t.Fatalf("detaching a version changed the album's default (%q) or revision (%d)", fixture.storedDefault(group), fixture.groupRevision(group))
	}

	// 改回自动选择：按年份最早（原版已经移出，剩下的里 2015 的在前）。
	cleared, _ := fixture.mustSetDefault(group, 2, "")
	if cleared.DefaultReleaseID != nil || cleared.Revision != 3 || fixture.storedDefault(group) != "" {
		t.Fatalf("clearing the default: result %+v, stored %q", cleared, fixture.storedDefault(group))
	}
	detail = fixture.requireOrder(group, reissue.releaseID, hires.releaseID)
	fixture.requireDefault(detail, "")
}

// 默认版本不可见（曲目缺失）或被移出这张专辑时静默失效，专辑回到自动规则；版本回来（文件恢复、撤销移出）时重新生效；
// 扫描与归组都不改已存的默认版本。
func TestPostgreSQLDefaultVersionGoesDormantAndComesBack(t *testing.T) {
	fixture := newCorrectionFixture(t)
	ctx := context.Background()
	original := fixture.versionOf("Alpha", 2000)
	hires := fixture.versionOf("Alpha Hi-Res", 2020)
	group := original.groupID
	fixture.mustChangeGroup(hires.releaseID, 1, GroupJoin, group)
	fixture.mustSetDefault(group, 1, hires.releaseID)
	fixture.requireDefault(fixture.requireOrder(group, hires.releaseID, original.releaseID), hires.releaseID)

	// 曲目缺失：默认版本不可见，专辑按自动规则显示（卡片是原版），已存的默认版本保留。
	fixture.setVisible(hires, false)
	detail := fixture.requireOrder(group, original.releaseID)
	fixture.requireDefault(detail, "")
	if detail.ReleaseCount != 1 || fixture.storedDefault(group) != hires.releaseID || fixture.groupRevision(group) != 2 {
		t.Fatalf("while the default is missing: %d versions, stored %q, revision %d; want 1, the default kept, revision 2", detail.ReleaseCount, fixture.storedDefault(group), fixture.groupRevision(group))
	}
	groups, err := fixture.store.ListReleaseGroups(ctx, catalog.ListParams{Page: 1, PageSize: 10})
	if err != nil || len(groups.Items) != 1 || groups.Items[0].Representative.ID != original.releaseID {
		t.Fatalf("group list while the default is missing = %+v, %v; want the original as the card", groups, err)
	}
	// 文件回来：重新生效。
	fixture.setVisible(hires, true)
	fixture.requireDefault(fixture.requireOrder(group, hires.releaseID, original.releaseID), hires.releaseID)

	// 被移出这张专辑：默认版本失效，专辑回到自动规则；移出去的版本自己的专辑没有默认版本。
	_, detachOperation := fixture.mustChangeGroup(hires.releaseID, fixture.revisionOf(hires.releaseID), GroupDetach, "")
	fixture.requireDefault(fixture.requireOrder(group, original.releaseID), "")
	if fixture.storedDefault(group) != hires.releaseID || fixture.groupRevision(group) != 2 {
		t.Fatalf("after the default was moved out: stored %q, revision %d; want the default kept and revision 2", fixture.storedDefault(group), fixture.groupRevision(group))
	}
	own := fixture.groupOf(hires.releaseID)
	ownDetail := fixture.requireOrder(own, hires.releaseID)
	fixture.requireDefault(ownDetail, "")
	if ownDetail.Revision != 1 || fixture.storedDefault(own) != "" {
		t.Fatalf("the detached version's own album: revision %d, stored %q; want a fresh album", ownDetail.Revision, fixture.storedDefault(own))
	}
	// 撤销这次移出：版本回到原专辑，重新成为默认版本。
	fixture.mustRevert(GroupChangeOperations(), detachOperation.OperationID)
	fixture.requireDefault(fixture.requireOrder(group, hires.releaseID, original.releaseID), hires.releaseID)

	// 另一条路径：再次移出后归入另一张专辑，它不会成为那张专辑的默认版本；归回原来的专辑后重新生效。
	fixture.mustChangeGroup(hires.releaseID, fixture.revisionOf(hires.releaseID), GroupDetach, "")
	third := fixture.versionOf("Zeta", 2010)
	fixture.mustChangeGroup(hires.releaseID, fixture.revisionOf(hires.releaseID), GroupJoin, third.groupID)
	fixture.requireDefault(fixture.requireOrder(third.groupID, third.releaseID, hires.releaseID), "")
	fixture.requireDefault(fixture.requireOrder(group, original.releaseID), "")
	fixture.mustChangeGroup(hires.releaseID, fixture.revisionOf(hires.releaseID), GroupJoin, group)
	fixture.requireDefault(fixture.requireOrder(group, hires.releaseID, original.releaseID), hires.releaseID)
	if fixture.groupRevision(group) != 2 || fixture.storedDefault(group) != hires.releaseID {
		t.Fatalf("moving the default around changed the album: revision %d, stored %q", fixture.groupRevision(group), fixture.storedDefault(group))
	}
}

// operationLatest 返回某种类型最近一条记录的 ID。
func (fixture *correctionFixture) operationLatest(operationType string) string {
	fixture.t.Helper()
	var id string
	if err := fixture.database.QueryRow(`SELECT id::text FROM operations WHERE operation_type=$1 AND NOT EXISTS (SELECT 1 FROM operations r WHERE r.reverts_operation_id=operations.id) ORDER BY created_at DESC, id DESC LIMIT 1`, operationType).Scan(&id); err != nil {
		fixture.t.Fatalf("read the latest %s operation: %v", operationType, err)
	}
	return id
}

// 被拒绝的请求都分类明确且没有副作用：没有新记录，专辑的版本号与已存的默认版本不变。
func TestPostgreSQLSetDefaultRejectsInvalidRequestsWithoutSideEffects(t *testing.T) {
	fixture := newCorrectionFixture(t)
	original := fixture.versionOf("Alpha", 2000)
	hires := fixture.versionOf("Alpha Hi-Res", 2020)
	hiddenMember := fixture.versionOf("Alpha Hidden", 2010)
	elsewhere := fixture.versionOf("Elsewhere", 1990)
	gone := fixture.release("Gone") // 没有可见曲目：组里没有可见成员
	group := original.groupID
	fixture.mustChangeGroup(hires.releaseID, 1, GroupJoin, group)
	fixture.mustChangeGroup(hiddenMember.releaseID, 1, GroupJoin, group)
	fixture.setVisible(hiddenMember, false)
	operationsBefore := fixture.operationCount()

	for name, testCase := range map[string]struct {
		group    string
		revision int64
		release  string
		status   int
		code     string
	}{
		"stale revision":                    {group, 9, hires.releaseID, 409, "revision_conflict"},
		"stale revision when clearing":      {group, 9, "", 409, "revision_conflict"},
		"a version of another album":        {group, 1, elsewhere.releaseID, 409, "not_in_group"},
		"a member without visible tracks":   {group, 1, hiddenMember.releaseID, 409, "not_in_group"},
		"a release that does not exist":     {group, 1, uuid.New(), 409, "not_in_group"},
		"clearing when nothing is stored":   {group, 1, "", 400, "no_changes"},
		"an album that does not exist":      {uuid.New(), 1, hires.releaseID, 404, "not_found"},
		"clearing an unknown album":         {uuid.New(), 1, "", 404, "not_found"},
		"an album without visible members":  {gone.groupID, 1, "", 404, "not_found"},
		"a version set on an invisible one": {gone.groupID, 1, gone.releaseID, 404, "not_found"},
	} {
		_, err := fixture.setDefault(testCase.group, testCase.revision, testCase.release)
		if err == nil {
			t.Errorf("%s: succeeded, want %d %s", name, testCase.status, testCase.code)
			continue
		}
		if status, code := revertProblem(err); status != testCase.status || code != testCase.code {
			t.Errorf("%s: error = %v (status %d, code %q), want %d %s", name, err, status, code, testCase.status, testCase.code)
		}
	}
	if fixture.operationCount() != operationsBefore || fixture.groupRevision(group) != 1 || fixture.storedDefault(group) != "" {
		t.Fatalf("rejected requests left %d records (was %d), group revision %d, stored default %q",
			fixture.operationCount(), operationsBefore, fixture.groupRevision(group), fixture.storedDefault(group))
	}

	// 设成已存的值也是 no_changes；设定之后的版本号才是新的预期版本。
	fixture.mustSetDefault(group, 1, hires.releaseID)
	_, err := fixture.setDefault(group, 2, hires.releaseID)
	requireProblem(t, err, 400, "no_changes")
	_, err = fixture.setDefault(group, 1, original.releaseID)
	requireProblem(t, err, 409, "revision_conflict")
}

// 钉住自动选出的版本是有效变化，之后更早的版本归入也不会把它挤下去；清除一个失效中的默认版本同样是有效变化。
func TestPostgreSQLPinningTheAutomaticVersionAndClearingADormantDefault(t *testing.T) {
	fixture := newCorrectionFixture(t)
	original := fixture.versionOf("Alpha", 2000)
	hires := fixture.versionOf("Alpha Hi-Res", 2020)
	earlier := fixture.versionOf("Alpha First Pressing", 1990)
	group := original.groupID
	fixture.mustChangeGroup(hires.releaseID, 1, GroupJoin, group)

	// 钉住原版：专辑的显示没有变（它本来就是自动选出的），但状态变了。
	fixture.requireDefault(fixture.requireOrder(group, original.releaseID, hires.releaseID), "")
	fixture.mustSetDefault(group, 1, original.releaseID)
	fixture.requireDefault(fixture.requireOrder(group, original.releaseID, hires.releaseID), original.releaseID)
	// 更早的版本归入：没有钉住时它会成为代表版本，钉住之后原版仍在最前。
	fixture.mustChangeGroup(earlier.releaseID, 1, GroupJoin, group)
	fixture.requireOrder(group, original.releaseID, earlier.releaseID, hires.releaseID)

	// 默认版本失效（曲目缺失）后清除它：清掉之后它回来也不再生效。
	fixture.setVisible(original, false)
	fixture.requireOrder(group, earlier.releaseID, hires.releaseID)
	fixture.mustSetDefault(group, 2, "")
	if fixture.storedDefault(group) != "" {
		t.Fatalf("clearing a dormant default left %q stored", fixture.storedDefault(group))
	}
	fixture.setVisible(original, true)
	fixture.requireDefault(fixture.requireOrder(group, earlier.releaseID, original.releaseID, hires.releaseID), "")
}

// 撤销：恢复前一个状态（自动选择或某个版本），前态现在失效也照样恢复；之后又有调整、专辑没有可见成员时被拒绝。
func TestPostgreSQLRevertGroupDefault(t *testing.T) {
	fixture := newCorrectionFixture(t)
	ctx := context.Background()
	kind := GroupDefaultOperations()
	original := fixture.versionOf("Alpha", 2000)
	hires := fixture.versionOf("Alpha Hi-Res", 2020)
	group := original.groupID
	fixture.mustChangeGroup(hires.releaseID, 1, GroupJoin, group)

	// 撤销设定：回到自动选择；撤销本身写一条记录、版本号加一、响应指向专辑。
	set := fixture.mustSet(group, hires.releaseID)
	reverted := fixture.mustRevert(kind, set.OperationID)
	var response operations.RevertResult
	if err := json.Unmarshal(reverted.Response, &response); err != nil {
		t.Fatalf("decode the revert response: %v", err)
	}
	if response != (operations.RevertResult{OperationID: reverted.OperationID, RevertsOperationID: set.OperationID, TargetType: operations.TargetReleaseGroup, TargetID: group, Revision: 3}) {
		t.Fatalf("revert response = %+v", response)
	}
	if fixture.storedDefault(group) != "" || fixture.groupRevision(group) != 3 {
		t.Fatalf("after the revert: stored %q, revision %d; want automatic and revision 3", fixture.storedDefault(group), fixture.groupRevision(group))
	}
	record, err := operations.Load(ctx, fixture.database, reverted.OperationID)
	if err != nil || record.Type != operations.TypeRevert || record.TargetType != operations.TargetReleaseGroup || record.TargetID != group ||
		record.OriginalType != operations.TypeReleaseGroupDefault || record.ExpectedRevision != 2 || record.ResultRevision != 3 {
		t.Fatalf("revert record = %+v, %v", record, err)
	}
	fixture.requireDefault(fixture.requireOrder(group, original.releaseID, hires.releaseID), "")

	// 连续两次设定：只能撤销最近一次，它恢复前一个默认版本；更早的一次因为之后又有调整而被拒绝。
	first := fixture.mustSet(group, hires.releaseID)
	second := fixture.mustSet(group, original.releaseID)
	_, err = fixture.revert(kind, first.OperationID)
	requireProblem(t, err, 409, "revert_conflict")
	fixture.mustRevert(kind, second.OperationID)
	if fixture.storedDefault(group) != hires.releaseID {
		t.Fatalf("reverting the latest setting restored %q, want the previous default %s", fixture.storedDefault(group), hires.releaseID)
	}
	// 已撤销的再撤销、撤销记录本身再撤销。
	_, err = fixture.revert(kind, second.OperationID)
	requireProblem(t, err, 409, "already_reverted")
	_, err = fixture.revert(kind, fixture.operationLatest(operations.TypeRevert))
	requireProblem(t, err, 409, "not_revertible")

	// 前态已经失效也照样恢复：恢复的是意图，专辑仍按自动规则显示，版本回来时才生效。
	fixture.setVisible(hires, false)
	third := fixture.mustSet(group, original.releaseID)
	fixture.mustRevert(kind, third.OperationID)
	if fixture.storedDefault(group) != hires.releaseID {
		t.Fatalf("reverting restored %q, want the dormant previous default %s", fixture.storedDefault(group), hires.releaseID)
	}
	fixture.requireDefault(fixture.requireOrder(group, original.releaseID), "")
	fixture.setVisible(hires, true)
	fixture.requireDefault(fixture.requireOrder(group, hires.releaseID, original.releaseID), hires.releaseID)

	// 专辑没有可见成员时撤销被拒绝，且没有副作用。
	fourth := fixture.mustSet(group, original.releaseID)
	fixture.setVisible(original, false)
	fixture.setVisible(hires, false)
	records, revision := fixture.operationCount(), fixture.groupRevision(group)
	_, err = fixture.revert(kind, fourth.OperationID)
	requireProblem(t, err, 404, "not_found")
	if fixture.operationCount() != records || fixture.groupRevision(group) != revision || fixture.storedDefault(group) != original.releaseID {
		t.Fatalf("a rejected revert changed records (%d→%d), revision (%d→%d) or the default (%q)", records, fixture.operationCount(), revision, fixture.groupRevision(group), fixture.storedDefault(group))
	}
}

// mustSet 用专辑当前的版本号设定默认版本。
func (fixture *correctionFixture) mustSet(groupID, releaseID string) operations.Result {
	fixture.t.Helper()
	_, result := fixture.mustSetDefault(groupID, fixture.groupRevision(groupID), releaseID)
	return result
}

// 撤销同一条记录时用同一个幂等键重放，得到同一份响应，不再执行。
func TestPostgreSQLRevertGroupDefaultReplaysWithTheSameKey(t *testing.T) {
	fixture := newCorrectionFixture(t)
	original := fixture.versionOf("Alpha", 2000)
	hires := fixture.versionOf("Alpha Hi-Res", 2020)
	fixture.mustChangeGroup(hires.releaseID, 1, GroupJoin, original.groupID)
	set := fixture.mustSet(original.groupID, hires.releaseID)

	key := uuid.New()
	run := func() (operations.Result, error) {
		return operations.Run(context.Background(), fixture.database, operations.Spec{
			ActorID: fixture.actorID, Type: operations.TypeRevert, Key: key,
			Fingerprint: operations.Fingerprint(map[string]any{"type": operations.TypeRevert, "operation_id": set.OperationID}), RequestID: "test",
		}, operations.RevertExecutor(GroupDefaultOperations(), set.OperationID))
	}
	first, err := run()
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	records := fixture.operationCount()
	replay, err := run()
	var firstBody, replayBody map[string]any
	if err != nil || json.Unmarshal(first.Response, &firstBody) != nil || json.Unmarshal(replay.Response, &replayBody) != nil ||
		!replay.Replayed || replay.OperationID != first.OperationID || !reflect.DeepEqual(firstBody, replayBody) {
		t.Fatalf("replay = %+v, %v; want the same operation and response as %s", replay, err, first.Response)
	}
	if fixture.operationCount() != records || fixture.groupRevision(original.groupID) != 3 {
		t.Fatalf("the replay executed again: %d records (was %d), group revision %d", fixture.operationCount(), records, fixture.groupRevision(original.groupID))
	}
}

// Describe：专辑当前的标题与可见性、改动摘要（前后默认版本的快照）、能否撤销。
func TestPostgreSQLGroupDefaultDescribe(t *testing.T) {
	fixture := newCorrectionFixture(t)
	ctx := context.Background()
	kind := GroupDefaultOperations()
	original := fixture.versionOf("Alpha", 2000)
	hires := fixture.versionOf("Zulu Hi-Res", 2020)
	group := original.groupID
	fixture.mustChangeGroup(hires.releaseID, 1, GroupJoin, group)
	fixture.exec(`UPDATE releases SET edition='24 bit / 96 kHz' WHERE id=$1::uuid`, hires.releaseID)

	set := fixture.mustSet(group, hires.releaseID)
	clearOperation := fixture.mustSet(group, "")
	describe := func(ids ...string) map[string]operations.Description {
		t.Helper()
		var records []operations.Record
		for _, id := range ids {
			record, err := operations.Load(ctx, fixture.database, id)
			if err != nil {
				t.Fatalf("load record %s: %v", id, err)
			}
			records = append(records, record)
		}
		described, err := kind.Describe(ctx, fixture.database, records)
		if err != nil {
			t.Fatalf("describe: %v", err)
		}
		return described
	}
	changes := func(description operations.Description) (from, to groupDefaultState) {
		t.Helper()
		kindChanges, ok := description.Changes.(map[string]any)
		if !ok || kindChanges["kind"] != operations.TypeReleaseGroupDefault {
			t.Fatalf("changes = %#v, want a release_group_default summary", description.Changes)
		}
		return kindChanges["from"].(groupDefaultState), kindChanges["to"].(groupDefaultState)
	}

	described := describe(set.OperationID, clearOperation.OperationID)
	setDescription, clearDescription := described[set.OperationID], described[clearOperation.OperationID]
	// 专辑现在回到自动选择：代表版本是原版，标题是 Alpha；只有最近一次（清除）能撤销。
	if setDescription.Release != nil || setDescription.Album == nil || *setDescription.Album != (operations.AlbumInfo{ID: group, Title: "Alpha", Visible: true}) {
		t.Fatalf("set description = %+v; want the album Alpha, visible", setDescription)
	}
	if setDescription.Revertible || !clearDescription.Revertible {
		t.Fatalf("revertible = %v (set), %v (clear); want only the latest to be revertible", setDescription.Revertible, clearDescription.Revertible)
	}
	from, to := changes(setDescription)
	wantSnapshot := &defaultVersionSnapshot{ReleaseID: hires.releaseID, Title: "Zulu Hi-Res", Year: ptr(2020), Edition: ptrString("24 bit / 96 kHz")}
	if from.Default != nil || from.GroupTitle != "Alpha" || to.GroupTitle != "Zulu Hi-Res" || to.Default == nil || !reflect.DeepEqual(to.Default, wantSnapshot) {
		t.Fatalf("set changes = from %+v, to %+v; want automatic → %+v with the album title following the default", from, to, wantSnapshot)
	}
	clearFrom, clearTo := changes(clearDescription)
	if clearFrom.Default == nil || !reflect.DeepEqual(clearFrom.Default, wantSnapshot) || clearTo.Default != nil || clearTo.GroupTitle != "Alpha" {
		t.Fatalf("clear changes = from %+v, to %+v; want the default → automatic", clearFrom, clearTo)
	}

	// 撤销记录的前后状态是撤销之后的状态；专辑没有可见成员后：不可见、不能撤销，标题回退到某个成员的标题。
	revert := fixture.mustRevert(kind, clearOperation.OperationID)
	revertDescription := describe(revert.OperationID)[revert.OperationID]
	revertFrom, revertTo := changes(revertDescription)
	if revertFrom.Default != nil || revertTo.Default == nil || revertTo.Default.ReleaseID != hires.releaseID || revertDescription.Revertible != true {
		t.Fatalf("revert changes = from %+v, to %+v, revertible %v", revertFrom, revertTo, revertDescription.Revertible)
	}
	fixture.setVisible(original, false)
	fixture.setVisible(hires, false)
	hidden := describe(set.OperationID, revert.OperationID)
	for id, description := range hidden {
		if description.Album == nil || description.Album.Visible || description.Album.Title == "" || description.Revertible {
			t.Fatalf("description of %s with no visible members = %+v; want an invisible album that keeps a title and cannot be reverted", id, description.Album)
		}
	}
}

func ptr[T any](value T) *T { return &value }

func ptrString(value string) *string { return &value }

// 设定默认版本与归组使用同一个加锁顺序（先发行、后组）：多个操作者同时设定、清除、移出和归入，只会得到分类的失败
// （409、404、400），不会死锁（死锁会表现为 operation_failed），并且每次成功恰好写一条记录。
func TestPostgreSQLDefaultVersionAndGroupChangesNeverDeadlock(t *testing.T) {
	fixture := newCorrectionFixture(t)
	versions := []placementRelease{
		fixture.versionOf("A", 2000), fixture.versionOf("B", 2005), fixture.versionOf("C", 2010), fixture.versionOf("D", 2015),
	}
	for _, version := range versions[1:] {
		fixture.mustChangeGroup(version.releaseID, 1, GroupJoin, versions[0].groupID)
	}
	groupIDs := fixture.groupIDs()
	baseline := fixture.operationCount()

	readInt := func(query string, id string) int64 {
		var value int64
		_ = fixture.database.QueryRow(query, id).Scan(&value)
		return value
	}
	var successes atomic.Int64
	var waitGroup sync.WaitGroup
	for worker := 0; worker < 6; worker++ {
		waitGroup.Add(1)
		go func(seed int64) {
			defer waitGroup.Done()
			random := rand.New(rand.NewSource(seed))
			for round := 0; round < 40; round++ {
				release := versions[random.Intn(len(versions))]
				group := groupIDs[random.Intn(len(groupIDs))]
				var err error
				switch random.Intn(4) {
				case 0:
					_, err = fixture.setDefaultQuiet(group, readInt(`SELECT edit_revision FROM release_groups WHERE id=$1::uuid`, group), release.releaseID)
				case 1:
					_, err = fixture.setDefaultQuiet(group, readInt(`SELECT edit_revision FROM release_groups WHERE id=$1::uuid`, group), "")
				case 2:
					_, err = fixture.changeGroupQuiet(release.releaseID, readInt(`SELECT edit_revision FROM releases WHERE id=$1::uuid`, release.releaseID), GroupDetach, "")
				default:
					_, err = fixture.changeGroupQuiet(release.releaseID, readInt(`SELECT edit_revision FROM releases WHERE id=$1::uuid`, release.releaseID), GroupJoin, group)
				}
				switch {
				case err == nil:
					successes.Add(1)
				default:
					if status, code, _ := operations.Problem(err); status != 400 && status != 404 && status != 409 {
						t.Errorf("concurrent operation failed with status %d (%s): %v", status, code, err)
						return
					}
				}
			}
		}(int64(worker + 1))
	}
	waitGroup.Wait()

	if records := int64(fixture.operationCount() - baseline); records != successes.Load() || successes.Load() == 0 {
		t.Fatalf("%d successful operations wrote %d records", successes.Load(), records)
	}
	var orphans int
	_ = fixture.database.QueryRow(`SELECT COUNT(*) FROM releases WHERE group_id IS NULL`).Scan(&orphans)
	if orphans != 0 {
		t.Fatalf("%d releases lost their group", orphans)
	}
}

// setDefaultQuiet 与 changeGroupQuiet 不调用 t.Fatalf，可以在其他 goroutine 里使用。
func (fixture *correctionFixture) setDefaultQuiet(groupID string, expectedRevision int64, releaseID string) (operations.Result, error) {
	request := GroupDefaultRequest{GroupID: groupID, ExpectedRevision: expectedRevision, ReleaseID: releaseID}
	return operations.Run(context.Background(), fixture.database, operations.Spec{
		ActorID: fixture.actorID, Type: operations.TypeReleaseGroupDefault, Key: uuid.New(),
		Fingerprint: request.Fingerprint(), RequestID: "test",
	}, GroupDefaultExecutor(request))
}

func (fixture *correctionFixture) changeGroupQuiet(releaseID string, expectedRevision int64, action, groupID string) (operations.Result, error) {
	request := GroupChangeRequest{ReleaseID: releaseID, ExpectedRevision: expectedRevision, Action: action, GroupID: groupID}
	return operations.Run(context.Background(), fixture.database, operations.Spec{
		ActorID: fixture.actorID, Type: operations.TypeReleaseGroupChange, Key: uuid.New(),
		Fingerprint: request.Fingerprint(), RequestID: "test",
	}, GroupChangeExecutor(request))
}

// 同一个版本上的设定与移出被测试行锁及它阻塞的 CATP 写锁挡住：放开后按先后完成，结果与顺序一致；
// 无论谁先，专辑最终都不再把这个版本当作生效中的默认版本。
func TestPostgreSQLSetDefaultAndDetachQueueOnTheSameRelease(t *testing.T) {
	fixture := newCorrectionFixture(t)
	original := fixture.versionOf("Alpha", 2000)
	hires := fixture.versionOf("Alpha Hi-Res", 2020)
	group := original.groupID
	fixture.mustChangeGroup(hires.releaseID, 1, GroupJoin, group)
	hiresRevision := fixture.revisionOf(hires.releaseID)

	holder, err := fixture.database.Begin()
	if err != nil {
		t.Fatalf("begin the lock holder: %v", err)
	}
	t.Cleanup(func() { _ = holder.Rollback() })
	var holderPID int
	if err := holder.QueryRow(`SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
		t.Fatal(err)
	}
	if _, err := holder.Exec(`SELECT id FROM releases WHERE id=$1::uuid FOR UPDATE`, hires.releaseID); err != nil {
		t.Fatalf("lock the release: %v", err)
	}
	type outcome struct {
		name string
		err  error
	}
	outcomes := make(chan outcome, 2)
	go func() {
		_, err := fixture.setDefaultQuiet(group, 1, hires.releaseID)
		outcomes <- outcome{"set default", err}
	}()
	go func() {
		_, err := fixture.changeGroupQuiet(hires.releaseID, hiresRevision, GroupDetach, "")
		outcomes <- outcome{"detach", err}
	}()
	drained := false
	defer func() {
		_ = holder.Rollback()
		if !drained {
			// 失败时也先释放测试锁并等待业务事务结束，不能让 schema 清理与它们交叉。
			for range 2 {
				select {
				case <-outcomes:
				case <-time.After(5 * time.Second):
					t.Error("并发治理操作清理超时")
					return
				}
			}
		}
	}()
	waitForLockWaiters(t, fixture.database, holderPID, 2)
	if err := holder.Rollback(); err != nil {
		t.Fatalf("release the lock holder: %v", err)
	}
	failures := map[string]error{}
	for range 2 {
		result := <-outcomes
		failures[result.name] = result.err
	}
	drained = true
	// 设定先执行：两个都成功（设定不动版本自己的版本号）；移出先执行：设定发现版本已不在这张专辑里。
	if failures["detach"] != nil {
		t.Fatalf("detach failed: %v", failures["detach"])
	}
	if err := failures["set default"]; err != nil {
		requireProblem(t, err, 409, "not_in_group")
	}
	fixture.requireDefault(fixture.requireOrder(group, original.releaseID), "")
}

// waitForLockWaiters 沿测试持锁者的真实阻塞链计数，包含后续等待 CATP 的请求；不误数其他 schema 的测试。
func waitForLockWaiters(t *testing.T, database *sql.DB, holderPID, count int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := database.QueryRow(`WITH RECURSIVE blocked(pid) AS (
            SELECT pid FROM pg_stat_activity WHERE datname=current_database() AND $1=ANY(pg_blocking_pids(pid))
            UNION
            SELECT activity.pid FROM pg_stat_activity activity JOIN blocked b ON b.pid=ANY(pg_blocking_pids(activity.pid))
            WHERE activity.datname=current_database()
        ) SELECT count(*) FROM blocked`, holderPID).Scan(&waiting); err != nil {
			t.Fatalf("count lock waiters: %v", err)
		}
		if waiting >= count {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected %d sessions in the test lock chain, got %d", count, waiting)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
