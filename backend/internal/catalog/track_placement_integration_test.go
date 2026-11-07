package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/testsupport"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// placementFixture 是隔离 schema 的已迁移测试库加一个登记的目录；发行、曲目与封面都是合成行，不读任何文件。
type placementFixture struct {
	t        *testing.T
	database *sql.DB
	store    *Store
	rootID   string
	counter  int
}

func newPlacementFixture(t *testing.T) *placementFixture {
	t.Helper()
	scopedURL := postgrestest.NewSchema(t, "roomusic_test_")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	database, err := postgres.Open(ctx, scopedURL)
	if err != nil {
		t.Fatalf("open isolated catalog database: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close isolated catalog database: %v", err)
		}
	})
	if err := testsupport.SetArtistProjectionVersionCurrent(ctx, database); err != nil {
		t.Fatalf("set current artist projection version: %v", err)
	}
	fixture := &placementFixture{t: t, database: database, store: NewStore(database), rootID: uuid.New()}
	fixture.exec(`INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,'/library','active',1)`, fixture.rootID)
	return fixture
}

func (fixture *placementFixture) exec(statement string, arguments ...any) {
	fixture.t.Helper()
	if _, err := fixture.database.Exec(statement, arguments...); err != nil {
		fixture.t.Fatalf("exec %q: %v", statement, err)
	}
}

type placementRelease struct{ groupID, releaseID, mediumID string }

func (fixture *placementFixture) release(title string) placementRelease {
	fixture.t.Helper()
	fixture.counter++
	release := placementRelease{groupID: uuid.New(), releaseID: uuid.New(), mediumID: uuid.New()}
	fixture.exec(`INSERT INTO release_groups(id) VALUES($1::uuid)`, release.groupID)
	fixture.exec(`INSERT INTO releases(id,group_id,title,artist,album_artist,year,source_root_id,relative_directory,candidate_anchor,candidate_kind)
		VALUES($1::uuid,$2::uuid,$3,'艺术家','专辑艺术家',2026,$4::uuid,$5,$6,'ordinary_directory')`,
		release.releaseID, release.groupID, title, fixture.rootID, fmt.Sprintf("dir-%d", fixture.counter), fmt.Sprintf("%s:v2:%d", fixture.rootID, fixture.counter))
	fixture.exec(`INSERT INTO media(id,release_id,position,title) VALUES($1::uuid,$2::uuid,1,'Medium')`, release.mediumID, release.releaseID)
	return release
}

func (fixture *placementFixture) track(release placementRelease, title, status string, duration any) string {
	fixture.t.Helper()
	fixture.counter++
	id := uuid.New()
	fixture.exec(`INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,duration_seconds)
		VALUES($1::uuid,$2::uuid,$3,$4,'艺术家',1,$5::uuid,$6,$7,NOW(),'physical',$8)`,
		id, release.mediumID, fixture.counter, title, fixture.rootID, fmt.Sprintf("track-%d.flac", fixture.counter), status, duration)
	return id
}

func (fixture *placementFixture) artwork(release placementRelease, storageKey string) {
	fixture.t.Helper()
	fixture.exec(`INSERT INTO release_artworks(release_id,content_hash,mime_type,width,height,storage_key,source_type)
		VALUES($1::uuid,$2,'image/jpeg',600,600,$3,'folder')`, release.releaseID, strings.Repeat("a", 64), storageKey)
}

func TestPostgreSQLLoadTrackDuration(t *testing.T) {
	fixture := newPlacementFixture(t)
	ctx := context.Background()
	release := fixture.release("专辑")
	known := fixture.track(release, "有时长", "present", 245.5)
	null := fixture.track(release, "时长未知", "present", nil)
	zero := fixture.track(release, "时长为零", "present", 0)
	negative := fixture.track(release, "时长为负", "present", -3.0)
	notANumber := fixture.track(release, "时长不是数", "present", "NaN")
	infinite := fixture.track(release, "时长无穷", "present", "Infinity")
	missing := fixture.track(release, "缺失", "missing", 100.0)

	for _, testCase := range []struct {
		name  string
		track string
		want  float64
	}{
		{"known", known, 245.5},
		{"null", null, 0},
		{"zero", zero, 0},
		{"negative", negative, 0},
		{"NaN", notANumber, 0},
		{"infinite", infinite, 0},
	} {
		duration, err := fixture.store.LoadTrackDuration(ctx, testCase.track)
		if err != nil || duration != testCase.want {
			t.Errorf("%s: LoadTrackDuration = %v, %v; want %v", testCase.name, duration, err, testCase.want)
		}
	}
	for name, track := range map[string]string{"missing track": missing, "unknown track": uuid.New()} {
		if _, err := fixture.store.LoadTrackDuration(ctx, track); !errors.Is(err, ErrTrackNotFound) {
			t.Errorf("%s: LoadTrackDuration error = %v, want ErrTrackNotFound", name, err)
		}
	}
}

func TestPostgreSQLLoadTrackPlacements(t *testing.T) {
	fixture := newPlacementFixture(t)
	ctx := context.Background()
	withCover, plain, hidden := fixture.release("有封面的专辑"), fixture.release("没有封面的专辑"), fixture.release("只有缺失曲目的发行")
	fixture.artwork(withCover, "cover-"+strings.Repeat("a", 64)+".jpg")
	a1, a2, a3 := fixture.track(withCover, "A1", "present", 100.0), fixture.track(withCover, "A2", "present", 100.0), fixture.track(withCover, "A3", "present", nil)
	b1 := fixture.track(plain, "B1", "present", 100.0)
	c1 := fixture.track(hidden, "C1", "missing", 100.0)

	// 重复、大小写、缺失、不存在、不是 UUID 的输入：只有 present 曲目有位置，键是小写 UUID。
	input := []string{a1, a2, a3, b1, c1, uuid.New(), "not-a-uuid", "", strings.ToUpper(a1), a1}
	placements, err := fixture.store.LoadTrackPlacements(ctx, input)
	if err != nil {
		t.Fatalf("LoadTrackPlacements: %v", err)
	}
	if len(placements) != 4 {
		t.Fatalf("placements for %d tracks, want 4 (the present ones): %+v", len(placements), placements)
	}
	for _, testCase := range []struct {
		track   string
		title   string
		release placementRelease
	}{{a1, "A1", withCover}, {a2, "A2", withCover}, {a3, "A3", withCover}, {b1, "B1", plain}} {
		placement, ok := placements[testCase.track]
		if !ok {
			t.Fatalf("no placement for track %s", testCase.track)
		}
		if placement.TrackID != testCase.track || placement.Title != testCase.title || placement.GroupID != testCase.release.groupID {
			t.Fatalf("placement = %+v, want track %s titled %q in group %s", placement, testCase.track, testCase.title, testCase.release.groupID)
		}
		// 摘要与列表、详情用的是同一个投影：逐项相同，包括封面与数量。
		summary, err := fixture.store.LoadReleaseSummary(ctx, testCase.release.releaseID)
		if err != nil || !reflect.DeepEqual(placement.Release, summary) {
			t.Fatalf("placement release = %+v, want the catalog summary %+v (error %v)", placement.Release, summary, err)
		}
	}
	if placement := placements[a1]; placement.Release.Artwork == nil || placement.Release.TrackCount != 3 || placement.Release.MediumCount != 1 {
		t.Fatalf("release with a cover: %+v", placement.Release)
	}
	if placement := placements[b1]; placement.Release.Artwork != nil {
		t.Fatalf("release without a cover reports artwork: %+v", placement.Release.Artwork)
	}
	if _, ok := placements[c1]; ok {
		t.Fatal("a missing track has a placement")
	}

	// 曲目随重扫换到另一个发行后，位置跟着变。
	fixture.exec(`UPDATE tracks SET medium_id=$2::uuid WHERE id=$1::uuid`, a3, plain.mediumID)
	placements, err = fixture.store.LoadTrackPlacements(ctx, []string{a3})
	if err != nil || placements[a3].Release.ID != plain.releaseID || placements[a3].GroupID != plain.groupID {
		t.Fatalf("placement after the track moved = %+v (error %v), want release %s", placements[a3], err, plain.releaseID)
	}
}

func TestLoadTrackPlacementsWithoutUsableInputDoesNotQuery(t *testing.T) {
	// Store 没有数据库：任何查询都会 panic，所以这里同时证明空输入与全部无效的输入不访问数据库。
	store := NewStore(nil)
	for name, input := range map[string][]string{"nil": nil, "empty": {}, "only invalid": {"", "not-a-uuid", "1234"}} {
		placements, err := store.LoadTrackPlacements(context.Background(), input)
		if err != nil || placements == nil || len(placements) != 0 {
			t.Errorf("%s: LoadTrackPlacements = %v, %v; want an empty map", name, placements, err)
		}
	}
	tooMany := make([]string, maxPlacementTrackIDs+1)
	for index := range tooMany {
		tooMany[index] = uuid.New()
	}
	if _, err := store.LoadTrackPlacements(context.Background(), tooMany); err == nil {
		t.Error("more than the limit of distinct tracks was accepted")
	}
}

func TestPostgreSQLLoadTrackPlacementsFailClosedOnMalformedArtwork(t *testing.T) {
	fixture := newPlacementFixture(t)
	release := fixture.release("封面记录不安全的专辑")
	track := fixture.track(release, "曲目", "present", 100.0)
	const unsafeKey = "../private-cover.webp"
	fixture.artwork(release, unsafeKey)

	_, err := fixture.store.LoadTrackPlacements(context.Background(), []string{track})
	if err == nil {
		t.Fatal("an unsafe artwork record was projected")
	}
	if strings.Contains(err.Error(), "private-cover") {
		t.Fatalf("the error echoes the stored artwork key: %v", err)
	}
}
