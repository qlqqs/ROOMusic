package listening

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// listeningFixture 是隔离 schema 的已迁移测试库，加一个登记的目录；曲目、发行与用户都是合成行，不读任何文件。
type listeningFixture struct {
	database *sql.DB
	catalog  *catalog.Store
	service  *Service
	rootID   string
	counter  int
}

func newListeningFixture(t *testing.T) *listeningFixture {
	t.Helper()
	scopedURL := postgrestest.NewSchema(t, "roomusic_test_")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	database, err := postgres.Open(ctx, scopedURL)
	if err != nil {
		t.Fatalf("open isolated listening database: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close isolated listening database: %v", err)
		}
	})
	store := catalog.NewStore(database)
	fixture := &listeningFixture{database: database, catalog: store, service: NewService(database, store), rootID: uuid.New()}
	fixture.exec(t, `INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,'/library',$2,1)`, fixture.rootID, "active")
	return fixture
}

func (fixture *listeningFixture) exec(t *testing.T, statement string, arguments ...any) {
	t.Helper()
	if _, err := fixture.database.Exec(statement, arguments...); err != nil {
		t.Fatalf("exec %q: %v", statement, err)
	}
}

func (fixture *listeningFixture) count(t *testing.T, query string, arguments ...any) int {
	t.Helper()
	var count int
	if err := fixture.database.QueryRow(query, arguments...).Scan(&count); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return count
}

func (fixture *listeningFixture) user(t *testing.T) string {
	t.Helper()
	id := uuid.New()
	fixture.exec(t, `INSERT INTO users(id,username,password_hash,role) VALUES($1::uuid,$2,'test-only','user')`, id, "user-"+strings.ReplaceAll(id, "-", ""))
	return id
}

type fixtureRelease struct{ groupID, releaseID, mediumID string }

func (fixture *listeningFixture) release(t *testing.T, title string) fixtureRelease {
	t.Helper()
	release := fixtureRelease{groupID: uuid.New(), releaseID: uuid.New(), mediumID: uuid.New()}
	fixture.counter++
	fixture.exec(t, `INSERT INTO release_groups(id) VALUES($1::uuid)`, release.groupID)
	fixture.exec(t, `INSERT INTO releases(id,group_id,title,artist,source_root_id,relative_directory,candidate_anchor,candidate_kind)
		VALUES($1::uuid,$2::uuid,$3,'艺术家',$4::uuid,$5,$6,'ordinary_directory')`,
		release.releaseID, release.groupID, title, fixture.rootID, fmt.Sprintf("dir-%d", fixture.counter), fmt.Sprintf("%s:v2:%d", fixture.rootID, fixture.counter))
	fixture.exec(t, `INSERT INTO media(id,release_id,position,title) VALUES($1::uuid,$2::uuid,1,'Medium')`, release.mediumID, release.releaseID)
	return release
}

// track 在发行里加一首 present 曲目；duration 为 0 时写 NULL（扫描没有取得时长）。
func (fixture *listeningFixture) track(t *testing.T, release fixtureRelease, title string, duration float64) string {
	t.Helper()
	fixture.counter++
	id := uuid.New()
	var durationValue any
	if duration > 0 {
		durationValue = duration
	}
	fixture.exec(t, `INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,duration_seconds)
		VALUES($1::uuid,$2::uuid,$3,$4,'艺术家',1,$5::uuid,$6,'present',NOW(),'physical',$7)`,
		id, release.mediumID, fixture.counter, title, fixture.rootID, fmt.Sprintf("track-%d.flac", fixture.counter), durationValue)
	return id
}

func (fixture *listeningFixture) setStatus(t *testing.T, trackID, status string) {
	t.Helper()
	fixture.exec(t, `UPDATE tracks SET source_status=$2 WHERE id=$1::uuid`, trackID, status)
}

// playAt 记一次播放并把 played_at 改成确定的时间，让顺序断言不依赖时钟。
func (fixture *listeningFixture) playAt(t *testing.T, userID, trackID string, playedAt time.Time) {
	t.Helper()
	recorded, err := fixture.service.Record(context.Background(), userID, uuid.New(), trackID, 120)
	if err != nil {
		t.Fatalf("record a play of %s: %v", trackID, err)
	}
	fixture.exec(t, `UPDATE play_history SET played_at=$2 WHERE id=$1::uuid`, recorded.ID, playedAt)
}

var baseTime = time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)

func TestPostgreSQLRecordStoresAndReplaysByIdempotencyKey(t *testing.T) {
	fixture := newListeningFixture(t)
	ctx := context.Background()
	userA, userB := fixture.user(t), fixture.user(t)
	release := fixture.release(t, "专辑")
	track := fixture.track(t, release, "第一首", 200) // T = 100
	other := fixture.track(t, release, "第二首", 200)
	key := uuid.New()

	first, err := fixture.service.Record(ctx, userA, key, track, 120)
	if err != nil {
		t.Fatalf("first record: %v", err)
	}
	if first.ID == "" || first.TrackID != track || first.PlayedAt.IsZero() || first.PlayedAt.Location() != time.UTC {
		t.Fatalf("first record = %+v, want an ID, track %s and a UTC time", first, track)
	}
	var storedSeconds float64
	if err := fixture.database.QueryRow(`SELECT played_seconds FROM play_history WHERE id=$1::uuid`, first.ID).Scan(&storedSeconds); err != nil || storedSeconds != 120 {
		t.Fatalf("stored played_seconds = %v (error %v), want 120", storedSeconds, err)
	}

	// 同一个键、同一个请求：回放首次记录，不写新行；曲目 ID 的大小写不影响比较。
	for _, replayedTrack := range []string{track, strings.ToUpper(track)} {
		replay, err := fixture.service.Record(ctx, userA, key, replayedTrack, 120)
		if err != nil {
			t.Fatalf("replay with track %q: %v", replayedTrack, err)
		}
		if replay.ID != first.ID || replay.TrackID != first.TrackID || !replay.PlayedAt.Equal(first.PlayedAt) {
			t.Fatalf("replay = %+v, want the first record %+v", replay, first)
		}
	}
	if rows := fixture.count(t, `SELECT COUNT(*) FROM play_history WHERE user_id=$1::uuid`, userA); rows != 1 {
		t.Fatalf("rows after replays = %d, want 1", rows)
	}

	// 同一个键换了秒数或曲目：冲突，仍然只有一行。
	if _, err := fixture.service.Record(ctx, userA, key, track, 121); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("same key with other seconds: %v, want ErrIdempotencyConflict", err)
	}
	if _, err := fixture.service.Record(ctx, userA, key, other, 120); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("same key with another track: %v, want ErrIdempotencyConflict", err)
	}
	if rows := fixture.count(t, `SELECT COUNT(*) FROM play_history`); rows != 1 {
		t.Fatalf("rows after conflicts = %d, want 1", rows)
	}

	// 键的作用域是用户：另一个用户用同一个键得到自己的新记录，看不到别人的。
	second, err := fixture.service.Record(ctx, userB, key, track, 120)
	if err != nil || second.ID == first.ID {
		t.Fatalf("another user with the same key: %+v, %v; want a separate record", second, err)
	}
	if rows := fixture.count(t, `SELECT COUNT(*) FROM play_history`); rows != 2 {
		t.Fatalf("rows after the second user = %d, want 2", rows)
	}
}

func TestPostgreSQLRecordConcurrentRequestsWithTheSameKeyWriteOneRow(t *testing.T) {
	fixture := newListeningFixture(t)
	ctx := context.Background()
	user := fixture.user(t)
	track := fixture.track(t, fixture.release(t, "专辑"), "曲目", 200)
	key := uuid.New()

	const workers = 12
	results := make([]Recorded, workers)
	errs := make([]error, workers)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			results[index], errs[index] = fixture.service.Record(ctx, user, key, track, 150)
		}()
	}
	close(start)
	wait.Wait()

	for index := 0; index < workers; index++ {
		if errs[index] != nil {
			t.Fatalf("worker %d: %v", index, errs[index])
		}
		if results[index].ID != results[0].ID || !results[index].PlayedAt.Equal(results[0].PlayedAt) {
			t.Fatalf("worker %d got %+v, want the single record %+v", index, results[index], results[0])
		}
	}
	if rows := fixture.count(t, `SELECT COUNT(*) FROM play_history WHERE user_id=$1::uuid AND idempotency_key=$2::uuid`, user, key); rows != 1 {
		t.Fatalf("rows written by %d concurrent requests = %d, want 1", workers, rows)
	}
}

func TestPostgreSQLRecordEnforcesTheThresholdAndTrackRules(t *testing.T) {
	fixture := newListeningFixture(t)
	ctx := context.Background()
	user := fixture.user(t)
	release := fixture.release(t, "专辑")
	short := fixture.track(t, release, "短曲", 100)   // T = 50，接受 ≥ 49
	long := fixture.track(t, release, "长曲", 3600)   // T = 240，接受 ≥ 239
	unknown := fixture.track(t, release, "未知时长", 0) // 没有门槛，只约束数值范围
	missing := fixture.track(t, release, "缺失", 100)
	fixture.setStatus(t, missing, "missing")

	accepted := 0
	for _, testCase := range []struct {
		name    string
		track   string
		seconds float64
		want    error
	}{
		{"short track at T", short, 50, nil},
		{"short track at T minus the tolerance", short, 49, nil},
		{"short track below the tolerance", short, 48.99, ErrPlayTooShort},
		{"long track at T minus the tolerance", long, 239, nil},
		{"long track below the tolerance", long, 238.9, ErrPlayTooShort},
		{"unknown duration accepts half a second", unknown, 0.5, nil},
		{"zero seconds", unknown, 0, ErrInvalidPlay},
		{"negative seconds", unknown, -3, ErrInvalidPlay},
		{"NaN seconds", unknown, math.NaN(), ErrInvalidPlay},
		{"infinite seconds", unknown, math.Inf(1), ErrInvalidPlay},
		{"more than a day", unknown, MaxPlayedSeconds + 1, ErrInvalidPlay},
		{"a day exactly", unknown, MaxPlayedSeconds, nil},
		{"missing track", missing, 60, ErrTrackNotFound},
		{"unknown track", uuid.New(), 60, ErrTrackNotFound},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := fixture.service.Record(ctx, user, uuid.New(), testCase.track, testCase.seconds)
			if testCase.want == nil {
				if err != nil {
					t.Fatalf("Record = %v, want success", err)
				}
				accepted++
				return
			}
			if !errors.Is(err, testCase.want) {
				t.Fatalf("Record = %v, want %v", err, testCase.want)
			}
		})
	}
	if rows := fixture.count(t, `SELECT COUNT(*) FROM play_history`); rows != accepted {
		t.Fatalf("rows = %d, want %d (rejected plays must not be written)", rows, accepted)
	}
}

// 目录里的曲目从不物理删除，所以外键冲突只是兜底：它必须被分类成“曲目不存在”，不能变成 500。
type unconditionalDuration struct{ Catalog }

func (unconditionalDuration) LoadTrackDuration(context.Context, string) (float64, error) {
	return 0, nil
}

func TestPostgreSQLRecordClassifiesAForeignKeyViolationAsAMissingTrack(t *testing.T) {
	fixture := newListeningFixture(t)
	service := NewService(fixture.database, unconditionalDuration{})
	if _, err := service.Record(context.Background(), fixture.user(t), uuid.New(), uuid.New(), 10); !errors.Is(err, ErrTrackNotFound) {
		t.Fatalf("Record for a track that has no row = %v, want ErrTrackNotFound", err)
	}
}

func TestPostgreSQLPlayHistoryConstraints(t *testing.T) {
	fixture := newListeningFixture(t)
	user := fixture.user(t)
	track := fixture.track(t, fixture.release(t, "专辑"), "曲目", 200)
	insert := func(userID, trackID, key string, seconds float64) error {
		_, err := fixture.database.Exec(`INSERT INTO play_history(user_id,track_id,idempotency_key,played_seconds) VALUES($1::uuid,$2::uuid,$3::uuid,$4)`, userID, trackID, key, seconds)
		return err
	}
	for _, seconds := range []float64{0, -1, 86400.5, math.NaN(), math.Inf(1)} {
		if err := insert(user, track, uuid.New(), seconds); err == nil {
			t.Fatalf("a row with played_seconds %v was accepted", seconds)
		}
	}
	key := uuid.New()
	if err := insert(user, track, key, 60); err != nil {
		t.Fatalf("valid row: %v", err)
	}
	if err := insert(user, track, key, 60); err == nil {
		t.Fatal("a second row with the same (user, key) was accepted")
	}
	if err := insert(user, uuid.New(), uuid.New(), 60); err == nil {
		t.Fatal("a row for a track that does not exist was accepted")
	}
	if err := insert(uuid.New(), track, uuid.New(), 60); err == nil {
		t.Fatal("a row for a user that does not exist was accepted")
	}
	// 曲目被历史引用时不能物理删除（不级联）；用户被删除时他的记录一起删除（级联）。
	if _, err := fixture.database.Exec(`DELETE FROM tracks WHERE id=$1::uuid`, track); err == nil {
		t.Fatal("a track referenced by the play history was deleted")
	}
	fixture.exec(t, `DELETE FROM users WHERE id=$1::uuid`, user)
	if rows := fixture.count(t, `SELECT COUNT(*) FROM play_history`); rows != 0 {
		t.Fatalf("rows after the user was deleted = %d, want 0 (cascade)", rows)
	}
}

func TestPostgreSQLRecentReleasesAreNewestFirstOncePerRelease(t *testing.T) {
	fixture := newListeningFixture(t)
	ctx := context.Background()
	user := fixture.user(t)
	first, second, third := fixture.release(t, "第一张"), fixture.release(t, "第二张"), fixture.release(t, "第三张")
	a1, a2 := fixture.track(t, first, "A1", 200), fixture.track(t, first, "A2", 200)
	b1 := fixture.track(t, second, "B1", 200)
	c1 := fixture.track(t, third, "C1", 200)

	fixture.playAt(t, user, a1, baseTime)
	fixture.playAt(t, user, b1, baseTime.Add(10*time.Minute)) // 同一首的较早一次不影响结果
	fixture.playAt(t, user, c1, baseTime.Add(30*time.Minute))
	fixture.playAt(t, user, b1, baseTime.Add(time.Hour))
	fixture.playAt(t, user, a2, baseTime.Add(2*time.Hour))

	recent, err := fixture.service.RecentReleases(ctx, user)
	if err != nil {
		t.Fatalf("RecentReleases: %v", err)
	}
	if !recent.HasHistory || len(recent.Items) != 3 {
		t.Fatalf("recent = %+v, want 3 releases with history", recent)
	}
	for index, want := range []struct {
		release fixtureRelease
		track   string
		title   string
		playAt  time.Time
	}{
		{first, a2, "A2", baseTime.Add(2 * time.Hour)}, // 第一张被播放过两首，只出现一次，代表最近的那首
		{second, b1, "B1", baseTime.Add(time.Hour)},
		{third, c1, "C1", baseTime.Add(30 * time.Minute)},
	} {
		item := recent.Items[index]
		if item.GroupID != want.release.groupID || item.Release.ID != want.release.releaseID || item.LastTrack.ID != want.track || item.LastTrack.Title != want.title || !item.LastPlayedAt.Equal(want.playAt) {
			t.Fatalf("item %d = %+v, want release %s, track %s (%s) at %s", index, item, want.release.releaseID, want.track, want.title, want.playAt)
		}
		summary, err := fixture.catalog.LoadReleaseSummary(ctx, want.release.releaseID)
		if err != nil || !reflect.DeepEqual(item.Release, summary) {
			t.Fatalf("item %d release = %+v, want the catalog summary %+v (error %v)", index, item.Release, summary, err)
		}
	}
}

func TestPostgreSQLRecentReleasesAreLimitedAndHideMissingTracks(t *testing.T) {
	fixture := newListeningFixture(t)
	ctx := context.Background()
	user := fixture.user(t)
	var tracks []string
	var releases []fixtureRelease
	for index := 0; index < 10; index++ {
		release := fixture.release(t, fmt.Sprintf("专辑 %d", index))
		releases = append(releases, release)
		track := fixture.track(t, release, fmt.Sprintf("曲目 %d", index), 200)
		tracks = append(tracks, track)
		fixture.playAt(t, user, track, baseTime.Add(time.Duration(index)*time.Minute))
	}
	ids := func(recent RecentReleases) []string {
		result := make([]string, 0, len(recent.Items))
		for _, item := range recent.Items {
			result = append(result, item.Release.ID)
		}
		return result
	}

	// 十个发行只返回最近的八个，最新的在前。
	recent, err := fixture.service.RecentReleases(ctx, user)
	if err != nil {
		t.Fatalf("RecentReleases: %v", err)
	}
	var want []string
	for index := 9; index >= 2; index-- {
		want = append(want, releases[index].releaseID)
	}
	if !reflect.DeepEqual(ids(recent), want) {
		t.Fatalf("recent releases = %v, want %v", ids(recent), want)
	}

	// 最新的两首缺失后不显示，被挤出的两个旧发行补上；记录仍保存。
	fixture.setStatus(t, tracks[9], "missing")
	fixture.setStatus(t, tracks[8], "missing")
	recent, err = fixture.service.RecentReleases(ctx, user)
	if err != nil {
		t.Fatalf("RecentReleases with missing tracks: %v", err)
	}
	want = want[:0]
	for index := 7; index >= 0; index-- {
		want = append(want, releases[index].releaseID)
	}
	if !reflect.DeepEqual(ids(recent), want) {
		t.Fatalf("recent releases with two missing tracks = %v, want %v", ids(recent), want)
	}
	if rows := fixture.count(t, `SELECT COUNT(*) FROM play_history WHERE user_id=$1::uuid`, user); rows != 10 {
		t.Fatalf("stored rows = %d, want 10 (missing tracks keep their history)", rows)
	}

	// 全部缺失：没有可显示的发行，但仍有历史（界面据此提供清除）；items 是空数组而不是 nil。
	for _, track := range tracks {
		fixture.setStatus(t, track, "missing")
	}
	recent, err = fixture.service.RecentReleases(ctx, user)
	if err != nil {
		t.Fatalf("RecentReleases with every track missing: %v", err)
	}
	if recent.Items == nil || len(recent.Items) != 0 || !recent.HasHistory {
		t.Fatalf("recent with every track missing = %+v, want empty items and has_history", recent)
	}

	// 文件回来后记录照常出现。
	fixture.setStatus(t, tracks[4], "present")
	recent, err = fixture.service.RecentReleases(ctx, user)
	if err != nil || len(recent.Items) != 1 || recent.Items[0].Release.ID != releases[4].releaseID {
		t.Fatalf("recent after a file came back = %+v (error %v), want release 4", recent, err)
	}
}

func TestPostgreSQLRecentReleasesFollowTracksToTheirCurrentRelease(t *testing.T) {
	fixture := newListeningFixture(t)
	ctx := context.Background()
	user := fixture.user(t)
	old, current := fixture.release(t, "旧发行"), fixture.release(t, "新发行")
	moved := fixture.track(t, old, "被移走的曲目", 200)
	fixture.track(t, current, "原来就在这里", 200)
	fixture.playAt(t, user, moved, baseTime)

	// 重扫把曲目放进了另一个发行：记录引用曲目，所以显示它当前所在的发行与组。
	fixture.exec(t, `UPDATE tracks SET medium_id=$2::uuid WHERE id=$1::uuid`, moved, current.mediumID)
	recent, err := fixture.service.RecentReleases(ctx, user)
	if err != nil {
		t.Fatalf("RecentReleases: %v", err)
	}
	if len(recent.Items) != 1 || recent.Items[0].Release.ID != current.releaseID || recent.Items[0].GroupID != current.groupID || recent.Items[0].LastTrack.ID != moved {
		t.Fatalf("recent after the track moved = %+v, want release %s", recent.Items, current.releaseID)
	}
}

func TestPostgreSQLRecentReleasesAreReadInBatches(t *testing.T) {
	fixture := newListeningFixture(t)
	ctx := context.Background()
	user := fixture.user(t)
	crowded, lonely := fixture.release(t, "播放最多的专辑"), fixture.release(t, "很久以前听过的专辑")
	fixture.playAt(t, user, fixture.track(t, lonely, "旧曲目", 200), baseTime)
	for index := 0; index < 6; index++ {
		fixture.playAt(t, user, fixture.track(t, crowded, fmt.Sprintf("曲目 %d", index), 200), baseTime.Add(time.Duration(index+1)*time.Hour))
	}

	// 每批两首：同一个发行的六首曲目占满三批，旧发行在第七首，读到第四批才出现。
	for _, testCase := range []struct {
		maxBatches int
		want       int
	}{{3, 1}, {4, 2}} {
		service := NewService(fixture.database, fixture.catalog)
		service.batchSize, service.maxBatches = 2, testCase.maxBatches
		recent, err := service.RecentReleases(ctx, user)
		if err != nil || len(recent.Items) != testCase.want {
			t.Fatalf("maxBatches %d: %d releases (error %v), want %d", testCase.maxBatches, len(recent.Items), err, testCase.want)
		}
		if recent.Items[0].Release.ID != crowded.releaseID {
			t.Fatalf("maxBatches %d: first release %s, want the crowded one", testCase.maxBatches, recent.Items[0].Release.ID)
		}
	}

	// 五个发行、每批两首：批次边界不丢也不重，按时间倒序，最后一批不足一批时结束。
	other := fixture.user(t)
	var releases []fixtureRelease
	for index := 0; index < 5; index++ {
		release := fixture.release(t, fmt.Sprintf("专辑 %d", index))
		releases = append(releases, release)
		fixture.playAt(t, other, fixture.track(t, release, "曲目", 200), baseTime.Add(time.Duration(index)*time.Minute))
	}
	service := NewService(fixture.database, fixture.catalog)
	service.batchSize, service.maxBatches = 2, 10
	recent, err := service.RecentReleases(ctx, other)
	if err != nil || len(recent.Items) != 5 {
		t.Fatalf("five releases in batches of two: %d (error %v), want 5", len(recent.Items), err)
	}
	for index, item := range recent.Items {
		if want := releases[4-index].releaseID; item.Release.ID != want {
			t.Fatalf("item %d = %s, want %s", index, item.Release.ID, want)
		}
	}
}

func TestPostgreSQLHistoryAndClearAreIsolatedPerUser(t *testing.T) {
	fixture := newListeningFixture(t)
	ctx := context.Background()
	userA, userB, nobody := fixture.user(t), fixture.user(t), fixture.user(t)
	releaseA, releaseB := fixture.release(t, "A 听的"), fixture.release(t, "B 听的")
	trackA, trackB := fixture.track(t, releaseA, "曲目", 200), fixture.track(t, releaseB, "曲目", 200)
	fixture.playAt(t, userA, trackA, baseTime)
	fixture.playAt(t, userA, trackA, baseTime.Add(time.Minute))
	fixture.playAt(t, userB, trackB, baseTime)

	recentA, err := fixture.service.RecentReleases(ctx, userA)
	if err != nil || len(recentA.Items) != 1 || recentA.Items[0].Release.ID != releaseA.releaseID {
		t.Fatalf("user A sees %+v (error %v), want only their own release", recentA.Items, err)
	}
	recentNobody, err := fixture.service.RecentReleases(ctx, nobody)
	if err != nil || recentNobody.HasHistory || recentNobody.Items == nil || len(recentNobody.Items) != 0 {
		t.Fatalf("a user without plays sees %+v (error %v), want empty items and no history", recentNobody, err)
	}

	// 清除只影响本人：返回删除的行数，再清除得到 0，别人的记录不变。
	if deleted, err := fixture.service.Clear(ctx, userA); err != nil || deleted != 2 {
		t.Fatalf("Clear(A) = %d, %v; want 2", deleted, err)
	}
	if deleted, err := fixture.service.Clear(ctx, userA); err != nil || deleted != 0 {
		t.Fatalf("second Clear(A) = %d, %v; want 0", deleted, err)
	}
	if deleted, err := fixture.service.Clear(ctx, nobody); err != nil || deleted != 0 {
		t.Fatalf("Clear for a user without plays = %d, %v; want 0", deleted, err)
	}
	recentA, err = fixture.service.RecentReleases(ctx, userA)
	if err != nil || recentA.HasHistory || len(recentA.Items) != 0 {
		t.Fatalf("user A after clearing sees %+v (error %v), want nothing", recentA, err)
	}
	recentB, err := fixture.service.RecentReleases(ctx, userB)
	if err != nil || !recentB.HasHistory || len(recentB.Items) != 1 || recentB.Items[0].Release.ID != releaseB.releaseID {
		t.Fatalf("user B after A cleared sees %+v (error %v), want their release", recentB, err)
	}
}
