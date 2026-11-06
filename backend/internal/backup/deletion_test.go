package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func backupTestLogger() (*slog.Logger, *bytes.Buffer) {
	var output bytes.Buffer
	return slog.New(slog.NewJSONHandler(&output, nil)), &output
}

func backupLogEvents(t *testing.T, output *bytes.Buffer, event string) []map[string]any {
	t.Helper()
	var found []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record["event"] == event {
			found = append(found, record)
		}
	}
	return found
}

func requireBackupExists(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := os.Lstat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s 必须保留：%v", name, err)
		}
	}
}

func requireBackupMissing(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := os.Lstat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s 必须删除：%v", name, err)
		}
	}
}

func backupReadOnly(t *testing.T, dir string, mode os.FileMode) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root 可绕过目录权限，不能验证不可写语义")
	}
	if err := os.Chmod(dir, mode); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0700); err != nil {
			t.Error(err)
		}
	})
}

func TestDeleteBackupSuccessAndRepeatedDelete(t *testing.T) {
	dir := t.TempDir()
	name := "roomusic-20261106-120000-manual.dump"
	others := []string{"roomusic-20261105-030000.dump", stateName, retentionName, ".roomusic-test.partial.1", "unrelated.txt"}
	for _, file := range append([]string{name}, others...) {
		writeBackup(t, dir, file, time.Now())
	}
	logger, output := backupTestLogger()
	s := NewService(testSettings(dir), logger)
	if err := s.DeleteBackup(context.Background(), name, "actor-delete"); err != nil {
		t.Fatal(err)
	}
	requireBackupMissing(t, dir, name)
	requireBackupExists(t, dir, others...)
	for _, file := range others {
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil || string(data) != "PGDMP-old" {
			t.Fatalf("其他文件发生变化：%s %q %v", file, data, err)
		}
	}
	if err := s.DeleteBackup(context.Background(), name, "actor-delete"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("重复删除：%v", err)
	}
	records := backupLogEvents(t, output, "backup.deleted")
	if len(records) != 1 || records[0]["file"] != name || records[0]["actor_id"] != "actor-delete" || records[0]["level"] != "INFO" {
		t.Fatalf("成功日志：%s", output)
	}
	if len(backupLogEvents(t, output, "backup.delete_failed")) != 0 {
		t.Fatalf("404 不应记失败：%s", output)
	}
}

func TestDeleteBackupRejectsUnsafeNamesAndObjects(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("外部文件"), 0600); err != nil {
		t.Fatal(err)
	}
	target := "roomusic-20261106-110000.dump"
	writeBackup(t, dir, target, time.Now())
	insideLink, outsideLink := "roomusic-20261106-120001.dump", "roomusic-20261106-120002.dump"
	directory, fifo := "roomusic-20261106-120003.dump", "roomusic-20261106-120004.dump"
	for name, destination := range map[string]string{insideLink: filepath.Join(dir, target), outsideLink: outside} {
		if err := os.Symlink(destination, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, directory), 0700); err != nil {
		t.Fatal(err)
	}
	writeBackup(t, filepath.Join(dir, directory), "child", time.Now())
	if err := unix.Mkfifo(filepath.Join(dir, fifo), 0600); err != nil {
		t.Fatal(err)
	}
	protected := []string{stateName, retentionName, ".roomusic-test.partial.1", "roomusic-x.dump"}
	for _, name := range protected {
		writeBackup(t, dir, name, time.Now())
	}
	s := NewService(testSettings(dir), nil)
	for _, name := range append([]string{"../" + target, "..", "roomusic-20261106-120000\\manual.dump", "roomusic-20261106-120000\x00.dump", insideLink, outsideLink, directory, fifo}, protected...) {
		t.Run(name, func(t *testing.T) {
			if err := s.DeleteBackup(context.Background(), name, "actor"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("不安全目标应视为不存在：%v", err)
			}
		})
	}
	requireBackupExists(t, dir, append([]string{target, insideLink, outsideLink, directory, fifo, filepath.Join(directory, "child")}, protected...)...)
	for name, destination := range map[string]string{insideLink: filepath.Join(dir, target), outsideLink: outside} {
		if got, err := os.Readlink(filepath.Join(dir, name)); err != nil || got != destination {
			t.Fatalf("符号链接被改变：%q %v", got, err)
		}
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "外部文件" {
		t.Fatalf("外部文件被改变：%q %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, target)); err != nil || string(data) != "PGDMP-old" {
		t.Fatalf("内部目标被改变：%q %v", data, err)
	}
}

func TestDeleteBackupMissingDirectory(t *testing.T) {
	s := NewService(testSettings(filepath.Join(t.TempDir(), "missing")), nil)
	if err := s.DeleteBackup(context.Background(), "roomusic-20261106-120000.dump", "actor"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestDeleteBackupNotWritable(t *testing.T) {
	dir := t.TempDir()
	name := "roomusic-20261106-120000.dump"
	writeBackup(t, dir, name, time.Now())
	logger, output := backupTestLogger()
	s := NewService(testSettings(dir), logger)
	backupReadOnly(t, dir, 0500)
	if err := s.DeleteBackup(context.Background(), name, "actor"); !errors.Is(err, ErrNotWritable) {
		t.Fatalf("目录0500：%v", err)
	}
	requireBackupExists(t, dir, name)
	records := backupLogEvents(t, output, "backup.delete_failed")
	if len(records) != 1 || records[0]["file"] != name || records[0]["reason"] != "not_writable" || records[0]["level"] != "WARN" {
		t.Fatalf("失败日志：%s", output)
	}
	if strings.Contains(output.String(), dir) {
		t.Fatalf("日志泄露路径：%s", output)
	}
}

func TestDeleteBackupsDeduplicatesAndPreservesOrder(t *testing.T) {
	dir := t.TempDir()
	first, second := "roomusic-20261106-120002.dump", "roomusic-20261106-120001.dump"
	missing, link := "roomusic-20261106-120003.dump", "roomusic-20261106-120004.dump"
	writeBackup(t, dir, first, time.Now())
	writeBackup(t, dir, second, time.Now())
	if err := os.Symlink(filepath.Join(dir, second), filepath.Join(dir, link)); err != nil {
		t.Fatal(err)
	}
	s := NewService(testSettings(dir), nil)
	got, err := s.DeleteBackups(context.Background(), []string{first, missing, first, "../invalid", second, link, missing}, "actor")
	want := DeleteResult{Deleted: []string{first, second}, Missing: []string{missing, "../invalid", link}, Failed: []string{}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("分类或顺序：%+v %v，期望%+v", got, err, want)
	}
	requireBackupMissing(t, dir, first, second)
	requireBackupExists(t, dir, link)
}

func TestDeleteBackupsDirectoryUnavailable(t *testing.T) {
	names := []string{"roomusic-20261106-120002.dump", "roomusic-20261106-120001.dump", "roomusic-20261106-120002.dump"}
	for _, state := range []string{"missing", "not_directory", "not_readable", "not_writable"} {
		t.Run(state, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "backups")
			if state == "not_directory" {
				if err := os.WriteFile(dir, []byte("不是目录"), 0600); err != nil {
					t.Fatal(err)
				}
			} else if state != "missing" {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				for _, name := range names[:2] {
					writeBackup(t, dir, name, time.Now())
				}
			}
			logger, output := backupTestLogger()
			s := NewService(testSettings(dir), logger)
			if state == "not_readable" {
				backupReadOnly(t, dir, 0000)
			}
			if state == "not_writable" {
				backupReadOnly(t, dir, 0500)
			}
			got, err := s.DeleteBackups(context.Background(), names, "actor")
			want := DeleteResult{Deleted: []string{}, Missing: []string{}, Failed: []string{}}
			if state == "missing" {
				want.Missing = names[:2]
			} else {
				want.Failed = names[:2]
			}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("%+v %v，期望%+v", got, err, want)
			}
			if state != "missing" && len(backupLogEvents(t, output, "backup.delete_failed")) != 2 {
				t.Fatalf("应逐份记录失败：%s", output)
			}
			if state == "not_writable" {
				requireBackupExists(t, dir, names[:2]...)
			}
		})
	}
}

func TestBackupDeletionCanceledBeforeMutation(t *testing.T) {
	dir := t.TempDir()
	name := "roomusic-20261106-120000.dump"
	writeBackup(t, dir, name, time.Now())
	s := NewService(testSettings(dir), nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.DeleteBackup(ctx, name, "actor"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.DeleteBackups(ctx, []string{name}, "actor"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	requireBackupExists(t, dir, name)
}

// 日志发生在真实删除之后，用同步屏障精确改变下一份的权限，不依赖竞态或休眠。
type backupHookHandler struct {
	slog.Handler
	hook func(slog.Record)
}

func (h backupHookHandler) Handle(ctx context.Context, record slog.Record) error {
	err := h.Handler.Handle(ctx, record)
	h.hook(record)
	return err
}
func (h backupHookHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return backupHookHandler{Handler: h.Handler.WithAttrs(attrs), hook: h.hook}
}
func (h backupHookHandler) WithGroup(group string) slog.Handler {
	return backupHookHandler{Handler: h.Handler.WithGroup(group), hook: h.hook}
}
func backupRecordEvent(record slog.Record) string {
	var event string
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == "event" {
			event = attr.Value.String()
		}
		return true
	})
	return event
}

func TestDeleteBackupsMixedDeletedMissingFailed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 可绕过目录权限")
	}
	dir := t.TempDir()
	first, missing, last := "roomusic-20261106-120002.dump", "roomusic-20261106-120001.dump", "roomusic-20261106-120000.dump"
	writeBackup(t, dir, first, time.Now())
	writeBackup(t, dir, last, time.Now())
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0700); err != nil {
			t.Error(err)
		}
	})
	var output bytes.Buffer
	handler := backupHookHandler{Handler: slog.NewJSONHandler(&output, nil), hook: func(record slog.Record) {
		if backupRecordEvent(record) == "backup.deleted" {
			if err := os.Chmod(dir, 0500); err != nil {
				t.Fatal(err)
			}
		}
	}}
	s := NewService(testSettings(dir), slog.New(handler))
	result, err := s.DeleteBackups(context.Background(), []string{first, missing, last, first, last}, "actor")
	want := DeleteResult{Deleted: []string{first}, Missing: []string{missing}, Failed: []string{last}}
	if err != nil || !reflect.DeepEqual(result, want) {
		t.Fatalf("混合分类：%+v %v，期望%+v", result, err, want)
	}
	requireBackupMissing(t, dir, first)
	requireBackupExists(t, dir, last)
	if len(backupLogEvents(t, &output, "backup.deleted")) != 1 || len(backupLogEvents(t, &output, "backup.delete_failed")) != 1 {
		t.Fatalf("逐项日志：%s", &output)
	}
}
