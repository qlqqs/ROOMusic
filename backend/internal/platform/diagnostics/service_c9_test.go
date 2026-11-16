package diagnostics

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type c9Driver struct {
	ping       func(context.Context) error
	queryError bool
}

func (d c9Driver) Open(string) (driver.Conn, error) { return c9Connection{d}, nil }

type c9Connection struct{ c9Driver }

func (c9Connection) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (c9Connection) Close() error                        { return nil }
func (c9Connection) Begin() (driver.Tx, error)           { return nil, errors.New("unused") }
func (c c9Connection) Ping(ctx context.Context) error    { return c.ping(ctx) }
func (c c9Connection) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	if c.queryError {
		return nil, errors.New("postgres://synthetic:private@db/app /private/data")
	}
	return &c9Rows{}, nil
}

type c9Rows struct{ read bool }

func (*c9Rows) Columns() []string { return []string{"version"} }
func (*c9Rows) Close() error      { return nil }
func (r *c9Rows) Next(v []driver.Value) error {
	if r.read {
		return io.EOF
	}
	r.read = true
	v[0] = int64(56)
	return nil
}

var c9DriverID atomic.Int64

func c9DB(t *testing.T, d c9Driver) *sql.DB {
	t.Helper()
	name := fmt.Sprintf("c9-diagnostics-%d", c9DriverID.Add(1))
	sql.Register(name, d)
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestC9SystemStatusDatabaseFailuresAndTimeout(t *testing.T) {
	for _, kind := range []string{"nil", "unavailable", "timeout", "healthy", "partial"} {
		t.Run(kind, func(t *testing.T) {
			service := &Service{StartedAt: time.Now().Add(-time.Minute), CatalogStats: func(context.Context) (any, error) { return map[string]int{"tracks": 7}, nil }}
			if kind != "nil" {
				service.Database = c9DB(t, c9Driver{queryError: kind == "partial", ping: func(ctx context.Context) error {
					if kind == "timeout" {
						<-ctx.Done()
						return ctx.Err()
					}
					if kind == "unavailable" {
						return errors.New("postgres://synthetic:secret@db/app /private/data")
					}
					return nil
				}})
			}
			if kind == "partial" {
				service.CatalogStats = func(context.Context) (any, error) { return nil, errors.New("private data") }
			}
			start := time.Now()
			out, err := service.SystemStatus(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if out.Build["version"] == "" || out.UptimeSeconds < 59 {
				t.Fatalf("进程事实缺失: %+v", out)
			}
			if kind == "healthy" {
				if !out.DatabaseConnected || out.MigrationVersion == nil || *out.MigrationVersion != 56 || out.Catalog == nil || len(out.Errors) != 0 {
					t.Fatalf("正常状态: %+v", out)
				}
			} else if kind == "partial" {
				if !out.DatabaseConnected || out.MigrationVersion != nil || out.Catalog != nil || strings.Join(out.Errors, ",") != "migration_unavailable,catalog_unavailable" {
					t.Fatalf("局部失败: %+v", out)
				}
			} else {
				if out.DatabaseConnected || out.MigrationVersion != nil || out.Catalog != nil || strings.Join(out.Errors, ",") != "database_unavailable" {
					t.Fatalf("数据库失败: %+v", out)
				}
			}
			if kind == "timeout" && (time.Since(start) < 1900*time.Millisecond || time.Since(start) > 3*time.Second) {
				t.Fatalf("探测超时=%s", time.Since(start))
			}
			raw, _ := json.Marshal(out)
			for _, bad := range []string{"postgres://", "/private/", "secret", "ROOMUSIC_"} {
				if strings.Contains(string(raw), bad) {
					t.Errorf("诊断泄露 %q: %s", bad, raw)
				}
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&Service{}).SystemStatus(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestC9DiskUsageSafeSuccessAndFailure(t *testing.T) {
	root := t.TempDir()
	service := &Service{DataDirectory: root, BackupDirectory: filepath.Join(root, "missing-private-backup"), MusicDirectories: func(context.Context) ([]Directory, error) {
		return []Directory{{Kind: "music", RootID: "synthetic-root", Path: root}}, nil
	}}
	out, err := service.DiskUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 || out[0].TotalBytes == nil || *out[0].TotalBytes == 0 || out[0].AvailableBytes == nil || out[1].ErrorCode != "disk_unavailable" || out[1].TotalBytes != nil || out[1].AvailableBytes != nil || out[2].RootID != "synthetic-root" {
		t.Fatalf("磁盘结果: %+v", out)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), root) || strings.Contains(string(raw), "ROOMUSIC_") {
		t.Fatalf("路径泄露: %s", raw)
	}
	service.MusicDirectories = func(context.Context) ([]Directory, error) { return nil, errors.New("synthetic error") }
	if _, err := service.DiskUsage(context.Background()); err == nil {
		t.Fatal("目录读取失败被隐藏")
	}
}

func TestC9DiskUsageSafePartialFailure(t *testing.T) {
	directory := t.TempDir()
	missing := filepath.Join(directory, "missing-private-directory")
	s := &Service{DataDirectory: directory, BackupDirectory: missing, MusicDirectories: func(context.Context) ([]Directory, error) {
		return []Directory{{Kind: "music", RootID: "synthetic-root", Path: directory}}, nil
	}}
	items, err := s.DiskUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || items[0].TotalBytes == nil || *items[0].TotalBytes == 0 || items[0].AvailableBytes == nil || items[1].ErrorCode != "disk_unavailable" || items[1].TotalBytes != nil || items[1].AvailableBytes != nil || items[2].RootID != "synthetic-root" {
		t.Fatalf("磁盘局部故障=%+v", items)
	}
	raw, _ := json.Marshal(items)
	if strings.Contains(string(raw), directory) || strings.Contains(string(raw), "no such file") {
		t.Fatalf("磁盘输出泄露路径: %s", raw)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.DiskUsage(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消未传递: %v", err)
	}
}

func TestC9DiskUsageSlotTimeoutAndCancellation(t *testing.T) {
	// 占满系统调用槽位，验证排队也受每项两秒截止时间约束。
	for i := 0; i < cap(diskStatSlots); i++ {
		diskStatSlots <- struct{}{}
	}
	release := func() {
		for i := 0; i < cap(diskStatSlots); i++ {
			<-diskStatSlots
		}
	}
	defer release()
	directory := t.TempDir()
	service := &Service{DataDirectory: directory, BackupDirectory: directory, MusicDirectories: func(context.Context) ([]Directory, error) { return nil, nil }}
	start := time.Now()
	items, err := service.DiskUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 3900*time.Millisecond || elapsed > 6*time.Second {
		t.Errorf("两个独立探测截止时间=%s", elapsed)
	}
	for _, item := range items {
		if item.ErrorCode != "disk_unavailable" || item.TotalBytes != nil || item.AvailableBytes != nil {
			t.Errorf("超时容量=%+v", item)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := service.DiskUsage(ctx); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("调用者取消未传递")
	}
	if len(diskStatSlots) != cap(diskStatSlots) {
		t.Fatal("超时改变在途槽位")
	}
}
