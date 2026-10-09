package derivation

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var testTime = time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)

// fakeClock 是可手动推进的时钟：advance 按时间顺序同步触发到期的定时器。
type fakeClock struct {
	mu      sync.Mutex
	current time.Time
	timers  []*fakeTimer
}

type fakeTimer struct {
	at      time.Time
	f       func()
	stopped bool
	fired   bool
}

func newFakeClock() *fakeClock { return &fakeClock{current: testTime} }

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current
}

func (c *fakeClock) afterFunc(d time.Duration, f func()) func() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &fakeTimer{at: c.current.Add(d), f: f}
	c.timers = append(c.timers, timer)
	return func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		if timer.stopped || timer.fired {
			return false
		}
		timer.stopped = true
		return true
	}
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	target := c.current.Add(d)
	c.mu.Unlock()
	for {
		c.mu.Lock()
		var due *fakeTimer
		for _, timer := range c.timers {
			if timer.stopped || timer.fired || timer.at.After(target) {
				continue
			}
			if due == nil || timer.at.Before(due.at) {
				due = timer
			}
		}
		if due == nil {
			c.current = target
			c.mu.Unlock()
			return
		}
		due.fired = true
		if due.at.After(c.current) {
			c.current = due.at
		}
		c.mu.Unlock()
		due.f()
	}
}

// fakeConverter 是不需要 ffmpeg 的转换器：写出指定大小的文件，可以阻塞直到被放行或 context 结束，并记录调用。
type fakeConverter struct {
	version string
	size    int

	mu        sync.Mutex
	block     chan struct{}
	outcome   error
	started   int
	finished  int
	order     []string
	jobs      []ConvertJob
	entered   chan struct{}
	onConvert func(ConvertJob)
}

func newFakeConverter() *fakeConverter {
	return &fakeConverter{version: "fake-1", size: 100, entered: make(chan struct{}, 1000)}
}

func (f *fakeConverter) Version() string { return f.version }

func (f *fakeConverter) Convert(ctx context.Context, job ConvertJob) error {
	f.mu.Lock()
	f.started++
	f.jobs = append(f.jobs, job)
	block, outcome, size, hook := f.block, f.outcome, f.size, f.onConvert
	f.mu.Unlock()
	f.entered <- struct{}{}
	defer func() {
		f.mu.Lock()
		f.finished++
		f.mu.Unlock()
	}()
	if hook != nil {
		hook(job)
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if outcome != nil {
		return outcome
	}
	return os.WriteFile(job.Output, make([]byte, size), 0o600)
}

func (f *fakeConverter) startedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started
}

func (f *fakeConverter) finishedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.finished
}

func (f *fakeConverter) set(update func(*fakeConverter)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	update(f)
}

// sourceHooks 让测试控制源文件回调的行为。
type sourceHooks struct {
	mu     sync.Mutex
	open   func() error
	verify func(content bool) error
	opened atomic.Int32
}

func (h *sourceHooks) set(update func(*sourceHooks)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	update(h)
}

type harness struct {
	t         *testing.T
	dir       string
	sources   string
	clock     *fakeClock
	converter *fakeConverter
	manager   *Manager
	cache     *Cache
	free      atomic.Uint64
}

type harnessOptions struct {
	maxBytes int64
	limits   func(*limits)
}

func newHarness(t *testing.T, options harnessOptions) *harness {
	t.Helper()
	if options.maxBytes == 0 {
		options.maxBytes = 1 << 20
	}
	h := &harness{t: t, dir: filepath.Join(t.TempDir(), "media-cache"), sources: t.TempDir(), clock: newFakeClock(), converter: newFakeConverter()}
	h.free.Store(1 << 40)
	cache, err := openCache(h.dir, options.maxBytes, h.clock)
	if err != nil {
		t.Fatalf("openCache: %v", err)
	}
	h.cache = cache
	configured := defaultLimits()
	configured.minFree = 1 << 20
	if options.limits != nil {
		options.limits(&configured)
	}
	h.manager = newManager(cache, h.converter, nil, configured, h.clock, func(string) (uint64, error) { return h.free.Load(), nil })
	t.Cleanup(func() { _ = h.manager.Close(5 * time.Second) })
	return h
}

// source 返回第 n 个测试源文件（大小 1000 字节）：版本与回调都由 hooks 控制。
func (h *harness) source(n int, hooks *sourceHooks) Source {
	h.t.Helper()
	path := filepath.Join(h.sources, fmt.Sprintf("source-%d.m4a", n))
	if _, err := os.Stat(path); err != nil {
		if err := os.WriteFile(path, []byte(fmt.Sprintf("alac-%d", n)), 0o600); err != nil {
			h.t.Fatalf("write source: %v", err)
		}
	}
	if hooks == nil {
		hooks = &sourceHooks{}
	}
	return Source{
		Version: SourceVersion{
			ContentFingerprint: fmt.Sprintf("m4a-mdat-v1:%d", n), Size: 1000, ModifiedAt: testTime,
			SourceKind: "m4a", Codec: "alac", BitDepth: 16, SampleRate: 44100, Channels: 2,
		},
		Open: func(context.Context) (*os.File, error) {
			hooks.opened.Add(1)
			hooks.mu.Lock()
			open := hooks.open
			hooks.mu.Unlock()
			if open != nil {
				if err := open(); err != nil {
					return nil, err
				}
			}
			return os.Open(path)
		},
		Verify: func(_ context.Context, _ *os.File, content bool) error {
			hooks.mu.Lock()
			verify := hooks.verify
			hooks.mu.Unlock()
			if verify != nil {
				return verify(content)
			}
			return nil
		},
	}
}

func (h *harness) request(n int, user string, hooks *sourceHooks) Request {
	return Request{Source: h.source(n, hooks), User: user, TrackID: fmt.Sprintf("track-%d", n)}
}

// tempFiles 返回缓存 tmp/ 内剩余的文件名。
func (h *harness) tempFiles() []string {
	h.t.Helper()
	entries, err := os.ReadDir(filepath.Join(h.dir, tempDirName))
	if err != nil {
		h.t.Fatalf("read tmp: %v", err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func (h *harness) waitEntered(count int) {
	h.t.Helper()
	for range count {
		select {
		case <-h.converter.entered:
		case <-time.After(5 * time.Second):
			h.t.Fatal("converter was not entered in time")
		}
	}
}

func (h *harness) jobCount() int {
	h.manager.mu.Lock()
	defer h.manager.mu.Unlock()
	return len(h.manager.jobs)
}

func (h *harness) queuedCount() int {
	h.manager.mu.Lock()
	defer h.manager.mu.Unlock()
	return len(h.manager.queue)
}

func (h *harness) waiterCount(key string) int {
	h.manager.mu.Lock()
	defer h.manager.mu.Unlock()
	if running := h.manager.jobs[key]; running != nil {
		return running.waiters
	}
	return 0
}

func waitUntil(t *testing.T, description string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", description)
		}
		time.Sleep(time.Millisecond)
	}
}

type awaitResult struct {
	entry Entry
	err   error
}

// startAwait 在新协程里发起 Await，结果经通道返回。
func (h *harness) startAwait(ctx context.Context, request Request) <-chan awaitResult {
	results := make(chan awaitResult, 1)
	go func() {
		entry, err := h.manager.Await(ctx, request)
		results <- awaitResult{entry: entry, err: err}
	}()
	return results
}

func receive(t *testing.T, results <-chan awaitResult) awaitResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("Await did not return in time")
		return awaitResult{}
	}
}
