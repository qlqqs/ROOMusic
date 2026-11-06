package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/backup"
)

func TestRestoreFailurePrintsDirectlyExecutableSafetyCommand(t *testing.T) {
	const name = "roomusic-20261106-153512-pre-restore.dump"
	var stdout, stderr bytes.Buffer
	env := backupCommandEnv{stdout: &stdout, stderr: &stderr}
	for _, reason := range []string{"rebuild", "restore", "verify"} {
		t.Run(reason, func(t *testing.T) {
			stderr.Reset()
			code := restoreCommandError(context.Background(), &backup.RestoreError{Reason: reason, SafetyBackup: name}, env)
			if code != 1 {
				t.Fatalf("退出码%d", code)
			}
			var command []string
			for _, line := range strings.Split(stderr.String(), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "docker ") {
					command = strings.Fields(line)
				}
			}
			want := []string{"docker", "compose", "run", "--rm", "app", "restore", name}
			if strings.Join(command, "\x00") != strings.Join(want, "\x00") {
				t.Fatalf("命令不可直接执行：%q", stderr.String())
			}
			if !strings.Contains(stderr.String(), "数据库现在可能是空的") {
				t.Fatal("未提示恢复失败")
			}
		})
	}
}
func TestRestoreCommandErrorRefusalAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		err     error
		cancel  bool
		code    int
		message string
	}{
		{&backup.RestoreError{Reason: "connections", Connections: 2}, false, 1, "docker compose stop app"},
		{&backup.RestoreError{Reason: "permission"}, false, 1, "没有重建数据库的权限"},
		{errors.New("secret connection url"), false, 1, "无法准备数据库恢复"},
		{errors.New("cancelled"), true, 130, ""},
	} {
		var out bytes.Buffer
		ctx, cancel := context.WithCancel(context.Background())
		if tc.cancel {
			cancel()
		}
		code := restoreCommandError(ctx, tc.err, backupCommandEnv{stderr: &out})
		cancel()
		if code != tc.code || !strings.Contains(out.String(), tc.message) || strings.Contains(out.String(), "secret") {
			t.Errorf("拒绝输出%d %q", code, out.String())
		}
	}
}
