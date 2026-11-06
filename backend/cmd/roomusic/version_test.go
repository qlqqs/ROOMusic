package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVersionCommandDevelopmentAndUsage(t *testing.T) {
	for _, name := range []string{"--version", "version"} {
		env, out, stderr := resetCommandTestEnv(t, "")
		requireResetCommandResult(t, runCommand([]string{name}, env), 0, out, stderr, "roomusic 开发版\n", "")
		env, out, stderr = resetCommandTestEnv(t, "")
		requireResetCommandResult(t, runCommand([]string{name, "extra"}, env), 2, out, stderr, "", commandUsage+"\n")
	}
}

// 通过真实链接器注入验证二进制，不给产品代码增加可变的测试入口。
func TestVersionBinaryBuildMetadata(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"
	for _, tc := range []struct{ name, version, commit, want string }{
		{"正式构建", "0.1.0", sha, "roomusic 0.1.0 (0123456789ab)\n"},
		{"开发构建有提交", "", sha, "roomusic 开发版 (0123456789ab)\n"},
		{"开发构建无提交", "", "", "roomusic 开发版\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			binary := filepath.Join(t.TempDir(), "roomusic")
			flags := "-X github.com/qlqq/roomusic/backend/internal/platform/buildinfo.version=" + tc.version + " -X github.com/qlqq/roomusic/backend/internal/platform/buildinfo.commit=" + tc.commit
			if output, err := exec.CommandContext(ctx, "go", "build", "-ldflags", flags, "-o", binary, ".").CombinedOutput(); err != nil {
				t.Fatalf("构建：%v %s", err, output)
			}
			for _, arg := range []string{"--version", "version"} {
				command := exec.CommandContext(ctx, binary, arg)
				// 若入口回归，不能继承本机数据库配置并误连开发或生产库。
				command.Env = []string{}
				output, err := command.CombinedOutput()
				if err != nil || string(output) != tc.want || strings.Count(string(output), "\n") != 1 {
					t.Fatalf("%s: %v %q，期望 %q", arg, err, output, tc.want)
				}
			}
		})
	}
}
