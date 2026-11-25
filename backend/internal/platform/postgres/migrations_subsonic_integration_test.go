package postgres

import (
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/migrations"
)

func TestPostgreSQLSubsonicMigrationRoundTrip(t *testing.T) {
	available, err := loadMigrations(migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	db := openMigrationTestDatabase(t)
	previous := migrationPrefix(available, 80)
	current := migrationPrefix(available, 81)
	rollbackApply(t, db, previous)
	const id = "f8100000-0000-4000-8000-000000000001"
	rollbackExec(t, db, "INSERT INTO users(id, username, password_hash) VALUES($1, 'subsonic-legacy-member', 'synthetic-hash')", id)
	before := rollbackSnapshot(t, db)

	rollbackApply(t, db, current)
	upgraded := rollbackSnapshot(t, db)
	var settings, enabled, credentialCount int
	err = db.QueryRowContext(t.Context(), `SELECT count(*), count(*) FILTER (WHERE id=1 AND subsonic_enabled),
		(SELECT count(*) FROM subsonic_credentials) FROM subsonic_settings`).Scan(&settings, &enabled, &credentialCount)
	if err != nil || settings != 1 || enabled != 1 || credentialCount != 0 {
		t.Fatalf("0081 初始状态应只有一行启用设置和空副本表，实际 settings=%d enabled=%d credentials=%d，错误 %v",
			settings, enabled, credentialCount, err)
	}
	if _, err := db.ExecContext(t.Context(), "INSERT INTO subsonic_settings(id) VALUES(2)"); err == nil {
		t.Fatal("0081 设置表不应允许第二行")
	}
	rollbackExec(t, db, "UPDATE subsonic_settings SET subsonic_enabled=false WHERE id=1")
	rollbackExec(t, db, "INSERT INTO subsonic_credentials(user_id, sealed) VALUES($1, decode('abc123', 'hex'))", id)

	rollbackApply(t, db, previous)
	if diff := postgrestest.Diff(before, rollbackSnapshot(t, db)); len(diff) != 0 {
		t.Fatalf("0081 回退未恢复升级前的结构和数据：\n%s", strings.Join(diff, "\n"))
	}

	rollbackApply(t, db, current)
	if diff := postgrestest.Diff(upgraded, rollbackSnapshot(t, db)); len(diff) != 0 {
		t.Fatalf("0081 再升级未恢复默认结构和数据：\n%s", strings.Join(diff, "\n"))
	}
}
