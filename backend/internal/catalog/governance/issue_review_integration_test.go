package governance

import (
	"context"
	"database/sql"
	"encoding/json"
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// 核对与取消核对的受控流程（operations.Run + IssueReviewExecutor），撤销与列表展示（IssueReviewOperations）。

// issueBasis 返回一类问题现在的依据令牌（与读取接口给页面的相同）；问题不存在时为空。
func (fixture *correctionFixture) issueBasis(releaseID, issue string) string {
	fixture.t.Helper()
	basis, exists, err := loadIssueBasis(context.Background(), fixture.database, releaseID, issue)
	if err != nil {
		fixture.t.Fatalf("load the %s basis: %v", issue, err)
	}
	if !exists {
		return ""
	}
	return issueBasisToken(basis)
}

func (fixture *correctionFixture) reviewQuiet(releaseID string, expectedRevision int64, issue, action, basis string) (operations.Result, error) {
	request := IssueReviewRequest{ReleaseID: releaseID, ExpectedRevision: expectedRevision, Issue: issue, Action: action, Basis: basis}
	return operations.Run(context.Background(), fixture.database, operations.Spec{
		ActorID: fixture.actorID, Type: operations.TypeReleaseIssueReview, Key: uuid.New(),
		Fingerprint: request.Fingerprint(), RequestID: "test",
	}, IssueReviewExecutor(request))
}

// mustReview 用发行当前的版本号与问题现在的依据核对（action 为 unreview 时取消核对），核对响应。
func (fixture *correctionFixture) mustReview(releaseID, issue, action string) operations.Result {
	fixture.t.Helper()
	revision, basis := fixture.revisionOf(releaseID), ""
	if action == IssueReview {
		basis = fixture.issueBasis(releaseID, issue)
	}
	result, err := fixture.reviewQuiet(releaseID, revision, issue, action, basis)
	if err != nil {
		fixture.t.Fatalf("%s %s: %v", action, issue, err)
	}
	var decoded IssueReviewResult
	if err := json.Unmarshal(result.Response, &decoded); err != nil {
		fixture.t.Fatalf("decode the review result: %v", err)
	}
	want := IssueReviewResult{ReleaseID: releaseID, Issue: issue, Reviewed: action == IssueReview, Revision: revision + 1, OperationID: result.OperationID}
	if decoded != want {
		fixture.t.Fatalf("review response = %+v, want %+v", decoded, want)
	}
	return result
}

// storedReview 读取核对行：依据的文本与设下它的操作；没有核对行时都为空。
func (fixture *correctionFixture) storedReview(releaseID, issue string) (basis, operationID string) {
	fixture.t.Helper()
	snapshot, err := loadIssueReviewQuiet(fixture, releaseID, issue)
	if err != nil {
		fixture.t.Fatalf("read the %s review: %v", issue, err)
	}
	if snapshot == nil {
		return "", ""
	}
	return string(snapshot.Basis), snapshot.OperationID
}

func loadIssueReviewQuiet(fixture *correctionFixture, releaseID, issue string) (*issueReviewSnapshot, error) {
	transaction, err := fixture.database.Begin()
	if err != nil {
		return nil, err
	}
	defer transaction.Rollback()
	return loadIssueReview(context.Background(), transaction, releaseID, issue)
}

// issueRelease 是带三类可以核对的问题的可见发行：来源类型与介质类型拿不准、同一目录的归组冲突、两首缺失曲目。
func (fixture *correctionFixture) issueRelease(title string) (placementRelease, []string) {
	fixture.t.Helper()
	release := fixture.visibleRelease(title)
	fixture.uncertainField(release.releaseID, "source_type", "CD", []string{"CD", "SACD"})
	fixture.uncertainField(release.releaseID, "media_type", "CD", []string{"CD", "Vinyl"})
	fixture.groupingConflict(release.releaseID, `["dir/01.flac","dir/02.flac"]`)
	missing := []string{fixture.track(release, "缺失一", "missing", 10.0), fixture.track(release, "缺失二", "missing", 10.0)}
	return release, missing
}

// 四类核对各自消除计数、写一条记录、版本号加一；取消核对重新计入。核对行记住依据与设下它的操作。
func TestPostgreSQLIssueReviewStateTransitions(t *testing.T) {
	fixture := newCorrectionFixture(t)
	release, _ := fixture.issueRelease("有问题的版本")
	counts := func() issueCounts { return fixture.issueCounts(release.releaseID) }
	if got := counts(); got != (issueCounts{openFields: 2, openGrouping: 1, openMissing: 2}) {
		t.Fatalf("before any review: %+v", got)
	}
	records := fixture.operationCount()

	for index, testCase := range []struct {
		issue string
		want  issueCounts
	}{
		{IssueSourceType, issueCounts{openFields: 1, reviewedFields: 1, openGrouping: 1, openMissing: 2}},
		{IssueMediaType, issueCounts{reviewedFields: 2, openGrouping: 1, openMissing: 2}},
		{IssueGrouping, issueCounts{reviewedFields: 2, reviewedGrouping: 1, openMissing: 2}},
		{IssueMissingTracks, issueCounts{reviewedFields: 2, reviewedGrouping: 1, reviewedMissing: 2}},
	} {
		basis, _ := loadIssueBasisQuiet(t, fixture, release.releaseID, testCase.issue)
		result := fixture.mustReview(release.releaseID, testCase.issue, IssueReview)
		if got := counts(); got != testCase.want {
			t.Fatalf("after reviewing %s: %+v, want %+v", testCase.issue, got, testCase.want)
		}
		storedBasis, storedOperation := fixture.storedReview(release.releaseID, testCase.issue)
		if storedOperation != result.OperationID || storedBasis != basis {
			t.Fatalf("the %s review row = %s by %s; want the current basis %s by %s", testCase.issue, storedBasis, storedOperation, basis, result.OperationID)
		}
		record, err := operations.Load(context.Background(), fixture.database, result.OperationID)
		if err != nil || record.Type != operations.TypeReleaseIssueReview || record.TargetType != operations.TargetRelease || record.TargetID != release.releaseID ||
			record.ExpectedRevision != int64(index+1) || record.ResultRevision != int64(index+2) {
			t.Fatalf("the %s record = %+v, %v", testCase.issue, record, err)
		}
		if fixture.operationCount() != records+index+1 {
			t.Fatalf("after reviewing %s there are %d records, want %d", testCase.issue, fixture.operationCount(), records+index+1)
		}
	}

	// 取消核对：任何时候都能做，问题重新计入，核对行删除。
	fixture.mustReview(release.releaseID, IssueGrouping, IssueUnreview)
	if got := counts(); got != (issueCounts{reviewedFields: 2, openGrouping: 1, reviewedMissing: 2}) {
		t.Fatalf("after unreviewing the grouping conflict: %+v", got)
	}
	if basis, _ := fixture.storedReview(release.releaseID, IssueGrouping); basis != "" {
		t.Fatalf("the grouping review row is still there: %s", basis)
	}
	// 过期的核对也能取消，也能再次核对（覆盖过期的行，记住现在的依据）。
	fixture.uncertainField(release.releaseID, "media_type", "CD", []string{"CD", "Vinyl", "SACD"})
	if got := counts(); got.openFields != 1 || got.reviewedFields != 1 {
		t.Fatalf("after the media candidates changed: %+v, want the media type open again", got)
	}
	again := fixture.mustReview(release.releaseID, IssueMediaType, IssueReview)
	if _, operationID := fixture.storedReview(release.releaseID, IssueMediaType); operationID != again.OperationID || counts().reviewedFields != 2 {
		t.Fatalf("re-reviewing a stale review left %s, counts %+v", operationID, counts())
	}
	fixture.uncertainField(release.releaseID, "media_type", "CD", []string{"CD"})
	fixture.mustReview(release.releaseID, IssueMediaType, IssueUnreview)
	if basis, _ := fixture.storedReview(release.releaseID, IssueMediaType); basis != "" {
		t.Fatalf("unreviewing a stale review left %s", basis)
	}

	// 缺失曲目部分过期（核对之后又有曲目缺失）时可以再次核对，新的依据覆盖全部缺失曲目。
	fixture.track(release, "核对之后缺失的曲目", "missing", 10.0)
	if got := counts(); got.openMissing != 1 || got.reviewedMissing != 2 {
		t.Fatalf("after another track went missing: %+v", got)
	}
	fixture.mustReview(release.releaseID, IssueMissingTracks, IssueReview)
	if got := counts(); got.openMissing != 0 || got.reviewedMissing != 3 {
		t.Fatalf("after re-reviewing the missing tracks: %+v", got)
	}
}

func loadIssueBasisQuiet(t *testing.T, fixture *correctionFixture, releaseID, issue string) (string, bool) {
	t.Helper()
	basis, exists, err := loadIssueBasis(context.Background(), fixture.database, releaseID, issue)
	if err != nil {
		t.Fatalf("load the %s basis: %v", issue, err)
	}
	return basis, exists
}

// 被拒绝的请求都分类明确且没有副作用：没有新记录，版本号与核对行不变。
func TestPostgreSQLIssueReviewRejectsInvalidRequestsWithoutSideEffects(t *testing.T) {
	fixture := newCorrectionFixture(t)
	release, _ := fixture.issueRelease("有问题的版本")
	clean := fixture.visibleRelease("没有问题的版本")
	hidden := fixture.release("看不见的版本")
	fixture.groupingConflict(hidden.releaseID, `["hidden/01.flac"]`)
	fixture.mustReview(release.releaseID, IssueMediaType, IssueReview)
	revision, records := fixture.revisionOf(release.releaseID), fixture.operationCount()
	groupingBasis := fixture.issueBasis(release.releaseID, IssueGrouping)

	for name, testCase := range map[string]struct {
		releaseID        string
		expectedRevision int64
		issue, action    string
		basis            string
		status           int
		code             string
	}{
		"a stale revision":              {release.releaseID, revision - 1, IssueGrouping, IssueReview, groupingBasis, 409, "revision_conflict"},
		"a basis the scan has replaced": {release.releaseID, revision, IssueGrouping, IssueReview, issueBasisToken(`{"reason": "same_directory_conflict", "source_refs": []}`), 409, "issue_changed"},
		"an issue that does not exist":  {clean.releaseID, 1, IssueGrouping, IssueReview, groupingBasis, 409, "issue_changed"},
		"no missing tracks":             {clean.releaseID, 1, IssueMissingTracks, IssueReview, groupingBasis, 409, "issue_changed"},
		"an issue already reviewed":     {release.releaseID, revision, IssueMediaType, IssueReview, fixture.issueBasis(release.releaseID, IssueMediaType), 400, "no_changes"},
		"unreviewing without a review":  {release.releaseID, revision, IssueGrouping, IssueUnreview, "", 400, "no_changes"},
		"an invisible release":          {hidden.releaseID, 1, IssueGrouping, IssueReview, fixture.issueBasis(hidden.releaseID, IssueGrouping), 404, "not_found"},
		"a release that does not exist": {uuid.New(), 1, IssueGrouping, IssueReview, groupingBasis, 404, "not_found"},
	} {
		_, err := fixture.reviewQuiet(testCase.releaseID, testCase.expectedRevision, testCase.issue, testCase.action, testCase.basis)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if status, code := revertProblem(err); status != testCase.status || code != testCase.code {
			t.Errorf("%s: %d %s, want %d %s (%v)", name, status, code, testCase.status, testCase.code, err)
		}
	}
	if fixture.operationCount() != records || fixture.revisionOf(release.releaseID) != revision || fixture.revisionOf(clean.releaseID) != 1 {
		t.Fatalf("rejected requests changed records (%d→%d) or revisions", records, fixture.operationCount())
	}
	if basis, _ := fixture.storedReview(release.releaseID, IssueGrouping); basis != "" {
		t.Fatalf("a rejected request wrote a grouping review: %s", basis)
	}
	if got := fixture.issueCounts(release.releaseID); got != (issueCounts{openFields: 1, reviewedFields: 1, openGrouping: 1, openMissing: 2}) {
		t.Fatalf("counts after the rejected requests = %+v", got)
	}
}

// 依据令牌在两次读取之间不变，与 JSONB 的键顺序和空白无关；扫描原样重写依据之后，页面拿着的令牌仍然可以核对。
func TestPostgreSQLIssueBasisTokenIsStable(t *testing.T) {
	fixture := newCorrectionFixture(t)
	release, _ := fixture.issueRelease("有问题的版本")
	for _, issue := range []string{IssueGrouping, IssueMissingTracks, IssueSourceType, IssueMediaType} {
		first := fixture.issueBasis(release.releaseID, issue)
		if first == "" || fixture.issueBasis(release.releaseID, issue) != first {
			t.Fatalf("the %s basis token is not stable between reads", issue)
		}
	}
	token := fixture.issueBasis(release.releaseID, IssueGrouping)
	fixture.groupingConflict(release.releaseID, `[ "dir/01.flac",   "dir/02.flac" ]`)
	if fixture.issueBasis(release.releaseID, IssueGrouping) != token {
		t.Fatal("rewriting the same grouping evidence changed the token")
	}
	if _, err := fixture.reviewQuiet(release.releaseID, 1, IssueGrouping, IssueReview, token); err != nil {
		t.Fatalf("reviewing with the token read before an unchanged rescan: %v", err)
	}
	// 缺失曲目：同一时间相等，曲目再次出现后不等。
	missingToken := fixture.issueBasis(release.releaseID, IssueMissingTracks)
	fixture.exec(`UPDATE tracks SET observed_at = observed_at WHERE medium_id=$1::uuid`, release.mediumID)
	if fixture.issueBasis(release.releaseID, IssueMissingTracks) != missingToken {
		t.Fatal("the missing-tracks token changed without a change in the tracks")
	}
	fixture.exec(`UPDATE tracks SET observed_at = observed_at + INTERVAL '1 second' WHERE medium_id=$1::uuid AND source_status='missing'`, release.mediumID)
	if fixture.issueBasis(release.releaseID, IssueMissingTracks) == missingToken {
		t.Fatal("the missing-tracks token did not change after the tracks were seen again")
	}
}

// 撤销把核对行恢复成前态：撤销核对后问题重新计入；撤销取消核对恢复原来的核对（原来的操作，即原来的核对人），
// 恢复的核对现在过期也照样恢复；之后又有修改时 revert_conflict；撤销记录不能再撤销。
func TestPostgreSQLRevertIssueReview(t *testing.T) {
	fixture := newCorrectionFixture(t)
	kind := IssueReviewOperations()
	release, _ := fixture.issueRelease("有问题的版本")

	review := fixture.mustReview(release.releaseID, IssueGrouping, IssueReview)
	reverted := fixture.mustRevert(kind, review.OperationID)
	var response operations.RevertResult
	if err := json.Unmarshal(reverted.Response, &response); err != nil {
		t.Fatalf("decode the revert response: %v", err)
	}
	if response != (operations.RevertResult{OperationID: reverted.OperationID, RevertsOperationID: review.OperationID, TargetType: operations.TargetRelease, TargetID: release.releaseID, Revision: 3}) {
		t.Fatalf("revert response = %+v", response)
	}
	if basis, _ := fixture.storedReview(release.releaseID, IssueGrouping); basis != "" || fixture.issueCounts(release.releaseID).openGrouping != 1 {
		t.Fatalf("after reverting the review: row %q, counts %+v", basis, fixture.issueCounts(release.releaseID))
	}

	// 撤销取消核对：恢复的是原来那次核对（同一个操作 ID），依据也是原来的。
	original := fixture.mustReview(release.releaseID, IssueMissingTracks, IssueReview)
	originalBasis, _ := fixture.storedReview(release.releaseID, IssueMissingTracks)
	unreview := fixture.mustReview(release.releaseID, IssueMissingTracks, IssueUnreview)
	fixture.mustRevert(kind, unreview.OperationID)
	if basis, operationID := fixture.storedReview(release.releaseID, IssueMissingTracks); basis != originalBasis || operationID != original.OperationID {
		t.Fatalf("reverting the unreview restored %s by %s; want %s by %s", basis, operationID, originalBasis, original.OperationID)
	}
	// 之后又有修改（这里是一次修正）：更早的取消核对不能撤销；撤销记录本身也不能撤销。
	_, err := fixture.revert(kind, unreview.OperationID)
	requireProblem(t, err, 409, "already_reverted")
	_, err = fixture.revert(kind, fixture.operationLatest(operations.TypeRevert))
	requireProblem(t, err, 409, "not_revertible")
	review = fixture.mustReview(release.releaseID, IssueSourceType, IssueReview)
	fixture.mustCorrect(release.releaseID, fixture.revisionOf(release.releaseID), map[string]CorrectionChange{"genre": {Mode: catalog.CorrectionSet, Genres: []string{"古典"}}})
	_, err = fixture.revert(kind, review.OperationID)
	requireProblem(t, err, 409, "revert_conflict")

	// 恢复的核对现在过期也照样恢复：问题照样计入，核对行回来（详情据此说明“核对之后情况变了”）。
	unreview = fixture.mustReview(release.releaseID, IssueSourceType, IssueUnreview)
	fixture.uncertainField(release.releaseID, "source_type", "CD", []string{"CD", "SACD", "DVD-Audio"})
	fixture.mustRevert(kind, unreview.OperationID)
	if _, operationID := fixture.storedReview(release.releaseID, IssueSourceType); operationID != review.OperationID {
		t.Fatalf("reverting the unreview restored %q, want the stale review %s", operationID, review.OperationID)
	}
	if got := fixture.issueCounts(release.releaseID); got.openFields != 2 || got.reviewedFields != 0 {
		t.Fatalf("after restoring a stale source-type review: %+v, want the source type still open (with the media type)", got)
	}

	// 版本不可见时撤销被拒绝，且没有副作用。
	review = fixture.mustReview(release.releaseID, IssueMediaType, IssueReview)
	fixture.setVisible(release, false)
	records, revision := fixture.operationCount(), fixture.revisionOf(release.releaseID)
	_, err = fixture.revert(kind, review.OperationID)
	requireProblem(t, err, 404, "not_found")
	if fixture.operationCount() != records || fixture.revisionOf(release.releaseID) != revision {
		t.Fatalf("a rejected revert changed records (%d→%d) or the revision (%d→%d)", records, fixture.operationCount(), revision, fixture.revisionOf(release.releaseID))
	}
}

// 撤销时用同一个幂等键重放，得到同一份响应，不再执行。
func TestPostgreSQLRevertIssueReviewReplaysWithTheSameKey(t *testing.T) {
	fixture := newCorrectionFixture(t)
	release, _ := fixture.issueRelease("有问题的版本")
	review := fixture.mustReview(release.releaseID, IssueGrouping, IssueReview)
	key := uuid.New()
	run := func() (operations.Result, error) {
		return operations.Run(context.Background(), fixture.database, operations.Spec{
			ActorID: fixture.actorID, Type: operations.TypeRevert, Key: key,
			Fingerprint: operations.Fingerprint(map[string]any{"type": operations.TypeRevert, "operation_id": review.OperationID}), RequestID: "test",
		}, operations.RevertExecutor(IssueReviewOperations(), review.OperationID))
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
	if fixture.operationCount() != records || fixture.revisionOf(release.releaseID) != 3 {
		t.Fatalf("the replay executed again: %d records (was %d), revision %d", fixture.operationCount(), records, fixture.revisionOf(release.releaseID))
	}
}

// Describe：发行当前的标题与可见性、改动摘要（问题、前后是否有核对、缺失曲目的曲目数）、能否撤销。
func TestPostgreSQLIssueReviewDescribe(t *testing.T) {
	fixture := newCorrectionFixture(t)
	ctx := context.Background()
	kind := IssueReviewOperations()
	release, _ := fixture.issueRelease("有问题的版本")
	missing := fixture.mustReview(release.releaseID, IssueMissingTracks, IssueReview)
	grouping := fixture.mustReview(release.releaseID, IssueGrouping, IssueReview)
	unreview := fixture.mustReview(release.releaseID, IssueGrouping, IssueUnreview)
	revert := fixture.mustRevert(kind, unreview.OperationID)

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
	changes := func(description operations.Description) map[string]any {
		t.Helper()
		summary, ok := description.Changes.(map[string]any)
		if !ok || summary["kind"] != operations.TypeReleaseIssueReview {
			t.Fatalf("changes = %#v, want a release_issue_review summary", description.Changes)
		}
		return summary
	}
	two := 2
	described := describe(missing.OperationID, grouping.OperationID, unreview.OperationID, revert.OperationID)
	for id, want := range map[string]struct {
		issue      string
		from, to   issueReviewChange
		revertible bool
	}{
		missing.OperationID:  {IssueMissingTracks, issueReviewChange{}, issueReviewChange{Reviewed: true, TrackCount: &two}, false},
		grouping.OperationID: {IssueGrouping, issueReviewChange{}, issueReviewChange{Reviewed: true}, false},
		unreview.OperationID: {IssueGrouping, issueReviewChange{Reviewed: true}, issueReviewChange{}, false},
		revert.OperationID:   {IssueGrouping, issueReviewChange{}, issueReviewChange{Reviewed: true}, true},
	} {
		description := described[id]
		summary := changes(description)
		if summary["issue"] != want.issue || !reflect.DeepEqual(summary["from"], want.from) || !reflect.DeepEqual(summary["to"], want.to) {
			t.Errorf("changes of %s = %#v, want %s %+v → %+v", id, summary, want.issue, want.from, want.to)
		}
		if description.Release == nil || *description.Release != (operations.ReleaseInfo{ID: release.releaseID, Title: "有问题的版本", GroupID: release.groupID, Visible: true}) {
			t.Errorf("release of %s = %+v", id, description.Release)
		}
		if description.Revertible != want.revertible {
			t.Errorf("revertible of %s = %v, want %v (only the latest change can be reverted)", id, description.Revertible, want.revertible)
		}
	}
	// 版本不可见之后：都不能撤销，仍然有名字。
	fixture.setVisible(release, false)
	for id, description := range describe(revert.OperationID) {
		if description.Release == nil || description.Release.Visible || description.Release.Title == "" || description.Revertible {
			t.Fatalf("description of %s for an invisible release = %+v", id, description.Release)
		}
	}
}

// 核对、取消核对、修正、归组与它们的撤销随机交错，不死锁：所有失败都是分类的 400/404/409，成功数等于新增记录数。
// 撤销取消核对会写回引用更早操作的核对行，与撤销那条更早的操作同时进行时也不互相等待成环。
func TestPostgreSQLIssueReviewsAndOtherChangesNeverDeadlock(t *testing.T) {
	fixture := newCorrectionFixture(t)
	releases := []placementRelease{}
	for _, title := range []string{"A", "B", "C"} {
		release, _ := fixture.issueRelease(title)
		releases = append(releases, release)
	}
	baseline := fixture.operationCount()
	issues := []string{IssueGrouping, IssueMissingTracks, IssueSourceType, IssueMediaType}
	revision := func(releaseID string) int64 {
		var value int64
		_ = fixture.database.QueryRow(`SELECT edit_revision FROM releases WHERE id=$1::uuid`, releaseID).Scan(&value)
		return value
	}
	basisOf := func(releaseID, issue string) string {
		basis, exists, err := loadIssueBasis(context.Background(), fixture.database, releaseID, issue)
		if err != nil || !exists {
			return ""
		}
		return issueBasisToken(basis)
	}
	anyRecord := func(operationType string) string {
		var id string
		_ = fixture.database.QueryRow(`SELECT id::text FROM operations WHERE operation_type=$1 ORDER BY random() LIMIT 1`, operationType).Scan(&id)
		return id
	}
	var successes atomic.Int64
	var waitGroup sync.WaitGroup
	for worker := 0; worker < 6; worker++ {
		waitGroup.Add(1)
		go func(seed int64) {
			defer waitGroup.Done()
			random := rand.New(rand.NewSource(seed))
			for round := 0; round < 40; round++ {
				release := releases[random.Intn(len(releases))]
				issue := issues[random.Intn(len(issues))]
				var err error
				switch random.Intn(5) {
				case 0:
					_, err = fixture.reviewQuiet(release.releaseID, revision(release.releaseID), issue, IssueReview, basisOf(release.releaseID, issue))
				case 1:
					_, err = fixture.reviewQuiet(release.releaseID, revision(release.releaseID), issue, IssueUnreview, "")
				case 2:
					id := anyRecord(operations.TypeReleaseIssueReview)
					if id == "" {
						continue
					}
					_, err = fixture.revertQuiet(IssueReviewOperations(), id)
				case 3:
					request := CorrectionRequest{ReleaseID: release.releaseID, ExpectedRevision: revision(release.releaseID), Changes: map[string]CorrectionChange{"genre": {Mode: catalog.CorrectionSet, Genres: []string{"古典" + string(rune('A'+random.Intn(3)))}}}}
					_, err = operations.Run(context.Background(), fixture.database, operations.Spec{
						ActorID: fixture.actorID, Type: operations.TypeReleaseCorrection, Key: uuid.New(), Fingerprint: request.Fingerprint(), RequestID: "test",
					}, CorrectionExecutor(request))
				default:
					target := releases[random.Intn(len(releases))]
					_, err = fixture.changeGroupQuiet(release.releaseID, revision(release.releaseID), GroupJoin, fixture.groupOfQuiet(target.releaseID))
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
}

// 撤销取消核对与撤销原来那次核对同时进行：先撤销取消核对的那个要写回引用原来核对的行。两者被同一把发行行锁挡住后放开，
// 都得到分类的结果（成功或 409），不死锁。
func TestPostgreSQLRevertingAnUnreviewAndTheOriginalReviewDoNotDeadlock(t *testing.T) {
	for attempt := 0; attempt < 5; attempt++ {
		fixture := newCorrectionFixture(t)
		release, _ := fixture.issueRelease("有问题的版本")
		review := fixture.mustReview(release.releaseID, IssueGrouping, IssueReview)
		unreview := fixture.mustReview(release.releaseID, IssueGrouping, IssueUnreview)

		holder, err := fixture.database.Begin()
		if err != nil {
			t.Fatalf("begin the lock holder: %v", err)
		}
		if _, err := holder.Exec(`SELECT id FROM releases WHERE id=$1::uuid FOR UPDATE`, release.releaseID); err != nil {
			t.Fatalf("lock the release: %v", err)
		}
		results := make(chan error, 2)
		go func() { _, err := fixture.revertQuiet(IssueReviewOperations(), unreview.OperationID); results <- err }()
		go func() { _, err := fixture.revertQuiet(IssueReviewOperations(), review.OperationID); results <- err }()
		waitForRowLockWaiters(t, fixture.database, 2)
		if err := holder.Rollback(); err != nil {
			t.Fatalf("release the lock holder: %v", err)
		}
		for range 2 {
			if err := <-results; err != nil {
				if status, code, _ := operations.Problem(err); status != 409 {
					t.Fatalf("attempt %d: a revert failed with %d %s: %v (a deadlock would be 500)", attempt, status, code, err)
				}
			}
		}
	}
}

func (fixture *correctionFixture) revertQuiet(kind operations.Kind, originalID string) (operations.Result, error) {
	return operations.Run(context.Background(), fixture.database, operations.Spec{
		ActorID: fixture.actorID, Type: operations.TypeRevert, Key: uuid.New(),
		Fingerprint: operations.Fingerprint(map[string]any{"type": operations.TypeRevert, "operation_id": originalID}), RequestID: "test",
	}, operations.RevertExecutor(kind, originalID))
}

func (fixture *correctionFixture) groupOfQuiet(releaseID string) string {
	var groupID string
	_ = fixture.database.QueryRow(`SELECT group_id::text FROM releases WHERE id=$1::uuid`, releaseID).Scan(&groupID)
	return groupID
}

// waitForRowLockWaiters 与 waitForLockWaiters 相同，但也数等 FOR KEY SHARE 的会话：撤销取消核对先给原来的核对记录加这把锁。
func waitForRowLockWaiters(t *testing.T, database *sql.DB, count int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := database.QueryRow(`SELECT COUNT(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock'
			AND pid<>pg_backend_pid() AND (query ILIKE '%FOR UPDATE%' OR query ILIKE '%FOR KEY SHARE%')`).Scan(&waiting); err != nil {
			t.Fatalf("count lock waiters: %v", err)
		}
		if waiting >= count {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected %d sessions waiting on row locks, got %d", count, waiting)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
