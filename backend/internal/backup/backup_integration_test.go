package backup

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
)

func integrationSettings(t *testing.T, databaseURL string) config.Backup {
	t.Helper()
	settings := testSettings(t.TempDir())
	settings.DatabaseURL = databaseURL
	settings.PGBinDirectory = os.Getenv("ROOMUSIC_TEST_PG_BIN_DIR")
	if settings.PGBinDirectory == "" {
		t.Fatal("集成测试必须通过 scripts/test-integration.sh 提供固定 PG 工具包装")
	}
	return settings
}
func integrationDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	address := postgrestest.NewDatabase(t, "backup_")
	db, err := postgres.Connect(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var version int
	if err := db.QueryRow(`SHOW server_version_num`).Scan(&version); err != nil || version/10000 != 18 {
		t.Fatalf("必须使用PG18：%d %v", version, err)
	}
	if _, err := db.Exec(`CREATE TABLE backup_probe(id integer PRIMARY KEY, title text); INSERT INTO backup_probe VALUES(1,'合成数据'),(2,'backup-test')`); err != nil {
		t.Fatal(err)
	}
	return db, address
}
func assertNoPartial(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), "partial") {
			t.Errorf("残留临时文件：%s", e.Name())
		}
	}
}

func TestPostgreSQLBackupPinnedToolsProduceValidArchive(t *testing.T) {
	db, address := integrationDB(t)
	settings := integrationSettings(t, address)
	s := NewService(settings, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	file, err := s.RunNow(ctx, db, SourceManual)
	if err != nil {
		t.Fatalf("固定包装在精简子进程环境中必须可执行：%v", err)
	}
	input, err := os.Open(filepath.Join(settings.Directory, file.Name))
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
	if !strings.Contains(list.String(), "TABLE public backup_probe") {
		t.Fatalf("归档缺少真实业务表：%s", list.String())
	}
	info, _ := input.Stat()
	if info.Mode().Perm() != 0600 {
		t.Fatalf("权限 %v", info.Mode())
	}
	assertNoPartial(t, settings.Directory)
}
func TestPostgreSQLBackupDumpFailureLeavesNoPublishedOrPartialFile(t *testing.T) {
	db, address := integrationDB(t)
	settings := integrationSettings(t, address)
	parsed, _ := url.Parse(address)
	parsed.User = url.UserPassword("roomusic_test", "deliberately-wrong")
	settings.DatabaseURL = parsed.String()
	s := NewService(settings, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, err := s.RunNow(ctx, db, SourceManual)
	var failed *Error
	if !errors.As(err, &failed) || failed.Stage != "pg_dump" || failed.Reason != "auth_failed" {
		t.Fatalf("应在真正的pg_dump认证阶段失败，得到 %v", err)
	}
	got, _ := s.Overview(ctx)
	if got.Total != 0 {
		t.Fatalf("失败留下完整备份：%+v", got)
	}
	assertNoPartial(t, settings.Directory)
}

// 阻塞真实 pg_dump 的开始点，使两个请求确定重叠，而不是依靠调度碰巧产生竞争。
func blockingRealRunner(entered chan<- struct{}, release <-chan struct{}) Runner {
	return runnerFunc(func(ctx context.Context, c Command) error {
		if filepath.Base(c.Path) == "pg_dump" && len(c.Args) > 1 {
			entered <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return (ExecRunner{}).Run(ctx, c)
	})
}
func TestPostgreSQLBackupConcurrentManualOnlyPublishesOnce(t *testing.T) {
	for _, separate := range []bool{false, true} {
		t.Run(map[bool]string{false: "两个goroutine同一Service", true: "两个Service"}[separate], func(t *testing.T) {
			db, address := integrationDB(t)
			settings := integrationSettings(t, address)
			entered := make(chan struct{}, 2)
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			s := NewService(settings, nil, WithRunner(blockingRealRunner(entered, release)))
			other := s
			if separate {
				other = NewService(settings, nil)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			type result struct {
				started bool
				err     error
			}
			first := make(chan result, 1)
			go func() { _, started, err := s.StartManual(ctx, db); first <- result{started, err} }()
			select {
			case got := <-first:
				if got.err != nil || !got.started {
					t.Fatalf("首个请求：%+v", got)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("真实pg_dump未开始")
			}
			second := make(chan result, 1)
			go func() { _, started, err := other.StartManual(ctx, db); second <- result{started, err} }()
			got := <-second
			if separate {
				if !errors.Is(got.err, ErrBusy) {
					t.Errorf("另一Service应busy：%+v", got)
				}
			} else if got.err != nil || got.started {
				t.Errorf("同进程应返回已有状态：%+v", got)
			}
			unblock()
			if err := s.Wait(ctx); err != nil {
				t.Fatal(err)
			}
			if separate {
				if err := other.Wait(ctx); err != nil {
					t.Fatal(err)
				}
			}
			overview, _ := s.Overview(ctx)
			if overview.Total != 1 {
				t.Fatalf("只应发布一份：%+v", overview)
			}
			assertNoPartial(t, settings.Directory)
		})
	}
}
func TestPostgreSQLBackupExternalLockReturnsBusy(t *testing.T) {
	db, address := integrationDB(t)
	settings := integrationSettings(t, address)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1::bigint)`, backupAdvisoryLockKey); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1::bigint)`, backupAdvisoryLockKey)
	s := NewService(settings, nil)
	_, started, err := s.StartManual(ctx, db)
	if !errors.Is(err, ErrBusy) || started {
		t.Fatalf("期望 busy：%v %v", started, err)
	}
	overview, _ := s.Overview(ctx)
	if overview.Total != 0 || overview.Status.LastFailure != nil || overview.Status.Running != nil {
		t.Fatalf("争锁不应记录失败或写文件：%+v", overview)
	}
}
