package collector

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestInsightsCollectorWriteFailurePreservesOldFile(t *testing.T) {
	const childDir = "ROOMUSIC_INSIGHTS_TEST_ATOMIC_DIR"
	if dir := os.Getenv(childDir); dir != "" {
		store, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		// 只在子进程限制单个文件的大小，令写入第 128 字节后失败。
		signal.Ignore(syscall.SIGXFSZ)
		var limit syscall.Rlimit
		if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &limit); err != nil {
			t.Fatal(err)
		}
		limit.Cur = 128
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limit); err != nil {
			t.Fatal(err)
		}
		next := insightsHeartbeat("a1000000-0000-4000-8000-000000000002")
		if err := store.put(next, time.Now()); !errors.Is(err, syscall.EFBIG) {
			t.Fatalf("写入错误 = %v，期望写入中途达到文件大小限制", err)
		}
		if got := store.Summary(time.Now()).Active30d; got != 1 {
			t.Fatalf("失败写入被发布到内存，实例数 %d", got)
		}
		return
	}

	store := newInsightsCollector(t)
	if err := store.put(insightsHeartbeat(collectorFixtureID), time.Now()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.dir, "instances.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestInsightsCollectorWriteFailurePreservesOldFile$")
	cmd.Env = append(os.Environ(), childDir+"="+store.dir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("模拟磁盘写入失败：%v\n%s", err, output)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("失败写入改变了旧文件，读取错误 %v\n写入前 %s\n写入后 %s", err, before, after)
	}
	entries, err := os.ReadDir(store.dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "instances.json" {
		t.Fatalf("失败写入留下临时文件：%v，读取错误 %v", entries, err)
	}
	reopened, err := Open(store.dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Summary(time.Now()).Active30d; got != 1 {
		t.Errorf("失败后无法读回旧记录，实例数 %d", got)
	}
}
