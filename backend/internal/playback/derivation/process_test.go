//go:build unix

package derivation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// writeScript 写一个可执行的 sh 脚本，充当假的 ffmpeg：用来验证进程管理，不需要真的 ffmpeg。
func writeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-ffmpeg")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

func readPID(path string) (int, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(content)))
	return pid, err == nil && pid > 0
}

// processGone 判断进程已经不存在，或只剩等待收养的僵尸。
func processGone(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return true
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	// 第二个字段（括号内的命令名）之后是状态字符。
	closing := strings.LastIndexByte(string(stat), ')')
	return closing >= 0 && closing+2 < len(stat) && stat[closing+2] == 'Z'
}

func TestRunFFmpegKillsTheWholeProcessGroupOnCancel(t *testing.T) {
	script := writeScript(t, `sleep 300 &
echo $! > "$1.child"
echo $$ > "$1.self"
wait
`)
	pidBase := filepath.Join(t.TempDir(), "pid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := runFFmpeg(ctx, script, t.TempDir(), nil, digestOutputLimit, pidBase)
		finished <- err
	}()
	var child, self int
	waitUntil(t, "the fake ffmpeg to publish its process IDs", func() bool {
		var childOK, selfOK bool
		child, childOK = readPID(pidBase + ".child")
		self, selfOK = readPID(pidBase + ".self")
		return childOK && selfOK
	})
	if processGone(child) || processGone(self) {
		t.Fatal("the fake ffmpeg exited by itself")
	}

	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runFFmpeg error = %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runFFmpeg did not return after cancellation")
	}
	waitUntil(t, "the process group to be reaped", func() bool { return processGone(child) && processGone(self) })
}

func TestRunFFmpegReportsAContextDeadlineAsAnError(t *testing.T) {
	script := writeScript(t, "sleep 300\n")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := runFFmpeg(ctx, script, t.TempDir(), nil, digestOutputLimit); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runFFmpeg error = %v, want context.DeadlineExceeded", err)
	}
}

func TestRunFFmpegClassifiesAnExitStatusWithoutLeakingStderr(t *testing.T) {
	script := writeScript(t, "echo 'cannot read /private/music/track.m4a' >&2\nexit 3\n")
	_, err := runFFmpeg(context.Background(), script, t.TempDir(), nil, digestOutputLimit)
	var failed *FailedError
	if !errors.As(err, &failed) || failed.Category != FailureFFmpegExit {
		t.Fatalf("error = %v, want an ffmpeg_exit failure", err)
	}
	if strings.Contains(err.Error(), "private") {
		t.Fatalf("Error() leaks stderr: %q", err.Error())
	}
	if !strings.Contains(failed.Stderr, "/private/music/track.m4a") {
		t.Fatalf("Stderr = %q, want the captured output", failed.Stderr)
	}
}

func TestRunFFmpegPassesTheSourceAsFileDescriptorThreeWithAMinimalEnvironment(t *testing.T) {
	script := writeScript(t, "cat <&3\npwd\necho \"LC=$LC_ALL HOME=$HOME\"\n")
	source := filepath.Join(t.TempDir(), "source.m4a")
	if err := os.WriteFile(source, []byte("source-bytes\n"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	file, err := os.Open(source)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer file.Close()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	t.Setenv("HOME", "/should/not/leak")
	output, err := runFFmpeg(context.Background(), script, directory, file, 4096)
	if err != nil {
		t.Fatalf("runFFmpeg: %v", err)
	}
	want := "source-bytes\n" + directory + "\nLC=C HOME=\n"
	if output != want {
		t.Fatalf("output = %q, want %q", output, want)
	}
}

func TestTailBuffer(t *testing.T) {
	head := &tailBuffer{limit: 5, head: true}
	for _, chunk := range []string{"ab", "cdef", "gh"} {
		if n, err := head.Write([]byte(chunk)); err != nil || n != len(chunk) {
			t.Fatalf("Write(%q) = %d, %v", chunk, n, err)
		}
	}
	if head.String() != "abcde" {
		t.Fatalf("head buffer = %q, want %q", head.String(), "abcde")
	}
	tail := &tailBuffer{limit: 5}
	for _, chunk := range []string{"ab", "cdef", "gh"} {
		if n, err := tail.Write([]byte(chunk)); err != nil || n != len(chunk) {
			t.Fatalf("Write(%q) = %d, %v", chunk, n, err)
		}
	}
	if tail.String() != "defgh" {
		t.Fatalf("tail buffer = %q, want %q", tail.String(), "defgh")
	}
}

func unavailableReason(t *testing.T, err error) string {
	t.Helper()
	var unavailable *UnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("error = %v, want an *UnavailableError", err)
	}
	return unavailable.Reason
}

func TestProbeFFmpegReportsWhyItIsUnavailable(t *testing.T) {
	ctx := context.Background()
	notExecutable := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(notExecutable, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	emptyPath := t.TempDir()

	for name, testCase := range map[string]struct {
		path   func() string
		reason string
	}{
		"missing":        {func() string { return filepath.Join(t.TempDir(), "absent") }, ReasonNotFound},
		"not executable": {func() string { return notExecutable }, ReasonNotFound},
		"version fails":  {func() string { return writeScript(t, "exit 1\n") }, ReasonVersionFailed},
		"not ffmpeg":     {func() string { return writeScript(t, "echo 'hello world'\n") }, ReasonVersionFailed},
		"self-test fails": {func() string {
			return writeScript(t, "if [ \"$2\" = \"-version\" ]; then echo 'ffmpeg version fake-1'; exit 0; fi\necho broken >&2\nexit 1\n")
		}, ReasonSelfTestFailed},
		"self-test digest is missing": {func() string {
			return writeScript(t, "if [ \"$2\" = \"-version\" ]; then echo 'ffmpeg version fake-1'; exit 0; fi\nexit 0\n")
		}, ReasonSelfTestFailed},
	} {
		t.Run(name, func(t *testing.T) {
			work := t.TempDir()
			_, err := ProbeFFmpeg(ctx, testCase.path(), work)
			if got := unavailableReason(t, err); got != testCase.reason {
				t.Fatalf("reason = %q, want %q (%v)", got, testCase.reason, err)
			}
			if entries, _ := os.ReadDir(work); len(entries) != 0 {
				t.Fatalf("the self-test left files behind: %v", entries)
			}
		})
	}

	t.Run("not on PATH", func(t *testing.T) {
		t.Setenv("PATH", emptyPath)
		_, err := ProbeFFmpeg(ctx, "", t.TempDir())
		if got := unavailableReason(t, err); got != ReasonNotFound {
			t.Fatalf("reason = %q, want %q", got, ReasonNotFound)
		}
	})
}

func TestConvertRejectsAnInvalidJob(t *testing.T) {
	ffmpeg := &FFmpeg{path: "/nonexistent", version: "ffmpeg version fake"}
	for name, job := range map[string]ConvertJob{
		"no source": {Output: "/tmp/x", Limit: 1},
		"no output": {Source: os.Stdin, Limit: 1},
		"no limit":  {Source: os.Stdin, Output: "/tmp/x"},
	} {
		var failed *FailedError
		if err := ffmpeg.Convert(context.Background(), job); !errors.As(err, &failed) || failed.Category != FailureIO {
			t.Errorf("%s: error = %v, want an io failure", name, err)
		}
	}
}
