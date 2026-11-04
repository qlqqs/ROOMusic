package catalog

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type personalCountingQueryer struct {
	Queryer
	queries int
}

func (q *personalCountingQueryer) QueryContext(ctx context.Context, statement string, args ...any) (*sql.Rows, error) {
	q.queries++
	return q.Queryer.QueryContext(ctx, statement, args...)
}

func (q *personalCountingQueryer) QueryRowContext(ctx context.Context, statement string, args ...any) *sql.Row {
	q.queries++
	return q.Queryer.QueryRowContext(ctx, statement, args...)
}

func TestPostgreSQLLoadTrackFacts(t *testing.T) {
	f := newPlacementFixture(t)
	f.database.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	r, moved := f.release("原发行"), f.release("新发行")
	present := f.track(r, "存在", "present", 245.5)
	missing := f.track(r, "缺失", "missing", 10)
	unknown := uuid.New()
	// 轻量事实不计算完整摘要，坏封面不得妨碍存在性与统计读取。
	f.artwork(r, "../private-cover.jpg")
	input := []string{present, strings.ToUpper(present), present, missing, unknown, "invalid"}
	unknownDurations := []string{}
	for _, value := range []any{nil, 0, -1, "NaN", "Infinity", "-Infinity"} {
		id := f.track(r, "未知时长", "missing", value)
		input = append(input, id)
		unknownDurations = append(unknownDurations, id)
	}
	q := &personalCountingQueryer{Queryer: f.database}
	got, err := NewStore(q).LoadTrackFacts(ctx, input)
	if err != nil || len(got) != 8 || q.queries != 1 {
		t.Fatalf("batched facts: len=%d queries=%d err=%v", len(got), q.queries, err)
	}
	for _, id := range []string{present, missing} {
		fact := got[id]
		if fact.TrackID != id || fact.ReleaseID != r.releaseID || fact.GroupID != r.groupID || fact.Present != (id == present) || fact.DurationSeconds == nil {
			t.Fatalf("current fact: %+v", fact)
		}
	}
	if *got[present].DurationSeconds != 245.5 || *got[missing].DurationSeconds != 10 {
		t.Fatalf("duration changed: %+v", got)
	}
	for _, id := range unknownDurations {
		if got[id].DurationSeconds != nil {
			t.Fatalf("unsafe duration: %+v", got[id])
		}
	}
	if _, exists := got[unknown]; exists {
		t.Fatal("unknown track acquired an identity")
	}
	f.exec(`UPDATE tracks SET medium_id=$2::uuid,source_status='present' WHERE id=$1::uuid`, missing, moved.mediumID)
	f.exec(`UPDATE releases SET group_id=$2::uuid WHERE id=$1::uuid`, moved.releaseID, r.groupID)
	got, err = f.store.LoadTrackFacts(ctx, []string{missing})
	if err != nil || !got[missing].Present || got[missing].ReleaseID != moved.releaseID || got[missing].GroupID != r.groupID {
		t.Fatalf("restored current placement: %+v %v", got, err)
	}
	// 单连接可立即再次查询，证明上一批已释放 rows。
	if err := f.database.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPostgreSQLLoadReleaseGroupSummaries(t *testing.T) {
	f := newEffectiveFixture(t)
	f.database.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	a, b, hidden, plain := f.visibleRelease("早版"), f.visibleRelease("晚版"), f.release("不可见版"), f.visibleRelease("无封面")
	f.track(hidden, "缺失", "missing", 100)
	f.exec(`UPDATE releases SET year=1980 WHERE id=$1::uuid`, a.releaseID)
	f.exec(`UPDATE releases SET group_id=$2::uuid,year=2000 WHERE id=$1::uuid`, b.releaseID, a.groupID)
	f.artwork(a, "safe-cover.jpg")
	f.artwork(hidden, "../hidden-private.jpg")
	f.correct(a.releaseID, "title", "修正的早版")
	f.correct(a.releaseID, "album_artist", "修正的艺人")
	unknown := uuid.New()
	input := []string{a.groupID, b.groupID, hidden.groupID, plain.groupID, unknown, strings.ToUpper(a.groupID), "invalid"}
	q := &personalCountingQueryer{Queryer: f.database}
	got, err := NewStore(q).LoadReleaseGroupSummaries(ctx, input)
	if err != nil || len(got) != 4 || q.queries != 1 {
		t.Fatalf("batch groups: %+v queries=%d err=%v", got, q.queries, err)
	}
	want, err := f.store.LoadReleaseGroup(ctx, a.groupID)
	if err != nil || !got[a.groupID].Available || got[a.groupID].ReleaseCount != 2 || !reflect.DeepEqual(got[a.groupID].Representative, &want.Representative) {
		t.Fatalf("representative mismatch: %+v, %+v, %v", got[a.groupID], want, err)
	}
	if got[a.groupID].Representative.Title != "修正的早版" || got[a.groupID].Representative.Artist != "修正的艺人" || got[a.groupID].Representative.Artwork == nil {
		t.Fatalf("effective values or safe artwork lost: %+v", got[a.groupID].Representative)
	}
	if !got[plain.groupID].Available || got[plain.groupID].Representative.Artwork != nil {
		t.Fatalf("plain group: %+v", got[plain.groupID])
	}
	for _, id := range []string{b.groupID, hidden.groupID} {
		if got[id] != (PersonalReleaseGroupSummary{GroupID: id}) {
			t.Fatalf("unavailable group exposed a release: %+v", got[id])
		}
		if _, err := f.store.LoadReleaseGroup(ctx, id); !errors.Is(err, ErrReleaseGroupNotFound) {
			t.Fatalf("legacy group visibility changed: %v", err)
		}
	}
	if _, exists := got[unknown]; exists {
		t.Fatal("unknown group acquired an identity")
	}

	// 指定默认、不可见默认、移出组的默认都复用原代表排序。
	f.exec(`UPDATE release_groups SET default_release_id=$2::uuid WHERE id=$1::uuid`, a.groupID, b.releaseID)
	assertPersonalRepresentative(t, f.store, ctx, a.groupID, b.releaseID, 2)
	f.exec(`UPDATE tracks SET source_status='missing' WHERE medium_id=$1::uuid`, b.mediumID)
	assertPersonalRepresentative(t, f.store, ctx, a.groupID, a.releaseID, 1)
	f.exec(`UPDATE tracks SET source_status='present' WHERE medium_id=$1::uuid`, b.mediumID)
	f.exec(`UPDATE releases SET group_id=$2::uuid WHERE id=$1::uuid`, b.releaseID, b.groupID)
	assertPersonalRepresentative(t, f.store, ctx, a.groupID, a.releaseID, 1)
	assertPersonalRepresentative(t, f.store, ctx, b.groupID, b.releaseID, 1)

	f.exec(`UPDATE tracks SET source_status='missing' WHERE medium_id=$1::uuid`, a.mediumID)
	got, err = f.store.LoadReleaseGroupSummaries(ctx, []string{a.groupID})
	if err != nil || got[a.groupID] != (PersonalReleaseGroupSummary{GroupID: a.groupID}) {
		t.Fatalf("last visible member disappeared: %+v %v", got, err)
	}
	f.exec(`UPDATE tracks SET source_status='present' WHERE medium_id=$1::uuid`, a.mediumID)
	assertPersonalRepresentative(t, f.store, ctx, a.groupID, a.releaseID, 1)
	// 非请求组以及不可见成员的坏封面不影响本批；请求的可见代表则必须安全失败。
	f.exec(`UPDATE release_artworks SET storage_key='../visible-private.jpg' WHERE release_id=$1::uuid`, a.releaseID)
	failed, err := f.store.LoadReleaseGroupSummaries(ctx, []string{a.groupID, plain.groupID})
	if err == nil || failed != nil || strings.Contains(err.Error(), "visible-private") {
		t.Fatalf("unsafe representative not rejected safely: %+v %v", failed, err)
	}
	if err := f.database.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
}

func assertPersonalRepresentative(t *testing.T, s *Store, ctx context.Context, groupID, releaseID string, count int) {
	t.Helper()
	got, err := s.LoadReleaseGroupSummaries(ctx, []string{groupID})
	if err != nil {
		t.Fatal(err)
	}
	item, ok := got[groupID]
	if !ok || !item.Available || item.Representative == nil || item.Representative.ID != releaseID || item.ReleaseCount != count {
		t.Fatalf("unexpected representative: %+v, want %s count=%d", item, releaseID, count)
	}
}

func TestPostgreSQLPersonalReadsBoundsAndSnapshot(t *testing.T) {
	f := newPlacementFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	r := f.release("快照")
	id := f.track(r, "曲目", "present", 100)
	tracks := personalTestIDs(9999)
	tracks = append(tracks, id, strings.ToUpper(id), "invalid")
	if got, err := f.store.LoadTrackFacts(ctx, tracks); err != nil || len(got) != 1 {
		t.Fatalf("10000 facts IDs: len=%d err=%v", len(got), err)
	}
	groups := personalTestIDs(999)
	groups = append(groups, r.groupID, strings.ToUpper(r.groupID), "invalid")
	if got, err := f.store.LoadReleaseGroupSummaries(ctx, groups); err != nil || len(got) != 1 {
		t.Fatalf("1000 groups IDs: len=%d err=%v", len(got), err)
	}
	tx, err := f.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	snapshot := NewStore(tx)
	if _, err := snapshot.LoadTrackFacts(ctx, []string{id}); err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE tracks SET source_status='missing' WHERE id=$1::uuid`, id)
	facts, err := snapshot.LoadTrackFacts(ctx, []string{id})
	if err != nil || !facts[id].Present {
		t.Fatalf("mixed fact snapshot: %+v %v", facts, err)
	}
	assertPersonalRepresentative(t, snapshot, ctx, r.groupID, r.releaseID, 1)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	current, err := f.store.LoadReleaseGroupSummaries(ctx, []string{r.groupID})
	if err != nil || current[r.groupID].Available {
		t.Fatalf("latest visibility: %+v %v", current, err)
	}

	cancelled, stop := context.WithCancel(ctx)
	stop()
	if got, err := f.store.LoadTrackFacts(cancelled, []string{id}); !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("facts cancellation: %+v %v", got, err)
	}
	if got, err := f.store.LoadReleaseGroupSummaries(cancelled, []string{r.groupID}); !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("groups cancellation: %+v %v", got, err)
	}
}

func TestPostgreSQLPersonalReleaseGroupOrderAndFullCount(t *testing.T) {
	f := newEffectiveFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	a, b, c := f.visibleRelease("同年新版"), f.visibleRelease("同年旧版"), f.visibleRelease("无年份")
	f.exec(`UPDATE releases SET group_id=$1::uuid,year=1990,created_at='2020-01-02' WHERE id IN($2::uuid,$3::uuid,$4::uuid)`, a.groupID, a.releaseID, b.releaseID, c.releaseID)
	f.exec(`UPDATE releases SET year=NULL,created_at='2000-01-01' WHERE id=$1::uuid`, c.releaseID)
	f.exec(`UPDATE releases SET created_at='2020-01-01' WHERE id=$1::uuid`, b.releaseID)
	assertPersonalRepresentative(t, f.store, ctx, a.groupID, b.releaseID, 3)
	f.exec(`UPDATE releases SET created_at='2020-01-01' WHERE id=$1::uuid`, a.releaseID)
	firstID := a.releaseID
	if b.releaseID < firstID {
		firstID = b.releaseID
	}
	assertPersonalRepresentative(t, f.store, ctx, a.groupID, firstID, 3)
	f.correct(c.releaseID, "year", 1980)
	assertPersonalRepresentative(t, f.store, ctx, a.groupID, c.releaseID, 3)
	f.clearCorrection(c.releaseID, "year")
	f.exec(`UPDATE releases SET year=0 WHERE id=$1::uuid`, c.releaseID)
	assertPersonalRepresentative(t, f.store, ctx, a.groupID, firstID, 3)

	// 计数不受旧组详情的 100 条成员截断影响，也不因存在缺失成员而多计。
	f.exec(`WITH new_releases AS (
	 INSERT INTO releases(id,group_id,title,artist,year,source_root_id,relative_directory,candidate_anchor,candidate_kind)
	 SELECT md5('personal-many-release-'||i)::uuid,$1::uuid,'批量成员','艺人',2026,$2::uuid,'many-'||i,'many:'||i,'ordinary_directory'
	 FROM generate_series(1,120) i RETURNING id
	), new_media AS (
	 INSERT INTO media(id,release_id,position,title)
	 SELECT id,id,1,'介质' FROM new_releases RETURNING id
	)
	INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind)
	 SELECT id,id,1,'曲目','艺人',1,$2::uuid,id::text||'.flac','present',NOW(),'physical' FROM new_media`, a.groupID, f.rootID)
	assertPersonalRepresentative(t, f.store, ctx, a.groupID, firstID, 123)
	f.exec(`UPDATE tracks SET source_status='missing' WHERE medium_id=$1::uuid`, c.mediumID)
	assertPersonalRepresentative(t, f.store, ctx, a.groupID, firstID, 122)
}

func TestPostgreSQLPersonalBatchSummaryParity(t *testing.T) {
	f := newEffectiveFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	r, hidden := f.release("批量摘要"), f.release("全缺失摘要")
	present := f.track(r, "存在曲目", "present", 100)
	f.track(r, "同介质另一首", "present", 200)
	secondMedium := placementRelease{releaseID: r.releaseID, groupID: r.groupID, mediumID: uuid.New()}
	f.exec(`INSERT INTO media(id,release_id,position,title) VALUES($1::uuid,$2::uuid,2,'第二碟')`, secondMedium.mediumID, r.releaseID)
	f.track(secondMedium, "另一碟", "present", 300)
	missing := f.track(r, "旧来源之一", "missing", 100)
	duplicate := f.track(secondMedium, "同一旧来源", "missing", 100)
	f.exec(`UPDATE tracks SET relative_path='shared-missing.flac' WHERE id IN($1::uuid,$2::uuid)`, missing, duplicate)
	bound := f.track(r, "有绑定身份", "missing", 100)
	f.exec(`UPDATE tracks SET source_identity='synthetic-bound-source' WHERE id=$1::uuid`, bound)
	hiddenTrack := f.track(hidden, "全缺失曲目", "missing", 200)
	f.artwork(r, "parity-cover.jpg")
	for _, field := range []string{"title", "artist", "source_type", "media_type"} {
		f.uncertainField(r.releaseID, field, "值", []string{"值", "其他值"})
	}
	f.groupingConflict(r.releaseID, `["synthetic/one.flac","synthetic/two.flac"]`)

	check := func(stage string, attention int64) {
		t.Helper()
		got, err := f.store.LoadTrackSummaries(ctx, []string{present, missing, duplicate, bound, hiddenTrack})
		if err != nil || len(got) != 5 {
			t.Fatalf("%s: summaries=%d err=%v", stage, len(got), err)
		}
		for _, item := range got {
			// 对全缺失发行只取旧摘要投影作等价性对照，不扩大旧公开详情可见性。
			want, err := scanReleaseSummary(f.database.QueryRowContext(ctx, "SELECT "+releasesql.ReleaseSummaryProjection+
				" FROM "+releasesql.ReleaseSource+releasesql.ReleaseSummaryArtworkJoin+" WHERE releases.id=$1::uuid", item.Release.ID))
			if err != nil || !reflect.DeepEqual(item.Release, want) {
				t.Fatalf("%s: batch=%+v baseline=%+v err=%v", stage, item.Release, want, err)
			}
		}
		if got[present].Release.MediumCount != 2 || got[present].Release.TrackCount != 3 || got[present].Release.AttentionCount != attention {
			t.Fatalf("%s: counts=%+v want 2/3/%d", stage, got[present].Release, attention)
		}
		if got[hiddenTrack].Release.MediumCount != 0 || got[hiddenTrack].Release.TrackCount != 0 {
			t.Fatalf("%s: hidden counts=%+v", stage, got[hiddenTrack].Release)
		}
		groups, err := f.store.LoadReleaseGroupSummaries(ctx, []string{r.groupID, hidden.groupID})
		release := got[present].Release
		if err != nil || !reflect.DeepEqual(groups[r.groupID].Representative, &release) || groups[hidden.groupID].Available || groups[hidden.groupID].Representative != nil {
			t.Fatalf("%s: group projection parity: %+v %v", stage, groups, err)
		}
	}
	check("未修正或核对", 7) // 四字段、一归组、按来源去重的两条缺失。
	f.correct(r.releaseID, "title", "确认标题")
	f.correct(r.releaseID, "album_artist", "确认艺人")
	f.reviewIssue(r.releaseID, "source_type")
	f.reviewIssue(r.releaseID, "grouping")
	f.reviewIssue(r.releaseID, "missing_tracks")
	check("有效修正与核对", 1) // 仅介质类型仍待处理。
	f.exec(`UPDATE tracks SET observed_at=observed_at+INTERVAL '1 microsecond' WHERE id=$1::uuid`, duplicate)
	check("同来源一条轨道核对过期", 2)
	f.groupingConflict(r.releaseID, `["synthetic/changed.flac"]`)
	f.uncertainField(r.releaseID, "source_type", "新值", []string{"新值", "其他值"})
	check("字段与归组核对过期", 4)
	f.exec(`UPDATE tracks SET observed_at=observed_at+INTERVAL '1 microsecond' WHERE id=$1::uuid`, bound)
	check("绑定来源核对过期", 5)
}
