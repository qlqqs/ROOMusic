package governance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

// 认领的撤销与展示（阶段 M4 第三项第 3 步）。

// claimTrace 是两个发行与涉及的专辑的人工状态的完整抓取（不含版本号）：撤销之后应当与认领之前逐项相同，
// 包括修正与核对行引用的操作记录（核对人仍是原来的人）。
type claimTrace struct {
	Corrections map[string]map[string]string // 发行 -> 字段 -> 意图与操作
	Reviews     map[string]map[string]string // 发行 -> 问题 -> 依据与操作
	Groups      map[string]string            // 发行 -> 所在的组
	Defaults    map[string]string            // 专辑 -> 已存的默认版本
}

func (scenario *claimScenario) trace(extraGroups ...string) claimTrace {
	scenario.fixture.t.Helper()
	database := scenario.fixture.database
	trace := claimTrace{Corrections: map[string]map[string]string{}, Reviews: map[string]map[string]string{}, Groups: map[string]string{}, Defaults: map[string]string{}}
	groups := append([]string{scenario.album, scenario.target.groupID, scenario.source.groupID}, extraGroups...)
	for name, releaseID := range map[string]string{"source": scenario.source.releaseID, "target": scenario.target.releaseID} {
		trace.Corrections[name], trace.Reviews[name] = map[string]string{}, map[string]string{}
		rows, err := database.Query(`SELECT field, mode, COALESCE(text_value, year_value::text, ''), operation_id::text FROM release_corrections WHERE release_id=$1::uuid`, releaseID)
		if err != nil {
			scenario.fixture.t.Fatalf("trace corrections: %v", err)
		}
		for rows.Next() {
			var field, mode, value, operationID string
			if err := rows.Scan(&field, &mode, &value, &operationID); err != nil {
				scenario.fixture.t.Fatal(err)
			}
			trace.Corrections[name][field] = mode + ":" + value + "@" + operationID
		}
		rows.Close()
		reviewRows, err := database.Query(`SELECT issue, basis::text, operation_id::text FROM release_issue_reviews WHERE release_id=$1::uuid`, releaseID)
		if err != nil {
			scenario.fixture.t.Fatalf("trace reviews: %v", err)
		}
		for reviewRows.Next() {
			var issue, basis, operationID string
			if err := reviewRows.Scan(&issue, &basis, &operationID); err != nil {
				scenario.fixture.t.Fatal(err)
			}
			trace.Reviews[name][issue] = basis + "@" + operationID
		}
		reviewRows.Close()
		trace.Groups[name] = scenario.fixture.groupOf(releaseID)
	}
	for _, group := range groups {
		var stored sql.NullString
		if err := database.QueryRow(`SELECT default_release_id::text FROM release_groups WHERE id=$1::uuid`, group).Scan(&stored); err != nil {
			scenario.fixture.t.Fatalf("trace group %s: %v", group, err)
		}
		trace.Defaults[group] = stored.String
	}
	return trace
}

func (scenario *claimScenario) revertClaim(operationID string) (operations.Result, error) {
	return scenario.fixture.revert(ClaimOperations(), operationID)
}

func (scenario *claimScenario) mustRevertClaim(t *testing.T, operationID string) operations.Result {
	t.Helper()
	result, err := scenario.revertClaim(operationID)
	if err != nil {
		t.Fatalf("revert the claim: %v", err)
	}
	return result
}

// 撤销逐项恢复认领之前的状态：修正、核对（连同原来的操作，核对人不变）、两个发行所在的专辑、默认版本；
// 两个发行的版本号各加一，旧版本重新待认领；撤销记录的目标是新版本，状态里带来源，所以按来源也找得到。
func TestPostgreSQLRevertClaimRestoresEverything(t *testing.T) {
	scenario := newClaimScenario(t)
	fixture := scenario.fixture
	before := scenario.trace()
	_, claim := scenario.mustClaim(t)
	if reflect.DeepEqual(scenario.trace(), before) {
		t.Fatal("the claim changed nothing")
	}
	targetRevision, sourceRevision, albumRevision := fixture.revisionOf(scenario.target.releaseID), fixture.revisionOf(scenario.source.releaseID), fixture.groupRevision(scenario.album)
	// 认领改变了新版本所在的专辑（单版本专辑归入了旧版本的专辑）：记录的摘要写明认领之前的组，从记录撤销时页面据此回去。
	claimRecord, err := operations.Load(context.Background(), fixture.database, claim.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	described, err := ClaimOperations().Describe(context.Background(), fixture.database, []operations.Record{claimRecord})
	if err != nil {
		t.Fatal(err)
	}
	if changes, ok := described[claim.OperationID].Changes.(claimChanges); !ok || changes.PreviousGroupID != scenario.target.groupID || changes.PreviousGroupID == scenario.album {
		t.Errorf("described changes = %+v, want the previous group %s (not the old album %s)", described[claim.OperationID].Changes, scenario.target.groupID, scenario.album)
	}

	revert := scenario.mustRevertClaim(t, claim.OperationID)
	if after := scenario.trace(); !reflect.DeepEqual(after, before) {
		t.Fatalf("the state after the revert differs from the state before the claim:\nbefore %+v\nafter  %+v", before, after)
	}
	if got := fixture.revisionOf(scenario.target.releaseID); got != targetRevision+1 {
		t.Errorf("target revision = %d, want %d", got, targetRevision+1)
	}
	if got := fixture.revisionOf(scenario.source.releaseID); got != sourceRevision+1 {
		t.Errorf("source revision = %d, want %d", got, sourceRevision+1)
	}
	if got := fixture.groupRevision(scenario.album); got != albumRevision+1 {
		t.Errorf("album revision = %d, want %d (its default version was restored)", got, albumRevision+1)
	}
	if !scenario.claimable(scenario.source.releaseID) {
		t.Error("the old release is not claimable again after the revert")
	}

	record, err := operations.Load(context.Background(), fixture.database, revert.OperationID)
	if err != nil || record.Type != operations.TypeRevert || record.RevertsID != claim.OperationID || record.OriginalType != operations.TypeReleaseClaim ||
		record.TargetType != operations.TargetRelease || record.TargetID != scenario.target.releaseID || record.ExpectedRevision != targetRevision || record.ResultRevision != targetRevision+1 {
		t.Fatalf("the revert record = %+v, %v", record, err)
	}
	var state claimRecordState
	if err := json.Unmarshal(record.After, &state); err != nil || state.Source.ReleaseID != scenario.source.releaseID {
		t.Errorf("the revert record does not name its source release: %+v, %v", state.Source.ReleaseID, err)
	}
	listed, err := operations.List(context.Background(), fixture.database, operations.ListParams{Sources: []string{scenario.source.releaseID}, Page: 1, PageSize: 10})
	if err != nil || listed.Total != 2 {
		t.Errorf("records with the old release as the source = %d (%v), want the claim and its revert", listed.Total, err)
	}

	// 撤销之后可以再认领，回到同一个结果。
	scenario.mustClaim(t)
	if scenario.claimable(scenario.source.releaseID) {
		t.Error("the old release is still claimable after claiming again")
	}
}

// K3 的情形：新版本自己的修正、核对和专辑被保留；撤销之后它们原样还在，旧版本被跳过的项也回到旧版本上。
func TestPostgreSQLRevertClaimRestoresWhatWasSkipped(t *testing.T) {
	scenario := newClaimScenario(t)
	fixture := scenario.fixture
	other := fixture.versionOf("新版本的同专辑版本", 2015)
	fixture.mustChangeGroup(scenario.target.releaseID, fixture.revisionOf(scenario.target.releaseID), GroupJoin, other.groupID)
	fixture.uncertainField(scenario.target.releaseID, "source_type", "CD", []string{"CD", "SACD"})
	fixture.mustCorrect(scenario.target.releaseID, fixture.revisionOf(scenario.target.releaseID), map[string]CorrectionChange{"title": {Mode: catalog.CorrectionSet, Text: "新版本自己的标题"}})
	fixture.mustReview(scenario.target.releaseID, IssueSourceType, IssueReview)
	before := scenario.trace(other.groupID)

	_, claim := scenario.mustClaim(t)
	scenario.mustRevertClaim(t, claim.OperationID)
	if after := scenario.trace(other.groupID); !reflect.DeepEqual(after, before) {
		t.Fatalf("the state after the revert differs from the state before the claim:\nbefore %+v\nafter  %+v", before, after)
	}
}

// 之后任何一方又被修改过就拒绝，不留副作用：新版本被修改、旧版本被改（直接写库，受治理写入对不可见的版本是 404）、
// 专辑的默认版本被别人改。
func TestPostgreSQLRevertClaimRejectsLaterChanges(t *testing.T) {
	for name, change := range map[string]func(*claimScenario){
		"the new release was edited": func(scenario *claimScenario) {
			scenario.fixture.mustCorrect(scenario.target.releaseID, scenario.fixture.revisionOf(scenario.target.releaseID), map[string]CorrectionChange{"label": {Mode: catalog.CorrectionSet, Text: "后来改的"}})
		},
		"the old release was edited": func(scenario *claimScenario) {
			scenario.fixture.exec(`UPDATE releases SET edit_revision = edit_revision + 1 WHERE id=$1::uuid`, scenario.source.releaseID)
		},
		"the album's default version was changed": func(scenario *claimScenario) {
			scenario.fixture.mustSetDefault(scenario.album, scenario.fixture.groupRevision(scenario.album), scenario.sibling.releaseID)
		},
	} {
		t.Run(name, func(t *testing.T) {
			scenario := newClaimScenario(t)
			_, claim := scenario.mustClaim(t)
			change(scenario)
			stateBefore, operationsBefore := scenario.trace(), scenario.fixture.operationCount()
			_, err := scenario.revertClaim(claim.OperationID)
			var failure *operations.Failure
			if !errors.As(err, &failure) || failure.Code != "revert_conflict" {
				t.Fatalf("revert error = %v, want revert_conflict", err)
			}
			if !reflect.DeepEqual(scenario.trace(), stateBefore) || scenario.fixture.operationCount() != operationsBefore {
				t.Error("a refused revert changed something")
			}
		})
	}
}

// 撤销本身不能再撤销，已撤销的认领不能重复撤销。
func TestPostgreSQLRevertClaimFollowsTheCommonRules(t *testing.T) {
	scenario := newClaimScenario(t)
	_, claim := scenario.mustClaim(t)
	revert := scenario.mustRevertClaim(t, claim.OperationID)
	for name, original := range map[string]string{"the revert itself": revert.OperationID, "the claim again": claim.OperationID} {
		_, err := scenario.revertClaim(original)
		var failure *operations.Failure
		want := map[string]string{"the revert itself": "not_revertible", "the claim again": "already_reverted"}[name]
		if !errors.As(err, &failure) || failure.Code != want {
			t.Errorf("reverting %s: %v, want %s", name, err, want)
		}
	}
}

// 撤销不要求新版本可见（K9）：认领之后新版本又被整张替换、变得不可见，撤销仍能把人工状态还给旧版本。
func TestPostgreSQLRevertClaimWorksWhenTheNewReleaseBecameInvisible(t *testing.T) {
	scenario := newClaimScenario(t)
	before := scenario.trace()
	_, claim := scenario.mustClaim(t)
	scenario.fixture.exec(`UPDATE tracks SET source_status='missing' WHERE medium_id=$1::uuid`, scenario.target.mediumID)
	// 其他类型在目标不可见时是 404；认领的撤销仍然可以。
	scenario.mustRevertClaim(t, claim.OperationID)
	if after := scenario.trace(); !reflect.DeepEqual(after, before) {
		t.Fatalf("the state after the revert differs from the state before the claim:\nbefore %+v\nafter  %+v", before, after)
	}
}

// 展示：改动摘要写明来源、迁移了什么、保留了什么；能否撤销只看版本号，不要求可见；撤销记录只带来源。
func TestPostgreSQLClaimDescribe(t *testing.T) {
	scenario := newClaimScenario(t)
	fixture := scenario.fixture
	other := fixture.versionOf("新版本的同专辑版本", 2015)
	fixture.mustChangeGroup(scenario.target.releaseID, fixture.revisionOf(scenario.target.releaseID), GroupJoin, other.groupID)
	fixture.mustCorrect(scenario.target.releaseID, fixture.revisionOf(scenario.target.releaseID), map[string]CorrectionChange{"title": {Mode: catalog.CorrectionSet, Text: "新版本自己的标题"}})
	_, claim := scenario.mustClaim(t)
	record, err := operations.Load(context.Background(), fixture.database, claim.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	describe := func(records ...operations.Record) map[string]operations.Description {
		t.Helper()
		descriptions, err := ClaimOperations().Describe(context.Background(), fixture.database, records)
		if err != nil {
			t.Fatalf("describe: %v", err)
		}
		return descriptions
	}
	description := describe(record)[claim.OperationID]
	changes, ok := description.Changes.(claimChanges)
	if !ok || changes.Kind != operations.TypeReleaseClaim || changes.Source.ID != scenario.source.releaseID || changes.Source.Visible {
		t.Fatalf("changes = %+v", description.Changes)
	}
	// 标题被新版本自己的保留，流派迁来；专辑被保留（新版本在另一张多版本专辑里）；默认版本改回自动选择（不算接任）；核对迁来。
	if !reflect.DeepEqual(changes.Moved.Corrections, []string{"genre"}) || !reflect.DeepEqual(changes.Skipped.Corrections, []string{"title"}) ||
		changes.Moved.Album || !changes.Skipped.Album || changes.Moved.Defaults != 0 ||
		!reflect.DeepEqual(changes.Moved.Reviews, []string{IssueSourceType, IssueGrouping}) {
		t.Errorf("moved %+v, skipped %+v", changes.Moved, changes.Skipped)
	}
	// 认领之前新版本已经在 other 的专辑里（上面归入的）：撤销认领之后页面要跟着它回到那里，而不是它最初的组。
	if changes.PreviousGroupID != other.groupID {
		t.Errorf("previous group = %q, want the group the new release was in before the claim %q", changes.PreviousGroupID, other.groupID)
	}
	if description.Release == nil || description.Release.ID != scenario.target.releaseID || !description.Revertible {
		t.Errorf("description = %+v; want the new release and revertible", description)
	}

	// 来源的名字是认领时用户认得的那个：认领把旧版本的修正清走之后，它现在的有效标题已是扫描值，记录里仍写认领之前的名字；
	// 撤销记录写恢复之后的名字（修正回来了）。
	if changes.Source.Title != "我改过的标题" {
		t.Errorf("source title in the claim record = %q, want the title the administrator knew when claiming %q", changes.Source.Title, "我改过的标题")
	}
	var currentTitle string
	if err := fixture.database.QueryRow(`SELECT title FROM release_effective WHERE id=$1::uuid`, scenario.source.releaseID).Scan(&currentTitle); err != nil || currentTitle != "旧版本" {
		t.Fatalf("the old release's current effective title = %q (%v), want its scan value %q once the corrections moved away", currentTitle, err, "旧版本")
	}

	// 新版本不可见仍可撤销；之后又有修改就不可撤销（新版本、旧版本、专辑各自）。
	fixture.exec(`UPDATE tracks SET source_status='missing' WHERE medium_id=$1::uuid`, scenario.target.mediumID)
	if got := describe(record)[claim.OperationID]; !got.Revertible || got.Release == nil || got.Release.Visible {
		t.Errorf("an invisible new release: %+v; want revertible and invisible", got)
	}
	fixture.exec(`UPDATE tracks SET source_status='present' WHERE medium_id=$1::uuid`, scenario.target.mediumID)
	for name, change := range map[string]string{
		"the new release was edited": `UPDATE releases SET edit_revision = edit_revision + 1 WHERE id='` + scenario.target.releaseID + `'`,
		"the old release was edited": `UPDATE releases SET edit_revision = edit_revision + 1 WHERE id='` + scenario.source.releaseID + `'`,
		"the album was edited":       `UPDATE release_groups SET edit_revision = edit_revision + 1 WHERE id='` + scenario.album + `'`,
	} {
		fixture.exec(change)
		if got := describe(record)[claim.OperationID]; got.Revertible {
			t.Errorf("%s: still revertible", name)
		}
		fixture.exec(strings.Replace(change, "+ 1", "- 1", 1))
		if got := describe(record)[claim.OperationID]; !got.Revertible {
			t.Errorf("%s (undone): not revertible again", name)
		}
	}

	// 撤销记录只带来源，不带迁移与保留；撤销之后原记录由调用方判断已被撤销。
	revert := scenario.mustRevertClaim(t, claim.OperationID)
	revertRecord, err := operations.Load(context.Background(), fixture.database, revert.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	revertChanges, ok := describe(revertRecord)[revert.OperationID].Changes.(claimChanges)
	if !ok || revertChanges.Source.ID != scenario.source.releaseID || revertChanges.Source.Title != "我改过的标题" || revertChanges.Moved != nil || revertChanges.Skipped != nil || revertChanges.PreviousGroupID != "" {
		t.Errorf("the revert's changes = %+v", describe(revertRecord)[revert.OperationID].Changes)
	}
}

// 撤销认领会把修正与核对行写回两个发行，这些行引用更早的操作记录，提交时的外键检查要给那条记录加 KEY SHARE 锁；
// 而撤销那条更早的记录会先 FOR UPDATE 锁住它、再锁发行行。撤销认领先给前态引用的记录加 KEY SHARE、后锁发行行（lockOperationRecords），
// 两者就不会互相等待成环。持锁者先占住旧版本的发行行，撤销认领排在前面、撤销原核对随后拿到记录锁，放开后两者都得到分类的结果
// （成功、404 或 409），而不是 500。去掉 KEY SHARE 这一步，撤销认领先拿到发行行、写核对行时被撤销原核对持有的记录锁挡住，
// 而后者在等发行行：PostgreSQL 报 40P01。
func TestPostgreSQLRevertingAClaimAndTheOriginalReviewDoNotDeadlock(t *testing.T) {
	for attempt := 0; attempt < 5; attempt++ {
		scenario := newClaimScenario(t)
		fixture := scenario.fixture
		review := fixture.operationLatest(operations.TypeReleaseIssueReview)
		_, claim := scenario.mustClaim(t)

		holder, err := fixture.database.Begin()
		if err != nil {
			t.Fatalf("begin the lock holder: %v", err)
		}
		if _, err := holder.Exec(`SELECT id FROM releases WHERE id=$1::uuid FOR UPDATE`, scenario.source.releaseID); err != nil {
			t.Fatalf("lock the old release: %v", err)
		}
		results := make(chan error, 2)
		go func() { _, err := scenario.revertClaim(claim.OperationID); results <- err }()
		waitForRowLockWaiters(t, fixture.database, 1)
		go func() { _, err := fixture.revertQuiet(IssueReviewOperations(), review); results <- err }()
		waitForRowLockWaiters(t, fixture.database, 2)
		if err := holder.Rollback(); err != nil {
			t.Fatalf("release the lock holder: %v", err)
		}
		for range 2 {
			if err := <-results; err != nil {
				if status, code, _ := operations.Problem(err); status != 404 && status != 409 {
					t.Fatalf("attempt %d: an operation failed with %d %s: %v (a deadlock would be 500)", attempt, status, code, err)
				}
			}
		}
	}
}
