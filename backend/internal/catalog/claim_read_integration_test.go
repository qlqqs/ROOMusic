package catalog

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

// 待认领的读取（阶段 M4 第三项第 4 步）：列表、详情与专辑详情用的提示。

func (fixture *correctionFixture) listClaims(page, pageSize int) ClaimPage {
	fixture.t.Helper()
	result, err := fixture.store.ListClaims(context.Background(), ClaimListParams{Page: page, PageSize: pageSize})
	if err != nil {
		fixture.t.Fatalf("list claims: %v", err)
	}
	return result
}

func claimIDs(page ClaimPage) []string {
	ids := make([]string, len(page.Items))
	for index, item := range page.Items {
		ids[index] = item.Release.ID
	}
	return ids
}

// seen 把旧版本名下所有曲目最后一次被看到的时间设成 base 之前 age（age 越小越新）。
func (fixture *correctionFixture) seen(release placementRelease, age time.Duration) {
	fixture.t.Helper()
	fixture.exec(`UPDATE tracks SET observed_at = TIMESTAMPTZ '2026-06-01 12:00:00+00' - $2::interval WHERE medium_id=$1::uuid`, release.mediumID, fmt.Sprintf("%d seconds", int(age.Seconds())))
}

// 列表只含待认领的版本，按最后一次被看到的时间倒序；每项写明原来的专辑、位置与留着的修改。
func TestPostgreSQLListClaims(t *testing.T) {
	fixture := newCorrectionFixture(t)
	visible := fixture.versionOf("可见版本", 2000)
	fixture.mustCorrect(visible.releaseID, 1, map[string]CorrectionChange{"label": {Mode: CorrectionSet, Text: "可见的版本有修正也不是旧版本"}})

	// 三个旧版本：最久没见的、较新的、最新的。
	oldest, _ := fixture.issueRelease("最久没见的")
	fixture.mustCorrect(oldest.releaseID, 1, map[string]CorrectionChange{"genre": {Mode: CorrectionSet, Text: "古典"}})
	middle, _ := fixture.issueRelease("中间的")
	fixture.mustCorrect(middle.releaseID, 1, map[string]CorrectionChange{"label": {Mode: CorrectionSet, Text: "厂牌"}, "title": {Mode: CorrectionSet, Text: "我改过的标题"}})
	fixture.mustReview(middle.releaseID, IssueGrouping, IssueReview)
	newest, _ := fixture.issueRelease("最新的")
	fixture.mustReview(newest.releaseID, IssueSourceType, IssueReview)
	for release, age := range map[placementRelease]time.Duration{oldest: 30 * 24 * time.Hour, middle: 7 * 24 * time.Hour, newest: time.Hour} {
		fixture.exec(`UPDATE tracks SET source_status='missing' WHERE medium_id=$1::uuid`, release.mediumID)
		fixture.seen(release, age)
	}
	// 可见版本、没有人工状态的不可见版本不在列表里。
	fixture.hiddenRelease("什么都没有")

	page := fixture.listClaims(1, 10)
	if want := []string{newest.releaseID, middle.releaseID, oldest.releaseID}; !reflect.DeepEqual(claimIDs(page), want) || page.Total != 3 {
		t.Fatalf("claims = %v (total %d), want newest first %v", claimIDs(page), page.Total, want)
	}
	byID := map[string]ClaimEntry{}
	for _, item := range page.Items {
		byID[item.Release.ID] = item
	}
	if item := byID[middle.releaseID]; item.Release.Title != "我改过的标题" || !reflect.DeepEqual(item.Holds.Corrections, []string{"label", "title"}) ||
		!reflect.DeepEqual(item.Holds.Reviews, []string{IssueGrouping}) || item.Holds.Album || item.Holds.Default || item.Album != nil {
		t.Errorf("the middle release = %+v", item)
	}
	if item := byID[newest.releaseID]; !reflect.DeepEqual(item.Holds.Reviews, []string{IssueSourceType}) || len(item.Holds.Corrections) != 0 || item.Album != nil {
		t.Errorf("the newest release = %+v", item)
	}
	if item := byID[oldest.releaseID]; !item.LastSeenAt.Before(byID[middle.releaseID].LastSeenAt) {
		t.Errorf("the oldest release was seen at %v, not before the middle one %v", item.LastSeenAt, byID[middle.releaseID].LastSeenAt)
	}

	// 分页：总数不变，每页按同一顺序切。
	for pageNumber, want := range map[int][]string{1: {newest.releaseID, middle.releaseID}, 2: {oldest.releaseID}, 3: {}} {
		got := fixture.listClaims(pageNumber, 2)
		if !reflect.DeepEqual(claimIDs(got), want) || got.Total != 3 || got.Items == nil {
			t.Errorf("page %d of size 2 = %v (total %d, items nil: %v), want %v of 3", pageNumber, claimIDs(got), got.Total, got.Items == nil, want)
		}
	}
	if empty := newCorrectionFixture(t).listClaims(1, 10); empty.Items == nil || len(empty.Items) != 0 || empty.Total != 0 {
		t.Errorf("an empty library lists %+v, want an empty non-nil page", empty)
	}
}

// 原来的专辑：组里有别的发行时给出专辑 ID、标题与别的发行数；标题先取可见的代表版本，没有可见成员就取最早创建的成员。默认版本也算留着的修改。
func TestPostgreSQLListClaimsAlbumAndDefault(t *testing.T) {
	scenario := newClaimScenario(t)
	fixture := scenario.fixture
	page := fixture.listClaims(1, 10)
	if len(page.Items) != 1 {
		t.Fatalf("claims = %v", claimIDs(page))
	}
	item := page.Items[0]
	if item.Album == nil || item.Album.ID != scenario.album || item.Album.Title != "同专辑的另一个版本" || item.Album.OtherReleaseCount != 1 ||
		!item.Holds.Album || !item.Holds.Default {
		t.Fatalf("the album of the old release = %+v, holds %+v; want the visible sibling's title and the default flag", item.Album, item.Holds)
	}
	// 组里没有可见成员时，取最早创建的别的成员的标题。同专辑的另一个版本也不可见了，它因此同样是待认领的（组里有别的发行）：
	// 同组的多个旧版本可以逐个认领回同一张专辑，所以列表里现在有两项，按版本找。
	fixture.exec(`UPDATE tracks SET source_status='missing' WHERE medium_id=$1::uuid`, scenario.sibling.mediumID)
	fixture.exec(`UPDATE releases SET created_at = NOW() - INTERVAL '1 day' WHERE id=$1::uuid`, scenario.sibling.releaseID)
	entryOf := func(releaseID string) ClaimEntry {
		t.Helper()
		page := fixture.listClaims(1, 10)
		if page.Total != 2 {
			t.Fatalf("claims = %v, want the old release and its hidden sibling", claimIDs(page))
		}
		for _, item := range page.Items {
			if item.Release.ID == releaseID {
				return item
			}
		}
		t.Fatalf("release %s is not listed in %v", releaseID, claimIDs(page))
		return ClaimEntry{}
	}
	if item := entryOf(scenario.source.releaseID); item.Album == nil || item.Album.Title != "同专辑的另一个版本" {
		t.Errorf("the album title with no visible member = %+v", item.Album)
	}
	if item := entryOf(scenario.sibling.releaseID); item.Album == nil || item.Album.Title != "我改过的标题" || !item.Holds.Album {
		t.Errorf("the hidden sibling's album = %+v, holds %+v; want the other release's title", item.Album, item.Holds)
	}
	// 有可见成员时，专辑的名字取可见的代表版本，哪怕不可见的成员年份更早：先加一个年份更早的不可见成员，再让兄弟重新可见。
	early := fixture.hiddenRelease("年份更早但不可见的成员")
	fixture.exec(`UPDATE releases SET group_id=$2::uuid, year=1980 WHERE id=$1::uuid`, early.releaseID, scenario.album)
	fixture.exec(`UPDATE tracks SET source_status='present' WHERE medium_id=$1::uuid`, scenario.sibling.mediumID)
	var withVisible ClaimEntry
	for _, item := range fixture.listClaims(1, 10).Items {
		if item.Release.ID == scenario.source.releaseID {
			withVisible = item
		}
	}
	if withVisible.Album == nil || withVisible.Album.Title != "同专辑的另一个版本" {
		t.Errorf("the album title with a visible member = %+v; want the visible member's title even though a hidden one is earlier", withVisible.Album)
	}
	fixture.exec(`UPDATE tracks SET source_status='missing' WHERE medium_id=$1::uuid`, scenario.sibling.mediumID)
	fixture.exec(`UPDATE releases SET group_id=$2::uuid WHERE id=$1::uuid`, early.releaseID, early.groupID)

	// 没有可见成员时仍按代表版本规则取名（默认版本、年份最早、创建时间），与有可见成员时连续，专辑的名字不会因为可见性变化而突然换掉；
	// 不取旧版本自己：它的名字已经显示在行的标题里。再加一个年份更早的不可见成员，它成为这张专辑的名字。
	older := fixture.hiddenRelease("年份更早的不可见成员")
	fixture.exec(`UPDATE releases SET group_id=$2::uuid, year=1990 WHERE id=$1::uuid`, older.releaseID, scenario.album)
	var source ClaimEntry
	for _, item := range fixture.listClaims(1, 10).Items {
		if item.Release.ID == scenario.source.releaseID {
			source = item
		}
	}
	if source.Album == nil || source.Album.Title != "年份更早的不可见成员" || source.Album.OtherReleaseCount != 2 {
		t.Errorf("the album with no visible member = %+v; want the member that comes first by the representative rule and two others", source.Album)
	}
}

// 位置只给扫描目录的名称（路径末段）与相对目录，不含绝对路径；不安全的目录不返回。
func TestPostgreSQLListClaimsLocationNeverLeaksAbsolutePaths(t *testing.T) {
	fixture := newCorrectionFixture(t)
	for _, testCase := range []struct {
		directory string
		want      string
	}{{"album/disc 1", "album/disc 1"}, {"../outside", ""}, {"/etc", ""}, {"C:/x", ""}, {"", ""}} {
		release, _ := fixture.issueRelease("旧版本")
		fixture.mustCorrect(release.releaseID, 1, map[string]CorrectionChange{"genre": {Mode: CorrectionSet, Text: "古典"}})
		fixture.exec(`UPDATE tracks SET source_status='missing' WHERE medium_id=$1::uuid`, release.mediumID)
		fixture.exec(`UPDATE releases SET relative_directory=$2 WHERE id=$1::uuid`, release.releaseID, testCase.directory)
		var location ClaimLocation
		for _, item := range fixture.listClaims(1, 100).Items {
			if item.Release.ID == release.releaseID {
				location = item.Location
			}
		}
		if location.RootName != "library" || location.Directory != testCase.want {
			t.Errorf("directory %q -> %+v, want root library and directory %q", testCase.directory, location, testCase.want)
		}
	}
}

// 详情：修正明细（意图与扫描值）、同专辑的别的发行（含是否可见）、作为默认版本的专辑、核对（含核对人与时间）与推荐。
// 不满足判定的版本是 ErrReleaseNotFound。
func TestPostgreSQLLoadClaim(t *testing.T) {
	scenario := newClaimScenario(t)
	fixture := scenario.fixture
	detail, err := fixture.store.LoadClaim(context.Background(), scenario.source.releaseID)
	if err != nil {
		t.Fatalf("load the claim: %v", err)
	}
	if detail.Release.ID != scenario.source.releaseID || detail.Revision != fixture.revisionOf(scenario.source.releaseID) {
		t.Errorf("detail = %+v", detail.ClaimEntry)
	}
	if len(detail.Corrections) != 2 || detail.Corrections[0].Field != "title" || detail.Corrections[0].Correction.Value != "我改过的标题" ||
		detail.Corrections[0].ScanValue != "旧版本" || detail.Corrections[1].Field != "genre" {
		t.Errorf("corrections = %+v", detail.Corrections)
	}
	if len(detail.Siblings) != 1 || detail.Siblings[0].ID != scenario.sibling.releaseID || !detail.Siblings[0].Visible {
		t.Errorf("siblings = %+v", detail.Siblings)
	}
	if len(detail.DefaultOf) != 1 || detail.DefaultOf[0].ID != scenario.album || detail.DefaultOf[0].Title != "同专辑的另一个版本" {
		t.Errorf("default of = %+v", detail.DefaultOf)
	}
	if len(detail.Reviews) != 2 || detail.Reviews[0].Issue != IssueSourceType || detail.Reviews[1].Issue != IssueGrouping ||
		detail.Reviews[0].Reviewer.ID != fixture.actorID || detail.Reviews[0].ReviewedAt.IsZero() {
		t.Errorf("reviews = %+v", detail.Reviews)
	}
	if detail.Suggestions == nil || len(detail.Suggestions) != 0 {
		t.Errorf("suggestions = %+v, want an empty non-nil list", detail.Suggestions)
	}

	// 推荐：新版本在同一目录里。
	fixture.exec(`UPDATE releases SET relative_directory='album' WHERE id IN ($1::uuid, $2::uuid)`, scenario.source.releaseID, scenario.target.releaseID)
	detail, err = fixture.store.LoadClaim(context.Background(), scenario.source.releaseID)
	if err != nil || len(detail.Suggestions) != 1 || detail.Suggestions[0].Release.ID != scenario.target.releaseID ||
		!reflect.DeepEqual(detail.Suggestions[0].Reasons, []string{ClaimReasonSameDirectory}) {
		t.Errorf("suggestions = %+v, %v", detail.Suggestions, err)
	}

	// 不满足判定：可见的版本、什么人工状态都没有的版本、不存在的版本。
	for name, id := range map[string]string{"visible": scenario.sibling.releaseID, "plain hidden": fixture.hiddenRelease("什么都没有").releaseID, "unknown": "00000000-0000-4000-8000-000000000009"} {
		if _, err := fixture.store.LoadClaim(context.Background(), id); !errors.Is(err, ErrReleaseNotFound) {
			t.Errorf("%s: error = %v, want ErrReleaseNotFound", name, err)
		}
	}
	// 认领之后旧版本不再待认领。
	scenario.mustClaim(t)
	if _, err := fixture.store.LoadClaim(context.Background(), scenario.source.releaseID); !errors.Is(err, ErrReleaseNotFound) {
		t.Errorf("after the claim: error = %v, want ErrReleaseNotFound", err)
	}
	if page := fixture.listClaims(1, 10); page.Total != 0 {
		t.Errorf("after the claim the list has %d entries", page.Total)
	}
}

// 提示：把可见版本列为推荐的待认领旧版本，最多 3 个，最近看到的在前；命中条件与推荐一致，不受推荐 5 个上限的限制；
// 不可见或不存在的版本是 ErrReleaseNotFound。
func TestPostgreSQLLoadClaimHints(t *testing.T) {
	fixture := newCorrectionFixture(t)
	newer := fixture.versionOf("新版本", 2020)
	fixture.exec(`UPDATE releases SET relative_directory='dir' WHERE id=$1::uuid`, newer.releaseID)
	fixture.trackAt(newer, "dir/new-01.flac", "present")

	// 五个旧版本都命中这个新版本：两个同一目录、两个靠归组依据里记着的文件、一个两者都中；最多提示 3 个，最近看到的在前。
	var olds []placementRelease
	for index := 0; index < 5; index++ {
		old := fixture.hiddenRelease(fmt.Sprintf("旧版本 %d", index))
		fixture.exec(`UPDATE tracks SET relative_path=$2 WHERE medium_id=$1::uuid`, old.mediumID, fmt.Sprintf("gone/%d.flac", index))
		fixture.mustReviewDirect(old.releaseID)
		switch index % 3 {
		case 0: // 同一目录
			fixture.exec(`UPDATE releases SET relative_directory='dir' WHERE id=$1::uuid`, old.releaseID)
		case 1: // 归组依据里记着新版本的文件
			fixture.exec(`UPDATE release_grouping_evidence SET source_refs='["dir/new-01.flac"]'::jsonb WHERE release_id=$1::uuid`, old.releaseID)
		default: // 两者都中
			fixture.exec(`UPDATE releases SET relative_directory='dir' WHERE id=$1::uuid`, old.releaseID)
			fixture.exec(`UPDATE release_grouping_evidence SET source_refs='["dir/new-01.flac"]'::jsonb WHERE release_id=$1::uuid`, old.releaseID)
		}
		fixture.exec(`UPDATE tracks SET observed_at = TIMESTAMPTZ '2026-06-01 12:00:00+00' - $2::interval WHERE medium_id=$1::uuid`, old.mediumID, fmt.Sprintf("%d days", 5-index))
		olds = append(olds, old)
	}
	// 一个不命中的旧版本，和一个已经没有人工状态的命中旧版本。
	unrelated := fixture.hiddenRelease("无关的旧版本")
	fixture.mustReviewDirect(unrelated.releaseID)
	fixture.hiddenRelease("同一目录但什么人工状态都没有")

	hints, err := fixture.store.LoadClaimHints(context.Background(), newer.releaseID)
	if err != nil {
		t.Fatalf("load hints: %v", err)
	}
	// index 越大越新：4、3、2 最新的三个。
	if want := []string{olds[4].releaseID, olds[3].releaseID, olds[2].releaseID}; len(hints) != 3 || hints[0].ID != want[0] || hints[1].ID != want[1] || hints[2].ID != want[2] {
		t.Fatalf("hints = %+v, want the three most recently seen %v", hints, want)
	}
	if hints[0].Title != "旧版本 4" {
		t.Errorf("hint title = %q", hints[0].Title)
	}
	// 与推荐一致：提示里的旧版本，它的详情推荐里都有这个新版本。
	for _, hint := range hints {
		detail, err := fixture.store.LoadClaim(context.Background(), hint.ID)
		if err != nil {
			t.Fatalf("load the claim of a hint: %v", err)
		}
		found := false
		for _, suggestion := range detail.Suggestions {
			found = found || suggestion.Release.ID == newer.releaseID
		}
		if !found {
			t.Errorf("hint %s does not list the new release among its suggestions %+v", hint.ID, detail.Suggestions)
		}
	}

	// 只命中新版本的一首缺失曲目不算：那首曲目已经不在它现在的文件里。
	partial := fixture.versionOf("有缺失曲目的新版本", 2022)
	fixture.exec(`UPDATE releases SET relative_directory='partial' WHERE id=$1::uuid`, partial.releaseID)
	fixture.trackAt(partial, "partial/now.flac", "present")
	// 整体换名之后的旧曲目：缺失，但仍持有来源身份（由完整扫描标为缺失，身份没有被新曲目腾出）。
	fixture.exec(`INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,source_identity,duration_seconds)
		VALUES(gen_random_uuid(),$1::uuid,9,'缺失但持有身份','艺术家',1,$2::uuid,'lost/gone.flac','missing',NOW(),'physical',$3,100)`,
		partial.mediumID, fixture.rootID, fmt.Sprintf("%s:physical:v1:lost/gone.flac", fixture.rootID))
	lost := fixture.hiddenRelease("路径只对上缺失曲目的旧版本")
	fixture.exec(`UPDATE tracks SET relative_path='elsewhere/x.flac' WHERE medium_id=$1::uuid`, lost.mediumID)
	fixture.mustReviewDirect(lost.releaseID)
	fixture.exec(`UPDATE release_grouping_evidence SET source_refs='["lost/gone.flac"]'::jsonb WHERE release_id=$1::uuid`, lost.releaseID)
	if none, err := fixture.store.LoadClaimHints(context.Background(), partial.releaseID); err != nil || len(none) != 0 {
		t.Errorf("hints for a release whose only match is a missing track = %+v, %v; want none", none, err)
	}

	// 没有命中的版本没有提示（空切片，不是 nil）；不可见或不存在的版本是 not found。
	other := fixture.versionOf("别的新版本", 2021)
	fixture.exec(`UPDATE releases SET relative_directory='elsewhere' WHERE id=$1::uuid`, other.releaseID)
	if none, err := fixture.store.LoadClaimHints(context.Background(), other.releaseID); err != nil || none == nil || len(none) != 0 {
		t.Errorf("hints without a match = %#v, %v", none, err)
	}
	for name, id := range map[string]string{"hidden": unrelated.releaseID, "unknown": "00000000-0000-4000-8000-000000000009"} {
		if _, err := fixture.store.LoadClaimHints(context.Background(), id); !errors.Is(err, ErrReleaseNotFound) {
			t.Errorf("%s: error = %v, want ErrReleaseNotFound", name, err)
		}
	}
	// 认领之后这个旧版本不再提示。
	scenarioFixture := &claimScenario{fixture: fixture, source: olds[4], target: newer}
	scenarioFixture.mustClaim(t)
	again, err := fixture.store.LoadClaimHints(context.Background(), newer.releaseID)
	if err != nil || len(again) != 3 || again[0].ID != olds[3].releaseID {
		t.Errorf("hints after claiming one = %+v, %v", again, err)
	}
}

// mustReviewDirect 直接写一条核对行，让不可见的发行留有人工状态（受治理的写入对不可见的版本是 404）。
func (fixture *correctionFixture) mustReviewDirect(releaseID string) {
	fixture.t.Helper()
	fixture.groupingConflict(releaseID, `["x/01.flac"]`)
	fixture.reviewIssue(releaseID, "grouping")
}
