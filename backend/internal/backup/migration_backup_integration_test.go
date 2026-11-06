package backup

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/migrations"
)

func backupEmbeddedPrefix(t *testing.T, last int64) fstest.MapFS {
	t.Helper()
	source := fstest.MapFS{}
	entries, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range entries {
		prefix, _, _ := strings.Cut(name, "_")
		v, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		if v > last {
			continue
		}
		raw, err := fs.ReadFile(migrations.Files, name)
		if err != nil {
			t.Fatal(err)
		}
		source[name] = &fstest.MapFile{Data: raw}
	}
	return source
}
func assertPreMigrationArchive(t *testing.T, settingsDir, binDir, address, suffix string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(settingsDir, "*"+suffix))
	if err != nil || len(matches) != 1 {
		t.Fatalf("迁移前备份：%v %v", matches, err)
	}
	input, err := os.Open(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	_, env, err := connectionArguments(address)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var list bytes.Buffer
	if err := (ExecRunner{}).Run(ctx, Command{Path: filepath.Join(binDir, "pg_restore"), Args: []string{"--list"}, Env: env, Stdin: input, Stdout: &list}); err != nil {
		t.Fatalf("真实归档不可读：%v", err)
	}
	if !strings.Contains(list.String(), "TABLE public schema_migrations") || !strings.Contains(list.String(), "TABLE public users") {
		t.Fatalf("归档缺业务表：%s", list.String())
	}
	assertNoPartial(t, settingsDir)
}

func TestPostgreSQLPreMigrationBackupWithoutRollbackColumns(t *testing.T) {
	address := postgrestest.NewDatabase(t, "premigration_legacy_")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := postgres.Open(ctx, address, postgres.WithMigrationFS(backupEmbeddedPrefix(t, 20)))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `ALTER TABLE schema_migrations DROP COLUMN down_sql,DROP COLUMN down_checksum`); err != nil {
		t.Fatal(err)
	}
	var columns int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_attribute WHERE attrelid='schema_migrations'::regclass AND attname IN ('down_sql','down_checksum') AND NOT attisdropped`).Scan(&columns); err != nil || columns != 0 {
		t.Fatalf("前置库不应有回退列：%d %v", columns, err)
	}
	settings := integrationSettings(t, address)
	service := NewService(settings, nil)
	started := time.Now()
	opened, err := postgres.Open(ctx, address, postgres.WithMigrationBackups(service))
	if err != nil {
		t.Fatalf("缺回退列旧库升级必须在2分钟内完成（耗时%s）：%v", time.Since(started), err)
	}
	defer opened.Close()
	if ctx.Err() != nil {
		t.Fatal("Open 超过两分钟")
	}
	latest, err := postgres.LatestMigrationVersion()
	if err != nil {
		t.Fatal(err)
	}
	var version int64
	if err := opened.QueryRowContext(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&version); err != nil || version != latest {
		t.Fatalf("未升级到最新：%d/%d %v", version, latest, err)
	}
	if err := opened.QueryRowContext(ctx, `SELECT count(*) FROM pg_attribute WHERE attrelid='schema_migrations'::regclass AND attname IN ('down_sql','down_checksum') AND NOT attisdropped`).Scan(&columns); err != nil || columns != 2 {
		t.Fatalf("升级缺回退列：%d %v", columns, err)
	}
	var missing int
	if err := opened.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version>=31 AND (down_sql IS NULL OR down_checksum IS NULL)`).Scan(&missing); err != nil || missing != 0 {
		t.Fatalf("新迁移缺回退副本：%d %v", missing, err)
	}
	assertPreMigrationArchive(t, settings.Directory, settings.PGBinDirectory, address, "-pre-migration-20.dump")
}

func TestPostgreSQLPreMigrationBackupBeforeDowngrade(t *testing.T) {
	address := postgrestest.NewDatabase(t, "premigration_down_")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	latest, err := postgres.LatestMigrationVersion()
	if err != nil {
		t.Fatal(err)
	}
	source := backupEmbeddedPrefix(t, latest)
	v := latest + 1
	source[fmt.Sprintf("%04d_backup_probe.sql", v)] = &fstest.MapFile{Data: []byte(`CREATE TABLE downgrade_backup_probe(id int PRIMARY KEY); INSERT INTO downgrade_backup_probe VALUES(1)`)}
	source[fmt.Sprintf("%04d_backup_probe.down.sql", v)] = &fstest.MapFile{Data: []byte(`DROP TABLE downgrade_backup_probe`)}
	db, err := postgres.Open(ctx, address, postgres.WithMigrationFS(source))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	settings := integrationSettings(t, address)
	service := NewService(settings, nil)
	opened, err := postgres.Open(ctx, address, postgres.WithMigrationBackups(service))
	if err != nil {
		t.Fatalf("降级真实备份应按时返回：%v", err)
	}
	defer opened.Close()
	var version int64
	var exists bool
	if err := opened.QueryRowContext(ctx, `SELECT max(version),to_regclass('downgrade_backup_probe') IS NOT NULL FROM schema_migrations`).Scan(&version, &exists); err != nil || version != latest || exists {
		t.Fatalf("降级未完成：%d %v %v", version, exists, err)
	}
	assertPreMigrationArchive(t, settings.Directory, settings.PGBinDirectory, address, fmt.Sprintf("-pre-migration-%d.dump", v))
	// 归档必须保留降级前的新表，证明真实 pg_dump 在回退 DDL 之前完成。
	matches, _ := filepath.Glob(filepath.Join(settings.Directory, "*.dump"))
	input, err := os.Open(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	_, env, err := connectionArguments(address)
	if err != nil {
		t.Fatal(err)
	}
	var list bytes.Buffer
	if err := (ExecRunner{}).Run(ctx, Command{Path: filepath.Join(settings.PGBinDirectory, "pg_restore"), Args: []string{"--list"}, Env: env, Stdin: input, Stdout: &list}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list.String(), "TABLE public downgrade_backup_probe") {
		t.Fatal("迁移前备份未保留新表")
	}
}
