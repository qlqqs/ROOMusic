package personal

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/testsupport"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// personalFixture 是隔离 schema 的已迁移测试库，加一个登记的目录；曲目、发行与用户都是合成行，不读任何文件。
type personalFixture struct {
	database    *sql.DB
	databaseURL string
	catalog     *catalog.Store
	service     *Service
	rootID      string
	counter     int
}

func newPersonalFixture(t *testing.T) *personalFixture {
	t.Helper()
	scopedURL := postgrestest.NewSchema(t, "roomusic_test_")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	database, err := postgres.Open(ctx, scopedURL)
	if err != nil {
		t.Fatalf("open isolated listening database: %v", err)
	}
	if err := testsupport.SetArtistProjectionVersionCurrent(ctx, database); err != nil {
		t.Fatalf("set current artist projection version: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close isolated listening database: %v", err)
		}
	})
	store := catalog.NewStore(database)
	fixture := &personalFixture{database: database, databaseURL: scopedURL, catalog: store, service: NewService(database), rootID: uuid.New()}
	fixture.exec(t, `INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,'/library',$2,1)`, fixture.rootID, "active")
	return fixture
}

func (fixture *personalFixture) exec(t *testing.T, statement string, arguments ...any) {
	t.Helper()
	if _, err := fixture.database.Exec(statement, arguments...); err != nil {
		t.Fatalf("exec %q: %v", statement, err)
	}
}

func (fixture *personalFixture) count(t *testing.T, query string, arguments ...any) int {
	t.Helper()
	var count int
	if err := fixture.database.QueryRow(query, arguments...).Scan(&count); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return count
}

func (fixture *personalFixture) user(t *testing.T) string {
	t.Helper()
	id := uuid.New()
	fixture.exec(t, `INSERT INTO users(id,username,password_hash,role) VALUES($1::uuid,$2,'test-only','user')`, id, "user-"+strings.ReplaceAll(id, "-", ""))
	return id
}

type fixtureRelease struct{ groupID, releaseID, mediumID string }

func (fixture *personalFixture) release(t *testing.T, title string) fixtureRelease {
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
func (fixture *personalFixture) track(t *testing.T, release fixtureRelease, title string, duration float64) string {
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

func (fixture *personalFixture) setStatus(t *testing.T, trackID, status string) {
	t.Helper()
	fixture.exec(t, `UPDATE tracks SET source_status=$2 WHERE id=$1::uuid`, trackID, status)
}
