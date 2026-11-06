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
