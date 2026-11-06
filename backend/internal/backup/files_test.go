package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOverviewLegacyNamesAndRetention(t *testing.T) {
	dir := t.TempDir()
	now := instant("2026-11-06T12:00:00Z")
	files := map[string]Source{"roomusic-20261001-030000.dump": SourceScheduled, "roomusic-20261002-030000-manual.dump": SourceManual, "roomusic-20261106-030000-pre-migration-17.dump": SourcePreMigration, "roomusic-20261106-030000-pre-restore.dump": SourcePreRestore}
	for name := range files {
		at := now
		if name[9:15] == "202610" {
			at = now.Add(-30 * 24 * time.Hour)
		}
		writeBackup(t, dir, name, at)
	}
	writeBackup(t, dir, "roomusic-x.dump", now)
	writeBackup(t, dir, ".last-success", now)
	if err := os.Symlink(filepath.Join(dir, "roomusic-20261001-030000.dump"), filepath.Join(dir, "roomusic-20261106-040000.dump")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "roomusic-20261106-050000.dump"), 0700); err != nil {
		t.Fatal(err)
	}
	s := NewService(testSettings(dir), nil, WithClock(func() time.Time { return now }))
	got, err := s.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 4 {
		t.Fatalf("只应列出4份普通文件：%+v", got)
	}
	for _, file := range got.Backups {
		if files[file.Name] != file.Source {
			t.Fatalf("来源错误：%+v", file)
		}
	}
	s.retain()
	got, _ = s.Overview(context.Background())
	if got.Total != 2 {
		t.Fatalf("旧命名未参与清理：%+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, ".last-success")); err != nil {
		t.Fatal("不得删除旧状态文件")
	}
}

func TestOverviewLimitsListButReportsTotal(t *testing.T) {
	dir := t.TempDir()
	now := instant("2026-11-06T12:00:00Z")
	for i := 0; i < 501; i++ {
		at := now.Add(time.Duration(i) * time.Second)
		writeBackup(t, dir, fileName(at, SourceManual, 0), at)
	}
	s := NewService(testSettings(dir), nil)
	got, err := s.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 501 || len(got.Backups) != 500 {
		t.Fatalf("未保留总数或未截断：%+v", got)
	}
	if !got.Backups[0].CreatedAt.After(got.Backups[499].CreatedAt) {
		t.Fatal("未按mtime倒序")
	}
}
