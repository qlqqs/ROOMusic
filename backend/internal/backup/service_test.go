package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
)

type runnerFunc func(context.Context, Command) error

func (f runnerFunc) Run(ctx context.Context, c Command) error { return f(ctx, c) }

type fakeDatabase struct{}

func (fakeDatabase) Lock(context.Context, *sql.DB, bool) (func(), bool, error) {
	return func() {}, true, nil
}
func (fakeDatabase) ServerMajor(context.Context, *sql.DB) (int, error) { return 18, nil }
func fakeRunner(failStage string) Runner {
	return runnerFunc(func(_ context.Context, c Command) error {
		if len(c.Args) == 1 && c.Args[0] == "--version" {
			_, err := fmt.Fprintln(c.Stdout, "pg (PostgreSQL) 18.1")
			return err
		}
		stage := "pg_dump"
		if filepath.Base(c.Path) == "pg_restore" {
			stage = "verify"
		}
		if stage == failStage {
			return &Error{Reason: "auth_failed", ExitCode: 1}
		}
		if stage == "pg_dump" {
			_, err := c.Stdout.Write([]byte("PGDMP-test"))
			return err
		}
		return nil
	})
}
func writeBackup(t *testing.T, dir, name string, at time.Time) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("PGDMP-old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}
func testSettings(dir string) config.Backup {
	return config.Backup{Directory: dir, DatabaseURL: "postgres://test:secret@localhost/test?sslmode=disable", Location: time.UTC, Hour: 3, KeepDays: 14}
}

func TestServiceRetentionOnlyAfterSuccess(t *testing.T) {
	for _, stage := range []string{"", "pg_dump", "verify"} {
		t.Run("失败阶段="+stage, func(t *testing.T) {
			dir := t.TempDir()
			now := instant("2026-11-06T12:00:00Z")
			expired := "roomusic-20261001-030000.dump"
			boundary := "roomusic-20261023-120000-manual.dump"
			unknown := "roomusic-x.dump"
			writeBackup(t, dir, expired, now.Add(-15*24*time.Hour))
			writeBackup(t, dir, boundary, now.Add(-14*24*time.Hour))
			writeBackup(t, dir, unknown, now.Add(-30*24*time.Hour))
			s := NewService(testSettings(dir), nil, WithClock(func() time.Time { return now }), WithRunner(fakeRunner(stage)), WithDatabaseAccess(fakeDatabase{}))
			file, err := s.RunNow(context.Background(), nil, SourceManual)
			if stage == "" && err != nil {
				t.Fatal(err)
			}
			if stage != "" && err == nil {
				t.Fatal("失败注入没有生效")
			}
			_, statErr := os.Stat(filepath.Join(dir, expired))
			if (stage == "") != errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("过期文件状态 %v", statErr)
			}
			for _, name := range []string{boundary, unknown} {
				if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
					t.Fatalf("不得删除 %s：%v", name, err)
				}
			}
			overview, err := s.Overview(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if stage == "" {
				if overview.Status.LastFailure != nil || !overview.Status.LastSuccessAt.Equal(now) {
					t.Fatalf("成功状态 %+v", overview.Status)
				}
				info, err := os.Stat(filepath.Join(dir, file.Name))
				if err != nil || info.Mode().Perm() != 0600 {
					t.Fatalf("权限不是0600：%v %v", info, err)
				}
			} else {
				if overview.Status.LastFailure == nil || overview.Status.LastFailure.Stage != stage {
					t.Fatalf("失败状态 %+v", overview.Status)
				}
			}
			entries, _ := os.ReadDir(dir)
			for _, entry := range entries {
				if strings.Contains(entry.Name(), "partial") {
					t.Fatalf("残留 partial：%s", entry.Name())
				}
			}
		})
	}
}

func TestServiceStartupCleansOnlyStalePartials(t *testing.T) {
	dir := t.TempDir()
	now := instant("2026-11-06T12:00:00Z")
	for _, name := range []string{".roomusic-old.partial.1", ".roomusic-new.partial.2", ".unrelated.partial.1"} {
		at := now.Add(-25 * time.Hour)
		if strings.Contains(name, "new") {
			at = now.Add(-time.Hour)
		}
		writeBackup(t, dir, name, at)
	}
	s := NewService(testSettings(dir), nil, WithClock(func() time.Time { return now }))
	s.cleanPartials()
	if _, err := os.Stat(filepath.Join(dir, ".roomusic-old.partial.1")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("旧 partial 未清理")
	}
	for _, name := range []string{".roomusic-new.partial.2", ".unrelated.partial.1"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestServicePreMigrationBackupNamesOriginalVersion(t *testing.T) {
	dir := t.TempDir()
	now := instant("2026-11-06T12:00:00Z")
	settings := testSettings(dir)
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	settings.Location = location
	s := NewService(settings, nil, WithClock(func() time.Time { return now }), WithRunner(fakeRunner("")), WithDatabaseAccess(fakeDatabase{}))
	name, err := s.BackupBeforeMigration(context.Background(), nil, postgres.MigrationChange{Direction: postgres.MigrationUpgrade, FromVersion: 17, ToVersion: 18, Versions: []int64{18}})
	if err != nil {
		t.Fatal(err)
	}
	if name != "roomusic-20261106-200000-pre-migration-17.dump" {
		t.Fatalf("迁移前版本或本地时间错误：%s", name)
	}
	if latest, ok := s.LatestPreMigrationBackup(17); !ok || latest != name {
		t.Fatalf("无法找到原版本安全备份：%s %v", latest, ok)
	}
	if _, ok := s.LatestPreMigrationBackup(18); ok {
		t.Fatal("不能把目标版本当作原版本")
	}
	overview, _ := s.Overview(context.Background())
	if overview.Backups[0].Source != SourcePreMigration {
		t.Fatalf("来源错误：%+v", overview)
	}
}

func TestServiceBackupSuccessUsesSavedRetention(t *testing.T) {
	for _, source := range []Source{SourceScheduled, SourceManual, SourcePreMigration, SourcePreRestore} {
		t.Run(string(source), func(t *testing.T) {
			dir := t.TempDir()
			now := instant("2026-11-06T12:00:00Z")
			recent, overCount, expired := "roomusic-20261106-110000-manual.dump", "roomusic-20261106-100000-pre-migration-17.dump", "roomusic-20261001-120000-pre-restore.dump"
			writeBackup(t, dir, recent, now.Add(-time.Hour))
			writeBackup(t, dir, overCount, now.Add(-2*time.Hour))
			writeBackup(t, dir, expired, now.Add(-36*24*time.Hour))
			logger, output := backupTestLogger()
			s := NewService(testSettings(dir), logger, WithClock(func() time.Time { return now }), WithRunner(fakeRunner("")), WithDatabaseAccess(fakeDatabase{}))
			if err := s.writeRetention(Retention{KeepDays: retentionInt(14), KeepCount: retentionInt(2)}); err != nil {
				t.Fatal(err)
			}
			var file File
			var err error
			if source == SourcePreMigration {
				file.Name, err = s.BackupBeforeMigration(context.Background(), nil, postgres.MigrationChange{FromVersion: 17})
			} else {
				file, err = s.RunNow(context.Background(), nil, source)
			}
			if err != nil {
				t.Fatal(err)
			}
			requireBackupExists(t, dir, file.Name, recent, retentionName)
			requireBackupMissing(t, dir, overCount, expired)
			for _, record := range backupLogEvents(t, output, "backup.retention.deleted") {
				if record["trigger"] != "backup" {
					t.Fatalf("清理来源错误：%v", record)
				}
			}
		})
	}
}

func TestServiceNeverSavedRetentionOnlyUsesConfiguredDays(t *testing.T) {
	dir := t.TempDir()
	now := instant("2026-11-06T12:00:00Z")
	expired := "roomusic-20261001-120000.dump"
	writeBackup(t, dir, expired, now.Add(-8*24*time.Hour))
	var kept []string
	for i := 1; i <= 5; i++ {
		at := now.Add(-time.Duration(i) * 24 * time.Hour)
		name := fileName(at, SourceManual, 0)
		writeBackup(t, dir, name, at)
		kept = append(kept, name)
	}
	settings := testSettings(dir)
	settings.KeepDays = 7
	s := NewService(settings, nil, WithClock(func() time.Time { return now }), WithRunner(fakeRunner("")), WithDatabaseAccess(fakeDatabase{}))
	file, err := s.RunNow(context.Background(), nil, SourceManual)
	if err != nil {
		t.Fatal(err)
	}
	requireBackupExists(t, dir, append(kept, file.Name)...)
	requireBackupMissing(t, dir, expired, retentionName)
	overview, err := s.Overview(context.Background())
	if err != nil || overview.Total != 6 || overview.Retention == nil || overview.Retention.KeepDays == nil || *overview.Retention.KeepDays != 7 || overview.Retention.KeepCount != nil {
		t.Fatalf("未保存时不能限制份数：%+v %v", overview, err)
	}
}

func TestServiceSuccessfulBackupUnlimitedOverridesEnvironment(t *testing.T) {
	dir := t.TempDir()
	now := instant("2026-11-06T12:00:00Z")
	expired := "roomusic-20261001-120000-pre-restore.dump"
	writeBackup(t, dir, expired, now.Add(-36*24*time.Hour))
	s := NewService(testSettings(dir), nil, WithClock(func() time.Time { return now }), WithRunner(fakeRunner("")), WithDatabaseAccess(fakeDatabase{}))
	if err := s.writeRetention(Retention{}); err != nil {
		t.Fatal(err)
	}
	file, err := s.RunNow(context.Background(), nil, SourceManual)
	if err != nil {
		t.Fatal(err)
	}
	requireBackupExists(t, dir, expired, file.Name, retentionName)
	overview, err := s.Overview(context.Background())
	if err != nil || overview.Total != 2 || overview.Retention == nil || overview.Retention.KeepDays != nil || overview.Retention.KeepCount != nil {
		t.Fatalf("保存的不限必须优先于环境变量：%+v %v", overview, err)
	}
}
