package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// 认领的预览与写入（阶段 M4 第三项第 2 步）：用真实的修正、归组、默认版本与核对流程造出旧版本上的四类人工状态，
// 再让它变得不可见，然后预览并认领给新版本。

// claimScenario 是一个旧版本 S（不可见，带四类人工状态）、一个可见的新版本 T，以及 S 原来所在的专辑 G。
type claimScenario struct {
	fixture *correctionFixture
	// source 是旧版本；album 是它原来和 sibling 同在的专辑；sibling 是专辑里仍可见的另一个版本；target 是新版本。
	source, sibling, target placementRelease
	album                   string
}

// newClaimScenario 造出：S（有标题与流派修正、来源类型与归组冲突的核对、是专辑 G 的默认版本、与 sibling 同在 G 里）已不可见，
// T 是一张单版本的可见专辑。旧版本不可见之后所有受治理写入都会 404，所以需要额外状态的测试用 newVisibleClaimScenario，
// 在 hide 之前做完，再让它变得不可见。
func newClaimScenario(t *testing.T) *claimScenario {
	t.Helper()
	scenario := newVisibleClaimScenario(t)
	scenario.hide()
	return scenario
}

// hide 让旧版本变得不可见：扫描新建了别的发行，旧版本的曲目都不见了。
func (scenario *claimScenario) hide() {
	scenario.fixture.exec(`UPDATE tracks SET source_status='missing' WHERE medium_id=$1::uuid`, scenario.source.mediumID)
}

func newVisibleClaimScenario(t *testing.T) *claimScenario {
	t.Helper()
	fixture := newCorrectionFixture(t)
	source, _ := fixture.issueRelease("旧版本")
	sibling := fixture.versionOf("同专辑的另一个版本", 2010)
	target := fixture.versionOf("新版本", 2020)
	scenario := &claimScenario{fixture: fixture, source: source, sibling: sibling, target: target}

	fixture.mustCorrect(source.releaseID, 1, map[string]CorrectionChange{
		"title": {Mode: CorrectionSet, Text: "我改过的标题"}, "genre": {Mode: CorrectionSet, Text: "古典"}})
	fixture.mustReview(source.releaseID, IssueSourceType, IssueReview)
	fixture.mustReview(source.releaseID, IssueGrouping, IssueReview)
	scenario.album = sibling.groupID
	fixture.mustChangeGroup(source.releaseID, fixture.revisionOf(source.releaseID), GroupJoin, scenario.album)
	fixture.mustSetDefault(scenario.album, fixture.groupRevision(scenario.album), source.releaseID)
	return scenario
}

func (scenario *claimScenario) preview(t *testing.T) ClaimPreview {
	t.Helper()
	preview, err := scenario.fixture.store.PreviewClaim(context.Background(), scenario.source.releaseID, scenario.target.releaseID)
	if err != nil {
		t.Fatalf("preview the claim: %v", err)
	}
	return preview
}

// claimQuiet 用预览的令牌与两个版本现在的版本号提交认领。
func (scenario *claimScenario) claimWith(request ClaimRequest) (operations.Result, error) {
	return operations.Run(context.Background(), scenario.fixture.database, operations.Spec{
		ActorID: scenario.fixture.actorID, Type: operations.TypeReleaseClaim, Key: uuid.New(),
		Fingerprint: request.Fingerprint(), RequestID: "test",
	}, ClaimExecutor(request))
}

func (scenario *claimScenario) request(preview ClaimPreview) ClaimRequest {
	return ClaimRequest{ReleaseID: scenario.target.releaseID, ExpectedRevision: preview.Revision, SourceID: scenario.source.releaseID,
		SourceRevision: preview.SourceRevision, Preview: preview.Token}
}

// mustClaim 先预览再认领，核对响应。
func (scenario *claimScenario) mustClaim(t *testing.T) (ClaimResult, operations.Result) {
	t.Helper()
	preview := scenario.preview(t)
	result, err := scenario.claimWith(scenario.request(preview))
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	var decoded ClaimResult
	if err := json.Unmarshal(result.Response, &decoded); err != nil {
		t.Fatalf("decode the claim result: %v", err)
	}
	if decoded.OperationID != result.OperationID || decoded.ReleaseID != scenario.target.releaseID || decoded.SourceReleaseID != scenario.source.releaseID ||
		decoded.Revision != preview.Revision+1 || decoded.SourceRevision != preview.SourceRevision+1 {
		t.Fatalf("claim response = %+v, want operation %s for %s from %s at revisions %d and %d", decoded, result.OperationID,
			scenario.target.releaseID, scenario.source.releaseID, preview.Revision+1, preview.SourceRevision+1)
	}
	return decoded, result
}

// itemsOf 把预览里的项压成 "类别:字段或问题或专辑:去向[:原因]" 供比较。
func itemsOf(items []ClaimItem, album string) []string {
	result := []string{}
	for _, item := range items {
		name := item.Field + item.Issue
		if item.Kind == ClaimKindAlbum || item.Kind == ClaimKindDefault {
			name = "G"
			if item.Group != album {
				name = "其他专辑"
			}
		}
		entry := item.Kind + ":" + name + ":" + item.Action
		if item.Reason != "" {
			entry += ":" + item.Reason
		}
		result = append(result, entry)
	}
	return result
}

func (scenario *claimScenario) correctionFields(releaseID string) []string {
	intents, err := loadCorrectionIntents(context.Background(), scenario.fixture.database, releaseID)
	if err != nil {
		scenario.fixture.t.Fatalf("read corrections: %v", err)
	}
	names := []string{}
	for _, field := range claimedFieldOrder {
		if _, has := intents[field]; has {
			names = append(names, field)
		}
	}
	return names
}

func (scenario *claimScenario) reviewIssues(releaseID string) []string {
	rows, err := scenario.fixture.database.Query(`SELECT issue FROM release_issue_reviews WHERE release_id=$1::uuid ORDER BY issue`, releaseID)
	if err != nil {
		scenario.fixture.t.Fatalf("read reviews: %v", err)
	}
	defer rows.Close()
	issues := []string{}
	for rows.Next() {
		var issue string
		if err := rows.Scan(&issue); err != nil {
			scenario.fixture.t.Fatal(err)
		}
		issues = append(issues, issue)
	}
	return issues
}

func (scenario *claimScenario) claimable(releaseID string) bool {
	return scenario.fixture.claimable(releaseID)
}

// 四类都迁移：修正复制到新版本（operation_id 是本次认领）、新版本归入原来的专辑并接任默认版本、核对原样迁过去（核对人不变），
// 旧版本上什么都不剩，离开原来的专辑，不再待认领。
func TestPostgreSQLClaimMovesAllFourKinds(t *testing.T) {
	scenario := newClaimScenario(t)
	fixture := scenario.fixture
	if !scenario.claimable(scenario.source.releaseID) {
		t.Fatal("the old release is not claimable before the claim")
	}
	_, reviewOperation := fixture.storedReview(scenario.source.releaseID, IssueSourceType)
	_, groupingOperation := fixture.storedReview(scenario.source.releaseID, IssueGrouping)
	albumRevision := fixture.groupRevision(scenario.album)

	preview := scenario.preview(t)
	wantItems := []string{
		"correction:title:move", "correction:genre:move",
		"album:G:move", "default:G:move",
		"review:source_type:move", "review:grouping:move",
	}
	if got := itemsOf(preview.Items, scenario.album); !reflect.DeepEqual(got, wantItems) {
		t.Fatalf("preview items = %q\nwant %q", got, wantItems)
	}
	for _, item := range preview.Items {
		if item.Kind == ClaimKindReview && item.Effect != ClaimEffectNoIssue {
			// 新版本上没有这两类问题：迁过去的核对暂时无事可压。
			t.Errorf("review item %+v: want effect %s (the new release does not have this issue)", item, ClaimEffectNoIssue)
		}
	}

	decoded, result := scenario.mustClaim(t)
	if !reflect.DeepEqual(decoded.Moved.Corrections, []string{"title", "genre"}) || !decoded.Moved.Album ||
		!reflect.DeepEqual(decoded.Moved.Reviews, []string{IssueSourceType, IssueGrouping}) ||
		!reflect.DeepEqual(decoded.Defaults.Moved, []string{scenario.album}) || len(decoded.Defaults.Reset) != 0 ||
		decoded.Skipped.Album || len(decoded.Skipped.Corrections) != 0 || len(decoded.Skipped.Reviews) != 0 {
		t.Fatalf("claim result = %+v", decoded)
	}
	if decoded.GroupID != scenario.album || decoded.PreviousGroupID != scenario.target.groupID {
		t.Fatalf("groups = %s (was %s); want the old album %s (was %s)", decoded.GroupID, decoded.PreviousGroupID, scenario.album, scenario.target.groupID)
	}

	// 新版本：修正、专辑、默认版本、核对都在，版本号加一。
	if got := scenario.correctionFields(scenario.target.releaseID); !reflect.DeepEqual(got, []string{"title", "genre"}) {
		t.Errorf("target corrections = %v", got)
	}
	effective := fixture.effectiveColumns(scenario.target.releaseID)
	if effective["title"] != "我改过的标题" || effective["genre"] != "古典" {
		t.Errorf("target effective title/genre = %v / %v", effective["title"], effective["genre"])
	}
	if fixture.groupOf(scenario.target.releaseID) != scenario.album {
		t.Errorf("target group = %s, want %s", fixture.groupOf(scenario.target.releaseID), scenario.album)
	}
	if fixture.storedDefault(scenario.album) != scenario.target.releaseID {
		t.Errorf("the default of the album = %s, want the new release", fixture.storedDefault(scenario.album))
	}
	if got := scenario.reviewIssues(scenario.target.releaseID); !reflect.DeepEqual(got, []string{IssueGrouping, IssueSourceType}) {
		t.Errorf("target reviews = %v", got)
	}
	// 核对人不变：迁过去的核对行仍指向原来的操作；修正行指向这次认领。
	if _, operationID := fixture.storedReview(scenario.target.releaseID, IssueSourceType); operationID != reviewOperation {
		t.Errorf("migrated source_type review operation = %s, want the original %s", operationID, reviewOperation)
	}
	if _, operationID := fixture.storedReview(scenario.target.releaseID, IssueGrouping); operationID != groupingOperation {
		t.Errorf("migrated grouping review operation = %s, want the original %s", operationID, groupingOperation)
	}
	var correctionOperation string
	if err := fixture.database.QueryRow(`SELECT operation_id::text FROM release_corrections WHERE release_id=$1::uuid AND field='title'`, scenario.target.releaseID).Scan(&correctionOperation); err != nil || correctionOperation != result.OperationID {
		t.Errorf("migrated correction operation = %s (%v), want this claim %s", correctionOperation, err, result.OperationID)
	}

	// 旧版本：什么都不剩，离开原来的专辑，进了一个新建的空组，不再待认领。
	source := scenario.source.releaseID
	if got := scenario.correctionFields(source); len(got) != 0 {
		t.Errorf("source corrections = %v, want none", got)
	}
	if got := scenario.reviewIssues(source); len(got) != 0 {
		t.Errorf("source reviews = %v, want none", got)
	}
	if group := fixture.groupOf(source); group == scenario.album || group == scenario.source.groupID || group == scenario.target.groupID {
		t.Errorf("source group = %s; want a newly created empty group", group)
	}
	if scenario.claimable(source) {
		t.Error("the old release is still claimable after the claim")
	}
	// 记录：一条认领，目标是新版本，版本号对得上。
	record, err := operations.Load(context.Background(), fixture.database, result.OperationID)
	if err != nil || record.Type != operations.TypeReleaseClaim || record.TargetType != operations.TargetRelease || record.TargetID != scenario.target.releaseID ||
		record.ExpectedRevision != preview.Revision || record.ResultRevision != preview.Revision+1 {
		t.Fatalf("the claim record = %+v, %v", record, err)
	}
	if fixture.revisionOf(scenario.source.releaseID) != preview.SourceRevision+1 {
		t.Errorf("source revision = %d, want %d", fixture.revisionOf(scenario.source.releaseID), preview.SourceRevision+1)
	}
	if fixture.revisionOf(scenario.target.releaseID) != preview.Revision+1 {
		t.Errorf("target revision = %d, want %d", fixture.revisionOf(scenario.target.releaseID), preview.Revision+1)
	}
	if fixture.groupRevision(scenario.album) != albumRevision+1 {
		t.Errorf("album revision = %d, want %d (the default version moved)", fixture.groupRevision(scenario.album), albumRevision+1)
	}
}

// K3：新版本已有的以新版本为准。字段已有修正、问题已核对过、已在另一张多版本专辑里，这三项都跳过；跳过的旧状态也随认领从旧版本清除。
func TestPostgreSQLClaimKeepsWhatTheNewReleaseAlreadyHas(t *testing.T) {
	scenario := newClaimScenario(t)
	fixture := scenario.fixture
	// 新版本已有标题修正、已核对过来源类型、并且已经和另一个版本同在一张多版本专辑里。
	other := fixture.versionOf("新版本的同专辑版本", 2015)
	fixture.mustChangeGroup(scenario.target.releaseID, fixture.revisionOf(scenario.target.releaseID), GroupJoin, other.groupID)
	fixture.uncertainField(scenario.target.releaseID, "source_type", "CD", []string{"CD", "SACD"})
	fixture.mustCorrect(scenario.target.releaseID, fixture.revisionOf(scenario.target.releaseID), map[string]CorrectionChange{"title": {Mode: CorrectionSet, Text: "新版本自己的标题"}})
	fixture.mustReview(scenario.target.releaseID, IssueSourceType, IssueReview)

	preview := scenario.preview(t)
	want := []string{
		"correction:title:skip:target_has_correction", "correction:genre:move",
		"album:G:skip:target_in_album", "default:其他专辑:reset",
		"review:source_type:skip:target_has_review", "review:grouping:move",
	}
	// 默认版本项：新版本没有进入 G，所以 G 改回自动选择（Group 就是 G，用 "G" 表示）。
	want[3] = "default:G:reset"
	if got := itemsOf(preview.Items, scenario.album); !reflect.DeepEqual(got, want) {
		t.Fatalf("preview items = %q\nwant %q", got, want)
	}

	decoded, _ := scenario.mustClaim(t)
	if !reflect.DeepEqual(decoded.Moved.Corrections, []string{"genre"}) || !reflect.DeepEqual(decoded.Skipped.Corrections, []string{"title"}) ||
		decoded.Moved.Album || !decoded.Skipped.Album ||
		!reflect.DeepEqual(decoded.Moved.Reviews, []string{IssueGrouping}) || !reflect.DeepEqual(decoded.Skipped.Reviews, []string{IssueSourceType}) ||
		len(decoded.Defaults.Moved) != 0 || !reflect.DeepEqual(decoded.Defaults.Reset, []string{scenario.album}) {
		t.Fatalf("claim result = %+v", decoded)
	}
	// 新版本自己的标题与核对保持，只补上它没有的；专辑不变。
	if fixture.effectiveColumns(scenario.target.releaseID)["title"] != "新版本自己的标题" {
		t.Errorf("target title = %v, want its own", fixture.effectiveColumns(scenario.target.releaseID)["title"])
	}
	if fixture.groupOf(scenario.target.releaseID) != other.groupID || decoded.GroupID != other.groupID {
		t.Errorf("target group = %s, want to stay in %s", fixture.groupOf(scenario.target.releaseID), other.groupID)
	}
	// 默认版本改回自动选择（旧版本不可见，页面上的显示不变）；组的版本号加一。
	if fixture.storedDefault(scenario.album) != "" {
		t.Errorf("the default of the old album = %s, want automatic", fixture.storedDefault(scenario.album))
	}
	// 旧版本上跳过的项也被清除；它离开了原来的专辑，不再待认领。
	if scenario.claimable(scenario.source.releaseID) {
		t.Error("the old release is still claimable")
	}
	if got := scenario.correctionFields(scenario.source.releaseID); len(got) != 0 {
		t.Errorf("source corrections = %v", got)
	}
}

// 专辑的另外两种情形：新版本已经在那张专辑里（不用动）；新版本在一张没有别的可见版本的专辑里（可以归入）。
func TestPostgreSQLClaimAlbumCases(t *testing.T) {
	t.Run("the new release is already in the album", func(t *testing.T) {
		scenario := newClaimScenario(t)
		fixture := scenario.fixture
		fixture.mustChangeGroup(scenario.target.releaseID, fixture.revisionOf(scenario.target.releaseID), GroupJoin, scenario.album)
		preview := scenario.preview(t)
		if got := itemsOf(preview.Items, scenario.album); !reflect.DeepEqual(got, []string{"correction:title:move", "correction:genre:move", "album:G:skip:already_in_album", "default:G:move", "review:source_type:move", "review:grouping:move"}) {
			t.Fatalf("preview items = %q", got)
		}
		decoded, _ := scenario.mustClaim(t)
		if decoded.GroupID != scenario.album || decoded.PreviousGroupID != scenario.album {
			t.Fatalf("groups = %s (was %s), want both the album %s", decoded.GroupID, decoded.PreviousGroupID, scenario.album)
		}
		if fixture.storedDefault(scenario.album) != scenario.target.releaseID {
			t.Errorf("the default = %s, want the new release", fixture.storedDefault(scenario.album))
		}
		if fixture.groupOf(scenario.source.releaseID) == scenario.album {
			t.Error("the old release stayed in the album")
		}
	})
	t.Run("the new release is alone in an album whose other versions are hidden", func(t *testing.T) {
		scenario := newClaimScenario(t)
		fixture := scenario.fixture
		// 新版本所在的组里另有一个不可见的发行：页面上仍是单版本专辑，可以归入。
		hidden := fixture.hiddenRelease("同组的不可见版本")
		fixture.exec(`UPDATE releases SET group_id=$1::uuid WHERE id=$2::uuid`, scenario.target.groupID, hidden.releaseID)
		decoded, _ := scenario.mustClaim(t)
		if !decoded.Moved.Album || decoded.GroupID != scenario.album {
			t.Fatalf("claim result = %+v; want the new release in the old album", decoded)
		}
	})
}

// 没有同组别的发行的旧版本：没有专辑项，不移出组；它是自己那个组的默认版本时，那个组改回自动选择。
func TestPostgreSQLClaimWithoutAnAlbum(t *testing.T) {
	fixture := newCorrectionFixture(t)
	source, _ := fixture.issueRelease("独立的旧版本")
	target := fixture.versionOf("新版本", 2020)
	fixture.mustCorrect(source.releaseID, 1, map[string]CorrectionChange{"label": {Mode: CorrectionSet, Text: "厂牌"}})
	fixture.mustSetDefault(source.groupID, fixture.groupRevision(source.groupID), source.releaseID)
	fixture.exec(`UPDATE tracks SET source_status='missing' WHERE medium_id=$1::uuid`, source.mediumID)
	scenario := &claimScenario{fixture: fixture, source: source, target: target, album: source.groupID}

	preview := scenario.preview(t)
	if got := itemsOf(preview.Items, source.groupID); !reflect.DeepEqual(got, []string{"correction:label:move", "default:G:reset"}) {
		t.Fatalf("preview items = %q", got)
	}
	decoded, _ := scenario.mustClaim(t)
	if decoded.Moved.Album || decoded.Skipped.Album || decoded.GroupID != target.groupID {
		t.Fatalf("claim result = %+v", decoded)
	}
	if fixture.groupOf(source.releaseID) != source.groupID {
		t.Error("a release alone in its group was moved out of it")
	}
	if fixture.storedDefault(source.groupID) != "" {
		t.Errorf("the default of the old group = %s, want automatic", fixture.storedDefault(source.groupID))
	}
	if scenario.claimable(source.releaseID) {
		t.Error("the old release is still claimable")
	}
}

// 缺失曲目的核对不迁移（它针对旧版本自己的曲目）；四类全部被跳过时也照样认领，旧版本的状态被清走、离开待认领列表。
func TestPostgreSQLClaimSkipsMissingTrackReviewsAndEverythingCanBeSkipped(t *testing.T) {
	scenario := newVisibleClaimScenario(t)
	fixture := scenario.fixture
	// 旧版本再核对缺失曲目，然后才让它不可见。
	fixture.mustReview(scenario.source.releaseID, IssueMissingTracks, IssueReview)
	scenario.hide()
	// 新版本把旧版本的每一项都占满：两个字段的修正、两类核对（含归组）、已经在另一张多版本专辑里；旧版本不是任何组的默认版本。
	other := fixture.versionOf("新版本的同专辑版本", 2015)
	fixture.mustChangeGroup(scenario.target.releaseID, fixture.revisionOf(scenario.target.releaseID), GroupJoin, other.groupID)
	fixture.uncertainField(scenario.target.releaseID, "source_type", "CD", []string{"CD", "SACD"})
	fixture.groupingConflict(scenario.target.releaseID, `["x/01.flac","x/02.flac"]`)
	fixture.mustCorrect(scenario.target.releaseID, fixture.revisionOf(scenario.target.releaseID), map[string]CorrectionChange{
		"title": {Mode: CorrectionSet, Text: "新标题"}, "genre": {Mode: CorrectionSet, Text: "新流派"}})
	fixture.mustReview(scenario.target.releaseID, IssueSourceType, IssueReview)
	fixture.mustReview(scenario.target.releaseID, IssueGrouping, IssueReview)
	fixture.exec(`UPDATE release_groups SET default_release_id=NULL WHERE id=$1::uuid`, scenario.album)

	preview := scenario.preview(t)
	want := []string{
		"correction:title:skip:target_has_correction", "correction:genre:skip:target_has_correction",
		"album:G:skip:target_in_album",
		"review:source_type:skip:target_has_review", "review:grouping:skip:target_has_review", "review:missing_tracks:skip:not_transferable",
	}
	if got := itemsOf(preview.Items, scenario.album); !reflect.DeepEqual(got, want) {
		t.Fatalf("preview items = %q\nwant %q", got, want)
	}
	decoded, _ := scenario.mustClaim(t)
	if len(decoded.Moved.Corrections) != 0 || decoded.Moved.Album || len(decoded.Moved.Reviews) != 0 || len(decoded.Defaults.Moved)+len(decoded.Defaults.Reset) != 0 {
		t.Fatalf("claim result = %+v; want everything skipped", decoded)
	}
	if !reflect.DeepEqual(decoded.Skipped.Reviews, []string{IssueSourceType, IssueGrouping, IssueMissingTracks}) {
		t.Errorf("skipped reviews = %v", decoded.Skipped.Reviews)
	}
	if scenario.claimable(scenario.source.releaseID) {
		t.Error("the old release is still claimable after an all-skipped claim")
	}
	if got := scenario.reviewIssues(scenario.source.releaseID); len(got) != 0 {
		t.Errorf("source reviews = %v, want none (the missing-track review is cleared, not migrated)", got)
	}
	if got := scenario.reviewIssues(scenario.target.releaseID); !reflect.DeepEqual(got, []string{IssueGrouping, IssueSourceType}) {
		t.Errorf("target reviews = %v; the missing-track review must not be migrated", got)
	}
}

// 迁过去的核对在新版本上依据相同才有效：来源类型的候选与新版本相同时有效，不同时重新计入“需要关注”；
// 新版本上没有这类问题时无事可压（预览里是 no_issue，与“会重新提醒”不同）。
func TestPostgreSQLClaimedReviewsAreValidOnlyWhenTheBasisMatches(t *testing.T) {
	scenario := newClaimScenario(t)
	fixture := scenario.fixture
	// 新版本的来源类型也拿不准，候选和选出的值与旧版本核对时完全相同（issueRelease 里是 CD / [CD SACD]）。
	fixture.uncertainField(scenario.target.releaseID, "source_type", "CD", []string{"CD", "SACD"})
	// 归组冲突：新版本的来源文件不同，依据不同。
	fixture.groupingConflict(scenario.target.releaseID, `["other/01.flac","other/02.flac"]`)

	preview := scenario.preview(t)
	effects := map[string]string{}
	for _, item := range preview.Items {
		if item.Kind == ClaimKindReview {
			effects[item.Issue] = item.Effect
		}
	}
	if effects[IssueSourceType] != ClaimEffectValid || effects[IssueGrouping] != ClaimEffectStale {
		t.Fatalf("effects = %v; want source_type valid (same basis) and grouping stale (different files)", effects)
	}

	before := fixture.issueCounts(scenario.target.releaseID)
	if before.openFields != 1 || before.openGrouping != 1 {
		t.Fatalf("counts before the claim: %+v", before)
	}
	scenario.mustClaim(t)
	after := fixture.issueCounts(scenario.target.releaseID)
	if after.openFields != 0 || after.reviewedFields != 1 {
		t.Errorf("the migrated source_type review should suppress the issue: %+v", after)
	}
	if after.openGrouping != 1 || after.reviewedGrouping != 0 {
		t.Errorf("the migrated grouping review has a stale basis and must count again: %+v", after)
	}
}

// 拒绝：每种都不留副作用（版本号、人工状态、记录数都不变）。
func TestPostgreSQLClaimRejectionsLeaveNoTrace(t *testing.T) {
	scenario := newClaimScenario(t)
	fixture := scenario.fixture
	preview := scenario.preview(t)
	good := scenario.request(preview)

	type state struct {
		targetRevision, sourceRevision, groupRevision int64
		operations                                    int
		sourceCorrections, targetCorrections          []string
		sourceGroup, targetGroup, storedDefault       string
	}
	snapshot := func() state {
		return state{fixture.revisionOf(scenario.target.releaseID), fixture.revisionOf(scenario.source.releaseID), fixture.groupRevision(scenario.album),
			fixture.operationCount(), scenario.correctionFields(scenario.source.releaseID), scenario.correctionFields(scenario.target.releaseID),
			fixture.groupOf(scenario.source.releaseID), fixture.groupOf(scenario.target.releaseID), fixture.storedDefault(scenario.album)}
	}
	before := snapshot()

	staleToken := good
	staleToken.Preview = "0000"
	staleTarget := good
	staleTarget.ExpectedRevision++
	staleSource := good
	staleSource.SourceRevision++
	visibleSource := good
	visibleSource.SourceID = scenario.sibling.releaseID // 可见的版本不是旧版本
	hiddenTarget := good
	hiddenTarget.ReleaseID = scenario.source.releaseID // 新版本必须可见
	hiddenTarget.SourceID = scenario.source.releaseID
	unknownSource := good
	unknownSource.SourceID = uuid.New()
	unknownTarget := good
	unknownTarget.ReleaseID = uuid.New()
	noManualState := good
	plain := fixture.hiddenRelease("什么人工状态都没有的不可见版本")
	noManualState.SourceID = plain.releaseID

	for name, testCase := range map[string]struct {
		request ClaimRequest
		status  operations.Class
		code    string
	}{
		"a token the page did not see":        {staleToken, operations.ClassConflict, "claim_changed"},
		"a stale revision of the new release": {staleTarget, operations.ClassConflict, "revision_conflict"},
		"a stale revision of the old release": {staleSource, operations.ClassConflict, "revision_conflict"},
		"a visible release as the source":     {visibleSource, operations.ClassConflict, "not_claimable"},
		"a source without any manual state":   {noManualState, operations.ClassConflict, "not_claimable"},
		"a hidden release as the new release": {hiddenTarget, operations.ClassNotFound, "not_found"},
		"an unknown source":                   {unknownSource, operations.ClassNotFound, "not_found"},
		"an unknown new release":              {unknownTarget, operations.ClassNotFound, "not_found"},
	} {
		_, err := scenario.claimWith(testCase.request)
		var failure *operations.Failure
		if !errors.As(err, &failure) || failure.Class != testCase.status || failure.Code != testCase.code {
			t.Errorf("%s: error = %v, want %v/%s", name, err, testCase.status, testCase.code)
		}
		if after := snapshot(); !reflect.DeepEqual(after, before) {
			t.Errorf("%s left a trace:\nbefore %+v\nafter  %+v", name, before, after)
		}
	}
	// 之后用正确的请求仍然成功：被拒绝的请求没有占用幂等键或锁。
	if _, err := scenario.claimWith(good); err != nil {
		t.Fatalf("the valid claim after the rejections: %v", err)
	}
}

// 预览之后状态变了：新版本又被修改（令牌与版本号同时变）、旧版本的核对被改动，都拒绝。
func TestPostgreSQLClaimDetectsChangesAfterThePreview(t *testing.T) {
	t.Run("the new release gained a correction", func(t *testing.T) {
		scenario := newClaimScenario(t)
		preview := scenario.preview(t)
		scenario.fixture.mustCorrect(scenario.target.releaseID, scenario.fixture.revisionOf(scenario.target.releaseID), map[string]CorrectionChange{"title": {Mode: CorrectionSet, Text: "后来改的"}})
		_, err := scenario.claimWith(scenario.request(preview))
		var failure *operations.Failure
		if !errors.As(err, &failure) || failure.Code != "revision_conflict" {
			t.Fatalf("error = %v, want revision_conflict (the revision moved on)", err)
		}
	})
	t.Run("a token that was issued for another pair of releases", func(t *testing.T) {
		scenario := newClaimScenario(t)
		// 另一个新版本的计划与这个完全相同（都是单版本专辑、没有修正与核对）：令牌仍与具体的一对版本绑定。
		other := scenario.fixture.versionOf("另一个新版本", 2021)
		otherPreview, err := scenario.fixture.store.PreviewClaim(context.Background(), scenario.source.releaseID, other.releaseID)
		if err != nil {
			t.Fatalf("preview for the other release: %v", err)
		}
		preview := scenario.preview(t)
		if otherPreview.Token == preview.Token {
			t.Fatal("two different pairs of releases share a token")
		}
		borrowed := scenario.request(preview)
		borrowed.Preview = otherPreview.Token
		_, err = scenario.claimWith(borrowed)
		var failure *operations.Failure
		if !errors.As(err, &failure) || failure.Code != "claim_changed" {
			t.Fatalf("error = %v, want claim_changed", err)
		}
	})
	t.Run("a token that was issued for another old release", func(t *testing.T) {
		fixture := newCorrectionFixture(t)
		// 两个旧版本的人工状态形状完全相同（各自一个流派修正、各自独立的组），新版本相同：计划的各项也相同，
		// 令牌只能靠来源 ID 区分。
		target := fixture.versionOf("新版本", 2020)
		makeSource := func(title string) placementRelease {
			source, _ := fixture.issueRelease(title)
			fixture.mustCorrect(source.releaseID, 1, map[string]CorrectionChange{"genre": {Mode: CorrectionSet, Text: "古典"}})
			fixture.exec(`UPDATE tracks SET source_status='missing' WHERE medium_id=$1::uuid`, source.mediumID)
			return source
		}
		first, second := makeSource("第一个旧版本"), makeSource("第二个旧版本")
		firstPreview, err := fixture.store.PreviewClaim(context.Background(), first.releaseID, target.releaseID)
		if err != nil {
			t.Fatal(err)
		}
		secondPreview, err := fixture.store.PreviewClaim(context.Background(), second.releaseID, target.releaseID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(itemsOf(firstPreview.Items, ""), itemsOf(secondPreview.Items, "")) {
			t.Fatalf("the two plans differ, so the test would not isolate the source: %q vs %q", itemsOf(firstPreview.Items, ""), itemsOf(secondPreview.Items, ""))
		}
		if firstPreview.Token == secondPreview.Token {
			t.Fatal("two different old releases share a token")
		}
		scenario := &claimScenario{fixture: fixture, source: first, target: target}
		_, err = scenario.claimWith(ClaimRequest{ReleaseID: target.releaseID, ExpectedRevision: firstPreview.Revision, SourceID: first.releaseID,
			SourceRevision: firstPreview.SourceRevision, Preview: secondPreview.Token})
		var failure *operations.Failure
		if !errors.As(err, &failure) || failure.Code != "claim_changed" {
			t.Fatalf("error = %v, want claim_changed", err)
		}
	})
	t.Run("the album was edited by someone else", func(t *testing.T) {
		scenario := newClaimScenario(t)
		preview := scenario.preview(t)
		// 别人在预览之后换了那张专辑的默认版本：计划里的各项没变，但专辑的版本号变了，页面看到的不是现在的专辑。
		scenario.fixture.mustSetDefault(scenario.album, scenario.fixture.groupRevision(scenario.album), scenario.sibling.releaseID)
		_, err := scenario.claimWith(scenario.request(preview))
		var failure *operations.Failure
		if !errors.As(err, &failure) || failure.Code != "claim_changed" {
			t.Fatalf("error = %v, want claim_changed", err)
		}
	})
	t.Run("the state changed without a revision (a rescan changed the files)", func(t *testing.T) {
		scenario := newClaimScenario(t)
		preview := scenario.preview(t)
		// 扫描不改版本号：新版本的来源类型核对依据在页面打开之后因重扫而不同。这里模拟扫描改了新版本所在组的成员（影响专辑项）。
		other := scenario.fixture.versionOf("扫描后出现的同专辑版本", 2016)
		scenario.fixture.exec(`UPDATE releases SET group_id=$1::uuid WHERE id=$2::uuid`, scenario.target.groupID, other.releaseID)
		_, err := scenario.claimWith(scenario.request(preview))
		var failure *operations.Failure
		if !errors.As(err, &failure) || failure.Code != "claim_changed" {
			t.Fatalf("error = %v, want claim_changed", err)
		}
	})
}

// 同键同义重放得到同一响应，不再执行；同键异义是 409 idempotency_conflict。
func TestPostgreSQLClaimReplaysWithTheSameKey(t *testing.T) {
	scenario := newClaimScenario(t)
	preview := scenario.preview(t)
	request := scenario.request(preview)
	key := uuid.New()
	run := func(request ClaimRequest) (operations.Result, error) {
		return operations.Run(context.Background(), scenario.fixture.database, operations.Spec{
			ActorID: scenario.fixture.actorID, Type: operations.TypeReleaseClaim, Key: key,
			Fingerprint: request.Fingerprint(), RequestID: "test",
		}, ClaimExecutor(request))
	}
	first, err := run(request)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	operationsAfterFirst := scenario.fixture.operationCount()
	replay, err := run(request)
	var firstBody, replayBody map[string]any
	if err != nil || json.Unmarshal(first.Response, &firstBody) != nil || json.Unmarshal(replay.Response, &replayBody) != nil ||
		!replay.Replayed || replay.OperationID != first.OperationID || !reflect.DeepEqual(firstBody, replayBody) {
		t.Fatalf("replay = %+v, %v; want the same operation and response as %s", replay, err, first.Response)
	}
	if scenario.fixture.operationCount() != operationsAfterFirst {
		t.Error("a replay wrote another record")
	}
	different := request
	different.Preview = "ffff"
	_, err = run(different)
	var failure *operations.Failure
	if !errors.As(err, &failure) || failure.Code != "idempotency_conflict" {
		t.Fatalf("same key with a different request: %v, want idempotency_conflict", err)
	}
}

// 预览本身：只读（不改任何东西）、看不见的旧版本 404 / not_claimable，令牌随状态变化。
func TestPostgreSQLClaimPreviewIsReadOnlyAndTokenTracksState(t *testing.T) {
	scenario := newClaimScenario(t)
	fixture := scenario.fixture
	operationsBefore, revisionBefore := fixture.operationCount(), fixture.revisionOf(scenario.source.releaseID)
	first := scenario.preview(t)
	second := scenario.preview(t)
	if first.Token != second.Token || first.Token == "" {
		t.Fatalf("tokens differ between two previews of the same state: %q vs %q", first.Token, second.Token)
	}
	if fixture.operationCount() != operationsBefore || fixture.revisionOf(scenario.source.releaseID) != revisionBefore {
		t.Fatal("a preview changed the state")
	}
	if first.Release.ID != scenario.target.releaseID || first.Source.ID != scenario.source.releaseID || first.Source.Visible || !first.Release.Visible {
		t.Fatalf("preview releases = %+v / %+v", first.Release, first.Source)
	}
	// 展示用的标题不进令牌：新版本改名（修正标题）会让版本号变，但令牌的变化只来自决定性内容——这里直接改标题列验证令牌不变。
	fixture.exec(`UPDATE releases SET title='扫描改了新版本的标题' WHERE id=$1::uuid`, scenario.target.releaseID)
	if renamed := scenario.preview(t); renamed.Token != first.Token {
		t.Error("renaming the new release changed the token; titles are display-only")
	}
	// 状态变了令牌就变：旧版本多了一条核对（直接写核对行，受治理写入对不可见的版本都是 404）。
	fixture.reviewIssue(scenario.source.releaseID, IssueMediaType)
	if changed := scenario.preview(t); changed.Token == first.Token {
		t.Error("the token did not change when the old release gained a review")
	}

	// 可见的旧版本、没有人工状态的版本都是 not_claimable；不可见的新版本与不存在的版本是 not found。
	var failure *operations.Failure
	if _, err := fixture.store.PreviewClaim(context.Background(), scenario.sibling.releaseID, scenario.target.releaseID); !errors.As(err, &failure) || failure.Code != "not_claimable" {
		t.Errorf("previewing a visible source: %v, want not_claimable", err)
	}
	if _, err := fixture.store.PreviewClaim(context.Background(), scenario.source.releaseID, scenario.source.releaseID); !errors.Is(err, ErrReleaseNotFound) {
		t.Errorf("previewing a hidden target: %v, want not found", err)
	}
	if _, err := fixture.store.PreviewClaim(context.Background(), uuid.New(), scenario.target.releaseID); !errors.Is(err, ErrReleaseNotFound) {
		t.Errorf("previewing an unknown source: %v, want not found", err)
	}
}

// 认领把旧版本的核对行迁到新版本时保留原来的 operation_id，提交时的外键检查要给那条记录加 KEY SHARE 锁；而撤销那条更早的记录
// 会先 FOR UPDATE 锁住它、再锁发行行。认领先加 KEY SHARE、后锁发行行（lockReviewOperations），两者就不会互相等待成环。
// 持锁者先占住旧版本的发行行，让撤销拿到记录锁后卡在发行行上、认领排在后面，放开后两者都得到分类的结果（成功、404 或 409），而不是 500。
// 去掉 KEY SHARE 这一步，认领先拿到发行行、后写核对行时被撤销持有的记录锁挡住，撤销又在等发行行：PostgreSQL 报 40P01。
func TestPostgreSQLClaimAndRevertingTheOriginalReviewDoNotDeadlock(t *testing.T) {
	for attempt := 0; attempt < 5; attempt++ {
		scenario := newVisibleClaimScenario(t)
		fixture := scenario.fixture
		review := fixture.operationLatest(operations.TypeReleaseIssueReview)
		scenario.hide()
		preview := scenario.preview(t)

		holder, err := fixture.database.Begin()
		if err != nil {
			t.Fatalf("begin the lock holder: %v", err)
		}
		if _, err := holder.Exec(`SELECT id FROM releases WHERE id=$1::uuid FOR UPDATE`, scenario.source.releaseID); err != nil {
			t.Fatalf("lock the old release: %v", err)
		}
		results := make(chan error, 2)
		// 认领先排在旧版本的发行行锁后面；撤销随后拿到那条记录的锁，也排在发行行锁后面。放开之后认领先拿到发行行，
		// 要写核对行时被撤销持有的记录锁挡住，而撤销在等认领持有的发行行：这就是 KEY SHARE 锁要避免的环。
		go func() { _, err := scenario.claimWith(scenario.request(preview)); results <- err }()
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

// 认领、修正、归组、默认版本、核对、各类撤销与模拟的扫描（翻转曲目的可见性）随机交错，不死锁：所有失败都是分类的 400/404/409，
// 成功数等于新增记录数。每个旧版本带着四类人工状态，认领、修正、核对与撤销争用同样的发行行、组行与核对引用的记录。
func TestPostgreSQLClaimsAndOtherChangesNeverDeadlock(t *testing.T) {
	fixture := newCorrectionFixture(t)
	var sources, targets []placementRelease
	album := fixture.versionOf("共同的专辑", 2000)
	for index := 0; index < 3; index++ {
		source, _ := fixture.issueRelease("旧版本")
		fixture.mustCorrect(source.releaseID, 1, map[string]CorrectionChange{"genre": {Mode: CorrectionSet, Text: "古典"}})
		fixture.mustReview(source.releaseID, IssueGrouping, IssueReview)
		fixture.mustChangeGroup(source.releaseID, fixture.revisionOf(source.releaseID), GroupJoin, album.groupID)
		sources = append(sources, source)
		targets = append(targets, fixture.versionOf("新版本", 2010+index))
	}
	fixture.mustSetDefault(album.groupID, fixture.groupRevision(album.groupID), sources[0].releaseID)
	// 其中两个旧版本已经不可见，第三个靠"扫描"在可见与不可见之间翻转。
	for _, source := range sources[:2] {
		fixture.exec(`UPDATE tracks SET source_status='missing' WHERE medium_id=$1::uuid`, source.mediumID)
	}
	baseline := fixture.operationCount()
	revisionOf := func(releaseID string) int64 {
		var value int64
		_ = fixture.database.QueryRow(`SELECT edit_revision FROM releases WHERE id=$1::uuid`, releaseID).Scan(&value)
		return value
	}
	anyRecord := func() (id, operationType string) {
		_ = fixture.database.QueryRow(`SELECT id::text, operation_type FROM operations WHERE operation_type IN ('release_issue_review','release_correction','release_group_change','release_group_default','release_claim') ORDER BY random() LIMIT 1`).Scan(&id, &operationType)
		return id, operationType
	}
	kinds := map[string]operations.Kind{
		operations.TypeReleaseIssueReview: IssueReviewOperations(), operations.TypeReleaseCorrection: CorrectionOperations(),
		operations.TypeReleaseGroupChange: GroupChangeOperations(), operations.TypeReleaseGroupDefault: GroupDefaultOperations(),
		operations.TypeReleaseClaim: ClaimOperations(),
	}
	run := func(operationType string, request interface{ Fingerprint() string }, executor operations.Executor) error {
		_, err := operations.Run(context.Background(), fixture.database, operations.Spec{
			ActorID: fixture.actorID, Type: operationType, Key: uuid.New(), Fingerprint: request.Fingerprint(), RequestID: "test",
		}, executor)
		return err
	}
	var successes atomic.Int64
	var waitGroup sync.WaitGroup
	for worker := 0; worker < 6; worker++ {
		waitGroup.Add(1)
		go func(seed int64) {
			defer waitGroup.Done()
			random := rand.New(rand.NewSource(seed))
			for round := 0; round < 40; round++ {
				source, target := sources[random.Intn(len(sources))], targets[random.Intn(len(targets))]
				var err error
				switch random.Intn(8) {
				case 0, 1, 2:
					// 认领：现取预览，预览可能马上过期。
					preview, previewErr := fixture.store.PreviewClaim(context.Background(), source.releaseID, target.releaseID)
					if previewErr != nil {
						err = previewErr
						break
					}
					request := ClaimRequest{ReleaseID: target.releaseID, ExpectedRevision: preview.Revision, SourceID: source.releaseID, SourceRevision: preview.SourceRevision, Preview: preview.Token}
					err = run(operations.TypeReleaseClaim, request, ClaimExecutor(request))
				case 3:
					request := CorrectionRequest{ReleaseID: target.releaseID, ExpectedRevision: revisionOf(target.releaseID), Changes: map[string]CorrectionChange{"genre": {Mode: CorrectionSet, Text: "流派" + string(rune('A'+random.Intn(3)))}}}
					err = run(operations.TypeReleaseCorrection, request, CorrectionExecutor(request))
				case 4:
					request := GroupChangeRequest{ReleaseID: target.releaseID, ExpectedRevision: revisionOf(target.releaseID), Action: GroupJoin, GroupID: album.groupID}
					err = run(operations.TypeReleaseGroupChange, request, GroupChangeExecutor(request))
				case 5:
					var groupRevision int64
					_ = fixture.database.QueryRow(`SELECT edit_revision FROM release_groups WHERE id=$1::uuid`, album.groupID).Scan(&groupRevision)
					request := GroupDefaultRequest{GroupID: album.groupID, ExpectedRevision: groupRevision, ReleaseID: targets[random.Intn(len(targets))].releaseID}
					err = run(operations.TypeReleaseGroupDefault, request, GroupDefaultExecutor(request))
				case 6:
					id, operationType := anyRecord()
					if id == "" {
						continue
					}
					_, err = fixture.revertQuiet(kinds[operationType], id)
				default:
					// 扫描：旧版本在可见与不可见之间翻转（真实扫描只更新曲目与发行行，不锁组行）。不产生记录，不计成功。
					if _, scanErr := fixture.database.Exec(`UPDATE tracks SET source_status = CASE WHEN source_status='present' THEN 'missing' ELSE 'present' END WHERE medium_id=$1::uuid AND title='曲目'`, sources[2].mediumID); scanErr != nil {
						t.Errorf("simulated scan: %v", scanErr)
						return
					}
					// 扫描还会更新它沿用的发行行（标题等扫描列），只锁发行行、从不锁组行。真实扫描逐个发行各自一条 UPDATE（persistRelease），
					// 不会在一条语句里同时锁两个发行，所以这里也逐个更新。
					for _, releaseID := range []string{sources[random.Intn(len(sources))].releaseID, targets[random.Intn(len(targets))].releaseID} {
						if _, scanErr := fixture.database.Exec(`UPDATE releases SET artist = artist WHERE id = $1::uuid`, releaseID); scanErr != nil {
							t.Errorf("simulated scan update: %v", scanErr)
							return
						}
					}
					continue
				}
				switch {
				case err == nil:
					successes.Add(1)

				default:
					if status, code, _ := operations.Problem(err); status != 400 && status != 404 && status != 409 && !errors.Is(err, ErrReleaseNotFound) {
						t.Errorf("concurrent operation failed with status %d (%s): %v", status, code, err)
						return
					}
				}
			}
		}(int64(worker + 1))
	}
	waitGroup.Wait()
	if records := int64(fixture.operationCount() - baseline); records != successes.Load() || successes.Load() == 0 {
		t.Errorf("%d new records for %d successful operations; want equal and at least one", records, successes.Load())
	}
}
