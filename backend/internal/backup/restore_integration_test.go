package backup

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
)

func TestPostgreSQLBackupRestoreIndependentDatabaseRowCounts(t *testing.T) {
	source, address := integrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	if _, err := source.Exec(`CREATE TABLE schema_migrations(version bigint PRIMARY KEY); INSERT INTO schema_migrations VALUES(1),(2); CREATE TABLE users(id int); INSERT INTO users VALUES(1),(2),(3); CREATE TABLE releases(id int); INSERT INTO releases VALUES(1),(2); CREATE TABLE tracks(id int); INSERT INTO tracks SELECT generate_series(1,7); CREATE TABLE favorite_tracks(id int); INSERT INTO favorite_tracks VALUES(1); CREATE TABLE favorite_albums(id int);`); err != nil {
		t.Fatal(err)
	}
	settings := integrationSettings(t, address)
	s := NewService(settings, nil)
	archive, err := s.RunNow(ctx, source, SourceManual)
	if err != nil {
		t.Fatal(err)
	}
	targetURL := postgrestest.NewDatabase(t, "restore_target_")
	target, err := postgres.Connect(ctx, targetURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.Exec(`CREATE TABLE users(id int); INSERT INTO users VALUES(99)`); err != nil {
		target.Close()
		t.Fatal(err)
	}
	target.Close()
	settings.DatabaseURL = targetURL
	r := NewService(settings, nil)
	session, err := r.PrepareRestore(ctx, archive.Name)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if session.Preview.BackupMigrations != (MigrationSummary{Version: 2, Rows: 2}) || session.Preview.CurrentMigrations.Rows != 0 {
		t.Fatalf("迁移预览不一致，--table筛选或管道归档解析失败：%+v", session.Preview)
	}
	result, err := session.Restore(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.SafetyBackup == "" || result.Migrations != session.Preview.BackupMigrations {
		t.Fatalf("恢复结果：%+v", result)
	}
	expected := map[string]int64{"users": 3, "releases": 2, "tracks": 7, "favorite_tracks": 1, "favorite_albums": 0}
	for _, table := range result.Tables {
		if table.Missing || table.Rows != expected[table.Name] {
			t.Errorf("恢复行数：%+v，期望%d", table, expected[table.Name])
		}
		delete(expected, table.Name)
	}
	if len(expected) != 0 {
		t.Fatalf("未校验表：%v", expected)
	}
	restored, err := postgres.Connect(ctx, targetURL)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var count int
	if err := restored.QueryRow(`SELECT count(*) FROM backup_probe`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("独立库内容未恢复：%d %v", count, err)
	}
	safety, _, err := r.OpenBackup(result.SafetyBackup)
	if err != nil {
		t.Fatal(err)
	}
	defer safety.Close()
	_, env, _ := connectionArguments(targetURL)
	summary, err := r.archiveMigrations(ctx, safety, env)
	if err != nil || summary.Rows != 0 {
		t.Fatalf("安全备份应为原目标库：%+v %v", summary, err)
	}
	assertNoPartial(t, settings.Directory)
}
func TestPostgreSQLBackupRestoreRefusesOtherConnection(t *testing.T) {
	db, address := integrationDB(t)
	settings := integrationSettings(t, address)
	s := NewService(settings, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	archive, err := s.RunNow(ctx, db, SourceManual)
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.PrepareRestore(ctx, archive.Name)
	if session != nil {
		defer session.Close()
	}
	var refused *RestoreError
	if !errors.As(err, &refused) || refused.Reason != "connections" || refused.Connections < 1 {
		t.Fatalf("其他连接存在时必须拒绝：%v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM backup_probe`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("拒绝后不得改变库：%d %v", count, err)
	}
}
func TestPostgreSQLBackupRestoreFailurePreservesSafetyBackup(t *testing.T) {
	db, address := integrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	settings := integrationSettings(t, address)
	s := NewService(settings, nil)
	archive, err := s.RunNow(ctx, db, SourceManual)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	runner := runnerFunc(func(ctx context.Context, c Command) error {
		if filepath.Base(c.Path) == "pg_restore" {
			for _, a := range c.Args {
				if a == "--single-transaction" {
					return &Error{Reason: "other", ExitCode: 1}
				}
			}
		}
		return (ExecRunner{}).Run(ctx, c)
	})
	r := NewService(settings, nil, WithRunner(runner))
	session, err := r.PrepareRestore(ctx, archive.Name)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	_, err = session.Restore(ctx)
	var failed *RestoreError
	if !errors.As(err, &failed) || failed.Reason != "restore" || failed.SafetyBackup == "" {
		t.Fatalf("恢复失败没有安全备份：%v", err)
	}
	// 安全备份名作为CLI的唯一位置参数，无需宿主路径，且可由同一个服务再次PrepareRestore。
	retry, err := s.PrepareRestore(ctx, failed.SafetyBackup)
	if err != nil {
		t.Fatalf("安全备份无法直接重用：%v", err)
	}
	defer retry.Close()
	if retry.Preview.Name != failed.SafetyBackup {
		t.Fatal(fmt.Sprintf("恢复文件不一致：%+v", retry.Preview))
	}
}

func TestPostgreSQLBackupRestoreRefusesInsufficientPrivileges(t *testing.T) {
	db, address := integrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	settings := integrationSettings(t, address)
	s := NewService(settings, nil)
	archive, err := s.RunNow(ctx, db, SourceManual)
	if err != nil {
		t.Fatal(err)
	}
	username := "restore_user_" + strings.ReplaceAll(uuid.New(), "-", "")
	quoted := pgx.Identifier{username}.Sanitize()
	if _, err := db.ExecContext(ctx, `CREATE ROLE `+quoted+` LOGIN PASSWORD 'restore-test-password' NOSUPERUSER NOCREATEDB`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`DROP ROLE ` + quoted); err != nil {
			t.Error(err)
		}
	})
	parsed, err := url.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	parsed.User = url.UserPassword(username, "restore-test-password")
	settings.DatabaseURL = parsed.String()
	restricted := NewService(settings, nil)
	session, err := restricted.PrepareRestore(ctx, archive.Name)
	if session != nil {
		session.Close()
	}
	var refused *RestoreError
	if !errors.As(err, &refused) || refused.Reason != "permission" {
		t.Fatalf("无CREATEDB应拒绝：%v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM backup_probe`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("拒绝恢复不得改变库：%d %v", count, err)
	}
}
