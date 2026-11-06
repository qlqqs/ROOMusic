package backup

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConnectionArgumentsRedactsCredentialsAndLimitsEnvironment(t *testing.T) {
	address, env, err := connectionArguments("postgres://user:p%40ss@localhost:5432/test?password=query-secret&application_name=untrusted&sslmode=disable&search_path=elsewhere")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(address, "secret") || strings.Contains(address, "p%40ss") || strings.Contains(address, "search_path") || strings.Contains(address, "application_name") {
		t.Fatalf("连接参数未去敏：%s", address)
	}
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "PGPASSWORD=query-secret") || !strings.Contains(joined, "PGAPPNAME=roomusic-backup") {
		t.Fatalf("子进程环境：%v", env)
	}
	for _, value := range env {
		if !strings.HasPrefix(value, "PGPASSWORD=") && !strings.HasPrefix(value, "PGAPPNAME=") && !strings.HasPrefix(value, "PGCONNECT_TIMEOUT=") && value != "LC_ALL=C" {
			t.Errorf("不应继承额外环境：%s", value)
		}
	}
}
func TestServiceFailureLogsNeverContainToolOutputOrCredentials(t *testing.T) {
	var logs strings.Builder
	secret := "password authentication failed: secret-pg-error"
	runner := runnerFunc(func(ctx context.Context, c Command) error {
		if len(c.Args) == 1 && c.Args[0] == "--version" {
			_, err := io.WriteString(c.Stdout, "pg (PostgreSQL) 18.1")
			return err
		}
		return errors.New(secret)
	})
	s := NewService(testSettings(t.TempDir()), slog.New(slog.NewJSONHandler(&logs, nil)), WithRunner(runner), WithDatabaseAccess(fakeDatabase{}))
	_, err := s.RunNow(context.Background(), nil, SourceManual)
	if err == nil {
		t.Fatal("失败未注入")
	}
	if strings.Contains(logs.String(), secret) || strings.Contains(logs.String(), "postgres://") || strings.Contains(logs.String(), "secret") {
		t.Fatalf("日志泄漏：%s", logs.String())
	}
	if !strings.Contains(logs.String(), "backup.failed") || !strings.Contains(logs.String(), `"stage":"pg_dump"`) {
		t.Fatalf("缺少分类：%s", logs.String())
	}
}
func TestServiceCancellationRemovesPartialAndKeepsOldBackup(t *testing.T) {
	dir := t.TempDir()
	now := instant("2026-11-06T12:00:00Z")
	old := "roomusic-20261001-030000.dump"
	writeBackup(t, dir, old, now.Add(-30*24*time.Hour))
	entered := make(chan struct{})
	runner := runnerFunc(func(ctx context.Context, c Command) error {
		if len(c.Args) == 1 && c.Args[0] == "--version" {
			_, err := io.WriteString(c.Stdout, "pg (PostgreSQL) 18.1")
			return err
		}
		if _, err := io.WriteString(c.Stdout, "PGDMP-incomplete"); err != nil {
			return err
		}
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	})
	s := NewService(testSettings(dir), nil, WithRunner(runner), WithDatabaseAccess(fakeDatabase{}), WithClock(func() time.Time { return now }))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.RunNow(ctx, nil, SourceManual); done <- err }()
	<-entered
	cancel()
	if err := <-done; err == nil {
		t.Fatal("取消应失败")
	}
	assertNoPartial(t, dir)
	if _, err := os.Stat(filepath.Join(dir, old)); err != nil {
		t.Fatal("取消时清理了旧备份")
	}
}
func TestPublishCollisionNeverOverwritesExistingBackup(t *testing.T) {
	dir := t.TempDir()
	start := instant("2026-11-06T12:00:00Z")
	existing := fileName(start, SourceManual, 0)
	writeBackup(t, dir, existing, start)
	partial, err := os.CreateTemp(dir, ".roomusic-collision.partial.*")
	if err != nil {
		t.Fatal(err)
	}
	defer partial.Close()
	defer os.Remove(partial.Name())
	if _, err := partial.WriteString("PGDMP-new"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	clock := func() time.Time {
		calls++
		if calls == 1 {
			return start
		}
		return start.Add(time.Second)
	}
	s := NewService(testSettings(dir), nil, WithClock(clock))
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	published, err := s.publish(ctx, partial, SourceManual, 0)
	if err != nil {
		t.Fatal(err)
	}
	if published.Name == existing || calls < 2 {
		t.Fatal("没有经过文件名冲突重试")
	}
	content, err := os.ReadFile(filepath.Join(dir, existing))
	if err != nil || string(content) != "PGDMP-old" {
		t.Fatalf("覆盖既有文件：%q %v", content, err)
	}
	overview, _ := s.Overview(ctx)
	if overview.Total != 2 {
		t.Fatalf("未保留两份：%+v", overview)
	}
}
