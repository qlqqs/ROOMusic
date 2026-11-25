package app

import (
	"database/sql"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/migrations"
)

func TestPostgreSQLSubsonicJukeboxMigrationRoundTrip(t *testing.T) {
	address := postgrestest.NewSchema(t, "roomusic_jukebox_migration_")
	files, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	previous, current := fstest.MapFS{}, fstest.MapFS{}
	for _, name := range files {
		if name[:4] > "0084" {
			continue
		}
		content, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		file := &fstest.MapFile{Data: content}
		current[name] = file
		if name[:4] < "0084" {
			previous[name] = file
		}
	}
	open := func(source fstest.MapFS, version int) *sql.DB {
		t.Helper()
		db, err := postgres.Open(t.Context(), address, postgres.WithMigrationFS(source))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := db.Close(); err != nil {
				t.Error(err)
			}
		})
		var actual int
		if err := db.QueryRowContext(t.Context(), "SELECT max(version) FROM schema_migrations").Scan(&actual); err != nil || actual != version {
			t.Fatalf("迁移版本为 %d，期望 %d，查询错误为 %v", actual, version, err)
		}
		return db
	}
	snapshot := func(db *sql.DB) postgrestest.SchemaSnapshot {
		t.Helper()
		value, err := postgrestest.Snapshot(t.Context(), db)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}

	db := open(previous, 83)
	app := &integrationTestApplication{database: db}
	user := app.createUser(t, "user")
	deviceID, zoneID := uuid.New(), uuid.New()
	mustExec(t, app, "INSERT INTO playback_devices(id,user_id,name) VALUES($1,$2,'合成旧版设备')", deviceID, user.id)
	mustExec(t, app, "INSERT INTO playback_zones(id,user_id,device_id) VALUES($1,$2,$3)", zoneID, user.id, deviceID)
	before := snapshot(db)

	db = open(current, 84)
	up := snapshot(db)
	app.database = db
	var lastPlayed sql.NullTime
	if err := db.QueryRowContext(t.Context(), "SELECT last_played_at FROM playback_zones WHERE id=$1", zoneID).Scan(&lastPlayed); err != nil || lastPlayed.Valid {
		t.Fatalf("旧区首次升级的最近播放时间为 %v，查询错误为 %v", lastPlayed, err)
	}
	mustExec(t, app, "UPDATE playback_zones SET last_played_at=now() WHERE id=$1", zoneID)
	mustExec(t, app, "INSERT INTO subsonic_jukebox_settings(user_id,jukebox_zone_id) VALUES($1,$2)", user.id, zoneID)

	db = open(previous, 83)
	if diff := postgrestest.Diff(before, snapshot(db)); len(diff) != 0 {
		t.Fatalf("0084 回退后的结构或数据与 0083 不符：\n%s", strings.Join(diff, "\n"))
	}
	db = open(current, 84)
	if diff := postgrestest.Diff(up, snapshot(db)); len(diff) != 0 {
		t.Fatalf("0084 再升级后的结构或数据与首次升级不符：\n%s", strings.Join(diff, "\n"))
	}
}
