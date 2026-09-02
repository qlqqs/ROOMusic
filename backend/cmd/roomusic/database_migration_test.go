package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/qlqq/roomusic/backend/migrations"
)

func TestLoadMigrationsValidatesVersionsAndChecksums(t *testing.T) {
	source := fstest.MapFS{
		"0002_second.sql": &fstest.MapFile{Data: []byte("second\n")},
		"0001_first.sql":  &fstest.MapFile{Data: []byte("first\n")},
		"0003_third.sql":  &fstest.MapFile{Data: []byte("third\n")},
	}
	migrations, err := loadMigrations(source)
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if got := []int64{migrations[0].Version, migrations[1].Version, migrations[2].Version}; !equalInt64s(got, []int64{1, 2, 3}) {
		t.Fatalf("migrations were not sorted by version: %v", got)
	}
	if migrations[0].Name != "0001_first.sql" || migrations[0].Checksum != migrationTestSHA256Hex([]byte("first\n")) {
		t.Fatalf("unexpected first migration descriptor: %+v", migrations[0])
	}

	for _, testCase := range []struct {
		name     string
		files    map[string]string
		contains string
	}{
		{name: "gap", files: map[string]string{"0001_first.sql": "1", "0003_third.sql": "3"}, contains: "missing migration version 2"},
		{name: "duplicate version", files: map[string]string{"0001_first.sql": "1", "0001_second.sql": "2"}, contains: "duplicate migration version 1"},
		{name: "invalid filename", files: map[string]string{"0001-first.sql": "1"}, contains: "invalid migration filename"},
		{name: "zero version", files: map[string]string{"0000_zero.sql": "0"}, contains: "invalid migration version"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			mapFS := fstest.MapFS{}
			for name, data := range testCase.files {
				mapFS[name] = &fstest.MapFile{Data: []byte(data)}
			}
			_, err := loadMigrations(mapFS)
			if err == nil || !strings.Contains(err.Error(), testCase.contains) {
				t.Fatalf("expected error containing %q, got %v", testCase.contains, err)
			}
		})
	}
}

func TestBuildMigrationPlanSupportsLegacySparseRows(t *testing.T) {
	available := testMigrationDescriptors(8)
	plan, err := buildMigrationPlan(available, []appliedMigration{
		{Version: 1},
		{Version: 6},
		{Version: 7},
	}, migrationMetadataColumns{})
	if err != nil {
		t.Fatalf("build legacy plan: %v", err)
	}
	for _, version := range []int64{1, 6, 7} {
		if !plan.applied[version] {
			t.Fatalf("legacy version %d was not marked applied: %+v", version, plan)
		}
	}
	for _, version := range []int64{2, 3, 4, 5} {
		if !plan.baseline[version] {
			t.Fatalf("legacy version %d was not marked baseline: %+v", version, plan)
		}
	}
}

func TestBuildMigrationPlanRejectsUnknownAndDrift(t *testing.T) {
	available := testMigrationDescriptors(8)
	for _, testCase := range []struct {
		name string
		row  appliedMigration
		want string
	}{
		{name: "unknown version", row: appliedMigration{Version: 99}, want: "unknown version 99"},
		{name: "name drift", row: appliedMigration{Version: 1, Name: sql.NullString{Valid: true, String: "wrong.sql"}}, want: "name drift"},
		{name: "checksum drift", row: appliedMigration{Version: 1, Checksum: sql.NullString{Valid: true, String: "bad"}}, want: "checksum drift"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := buildMigrationPlan(available, []appliedMigration{testCase.row}, migrationMetadataColumns{})
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("expected %q, got %v", testCase.want, err)
			}
		})
	}
}

func TestBuildMigrationPlanTreatsCommittedMetadataColumnsAsVersionEightBaseline(t *testing.T) {
	available := testMigrationDescriptors(8)
	plan, err := buildMigrationPlan(available, []appliedMigration{{Version: 1}, {Version: 6}, {Version: 7}}, migrationMetadataColumns{Name: true, Checksum: true})
	if err != nil {
		t.Fatalf("build metadata baseline plan: %v", err)
	}
	if !plan.baseline[8] || !plan.applied[8] {
		t.Fatalf("version 8 was not baselined: %+v", plan)
	}
}

func TestPostgreSQLMigrationRollsBackOnFailure(t *testing.T) {
	database := openMigrationTestDatabase(t)
	source := testMigrationFS(map[int64]string{
		1: "CREATE TABLE schema_migrations (version BIGINT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP); INSERT INTO schema_migrations(version) VALUES (1);",
		2: "CREATE TABLE migration_rollback_probe (id INTEGER PRIMARY KEY);",
		3: "SELECT 1;",
		4: "SELECT 1;",
		5: "SELECT 1;",
		6: "INSERT INTO schema_migrations(version) VALUES (6);",
		7: "INSERT INTO schema_migrations(version) VALUES (7);",
		8: "ALTER TABLE schema_migrations ADD COLUMN name TEXT, ADD COLUMN checksum TEXT;",
		9: "SELECT * FROM table_that_does_not_exist;",
	})
	if err := applyMigrationsFromFS(context.Background(), database, source); err == nil {
		t.Fatal("failing migration unexpectedly succeeded")
	}
	var exists bool
	if err := database.QueryRow(`SELECT to_regclass('schema_migrations') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatalf("check rollback schema: %v", err)
	}
	if exists {
		t.Fatal("migration rollback left schema_migrations behind")
	}
	if err := applyMigrations(context.Background(), database); err != nil {
		t.Fatalf("database connection was not reusable after rollback: %v", err)
	}
	if rows := queryMigrationRows(t, database); len(rows) != 9 {
		t.Fatalf("expected a clean retry to apply 9 migrations, got %d rows", len(rows))
	}
}

func TestPostgreSQLMigrationRejectsFutureVersionCreatedDuringMigration(t *testing.T) {
	database := openMigrationTestDatabase(t)
	source := testMigrationFS(map[int64]string{
		1: "CREATE TABLE schema_migrations (version BIGINT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP); INSERT INTO schema_migrations(version) VALUES (1);",
		2: "SELECT 1;",
		3: "SELECT 1;",
		4: "SELECT 1;",
		5: "SELECT 1;",
		6: "SELECT 1;",
		7: "SELECT 1;",
		8: "ALTER TABLE schema_migrations ADD COLUMN name TEXT, ADD COLUMN checksum TEXT;",
		9: "INSERT INTO schema_migrations(version) VALUES (99);",
	})
	if err := applyMigrationsFromFS(context.Background(), database, source); err == nil || !strings.Contains(err.Error(), "unknown version 99") {
		t.Fatalf("expected a future version created by migration to fail closed, got %v", err)
	}
	var exists bool
	if err := database.QueryRow(`SELECT to_regclass('schema_migrations') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatalf("check future-version rollback: %v", err)
	}
	if exists {
		t.Fatal("future-version validation failure left migration schema behind")
	}
}

func TestPostgreSQLMigrationFreshAndRerunMetadata(t *testing.T) {
	database := openMigrationTestDatabase(t)
	if err := applyMigrations(context.Background(), database); err != nil {
		t.Fatalf("apply fresh migrations: %v", err)
	}
	rows := queryMigrationRows(t, database)
	if len(rows) != 9 {
		t.Fatalf("expected 9 migration rows, got %d", len(rows))
	}
	for index, row := range rows {
		if row.version != int64(index+1) || row.name == "" || len(row.checksum) != 64 {
			t.Fatalf("invalid migration row: %+v", row)
		}
		candidate := findMigrationByName(t, row.name)
		if row.checksum != candidate.Checksum {
			t.Fatalf("checksum mismatch for %s: %s != %s", row.name, row.checksum, candidate.Checksum)
		}
	}
	var before time.Time
	if err := database.QueryRow(`SELECT applied_at FROM schema_migrations WHERE version=8`).Scan(&before); err != nil {
		t.Fatalf("read applied timestamp: %v", err)
	}
	if err := applyMigrations(context.Background(), database); err != nil {
		t.Fatalf("rerun migrations: %v", err)
	}
	var after time.Time
	if err := database.QueryRow(`SELECT applied_at FROM schema_migrations WHERE version=8`).Scan(&after); err != nil {
		t.Fatalf("read rerun timestamp: %v", err)
	}
	if !before.Equal(after) {
		t.Fatalf("rerun changed applied_at: before=%s after=%s", before, after)
	}
	var tableExists bool
	if err := database.QueryRow(`SELECT to_regclass('users') IS NOT NULL`).Scan(&tableExists); err != nil || !tableExists {
		t.Fatalf("fresh migrations did not create users table: exists=%v err=%v", tableExists, err)
	}
}

func TestPostgreSQLMigrationLegacySparseHistoryUpgrade(t *testing.T) {
	database := openMigrationTestDatabase(t)
	for version := int64(1); version <= 7; version++ {
		candidate := findMigrationByVersion(t, version)
		if _, err := database.Exec(string(candidate.SQL)); err != nil {
			t.Fatalf("apply historical migration %d: %v", version, err)
		}
	}
	userID := createIdentifier()
	if _, err := database.Exec(`INSERT INTO users(id,username,password_hash) VALUES($1::uuid,'legacy-user','hash')`, userID); err != nil {
		t.Fatalf("insert legacy data: %v", err)
	}
	if err := applyMigrations(context.Background(), database); err != nil {
		t.Fatalf("upgrade legacy migrations: %v", err)
	}
	rows := queryMigrationRows(t, database)
	if len(rows) != 9 {
		t.Fatalf("expected 9 migration rows after legacy upgrade, got %d", len(rows))
	}
	for _, version := range []int64{2, 3, 4, 5} {
		if rows[version-1].version != version {
			t.Fatalf("missing baselined version %d: %+v", version, rows)
		}
	}
	var username string
	if err := database.QueryRow(`SELECT username FROM users WHERE id=$1::uuid`, userID).Scan(&username); err != nil || username != "legacy-user" {
		t.Fatalf("legacy data was not preserved: %q err=%v", username, err)
	}
}

func TestPostgreSQLMigrationBaselinesCommittedMetadataColumnsWithoutVersionEightRow(t *testing.T) {
	database := openMigrationTestDatabase(t)
	for version := int64(1); version <= 7; version++ {
		candidate := findMigrationByVersion(t, version)
		if _, err := database.Exec(string(candidate.SQL)); err != nil {
			t.Fatalf("apply historical migration %d: %v", version, err)
		}
	}
	metadataMigration := findMigrationByVersion(t, migrationMetadataVersion)
	if _, err := database.Exec(string(metadataMigration.SQL)); err != nil {
		t.Fatalf("apply metadata DDL without tracking row: %v", err)
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=8`).Scan(&count); err != nil {
		t.Fatalf("count pre-governance metadata row: %v", err)
	}
	if count != 0 {
		t.Fatalf("metadata DDL unexpectedly inserted version 8 row")
	}
	if err := applyMigrations(context.Background(), database); err != nil {
		t.Fatalf("govern committed metadata columns: %v", err)
	}
	rows := queryMigrationRows(t, database)
	if len(rows) != 9 || rows[7].version != 8 || rows[7].name != metadataMigration.Name || rows[7].checksum != metadataMigration.Checksum {
		t.Fatalf("version 8 baseline was not recorded correctly: %+v", rows)
	}
}

func TestPostgreSQLMigrationRejectsDriftAndFutureVersion(t *testing.T) {
	database := openMigrationTestDatabase(t)
	if err := applyMigrations(context.Background(), database); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	if _, err := database.Exec(`UPDATE schema_migrations SET checksum='bad' WHERE version=3`); err != nil {
		t.Fatalf("inject checksum drift: %v", err)
	}
	if err := applyMigrations(context.Background(), database); err == nil || !strings.Contains(err.Error(), "checksum drift") {
		t.Fatalf("expected checksum drift error, got %v", err)
	}
	if _, err := database.Exec(`UPDATE schema_migrations SET checksum=$1 WHERE version=3`, findMigrationByVersion(t, 3).Checksum); err != nil {
		t.Fatalf("restore checksum: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO schema_migrations(version,name,checksum) VALUES(99,'0099_future.sql','future')`); err != nil {
		t.Fatalf("inject future version: %v", err)
	}
	if err := applyMigrations(context.Background(), database); err == nil || !strings.Contains(err.Error(), "unknown version 99") {
		t.Fatalf("expected unknown version error, got %v", err)
	}
}

func TestPostgreSQLMigrationConcurrentExecutorsSerialize(t *testing.T) {
	first, secondConnection := openConcurrentMigrationDatabases(t)
	var group sync.WaitGroup
	errorsCh := make(chan error, 2)
	group.Add(2)
	for _, connection := range []*sql.DB{first, secondConnection} {
		go func(connection *sql.DB) {
			defer group.Done()
			errorsCh <- applyMigrations(context.Background(), connection)
		}(connection)
	}
	group.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatalf("concurrent migration failed: %v", err)
		}
	}
	var count int
	if err := first.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count concurrent migration rows: %v", err)
	}
	if count != 9 {
		t.Fatalf("concurrent migration created %d rows, want 9", count)
	}
}

func TestPostgreSQLMigrationLockCancellation(t *testing.T) {
	first, second := openConcurrentMigrationDatabases(t)
	lockTransaction, err := first.Begin()
	if err != nil {
		t.Fatalf("begin lock holder: %v", err)
	}
	if _, err := lockTransaction.Exec(`SELECT pg_advisory_xact_lock($1::bigint)`, migrationAdvisoryLockKey); err != nil {
		t.Fatalf("acquire lock holder: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	err = applyMigrations(ctx, second)
	cancel()
	if err == nil || !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled lock wait, got %v", err)
	}
	if err := lockTransaction.Rollback(); err != nil {
		t.Fatalf("release lock holder: %v", err)
	}
	if err := applyMigrations(context.Background(), second); err != nil {
		t.Fatalf("migration after canceled lock wait: %v", err)
	}
}

type migrationRow struct {
	version  int64
	name     string
	checksum string
}

func queryMigrationRows(t *testing.T, database *sql.DB) []migrationRow {
	t.Helper()
	rows, err := database.Query(`SELECT version,name,checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("query migration rows: %v", err)
	}
	defer rows.Close()
	result := make([]migrationRow, 0)
	for rows.Next() {
		var row migrationRow
		if err := rows.Scan(&row.version, &row.name, &row.checksum); err != nil {
			t.Fatalf("scan migration row: %v", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate migration rows: %v", err)
	}
	return result
}

func testMigrationDescriptors(last int64) []migration {
	result := make([]migration, 0, last)
	for version := int64(1); version <= last; version++ {
		data := []byte(fmt.Sprintf("migration-%d", version))
		result = append(result, migration{Version: version, Name: fmt.Sprintf("%04d_test.sql", version), SQL: data, Checksum: migrationTestSHA256Hex(data)})
	}
	return result
}

func testMigrationFS(statements map[int64]string) fs.FS {
	mapFS := fstest.MapFS{}
	versions := make([]int64, 0, len(statements))
	for version := range statements {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	for _, version := range versions {
		name := fmt.Sprintf("%04d_test.sql", version)
		mapFS[name] = &fstest.MapFile{Data: []byte(statements[version])}
	}
	return mapFS
}

func openMigrationTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	if strings.TrimSpace(os.Getenv(integrationTestDatabaseEnvironment)) == "" {
		t.Skipf("%s is not configured", integrationTestDatabaseEnvironment)
	}
	database := openMigrationTestDatabaseURL(t, migrationTestDatabaseURL(t))
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func migrationTestDatabaseURL(t *testing.T) string {
	t.Helper()
	base := strings.TrimSpace(os.Getenv(integrationTestDatabaseEnvironment))
	if base == "" {
		t.Skipf("%s is not configured", integrationTestDatabaseEnvironment)
	}
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatalf("open migration test admin connection: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		_ = admin.Close()
		t.Fatalf("ping migration test admin connection: %v", err)
	}
	schema := "roomusic_migration_" + strings.ReplaceAll(createIdentifier(), "-", "")
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		_ = admin.Close()
		t.Fatalf("create migration test schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_, _ = admin.ExecContext(cleanupContext, `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
		_ = admin.Close()
	})
	scoped, err := integrationDatabaseURL(base, schema)
	if err != nil {
		t.Fatalf("scope migration test URL: %v", err)
	}
	return scoped
}

func openMigrationTestDatabaseURL(t *testing.T, databaseURL string) *sql.DB {
	t.Helper()
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open migration test database: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		t.Fatalf("ping migration test database: %v", err)
	}
	database.SetMaxOpenConns(2)
	return database
}

func openConcurrentMigrationDatabases(t *testing.T) (*sql.DB, *sql.DB) {
	t.Helper()
	base := strings.TrimSpace(os.Getenv(integrationTestDatabaseEnvironment))
	if base == "" {
		t.Skipf("%s is not configured", integrationTestDatabaseEnvironment)
	}
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatalf("open concurrent migration admin connection: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		_ = admin.Close()
		t.Fatalf("ping concurrent migration admin connection: %v", err)
	}
	schema := "roomusic_migration_concurrent_" + strings.ReplaceAll(createIdentifier(), "-", "")
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		_ = admin.Close()
		t.Fatalf("create concurrent migration schema: %v", err)
	}
	scoped, err := integrationDatabaseURL(base, schema)
	if err != nil {
		t.Fatalf("scope concurrent migration URL: %v", err)
	}
	first := openMigrationTestDatabaseURL(t, scoped)
	second := openMigrationTestDatabaseURL(t, scoped)
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_, _ = admin.ExecContext(cleanupContext, `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
		_ = admin.Close()
		_ = first.Close()
		_ = second.Close()
	})
	return first, second
}

func findMigrationByVersion(t *testing.T, version int64) migration {
	t.Helper()
	available, err := loadMigrations(migrations.Files)
	if err != nil {
		t.Fatalf("load embedded migrations: %v", err)
	}
	candidate, ok := migrationByVersion(available, version)
	if !ok {
		t.Fatalf("embedded migration %d not found", version)
	}
	return candidate
}

func findMigrationByName(t *testing.T, name string) migration {
	t.Helper()
	available, err := loadMigrations(migrations.Files)
	if err != nil {
		t.Fatalf("load embedded migrations: %v", err)
	}
	for _, candidate := range available {
		if candidate.Name == name {
			return candidate
		}
	}
	t.Fatalf("embedded migration %q not found", name)
	return migration{}
}

func equalInt64s(first, second []int64) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}

func migrationTestSHA256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
