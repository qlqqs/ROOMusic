package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func retentionInt(value int) *int { return &value }

func TestRetentionPlan(t *testing.T) {
	now := instant("2026-11-06T12:00:00Z")
	files := []File{
		{Name: "latest", Source: SourceScheduled, CreatedAt: now},
		{Name: "recent", Source: SourceManual, CreatedAt: now.Add(-time.Hour)},
		{Name: "boundary", Source: SourcePreMigration, CreatedAt: now.Add(-14 * 24 * time.Hour)},
		{Name: "expired", Source: SourcePreRestore, CreatedAt: now.Add(-14*24*time.Hour - time.Nanosecond)},
	}
	for _, tc := range []struct {
		name  string
		files []File
		rule  Retention
		want  []string
	}{
		{"天数边界严格早于", files, Retention{KeepDays: retentionInt(14)}, []string{"expired"}},
		{"只有份数", files, Retention{KeepCount: retentionInt(2)}, []string{"boundary", "expired"}},
		{"两个上限取并集", files, Retention{KeepDays: retentionInt(1), KeepCount: retentionInt(3)}, []string{"boundary", "expired"}},
		{"份数比天数更严格", files, Retention{KeepDays: retentionInt(14), KeepCount: retentionInt(1)}, []string{"recent", "boundary", "expired"}},
		{"两项不限", files, Retention{}, []string{}},
		{"空列表", nil, Retention{KeepCount: retentionInt(1)}, []string{}},
		{"唯一备份即使超期也保留", files[3:], Retention{KeepDays: retentionInt(1), KeepCount: retentionInt(1)}, []string{}},
		{"全部超期仍保留最新", files[2:], Retention{KeepDays: retentionInt(1)}, []string{"expired"}},
		{"四种来源一起计数", files, Retention{KeepCount: retentionInt(1)}, []string{"recent", "boundary", "expired"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := append([]File(nil), tc.files...)
			got := []string{}
			for _, file := range retentionPlan(tc.files, tc.rule, now) {
				got = append(got, file.Name)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("计划=%v，期望%v", got, tc.want)
			}
			if !reflect.DeepEqual(tc.files, before) {
				t.Fatal("计划不能修改输入列表")
			}
		})
	}
}

func TestRetentionPlanUsesFullListAndNameTieBreak(t *testing.T) {
	dir := t.TempDir()
	now := instant("2026-11-06T12:00:00Z")
	for i := 0; i < 501; i++ {
		writeBackup(t, dir, fileName(now.Add(time.Duration(i)*time.Second), SourceManual, 0), now)
	}
	s := NewService(testSettings(dir), nil)
	files, err := s.listFiles()
	if err != nil {
		t.Fatal(err)
	}
	plan := retentionPlan(files, Retention{KeepCount: retentionInt(1)}, now)
	if len(plan) != 500 || files[0].Name != "roomusic-20261106-120820-manual.dump" || plan[0].Name != "roomusic-20261106-120819-manual.dump" {
		t.Fatalf("应按同时间名字倒序对全部文件清理：%d %+v", len(plan), files[:2])
	}
	result, err := s.SaveRetention(context.Background(), Retention{KeepCount: retentionInt(1)}, true, "actor")
	if err != nil || result.Deleted != 500 {
		t.Fatalf("不能只清理 Overview 的500行：%+v %v", result, err)
	}
	requireBackupExists(t, dir, files[0].Name)
}

func TestRetentionFileDefaultsAndAtomicReplacement(t *testing.T) {
	dir := t.TempDir()
	now := instant("2026-11-06T12:00:00Z")
	settings := testSettings(dir)
	settings.KeepDays = 7
	s := NewService(settings, nil, WithClock(func() time.Time { return now }))
	fallback, err := s.effectiveRetention()
	if err != nil || !reflect.DeepEqual(fallback, Retention{KeepDays: retentionInt(7)}) {
		t.Fatalf("未保存应使用环境变量：%+v %v", fallback, err)
	}
	first := Retention{KeepDays: retentionInt(3650), KeepCount: retentionInt(1000)}
	if err := s.writeRetention(first); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, retentionName)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored diskRetention
	if err := json.Unmarshal(before, &stored); err != nil || stored.Version != 1 || !stored.SavedAt.Equal(now) || !reflect.DeepEqual(stored.Retention, first) {
		t.Fatalf("规则文件：%s %v", before, err)
	}
	old, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	oldInfo, err := old.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if err := s.writeRetention(Retention{}); err != nil {
		t.Fatal(err)
	}
	oldData, err := io.ReadAll(old)
	if err != nil || !bytes.Equal(oldData, before) {
		t.Fatalf("必须替换文件而非原地截断：%q %v", oldData, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 || os.SameFile(oldInfo, info) {
		t.Fatalf("原子替换与0600：%v %v", info, err)
	}
	rule, err := s.effectiveRetention()
	if err != nil || !reflect.DeepEqual(rule, Retention{}) {
		t.Fatalf("显式不限不能退回环境变量：%+v %v", rule, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != retentionName {
		t.Fatalf("临时文件残留：%v", entries)
	}
	// 即使时间很旧，规则文件也不能被残留清理或列表收进去。
	if err := os.Chtimes(path, now.Add(-48*time.Hour), now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	s.cleanPartials()
	overview, err := s.Overview(context.Background())
	if err != nil || overview.Total != 0 || overview.Retention == nil || !reflect.DeepEqual(*overview.Retention, Retention{}) {
		t.Fatalf("Overview规则：%+v %v", overview, err)
	}
	requireBackupExists(t, dir, retentionName)
}

func TestRetentionUnreadableSkipsCleanupAndCanBeRepaired(t *testing.T) {
	cases := map[string]string{
		"损坏": `{`, "超限": `{"version":1,"keep_days":null,"keep_count":null,"padding":"` + strings.Repeat("x", 4096) + `"}`,
		"缺天数":   `{"version":1,"keep_count":null}`,
		"缺份数":   `{"version":1,"keep_days":null}`,
		"多JSON": `{"version":1,"keep_days":null,"keep_count":null} {}`,
		"版本错":   `{"version":2,"keep_days":null,"keep_count":null}`,
		"缺版本":   `{"keep_days":null,"keep_count":null}`,
		"天数越界":  `{"version":1,"keep_days":3651,"keep_count":null}`,
		"份数越界":  `{"version":1,"keep_days":null,"keep_count":1001}`,
		"零天":    `{"version":1,"keep_days":0,"keep_count":null}`,
		"小数":    `{"version":1,"keep_days":1.5,"keep_count":null}`,
		"字符串":   `{"version":1,"keep_days":"3","keep_count":null}`,
		"null":  `null`,
	}
	for label, data := range cases {
		t.Run(label, func(t *testing.T) {
			dir := t.TempDir()
			now := instant("2026-11-06T12:00:00Z")
			newest, expired := "roomusic-20261106-120000.dump", "roomusic-20261001-120000.dump"
			writeBackup(t, dir, newest, now)
			writeBackup(t, dir, expired, now.Add(-36*24*time.Hour))
			if err := os.WriteFile(filepath.Join(dir, retentionName), []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			logger, output := backupTestLogger()
			s := NewService(testSettings(dir), logger, WithClock(func() time.Time { return now }))
			if _, err := s.effectiveRetention(); err == nil {
				t.Fatal("损坏文件不能退回默认值")
			}
			overview, err := s.Overview(context.Background())
			if err != nil || overview.Retention != nil {
				t.Fatalf("读不到应返回null：%+v %v", overview, err)
			}
			s.retain()
			requireBackupExists(t, dir, newest, expired)
			records := backupLogEvents(t, output, "backup.retention_unreadable")
			if len(records) != 1 || records[0]["level"] != "WARN" {
				t.Fatalf("应跳过清理并告警：%s", output)
			}
			result, err := s.SaveRetention(context.Background(), Retention{}, false, "actor-repair")
			if err != nil || result.ConfirmationRequired || result.Deleted != 0 || result.Failed != 0 {
				t.Fatalf("重新保存应修复：%+v %v", result, err)
			}
			rule, err := s.effectiveRetention()
			if err != nil || !reflect.DeepEqual(rule, Retention{}) {
				t.Fatalf("修复未生效：%+v %v", rule, err)
			}
			requireBackupExists(t, dir, newest, expired)
		})
	}
}

func TestSaveRetentionConfirmationIsSideEffectFree(t *testing.T) {
	for _, saved := range []bool{false, true} {
		t.Run(fmt.Sprint("原来有规则=", saved), func(t *testing.T) {
			dir := t.TempDir()
			now := instant("2026-11-06T12:00:00Z")
			newest, old := "roomusic-20261106-120000.dump", "roomusic-20261105-120000.dump"
			writeBackup(t, dir, newest, now)
			writeBackup(t, dir, old, now.Add(-time.Hour))
			logger, output := backupTestLogger()
			s := NewService(testSettings(dir), logger, WithClock(func() time.Time { return now }))
			if saved {
				if err := s.writeRetention(Retention{}); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(dir, retentionName)
			before, beforeErr := os.ReadFile(path)
			rule := Retention{KeepDays: retentionInt(30), KeepCount: retentionInt(1)}
			result, err := s.SaveRetention(context.Background(), rule, false, "actor-save")
			if err != nil || result != (RetentionResult{ConfirmationRequired: true, Deletions: 1}) {
				t.Fatalf("应要求确认：%+v %v", result, err)
			}
			after, afterErr := os.ReadFile(path)
			if !bytes.Equal(before, after) || errors.Is(beforeErr, os.ErrNotExist) != errors.Is(afterErr, os.ErrNotExist) {
				t.Fatal("未确认不得改规则文件")
			}
			requireBackupExists(t, dir, newest, old)
			if output.Len() != 0 {
				t.Fatalf("预览不能记录已保存或已删：%s", output)
			}
			result, err = s.SaveRetention(context.Background(), rule, true, "actor-save")
			if err != nil || result != (RetentionResult{Deleted: 1}) {
				t.Fatalf("确认后应保存清理：%+v %v", result, err)
			}
			requireBackupMissing(t, dir, old)
			requireBackupExists(t, dir, newest)
			effective, err := s.effectiveRetention()
			if err != nil || !reflect.DeepEqual(effective, rule) {
				t.Fatalf("未保存新规则：%+v %v", effective, err)
			}
			records := backupLogEvents(t, output, "backup.retention.saved")
			if len(records) != 1 || records[0]["actor_id"] != "actor-save" || records[0]["keep_days"] != float64(30) || records[0]["keep_count"] != float64(1) {
				t.Fatalf("保存日志不应包含指针：%s", output)
			}
			deleted := backupLogEvents(t, output, "backup.retention.deleted")
			if len(deleted) != 1 || deleted[0]["trigger"] != "saved" || deleted[0]["file"] != old {
				t.Fatalf("清理日志：%s", output)
			}
		})
	}
}

func TestSaveRetentionWriteFailureDoesNotDelete(t *testing.T) {
	for _, reason := range []string{"not_writable", "rename_failed"} {
		t.Run(reason, func(t *testing.T) {
			dir := t.TempDir()
			now := instant("2026-11-06T12:00:00Z")
			newest, old := "roomusic-20261106-120000.dump", "roomusic-20261105-120000.dump"
			writeBackup(t, dir, newest, now)
			writeBackup(t, dir, old, now.Add(-time.Hour))
			logger, output := backupTestLogger()
			s := NewService(testSettings(dir), logger, WithClock(func() time.Time { return now }))
			if reason == "not_writable" {
				if err := s.writeRetention(Retention{}); err != nil {
					t.Fatal(err)
				}
				backupReadOnly(t, dir, 0500)
			} else {
				if err := os.Mkdir(filepath.Join(dir, retentionName), 0700); err != nil {
					t.Fatal(err)
				}
			}
			result, err := s.SaveRetention(context.Background(), Retention{KeepCount: retentionInt(1)}, true, "actor")
			if !errors.Is(err, ErrNotWritable) || result.Deleted != 0 {
				t.Fatalf("写规则失败：%+v %v", result, err)
			}
			requireBackupExists(t, dir, newest, old)
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.Contains(entry.Name(), ".tmp-") {
					t.Fatalf("写失败残留临时文件：%s", entry.Name())
				}
			}
			if len(backupLogEvents(t, output, "backup.retention.saved")) != 0 || len(backupLogEvents(t, output, "backup.retention.deleted")) != 0 {
				t.Fatalf("失败不能记录成功：%s", output)
			}
			if reason == "not_writable" {
				rule, err := s.effectiveRetention()
				if err != nil || !reflect.DeepEqual(rule, Retention{}) {
					t.Fatalf("旧规则应保留：%+v %v", rule, err)
				}
			}
		})
	}
}

func TestSaveRetentionNoDeletionsAndDirectoryCreation(t *testing.T) {
	for _, create := range []bool{false, true} {
		t.Run(fmt.Sprint("允许建目录=", create), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "new")
			settings := testSettings(dir)
			settings.CreateDirectory = create
			logger, output := backupTestLogger()
			s := NewService(settings, logger)
			result, err := s.SaveRetention(context.Background(), Retention{}, false, "actor")
			if !create {
				if !errors.Is(err, ErrNotWritable) {
					t.Fatal(err)
				}
				requireBackupMissing(t, filepath.Dir(dir), filepath.Base(dir))
				return
			}
			if err != nil || result != (RetentionResult{}) {
				t.Fatalf("不用确认即可保存：%+v %v", result, err)
			}
			records := backupLogEvents(t, output, "backup.retention.saved")
			if len(records) != 1 || records[0]["keep_days"] != "unlimited" || records[0]["keep_count"] != "unlimited" {
				t.Fatalf("不限日志：%s", output)
			}
		})
	}
}

func TestRetainMissingFileIsSilent(t *testing.T) {
	dir := t.TempDir()
	now := instant("2026-11-06T12:00:00Z")
	newest, old := "roomusic-20261106-120000.dump", "roomusic-20261001-120000.dump"
	writeBackup(t, dir, newest, now)
	writeBackup(t, dir, old, now.Add(-36*24*time.Hour))
	logger, output := backupTestLogger()
	armed := false
	s := NewService(testSettings(dir), logger, WithClock(func() time.Time {
		// retain 在 listFiles 后取时钟，精确模拟列出后被手动删除，不依赖调度。
		if armed {
			armed = false
			if err := os.Remove(filepath.Join(dir, old)); err != nil {
				t.Fatal(err)
			}
		}
		return now
	}))
	armed = true
	s.retain()
	requireBackupExists(t, dir, newest)
	requireBackupMissing(t, dir, old)
	if len(backupLogEvents(t, output, "backup.retention_failed")) != 0 || len(backupLogEvents(t, output, "backup.retention.deleted")) != 0 {
		t.Fatalf("已经不存在不记失败或成功：%s", output)
	}
}

func TestSaveRetentionPartialCleanupFailureKeepsSavedRule(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 可绕过目录权限")
	}
	dir := t.TempDir()
	now := instant("2026-11-06T12:00:00Z")
	newest, first, last := "roomusic-20261106-120000.dump", "roomusic-20261105-120000.dump", "roomusic-20261104-120000.dump"
	for i, name := range []string{newest, first, last} {
		writeBackup(t, dir, name, now.Add(-time.Duration(i)*time.Hour))
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0700); err != nil {
			t.Error(err)
		}
	})
	var output bytes.Buffer
	handler := backupHookHandler{Handler: slog.NewJSONHandler(&output, nil), hook: func(record slog.Record) {
		if backupRecordEvent(record) == "backup.retention.deleted" {
			if err := os.Chmod(dir, 0500); err != nil {
				t.Fatal(err)
			}
		}
	}}
	s := NewService(testSettings(dir), slog.New(handler), WithClock(func() time.Time { return now }))
	rule := Retention{KeepCount: retentionInt(1)}
	result, err := s.SaveRetention(context.Background(), rule, true, "actor")
	if err != nil || result != (RetentionResult{Deleted: 1, Failed: 1}) {
		t.Fatalf("保存成功而部分清理失败：%+v %v", result, err)
	}
	requireBackupMissing(t, dir, first)
	requireBackupExists(t, dir, newest, last, retentionName)
	effective, err := s.effectiveRetention()
	if err != nil || !reflect.DeepEqual(effective, rule) {
		t.Fatalf("清理失败不能回滚已保存规则：%+v %v", effective, err)
	}
	if len(backupLogEvents(t, &output, "backup.retention_failed")) != 1 {
		t.Fatalf("清理失败日志：%s", &output)
	}
}

func TestRetentionFilePermissionDeniedDoesNotFallback(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 可绕过文件权限")
	}
	dir := t.TempDir()
	now := instant("2026-11-06T12:00:00Z")
	newest, expired := "roomusic-20261106-120000.dump", "roomusic-20261001-120000.dump"
	writeBackup(t, dir, newest, now)
	writeBackup(t, dir, expired, now.Add(-36*24*time.Hour))
	logger, output := backupTestLogger()
	s := NewService(testSettings(dir), logger, WithClock(func() time.Time { return now }))
	if err := s.writeRetention(Retention{}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, retentionName), 0000); err != nil {
		t.Fatal(err)
	}
	if _, err := s.effectiveRetention(); err == nil {
		t.Fatal("不能读取的规则不能回退")
	}
	s.retain()
	requireBackupExists(t, dir, newest, expired)
	if len(backupLogEvents(t, output, "backup.retention_unreadable")) != 1 {
		t.Fatalf("不可读必须告警：%s", output)
	}
	// 原子替换不需要旧文件的读写权限，直接保存即可修复。
	if _, err := s.SaveRetention(context.Background(), Retention{}, false, "actor"); err != nil {
		t.Fatal(err)
	}
	if rule, err := s.effectiveRetention(); err != nil || !reflect.DeepEqual(rule, Retention{}) {
		t.Fatalf("未修复：%+v %v", rule, err)
	}
}

func TestRetentionFileSizeBoundary(t *testing.T) {
	dir := t.TempDir()
	data := `{"version":1,"keep_days":null,"keep_count":null}`
	data += strings.Repeat(" ", 4096-len(data))
	if err := os.WriteFile(filepath.Join(dir, retentionName), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewService(testSettings(dir), nil)
	rule, err := s.effectiveRetention()
	if err != nil || !reflect.DeepEqual(rule, Retention{}) {
		t.Fatalf("4096字节有效JSON应可读：%+v %v", rule, err)
	}
	if err := os.WriteFile(filepath.Join(dir, retentionName), []byte(data+" "), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.effectiveRetention(); err == nil {
		t.Fatal("4097字节即使是有效JSON也必须拒绝")
	}
}
