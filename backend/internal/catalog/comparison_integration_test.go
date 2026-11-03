package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// countingQueryer 数出 Store 发给数据库的查询条数，用来证明版本对比的查询数量不随版本数增长。
type countingQueryer struct {
	Queryer
	queries atomic.Int64
}

func (counter *countingQueryer) QueryContext(ctx context.Context, query string, arguments ...any) (*sql.Rows, error) {
	counter.queries.Add(1)
	return counter.Queryer.QueryContext(ctx, query, arguments...)
}

func (counter *countingQueryer) QueryRowContext(ctx context.Context, query string, arguments ...any) *sql.Row {
	counter.queries.Add(1)
	return counter.Queryer.QueryRowContext(ctx, query, arguments...)
}

// intoGroup 把发行放进另一个组（直接写库：这里测的是读取，不是归组流程）。
func (fixture *effectiveFixture) intoGroup(release placementRelease, groupID string) {
	fixture.t.Helper()
	fixture.exec(`UPDATE releases SET group_id=$2::uuid WHERE id=$1::uuid`, release.releaseID, groupID)
}

// audio 给曲目写上编码、位深与采样率；nil 表示这一项没有值。
func (fixture *effectiveFixture) audio(trackID string, codec any, bitDepth, sampleRate any) {
	fixture.t.Helper()
	fixture.exec(`UPDATE tracks SET codec=$2, bit_depth=$3, sample_rate=$4 WHERE id=$1::uuid`, trackID, codec, bitDepth, sampleRate)
}

// secondMedium 给发行加第二个介质，返回只带这个介质的占位（供 track 使用）。
func (fixture *effectiveFixture) secondMedium(release placementRelease) placementRelease {
	fixture.t.Helper()
	second := placementRelease{groupID: release.groupID, releaseID: release.releaseID, mediumID: uuid.New()}
	fixture.exec(`INSERT INTO media(id,release_id,position,title) VALUES($1::uuid,$2::uuid,2,'Medium 2')`, second.mediumID, release.releaseID)
	return second
}

func (fixture *effectiveFixture) comparison(groupID string) ReleaseComparison {
	fixture.t.Helper()
	comparison, err := fixture.store.LoadReleaseComparison(context.Background(), groupID)
	if err != nil {
		fixture.t.Fatalf("compare group %s: %v", groupID, err)
	}
	return comparison
}

func comparedIDs(comparison ReleaseComparison) []string {
	ids := make([]string, len(comparison.Releases))
	for index, release := range comparison.Releases {
		ids[index] = release.ID
	}
	return ids
}

// 对比读取的内容：有效值（含人工修正与修正为空）、只计 present 曲目的介质、曲目与时长（有一首曲目的时长未知就是 null，
// 不拿已知部分冒充总数）、格式分组与排序（曲目数降序、再按编码、位深、采样率；未知的参数是 null），以及 corrected_fields。
func TestPostgreSQLReleaseComparisonReportsEffectiveValuesAndContent(t *testing.T) {
	fixture := newEffectiveFixture(t)

	// A：两个介质，四首 present（FLAC 16/44.1k，时长都已知）和一首 missing（不计入）；有版本说明、厂牌等字段。
	a := fixture.versionOf("Alpha", 1998)
	fixture.exec(`UPDATE releases SET edition='Remastered', label='Label A', catalog_number='CAT-A', barcode='111', genre='Classical', source_type='physical', media_type='cd' WHERE id=$1::uuid`, a.releaseID)
	first := fixture.track(a, "A1", "present", 100.5)
	second := fixture.track(a, "A2", "present", 200.0)
	secondMedium := fixture.secondMedium(a)
	third := fixture.track(secondMedium, "A3", "present", 50.0)
	gone := fixture.track(secondMedium, "A4", "missing", 999.0)
	for _, track := range []string{first, second, third, gone} {
		fixture.audio(track, "flac", 16, 44100)
	}
	// 发行自带的那首曲目（visibleRelease 造的，versionOf 里已有一首 100 秒的 present 曲目）也是 FLAC 16/44.1k。
	fixture.exec(`UPDATE tracks SET codec='flac', bit_depth=16, sample_rate=44100 WHERE medium_id=$1::uuid AND title='曲目'`, a.mediumID)

	// B：一个介质；三首 ALAC 24/96k、一首时长为零的 FLAC 16/44.1k（零是未知）、一首时长为空且编码只有空白、采样率为零的曲目（参数未知）；
	// 有两首曲目的时长未知，所以总时长是 null，而不是已知部分之和 700 秒。
	b := fixture.versionOf("Beta", 2005)
	alac1, alac2, zero, blank := fixture.track(b, "B1", "present", 300.0), fixture.track(b, "B2", "present", 300.0), fixture.track(b, "B3", "present", 0), fixture.track(b, "B4", "present", nil)
	fixture.audio(alac1, "alac", 24, 96000)
	fixture.audio(alac2, "alac", 24, 96000)
	fixture.audio(zero, "flac", 16, 44100)
	fixture.audio(blank, "   ", nil, 0)
	fixture.exec(`UPDATE tracks SET codec='alac', bit_depth=24, sample_rate=96000 WHERE medium_id=$1::uuid AND title='曲目'`, b.mediumID)

	// C：没有年份，所有曲目的参数都未知，时长也都不可用：零、空、负数与非有限值都当作未知，所以总时长是 null 而不是 0。
	c := fixture.versionOf("Gamma", 1)
	fixture.exec(`UPDATE releases SET year=NULL WHERE id=$1::uuid`, c.releaseID)
	fixture.exec(`UPDATE tracks SET duration_seconds=0 WHERE medium_id=$1::uuid`, c.mediumID)
	for index, duration := range []any{nil, -3.0, "NaN", "Infinity"} {
		fixture.track(c, fmt.Sprintf("C%d", index+2), "present", duration)
	}

	// D：没有任何 present 曲目，不是可见版本。
	d := fixture.release("Hidden")
	fixture.track(d, "D1", "missing", 10.0)

	group := a.groupID
	for _, release := range []placementRelease{b, c, d} {
		fixture.intoGroup(release, group)
	}
	// 人工修正：A 的标题改了、年份设为空；B 的厂牌改成别的值。
	fixture.correct(a.releaseID, "title", "Alpha (corrected)")
	fixture.correct(a.releaseID, "year", nil)
	fixture.correct(b.releaseID, "label", "Corrected Label")

	comparison := fixture.comparison(group)
	if comparison.GroupID != group || comparison.DefaultReleaseID != nil || comparison.ReleaseCount != 3 || len(comparison.Releases) != 3 {
		t.Fatalf("comparison = group %s, default %v, %d versions (%d listed); want group %s, no default, 3 visible versions", comparison.GroupID, comparison.DefaultReleaseID, comparison.ReleaseCount, len(comparison.Releases), group)
	}
	// 顺序与组详情相同：年份最早的在前，没有年份的最后。A 的有效年份是空，所以 B（2005）在前，A 与 C 都没有年份，按创建时间。
	order, _ := fixture.membersOf(group)
	if got := comparedIDs(comparison); !slices.Equal(got, order) {
		t.Fatalf("comparison order = %v, group detail order = %v", got, order)
	}
	byID := map[string]ComparedRelease{}
	for _, release := range comparison.Releases {
		byID[release.ID] = release
	}

	wantA := ComparedRelease{
		ID: a.releaseID, Title: "Alpha (corrected)", Artist: "艺术家", AlbumArtist: ptr("专辑艺术家"), Year: nil,
		Edition: ptr("Remastered"), Label: ptr("Label A"), CatalogNumber: ptr("CAT-A"), Barcode: ptr("111"), Genre: ptr("Classical"),
		SourceType: ptr("physical"), MediaType: ptr("cd"), MediumCount: 2, TrackCount: 4, DurationSeconds: ptr(100.0 + 100.5 + 200.0 + 50.0),
		Formats:         []AudioFormat{{Codec: ptr("flac"), BitDepth: ptr(16), SampleRate: ptr(44100), TrackCount: 4}},
		CorrectedFields: []string{"title", "year"},
	}
	if got := byID[a.releaseID]; !reflect.DeepEqual(got, wantA) {
		t.Errorf("version A = %s\nwant      %s", asJSON(got), asJSON(wantA))
	}
	wantB := ComparedRelease{
		ID: b.releaseID, Title: "Beta", Artist: "艺术家", AlbumArtist: ptr("专辑艺术家"), Year: ptr(2005),
		MediumCount: 1, TrackCount: 5, DurationSeconds: nil,
		Formats: []AudioFormat{
			{Codec: ptr("alac"), BitDepth: ptr(24), SampleRate: ptr(96000), TrackCount: 3},
			{Codec: ptr("flac"), BitDepth: ptr(16), SampleRate: ptr(44100), TrackCount: 1},
			{Codec: nil, BitDepth: nil, SampleRate: nil, TrackCount: 1},
		},
		CorrectedFields: []string{},
	}
	// B 的厂牌被修正了，所以有效值与 corrected_fields 都带上它；其余字段没有值（null）。
	wantB.Label, wantB.CorrectedFields = ptr("Corrected Label"), []string{"label"}
	if got := byID[b.releaseID]; !reflect.DeepEqual(got, wantB) {
		t.Errorf("version B = %s\nwant      %s", asJSON(got), asJSON(wantB))
	}
	wantC := ComparedRelease{
		ID: c.releaseID, Title: "Gamma", Artist: "艺术家", AlbumArtist: ptr("专辑艺术家"), Year: nil,
		MediumCount: 1, TrackCount: 5, DurationSeconds: nil,
		Formats:         []AudioFormat{{Codec: nil, BitDepth: nil, SampleRate: nil, TrackCount: 5}},
		CorrectedFields: []string{},
	}
	if got := byID[c.releaseID]; !reflect.DeepEqual(got, wantC) {
		t.Errorf("version C = %s\nwant      %s", asJSON(got), asJSON(wantC))
	}
	if _, listed := byID[d.releaseID]; listed {
		t.Error("a version without present tracks is part of the comparison")
	}

	// JSON 形状：空值是 null、列表是 []（不是 null），字段名固定。
	var shape map[string]any
	raw, _ := json.Marshal(comparison)
	if err := json.Unmarshal(raw, &shape); err != nil {
		t.Fatalf("decode the comparison JSON: %v", err)
	}
	releases := shape["releases"].([]any)
	third3 := releases[slices.Index(order, c.releaseID)].(map[string]any)
	if third3["duration_seconds"] != nil || third3["year"] != nil || third3["edition"] != nil {
		t.Errorf("unknown values are not null: %v", third3)
	}
	if len(shape) != 4 {
		t.Errorf("comparison has %d top-level fields, want group_id, default_release_id, release_count and releases", len(shape))
	}
	if len(third3) != 17 {
		t.Errorf("a compared version has %d fields, want 17: %v", len(third3), third3)
	}
}

func asJSON(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

// 默认版本在前，并且与组详情一致；默认版本失效（不可见）时顺序回到自动规则，DefaultReleaseID 为 null。
func TestPostgreSQLReleaseComparisonFollowsTheRepresentativeRule(t *testing.T) {
	fixture := newEffectiveFixture(t)
	early, middle, late := fixture.versionOf("Early", 1990), fixture.versionOf("Middle", 2000), fixture.versionOf("Late", 2010)
	group := early.groupID
	fixture.intoGroup(middle, group)
	fixture.intoGroup(late, group)

	comparison := fixture.comparison(group)
	if want := []string{early.releaseID, middle.releaseID, late.releaseID}; !slices.Equal(comparedIDs(comparison), want) || comparison.DefaultReleaseID != nil {
		t.Fatalf("automatic order = %v (default %v), want %v with no default", comparedIDs(comparison), comparison.DefaultReleaseID, want)
	}

	fixture.mustSetDefault(group, 1, late.releaseID)
	comparison = fixture.comparison(group)
	order, detail := fixture.membersOf(group)
	if want := []string{late.releaseID, early.releaseID, middle.releaseID}; !slices.Equal(comparedIDs(comparison), want) || !slices.Equal(order, want) {
		t.Fatalf("order with a default = %v (group detail %v), want %v", comparedIDs(comparison), order, want)
	}
	if comparison.DefaultReleaseID == nil || *comparison.DefaultReleaseID != late.releaseID || detail.DefaultReleaseID == nil || *detail.DefaultReleaseID != *comparison.DefaultReleaseID {
		t.Fatalf("comparison default = %v, group detail default = %v; want both %s", comparison.DefaultReleaseID, detail.DefaultReleaseID, late.releaseID)
	}

	// 默认版本不可见：对比与组详情一起回到自动规则；重新可见后一起恢复。
	fixture.setVisible(late, false)
	comparison = fixture.comparison(group)
	if want := []string{early.releaseID, middle.releaseID}; !slices.Equal(comparedIDs(comparison), want) || comparison.DefaultReleaseID != nil || comparison.ReleaseCount != 2 {
		t.Fatalf("with the default hidden: order %v, default %v, count %d; want %v, none, 2", comparedIDs(comparison), comparison.DefaultReleaseID, comparison.ReleaseCount, want)
	}
	fixture.setVisible(late, true)
	if comparison = fixture.comparison(group); comparison.DefaultReleaseID == nil || *comparison.DefaultReleaseID != late.releaseID || comparison.Releases[0].ID != late.releaseID {
		t.Fatalf("after the default is visible again: %v", comparedIDs(comparison))
	}
}

// 最多 100 个成员、release_count 是完整数量；成员与它们的顺序和组详情一致；无论多少个版本，只发三条查询。
func TestPostgreSQLReleaseComparisonIsBoundedAndUsesAFixedNumberOfQueries(t *testing.T) {
	fixture := newEffectiveFixture(t)
	const versions = 105
	first := fixture.versionOf("Version 0", 1900)
	group := first.groupID
	releases := []placementRelease{first}
	for index := 1; index < versions; index++ {
		release := fixture.versionOf(fmt.Sprintf("Version %d", index), 1900+index)
		fixture.intoGroup(release, group)
		releases = append(releases, release)
	}
	// 默认版本是第 105 个（按年份最晚）：它排在最前，挤掉最后一个自动排序的成员。
	fixture.mustSetDefault(group, 1, releases[versions-1].releaseID)

	counter := &countingQueryer{Queryer: fixture.database}
	comparison, err := NewStore(counter).LoadReleaseComparison(context.Background(), group)
	if err != nil {
		t.Fatalf("compare 105 versions: %v", err)
	}
	if len(comparison.Releases) != maxReleaseGroupMembers || comparison.ReleaseCount != versions {
		t.Fatalf("%d versions listed, release_count %d; want %d listed of %d", len(comparison.Releases), comparison.ReleaseCount, maxReleaseGroupMembers, versions)
	}
	if queries := counter.queries.Load(); queries != 3 {
		t.Fatalf("comparing %d versions took %d queries, want 3", versions, queries)
	}
	order, _ := fixture.membersOf(group)
	if got := comparedIDs(comparison); !slices.Equal(got, order) || got[0] != releases[versions-1].releaseID {
		t.Fatalf("the compared versions differ from the group detail members (or the default is not first)")
	}

	// 两个版本也是三条查询：数量不随版本数变化。
	small := fixture.versionOf("Small A", 2000)
	fixture.intoGroup(fixture.versionOf("Small B", 2001), small.groupID)
	counter = &countingQueryer{Queryer: fixture.database}
	if _, err := NewStore(counter).LoadReleaseComparison(context.Background(), small.groupID); err != nil {
		t.Fatalf("compare 1 version: %v", err)
	}
	if queries := counter.queries.Load(); queries != 3 {
		t.Fatalf("comparing a small album took %d queries, want 3", queries)
	}
}

// 一个版本里的不同音频参数组合不超过 32 个：刚好 32 个可以读，33 个让整个读取失败（不是静默截断）。
func TestPostgreSQLReleaseComparisonBoundsTheFormatsOfAVersion(t *testing.T) {
	fixture := newEffectiveFixture(t)
	release := fixture.versionOf("Many formats", 2000)
	fixture.exec(`UPDATE tracks SET codec='flac', bit_depth=16, sample_rate=44100 WHERE medium_id=$1::uuid`, release.mediumID)
	for index := 1; index < maxComparedFormats; index++ {
		fixture.audio(fixture.track(release, fmt.Sprintf("T%d", index), "present", 10.0), "flac", 16, 44100+index)
	}
	comparison := fixture.comparison(release.groupID)
	if formats := comparison.Releases[0].Formats; len(formats) != maxComparedFormats || formats[0].TrackCount != 1 {
		t.Fatalf("a version with %d formats lists %d", maxComparedFormats, len(formats))
	}
	fixture.audio(fixture.track(release, "One more", "present", 10.0), "flac", 24, 96000)
	if _, err := fixture.store.LoadReleaseComparison(context.Background(), release.groupID); err == nil || errors.Is(err, ErrReleaseGroupNotFound) {
		t.Fatalf("a version with %d formats: error = %v, want a bound failure", maxComparedFormats+1, err)
	}
}

// 组不存在、组里没有可见成员都是 ErrReleaseGroupNotFound；单版本的专辑可以读（界面只是不提供入口）。
func TestPostgreSQLReleaseComparisonReportsMissingAlbums(t *testing.T) {
	fixture := newEffectiveFixture(t)
	ctx := context.Background()
	hidden := fixture.release("Hidden only")
	fixture.track(hidden, "Gone", "missing", 10.0)
	for name, group := range map[string]string{"unknown album": uuid.New(), "album without visible versions": hidden.groupID} {
		if _, err := fixture.store.LoadReleaseComparison(ctx, group); !errors.Is(err, ErrReleaseGroupNotFound) {
			t.Errorf("%s: error = %v, want ErrReleaseGroupNotFound", name, err)
		}
	}
	only := fixture.versionOf("Only", 2000)
	if comparison := fixture.comparison(only.groupID); comparison.ReleaseCount != 1 || len(comparison.Releases) != 1 || comparison.Releases[0].ID != only.releaseID {
		t.Fatalf("single-version comparison = %+v", comparison)
	}
	// 大写的组 ID 返回规范的小写形式。
	if comparison := fixture.comparison(upper(only.groupID)); comparison.GroupID != only.groupID {
		t.Fatalf("group id in the response = %q, want the canonical %q", comparison.GroupID, only.groupID)
	}
}

func upper(value string) string {
	out := []byte(value)
	for index, char := range out {
		if char >= 'a' && char <= 'f' {
			out[index] = char - 'a' + 'A'
		}
	}
	return string(out)
}

// versionOf 造一个可见的版本：独立的组、给定的标题与年份。
func (fixture *effectiveFixture) versionOf(title string, year int) placementRelease {
	fixture.t.Helper()
	release := fixture.visibleRelease(title)
	fixture.exec(`UPDATE releases SET year=$2 WHERE id=$1::uuid`, release.releaseID, year)
	return release
}

// setVisible 让发行有或没有可见曲目：把它唯一的曲目标成 present 或 missing。
func (fixture *effectiveFixture) setVisible(release placementRelease, visible bool) {
	fixture.t.Helper()
	status := "missing"
	if visible {
		status = "present"
	}
	fixture.exec(`UPDATE tracks SET source_status=$2 WHERE medium_id=$1::uuid`, release.mediumID, status)
}

// membersOf 读取组详情里的成员顺序与生效中的默认版本。
func (fixture *effectiveFixture) membersOf(groupID string) (order []string, detail ReleaseGroupDetail) {
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

func ptr[T any](value T) *T { return &value }

// mustSetDefault 为核心读取测试准备默认版本，不调用治理流程；SQL 与治理写入使用同一条更新语句。
// 这里仅核对测试状态，设定流程的响应与操作记录仍由治理包的集成测试验证。
func (fixture *effectiveFixture) mustSetDefault(groupID string, expectedRevision int64, releaseID string) {
	fixture.t.Helper()
	fixture.exec(`UPDATE release_groups SET default_release_id=$2::uuid, edit_revision = edit_revision + 1 WHERE id=$1::uuid`, groupID, releaseID)
	var revision int64
	if err := fixture.database.QueryRow(`SELECT edit_revision FROM release_groups WHERE id=$1::uuid`, groupID).Scan(&revision); err != nil {
		fixture.t.Fatalf("read group revision: %v", err)
	}
	var stored sql.NullString
	if err := fixture.database.QueryRow(`SELECT default_release_id::text FROM release_groups WHERE id=$1::uuid`, groupID).Scan(&stored); err != nil {
		fixture.t.Fatalf("read the stored default: %v", err)
	}
	if revision != expectedRevision+1 || !stored.Valid || stored.String != releaseID {
		fixture.t.Fatalf("default fixture = %q at revision %d, want %q at revision %d", stored.String, revision, releaseID, expectedRevision+1)
	}
}
