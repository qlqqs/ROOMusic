package governance

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

// 问题的读取：列表、筛选项数字与详情都来自 issues.go 的同一批常量，所以各类待处理数目之和等于 attention_count。

func (fixture *correctionFixture) listIssues(status, category string, page, pageSize int) IssuePage {
	fixture.t.Helper()
	result, err := fixture.governance.ListIssues(context.Background(), IssueListParams{Status: status, Category: category, Page: page, PageSize: pageSize})
	if err != nil {
		fixture.t.Fatalf("list issues (%s, %s): %v", status, category, err)
	}
	return result
}

func (fixture *correctionFixture) releaseIssues(releaseID string) ReleaseIssues {
	fixture.t.Helper()
	issues, err := fixture.governance.LoadReleaseIssues(context.Background(), releaseID)
	if err != nil {
		fixture.t.Fatalf("load the issues of %s: %v", releaseID, err)
	}
	return issues
}

func listedIDs(page IssuePage) []string {
	ids := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		ids = append(ids, item.Release.ID)
	}
	return ids
}

func TestPostgreSQLIssueListCountsFiltersAndSummary(t *testing.T) {
	fixture := newCorrectionFixture(t)
	both, _ := fixture.issueRelease("Alpha 有问题的版本")
	fields := fixture.visibleRelease("Bravo 只有字段问题")
	fixture.uncertainField(fields.releaseID, "title", "Bravo", []string{"Bravo", "Bravo!"})
	clean := fixture.visibleRelease("Charlie 没有问题")
	hidden := fixture.release("Delta 看不见")
	fixture.groupingConflict(hidden.releaseID, `["hidden/01.flac"]`)
	fixture.exec(`UPDATE releases SET edition='Remastered' WHERE id=$1::uuid`, both.releaseID)

	// 每一行：三类数目之和等于 attention_count；不可见与没有问题的版本不在列表里。
	requireSums := func(page IssuePage) {
		t.Helper()
		for _, item := range page.Items {
			if sum := item.Open.Fields + item.Open.Grouping + item.Open.MissingTracks; sum != item.Release.AttentionCount {
				t.Fatalf("%s: open %+v sums to %d, attention_count %d", item.Release.Title, item.Open, sum, item.Release.AttentionCount)
			}
		}
	}
	open := fixture.listIssues(IssueStatusOpen, "", 1, 50)
	requireSums(open)
	if got := listedIDs(open); len(got) != 2 || got[0] != both.releaseID || got[1] != fields.releaseID || open.Total != 2 {
		t.Fatalf("open issues = %v (total %d), want Alpha then Bravo", got, open.Total)
	}
	first := open.Items[0]
	if first.Open != (IssueCounts{Fields: 2, Grouping: 1, MissingTracks: 2}) || first.Reviewed != (IssueCounts{}) ||
		first.GroupID != both.groupID || first.Edition == nil || *first.Edition != "Remastered" {
		t.Fatalf("Alpha = %+v", first)
	}
	if want := (IssueSummary{Open: IssueSummaryCounts{All: 2, Fields: 2, Grouping: 1, MissingTracks: 1}}); open.Summary != want {
		t.Fatalf("summary = %+v, want %+v", open.Summary, want)
	}

	// 核对之后：Alpha 的归组冲突与缺失曲目进入“已核对”；缺失曲目部分核对时同一个版本出现在两种状态里。
	fixture.mustReview(both.releaseID, IssueGrouping, IssueReview)
	fixture.mustReview(both.releaseID, IssueMissingTracks, IssueReview)
	fixture.track(both, "核对之后缺失", "missing", 10.0)
	open = fixture.listIssues(IssueStatusOpen, "", 1, 50)
	requireSums(open)
	reviewed := fixture.listIssues(IssueStatusReviewed, "", 1, 50)
	if got := listedIDs(reviewed); len(got) != 1 || got[0] != both.releaseID || reviewed.Items[0].Reviewed != (IssueCounts{Grouping: 1, MissingTracks: 2}) {
		t.Fatalf("reviewed = %+v", reviewed.Items)
	}
	if open.Items[0].Open != (IssueCounts{Fields: 2, MissingTracks: 1}) {
		t.Fatalf("Alpha after the reviews = %+v", open.Items[0])
	}
	if want := (IssueSummary{Open: IssueSummaryCounts{All: 2, Fields: 2, MissingTracks: 1}, Reviewed: IssueSummaryCounts{All: 1, Grouping: 1, MissingTracks: 1}}); open.Summary != want || reviewed.Summary != want {
		t.Fatalf("summary = %+v / %+v, want %+v", open.Summary, reviewed.Summary, want)
	}

	// 类别筛选与总数；分页。
	for _, testCase := range []struct {
		status, category string
		want             []string
	}{
		{IssueStatusOpen, IssueCategoryFields, []string{both.releaseID, fields.releaseID}},
		{IssueStatusOpen, IssueCategoryGrouping, nil},
		{IssueStatusOpen, IssueCategoryMissingTracks, []string{both.releaseID}},
		{IssueStatusReviewed, IssueCategoryGrouping, []string{both.releaseID}},
		{IssueStatusReviewed, IssueCategoryFields, nil},
	} {
		page := fixture.listIssues(testCase.status, testCase.category, 1, 50)
		if got := listedIDs(page); fmt.Sprint(got) != fmt.Sprint(append([]string{}, testCase.want...)) || page.Total != int64(len(testCase.want)) {
			t.Errorf("%s/%s = %v (total %d), want %v", testCase.status, testCase.category, got, page.Total, testCase.want)
		}
	}
	second := fixture.listIssues(IssueStatusOpen, "", 2, 1)
	if got := listedIDs(second); len(got) != 1 || got[0] != fields.releaseID || second.Total != 2 {
		t.Fatalf("page 2 of size 1 = %v (total %d), want Bravo", got, second.Total)
	}

	// 与“需要关注”一致：列表里的版本就是 attention=required 的版本。
	attention, err := fixture.store.ListReleases(context.Background(), catalog.ListParams{AttentionRequired: true, Page: 1, PageSize: 50})
	if err != nil || attention.Total != open.Total {
		t.Fatalf("attention=required lists %d (%v), the issue list %d", attention.Total, err, open.Total)
	}
	for _, item := range open.Items {
		if item.Release.ID == clean.releaseID {
			t.Fatal("a release without issues is listed")
		}
	}
}

func TestPostgreSQLReleaseIssuesDetail(t *testing.T) {
	fixture := newCorrectionFixture(t)
	release, missing := fixture.issueRelease("有问题的版本")
	fixture.uncertainField(release.releaseID, "title", "标题", []string{"标题", "另一个标题"})
	fixture.uncertainField(release.releaseID, "label", "厂牌 A", []string{"厂牌 A", "厂牌 B"})
	fixture.correct(release.releaseID, "label", "厂牌 A")
	// 同一目录的另一个可见版本，与一个不可见的。
	sibling := fixture.visibleRelease("同一目录的另一个版本")
	invisible := fixture.release("同一目录但不可见")
	for _, other := range []placementRelease{sibling, invisible} {
		fixture.exec(`UPDATE releases SET source_root_id=(SELECT source_root_id FROM releases WHERE id=$1::uuid), relative_directory=(SELECT relative_directory FROM releases WHERE id=$1::uuid) WHERE id=$2::uuid`, release.releaseID, other.releaseID)
	}

	issues := fixture.releaseIssues(release.releaseID)
	if issues.ReleaseID != release.releaseID || issues.GroupID != release.groupID || issues.Revision != 1 ||
		issues.Release.ID != release.releaseID || issues.Release.Title != "有问题的版本" || issues.Release.AttentionCount != 6 || issues.Edition != nil {
		t.Fatalf("header = %+v (release %+v)", issues, issues.Release)
	}
	// 字段：修正过的厂牌不列出；只有来源类型与介质类型可以核对、带依据令牌。
	byField := map[string]FieldIssue{}
	for _, field := range issues.Fields {
		byField[field.Field] = field
	}
	if len(issues.Fields) != 3 || byField["label"].Field != "" {
		t.Fatalf("fields = %+v, want title, source_type and media_type", issues.Fields)
	}
	title, media := byField["title"], byField["media_type"]
	if title.Reviewable || title.Basis != nil || title.Status != IssueStatusOpen || *title.Value != "标题" || *title.ReasonCode != "inconsistent_candidates" ||
		fmt.Sprint(title.Candidates) != "[标题 另一个标题]" {
		t.Fatalf("title = %+v", title)
	}
	if !media.Reviewable || media.Basis == nil || *media.Basis != fixture.issueBasis(release.releaseID, IssueMediaType) || media.Review != nil {
		t.Fatalf("media type = %+v", media)
	}
	// 归组：原因、来源路径、同一目录的其他可见版本、依据令牌。
	grouping := issues.Grouping
	if grouping == nil || fmt.Sprint(grouping.ReasonCodes) != "[same_directory_conflict]" || fmt.Sprint(grouping.SourceRefs) != "[dir/01.flac dir/02.flac]" ||
		len(grouping.Siblings) != 1 || grouping.Siblings[0] != (IssueSibling{ReleaseID: sibling.releaseID, GroupID: sibling.groupID, Title: "同一目录的另一个版本"}) ||
		grouping.Basis != fixture.issueBasis(release.releaseID, IssueGrouping) || grouping.Status != IssueStatusOpen {
		t.Fatalf("grouping = %+v", grouping)
	}
	// 缺失曲目：按碟与曲序，路径、最后一次看到的时间。
	tracks := issues.MissingTracks
	if tracks == nil || tracks.Count != 2 || tracks.Truncated || len(tracks.Tracks) != 2 || tracks.Tracks[0].ID != missing[0] || tracks.Tracks[1].ID != missing[1] ||
		tracks.Basis != fixture.issueBasis(release.releaseID, IssueMissingTracks) || tracks.Status != IssueStatusOpen {
		t.Fatalf("missing tracks = %+v", tracks)
	}
	if first := tracks.Tracks[0]; first.Title != "缺失一" || first.MediumPosition != 1 || first.Source == "" || first.LastSeenAt.IsZero() || first.Reviewed {
		t.Fatalf("first missing track = %+v", first)
	}

	// 两次读取之间令牌不变；核对之后显示核对人与时间；之后又有曲目缺失时缺失曲目重新是 open，核对行仍在（“核对之后又有曲目缺失”）。
	if again := fixture.releaseIssues(release.releaseID); again.Grouping.Basis != grouping.Basis || again.MissingTracks.Basis != tracks.Basis || *again.Fields[1].Basis != *issues.Fields[1].Basis {
		t.Fatal("the basis tokens changed between two reads")
	}
	reviewedAt := time.Now()
	review := fixture.mustReview(release.releaseID, IssueMissingTracks, IssueReview)
	fixture.mustReview(release.releaseID, IssueMediaType, IssueReview)
	issues = fixture.releaseIssues(release.releaseID)
	if issues.MissingTracks.Status != IssueStatusReviewed || issues.MissingTracks.Review == nil || issues.MissingTracks.Review.OperationID != review.OperationID ||
		issues.MissingTracks.Review.Reviewer.ID != fixture.actorID || issues.MissingTracks.Review.ReviewedAt.Before(reviewedAt.Add(-time.Minute)) {
		t.Fatalf("missing tracks after the review = %+v (review %+v)", issues.MissingTracks, issues.MissingTracks.Review)
	}
	for _, field := range issues.Fields {
		if field.Field == "media_type" && (field.Status != IssueStatusReviewed || field.Review == nil) {
			t.Fatalf("media type after the review = %+v", field)
		}
	}
	if issues.Revision != 3 {
		t.Fatalf("revision after two reviews = %d, want 3", issues.Revision)
	}
	fixture.track(release, "核对之后缺失", "missing", 10.0)
	issues = fixture.releaseIssues(release.releaseID)
	if issues.MissingTracks.Status != IssueStatusOpen || issues.MissingTracks.Review == nil || issues.MissingTracks.Count != 3 ||
		!issues.MissingTracks.Tracks[0].Reviewed || issues.MissingTracks.Tracks[2].Reviewed {
		t.Fatalf("missing tracks after another track went missing = %+v", issues.MissingTracks)
	}
	// 详情里待处理的条目与“需要关注”一致：2 个字段（标题、没有核对的来源类型）+ 1 个归组 + 1 首新缺失的曲目。
	openItems := int64(0)
	for _, field := range issues.Fields {
		if field.Status == IssueStatusOpen {
			openItems++
		}
	}
	if issues.Grouping.Status == IssueStatusOpen {
		openItems++
	}
	for _, track := range issues.MissingTracks.Tracks {
		if !track.Reviewed {
			openItems++
		}
	}
	summary, err := fixture.store.LoadReleaseSummary(context.Background(), release.releaseID)
	if err != nil || summary.AttentionCount != 4 || openItems != summary.AttentionCount {
		t.Fatalf("attention_count = %d (%v), open items in the detail %d; want both 4", summary.AttentionCount, err, openItems)
	}

	// 没有问题的版本：字段为空数组，归组与缺失曲目为 null；不可见与不存在的版本 ErrReleaseNotFound。
	if clean := fixture.releaseIssues(sibling.releaseID); len(clean.Fields) != 0 || clean.Fields == nil || clean.Grouping != nil || clean.MissingTracks != nil {
		t.Fatalf("a release without issues = %+v", clean)
	}
	if _, err := fixture.governance.LoadReleaseIssues(context.Background(), invisible.releaseID); err != catalog.ErrReleaseNotFound {
		t.Fatalf("an invisible release: %v, want ErrReleaseNotFound", err)
	}
}

// 缺失曲目最多列 maxIssueMissingTracks 首，依据始终覆盖全部；同一目录的其他版本最多 maxIssueSiblings 个。
func TestPostgreSQLReleaseIssuesBounds(t *testing.T) {
	fixture := newCorrectionFixture(t)
	release := fixture.visibleRelease("很多缺失曲目")
	fixture.exec(`INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind)
		SELECT gen_random_uuid(), $1::uuid, 100 + n, 'Missing ' || n, '艺术家', 1, $2::uuid, 'many/' || n || '.flac', 'missing', NOW(), 'physical'
		FROM generate_series(1, $3::int) AS n`, release.mediumID, fixture.rootID, maxIssueMissingTracks+5)
	fixture.groupingConflict(release.releaseID, `["many/1.flac"]`)
	for index := 0; index < maxIssueSiblings+2; index++ {
		other := fixture.visibleRelease(fmt.Sprintf("同目录 %02d", index))
		fixture.exec(`UPDATE releases SET relative_directory=(SELECT relative_directory FROM releases WHERE id=$1::uuid) WHERE id=$2::uuid`, release.releaseID, other.releaseID)
	}
	issues := fixture.releaseIssues(release.releaseID)
	if issues.MissingTracks.Count != int64(maxIssueMissingTracks+5) || len(issues.MissingTracks.Tracks) != maxIssueMissingTracks || !issues.MissingTracks.Truncated {
		t.Fatalf("missing tracks: count %d, listed %d, truncated %v", issues.MissingTracks.Count, len(issues.MissingTracks.Tracks), issues.MissingTracks.Truncated)
	}
	if len(issues.Grouping.Siblings) != maxIssueSiblings || !issues.Grouping.Truncated {
		t.Fatalf("siblings: %d listed, truncated %v", len(issues.Grouping.Siblings), issues.Grouping.Truncated)
	}
	// 核对覆盖全部缺失曲目，包括没有列出的。
	fixture.mustReview(release.releaseID, IssueMissingTracks, IssueReview)
	if counts := fixture.issueCounts(release.releaseID); counts.openMissing != 0 || counts.reviewedMissing != int64(maxIssueMissingTracks+5) {
		t.Fatalf("counts after reviewing every missing track = %+v", counts)
	}
}
