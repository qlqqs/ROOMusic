// Package ffmpeg 提供渲染所需的能力探测、受限进程与启动自检。
package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrBusy        = errors.New("ffmpeg process capacity exhausted")
	ErrReadTimeout = errors.New("ffmpeg output timed out")
)

// UnavailableError 的 Reason 可用于启动日志；底层错误不应进入 HTTP 响应。
type UnavailableError struct {
	Reason string
	Err    error
}

func (e *UnavailableError) Error() string { return "ffmpeg unavailable: " + e.Reason }
func (e *UnavailableError) Unwrap() error { return e.Err }

// Timer 允许测试推进等待时钟而不真实等待十秒。
type Timer interface {
	C() <-chan time.Time
	Stop() bool
}
type realTimer struct{ *time.Timer }

func (t realTimer) C() <-chan time.Time { return t.Timer.C }

// Options 只注入计时机制，不改变生产超时与容量策略。
type Options struct{ NewTimer func(time.Duration) Timer }

// Tool 的能力表构造后不变；所有编码、解码进程共用同一配额。
type Tool struct {
	path, version      string
	decoders, encoders map[string]bool
	soxr               bool
	newTimer           func(time.Duration) Timer
	mu                 sync.Mutex
	capacity, used     int
	changed            chan struct{}
}

// Open 定位 ffmpeg，完成能力探测与无损往返自检后才返回。
func Open(ctx context.Context, path string) (*Tool, error) {
	return OpenWithOptions(ctx, path, Options{})
}

// OpenWithOptions 与 Open 相同，允许注入计时器。
func OpenWithOptions(ctx context.Context, path string, options Options) (*Tool, error) {
	if !platformSupported {
		return nil, &UnavailableError{Reason: "unsupported_host"}
	}
	if path == "" {
		path = "ffmpeg"
	}
	resolved, err := exec.LookPath(path)
	if err != nil {
		return nil, &UnavailableError{Reason: "not_found", Err: err}
	}
	newTimer := options.NewTimer
	if newTimer == nil {
		newTimer = func(d time.Duration) Timer { return realTimer{time.NewTimer(d)} }
	}
	t := &Tool{path: resolved, capacity: 3 * runtime.GOMAXPROCS(0), changed: make(chan struct{}), newTimer: newTimer}
	output, err := t.probe(ctx, 4096, "-version")
	if err != nil {
		return nil, &UnavailableError{Reason: "version_failed", Err: err}
	}
	version, _, _ := strings.Cut(strings.TrimSpace(string(output)), "\n")
	if !strings.HasPrefix(version, "ffmpeg version ") {
		return nil, &UnavailableError{Reason: "version_failed", Err: errors.New("unexpected version output")}
	}
	t.version = strings.TrimSpace(version[:min(len(version), 256)])
	for _, item := range []struct {
		flag, reason string
		target       *map[string]bool
	}{
		{"-decoders", "decoders_failed", &t.decoders}, {"-encoders", "encoders_failed", &t.encoders},
	} {
		output, err = t.probe(ctx, 128<<10, item.flag)
		if err != nil {
			return nil, &UnavailableError{Reason: item.reason, Err: err}
		}
		*item.target = parseAudioCodecs(string(output))
		if len(*item.target) == 0 {
			return nil, &UnavailableError{Reason: item.reason, Err: errors.New("empty audio codec list")}
		}
	}
	// 实际运行重采样而非仅检查构建选项，避免把不可用的运行库误报为可用。
	_, err = t.probe(ctx, 4096, "-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono", "-t", "0.01", "-af", "aresample=48000:resampler=soxr", "-f", "null", "-")
	if ctx.Err() != nil {
		return nil, &UnavailableError{Reason: "soxr_failed", Err: ctx.Err()}
	}
	t.soxr = err == nil
	selfCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := t.selfTest(selfCtx); err != nil {
		return nil, &UnavailableError{Reason: "selftest_failed", Err: err}
	}
	return t, nil
}

func parseAudioCodecs(output string) map[string]bool {
	result := make(map[string]bool)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && len(fields[0]) == 6 && fields[0][0] == 'A' && fields[1] != "=" {
			result[fields[1]] = true
		}
	}
	return result
}

func (t *Tool) Version() string             { return t.version }
func (t *Tool) HasDecoder(name string) bool { return t.decoders[name] }
func (t *Tool) HasEncoder(name string) bool { return t.encoders[name] }
func (t *Tool) Soxr() bool                  { return t.soxr }

// Encoders 返回排序后的副本，调用方不能修改能力表。
func (t *Tool) Encoders() []string {
	names := make([]string, 0, len(t.encoders))
	for name := range t.encoders {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Reservation 原子预留多个进程名额，防止开流时各占一个名额而互相等待。
// Close 只释放未启动的名额；已启动的名额归对应 Process 所有。
type Reservation struct {
	tool      *Tool
	mu        sync.Mutex
	remaining int
}

// Reserve 最多等待五秒。调用方必须 Close，含后续准备曲目失败的路径。
func (t *Tool) Reserve(ctx context.Context, n int) (*Reservation, error) {
	if n <= 0 || n > t.capacity {
		return nil, fmt.Errorf("invalid ffmpeg reservation size: %d", n)
	}
	timer := t.newTimer(5 * time.Second)
	defer timer.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		t.mu.Lock()
		if t.capacity-t.used >= n {
			t.used += n
			t.mu.Unlock()
			return &Reservation{tool: t, remaining: n}, nil
		}
		changed := t.changed
		t.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C():
			return nil, ErrBusy
		case <-changed:
		}
	}
}
func (t *Tool) release(n int) {
	if n == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.used -= n
	close(t.changed)
	t.changed = make(chan struct{})
}
func (r *Reservation) Close() error {
	r.mu.Lock()
	n := r.remaining
	r.remaining = 0
	r.mu.Unlock()
	r.tool.release(n)
	return nil
}
