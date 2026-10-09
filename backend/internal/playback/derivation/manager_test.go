package derivation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var errSourceChanged = errors.New("source changed")

func TestAwaitPublishesAndTheNextAwaitHitsTheCache(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	request := h.request(1, "user-a", nil)

	entry, err := h.manager.Await(context.Background(), request)
	if err != nil {
		t.Fatalf("Await: %v", err)
	}
	if entry.Key != h.manager.Key(request.Source.Version) || entry.Size != 100 {
		t.Fatalf("entry = %+v", entry)
	}
	file, size, err := h.manager.OpenEntry(entry.Key)
	if err != nil {
		t.Fatalf("OpenEntry: %v", err)
	}
	defer file.Close()
	if content, _ := io.ReadAll(file); size != 100 || len(content) != 100 {
		t.Fatalf("size = %d, content = %d bytes", size, len(content))
	}
	if names := h.tempFiles(); len(names) != 0 {
		t.Fatalf("temporary files remain: %v", names)
	}

	again, err := h.manager.Await(context.Background(), request)
	if err != nil || again != entry {
		t.Fatalf("second Await = %+v, %v; want %+v", again, err, entry)
	}
	if count := h.converter.startedCount(); count != 1 {
		t.Fatalf("converter ran %d times, want 1", count)
	}
}

func TestConversionJobCarriesTheExpectedParametersAndALimit(t *testing.T) {
	h := newHarness(t, harnessOptions{maxBytes: 1 << 30})
	request := h.request(1, "user-a", nil)
	if _, err := h.manager.Await(context.Background(), request); err != nil {
		t.Fatalf("Await: %v", err)
	}
	h.converter.mu.Lock()
	defer h.converter.mu.Unlock()
	job := h.converter.jobs[0]
	if job.Expect != (Expect{BitDepth: 16, SampleRate: 44100, Channels: 2}) {
		t.Fatalf("Expect = %+v", job.Expect)
	}
	// 预留量 = 源大小 × 2 + 16 MiB。
	if want := int64(2*1000) + reserveSlack; job.Limit != want {
		t.Fatalf("Limit = %d, want %d", job.Limit, want)
	}
	if filepath.Dir(job.Output) != filepath.Join(h.dir, tempDirName) {
		t.Fatalf("Output = %s, want a file under tmp/", job.Output)
	}
}

func TestConcurrentWaitersShareOneConversion(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	release := make(chan struct{})
	h.converter.set(func(f *fakeConverter) { f.block = release })

	const waiters = 10
	var results []<-chan awaitResult
	for index := range waiters {
		results = append(results, h.startAwait(context.Background(), h.request(1, fmt.Sprintf("user-%d", index), nil)))
	}
	h.waitEntered(1)
	waitUntil(t, "all waiters to register", func() bool { return h.waiterCount(h.manager.Key(h.source(1, nil).Version)) == waiters })
	close(release)

	var first Entry
	for index, result := range results {
		received := receive(t, result)
		if received.err != nil {
			t.Fatalf("waiter %d: %v", index, received.err)
		}
		if index == 0 {
			first = received.entry
		} else if received.entry != first {
			t.Fatalf("waiter %d got %+v, want %+v", index, received.entry, first)
		}
	}
	if count := h.converter.startedCount(); count != 1 {
		t.Fatalf("converter ran %d times for %d waiters, want 1", count, waiters)
	}
}

func TestAwaitReturnsTheContextErrorWhileTheJobKeepsRunning(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	release := make(chan struct{})
	h.converter.set(func(f *fakeConverter) { f.block = release })
	request := h.request(1, "user-a", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := h.manager.Await(ctx, request); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Await error = %v, want a deadline error", err)
	}
	h.waitEntered(1)
	if h.jobCount() != 1 {
		t.Fatal("the job must keep running without a waiter until the grace period ends")
	}
	close(release)
	entry, err := h.manager.Await(context.Background(), request)
	if err != nil || entry.Size != 100 {
		t.Fatalf("Await after release = %+v, %v", entry, err)
	}
	if count := h.converter.startedCount(); count != 1 {
		t.Fatalf("converter ran %d times, want 1", count)
	}
}

func TestJobIsCanceledWhenNoWaiterReturnsWithinTheGracePeriod(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	h.converter.set(func(f *fakeConverter) { f.block = make(chan struct{}) })
	request := h.request(1, "user-a", nil)

	ctx, cancel := context.WithCancel(context.Background())
	results := h.startAwait(ctx, request)
	h.waitEntered(1)
	cancel()
	if received := receive(t, results); !errors.Is(received.err, context.Canceled) {
		t.Fatalf("Await error = %v, want context.Canceled", received.err)
	}

	h.clock.advance(9 * time.Second)
	if finished := h.converter.finishedCount(); finished != 0 {
		t.Fatal("the job was canceled before the grace period ended")
	}
	h.clock.advance(time.Second)
	waitUntil(t, "the canceled conversion to stop", func() bool { return h.converter.finishedCount() == 1 && h.jobCount() == 0 })
	if names := h.tempFiles(); len(names) != 0 {
		t.Fatalf("temporary files remain after cancellation: %v", names)
	}
	if _, ok := h.cache.lookup(h.manager.Key(request.Source.Version)); ok {
		t.Fatal("a canceled job published a file")
	}
	// 取消的任务没有记入失败记忆：下一次请求重新开始转换。
	h.converter.set(func(f *fakeConverter) { f.block = nil })
	if _, err := h.manager.Await(context.Background(), request); err != nil {
		t.Fatalf("Await after cancellation: %v", err)
	}
	if count := h.converter.startedCount(); count != 2 {
		t.Fatalf("converter ran %d times, want 2", count)
	}
}

func TestANewWaiterWithinTheGracePeriodKeepsTheJobAlive(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	release := make(chan struct{})
	h.converter.set(func(f *fakeConverter) { f.block = release })
	request := h.request(1, "user-a", nil)
	key := h.manager.Key(request.Source.Version)

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	first := h.startAwait(firstCtx, request)
	h.waitEntered(1)
	cancelFirst()
	receive(t, first)
	h.clock.advance(5 * time.Second)

	second := h.startAwait(context.Background(), h.request(1, "user-b", nil))
	waitUntil(t, "the second waiter to register", func() bool { return h.waiterCount(key) == 1 })
	h.clock.advance(30 * time.Second)
	if finished := h.converter.finishedCount(); finished != 0 {
		t.Fatal("the grace timer canceled a job that had a waiter again")
	}
	close(release)
	if received := receive(t, second); received.err != nil || received.entry.Size != 100 {
		t.Fatalf("second waiter = %+v", received)
	}
	if count := h.converter.startedCount(); count != 1 {
		t.Fatalf("converter ran %d times, want 1", count)
	}
}

func TestRunningQueuedAndPerUserLimits(t *testing.T) {
	h := newHarness(t, harnessOptions{limits: func(l *limits) { l.maxRunning, l.maxQueued, l.maxKeysPerUser = 2, 2, 3 }})
	release := make(chan struct{})
	h.converter.set(func(f *fakeConverter) { f.block = release })

	var results []<-chan awaitResult
	for n := 1; n <= 4; n++ {
		results = append(results, h.startAwait(context.Background(), h.request(n, fmt.Sprintf("user-%d", n), nil)))
	}
	h.waitEntered(2)
	waitUntil(t, "two jobs to queue", func() bool { return h.queuedCount() == 2 })

	// 两个在运行、两个排队：第五个不同的键被拒绝，且不排队。
	if _, err := h.manager.Await(context.Background(), h.request(5, "user-5", nil)); !errors.Is(err, ErrBusy) {
		t.Fatalf("fifth key error = %v, want ErrBusy", err)
	}
	if h.queuedCount() != 2 {
		t.Fatal("a rejected request must not be queued")
	}
	// 同一个键再加一个等待者不占用新的位置。
	joined := h.startAwait(context.Background(), h.request(1, "user-6", nil))
	waitUntil(t, "the extra waiter to join", func() bool { return h.waiterCount(h.manager.Key(h.source(1, nil).Version)) == 2 })

	close(release)
	for index, result := range results {
		if received := receive(t, result); received.err != nil {
			t.Fatalf("job %d: %v", index+1, received.err)
		}
	}
	if received := receive(t, joined); received.err != nil {
		t.Fatalf("joined waiter: %v", received.err)
	}
	if count := h.converter.startedCount(); count != 4 {
		t.Fatalf("converter ran %d times, want 4", count)
	}
}

func TestQueuedJobsStartInFirstInFirstOutOrder(t *testing.T) {
	h := newHarness(t, harnessOptions{limits: func(l *limits) { l.maxRunning, l.maxQueued = 1, 3 }})
	release := make(chan struct{})
	h.converter.set(func(f *fakeConverter) { f.block = release })

	var mu sync.Mutex
	var order []int
	var results []<-chan awaitResult
	for n := 1; n <= 4; n++ {
		hooks := &sourceHooks{}
		hooks.set(func(s *sourceHooks) {
			s.open = func() error {
				mu.Lock()
				defer mu.Unlock()
				order = append(order, n)
				return nil
			}
		})
		results = append(results, h.startAwait(context.Background(), h.request(n, fmt.Sprintf("user-%d", n), hooks)))
		if n == 1 {
			h.waitEntered(1)
		} else {
			waitUntil(t, "the job to queue", func() bool { return h.queuedCount() == n-1 })
		}
	}
	close(release)
	for index, result := range results {
		if received := receive(t, result); received.err != nil {
			t.Fatalf("job %d: %v", index+1, received.err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(order) != "[1 2 3 4]" {
		t.Fatalf("jobs started in order %v, want [1 2 3 4]", order)
	}
}

func TestPerUserLimitCountsDistinctKeysOnly(t *testing.T) {
	h := newHarness(t, harnessOptions{limits: func(l *limits) { l.maxKeysPerUser = 3 }})
	release := make(chan struct{})
	h.converter.set(func(f *fakeConverter) { f.block = release })

	var results []<-chan awaitResult
	for n := 1; n <= 3; n++ {
		results = append(results, h.startAwait(context.Background(), h.request(n, "user-a", nil)))
	}
	h.waitEntered(2)
	waitUntil(t, "three waiters", func() bool { return h.waiterCount(h.manager.Key(h.source(3, nil).Version)) == 1 })

	if _, err := h.manager.Await(context.Background(), h.request(4, "user-a", nil)); !errors.Is(err, ErrBusy) {
		t.Fatalf("fourth distinct key error = %v, want ErrBusy", err)
	}
	// 同一个成员再次等待自己已在等待的键不算新增。
	again := h.startAwait(context.Background(), h.request(1, "user-a", nil))
	waitUntil(t, "the repeated wait to register", func() bool { return h.waiterCount(h.manager.Key(h.source(1, nil).Version)) == 2 })
	// 另一个成员不受影响。
	other := h.startAwait(context.Background(), h.request(4, "user-b", nil))

	close(release)
	for _, result := range append(results, again, other) {
		if received := receive(t, result); received.err != nil {
			t.Fatalf("unexpected error: %v", received.err)
		}
	}
}

func TestAConversionFailureIsRememberedThenRetriedAfterTheMemoryExpires(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	failure := &FailedError{Category: FailureFFmpegExit, Err: errors.New("boom")}
	h.converter.set(func(f *fakeConverter) { f.outcome = failure })
	request := h.request(1, "user-a", nil)

	var failed *FailedError
	if _, err := h.manager.Await(context.Background(), request); !errors.As(err, &failed) || failed.Category != FailureFFmpegExit {
		t.Fatalf("Await error = %v, want an ffmpeg_exit failure", err)
	}
	if names := h.tempFiles(); len(names) != 0 {
		t.Fatalf("temporary files remain after a failure: %v", names)
	}
	h.converter.set(func(f *fakeConverter) { f.outcome = nil })
	h.clock.advance(9 * time.Minute)
	if _, err := h.manager.Await(context.Background(), request); !errors.As(err, &failed) {
		t.Fatalf("a remembered failure must be returned without retrying, got %v", err)
	}
	if count := h.converter.startedCount(); count != 1 {
		t.Fatalf("converter ran %d times within the memory window, want 1", count)
	}
	h.clock.advance(2 * time.Minute)
	if entry, err := h.manager.Await(context.Background(), request); err != nil || entry.Size != 100 {
		t.Fatalf("Await after the memory expired = %+v, %v", entry, err)
	}
	if count := h.converter.startedCount(); count != 2 {
		t.Fatalf("converter ran %d times, want 2", count)
	}
}

func TestFailureMemoryIsBounded(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	h.manager.mu.Lock()
	for n := range maxRememberedFailures + 10 {
		h.manager.rememberFailureLocked(fmt.Sprintf("key-%d", n), &FailedError{Category: FailureIO})
	}
	remembered := len(h.manager.failures)
	h.manager.mu.Unlock()
	if remembered > maxRememberedFailures {
		t.Fatalf("remembered %d failures, want at most %d", remembered, maxRememberedFailures)
	}
}

func TestSourceErrorsAreReturnedAsIsAndNotRemembered(t *testing.T) {
	for _, stage := range []string{"open", "verify before", "verify after"} {
		t.Run(stage, func(t *testing.T) {
			h := newHarness(t, harnessOptions{})
			hooks := &sourceHooks{}
			hooks.set(func(s *sourceHooks) {
				switch stage {
				case "open":
					s.open = func() error { return errSourceChanged }
				case "verify before":
					s.verify = func(content bool) error {
						if content {
							return errSourceChanged
						}
						return nil
					}
				case "verify after":
					s.verify = func(content bool) error {
						if !content {
							return errSourceChanged
						}
						return nil
					}
				}
			})
			request := h.request(1, "user-a", hooks)
			if _, err := h.manager.Await(context.Background(), request); !errors.Is(err, errSourceChanged) {
				t.Fatalf("Await error = %v, want the source error", err)
			}
			if _, ok := h.cache.lookup(h.manager.Key(request.Source.Version)); ok {
				t.Fatal("a job whose source check failed published a file")
			}
			if names := h.tempFiles(); len(names) != 0 {
				t.Fatalf("temporary files remain: %v", names)
			}
			beforeRetry := h.converter.startedCount()
			hooks.set(func(s *sourceHooks) { s.open, s.verify = nil, nil })
			if _, err := h.manager.Await(context.Background(), request); err != nil {
				t.Fatalf("retry after the source was fixed: %v", err)
			}
			if h.converter.startedCount() != beforeRetry+1 {
				t.Fatal("the retry did not convert: a source error must not be remembered as a failure")
			}
		})
	}
}

func TestContentIsVerifiedBeforeConversionAndSizeAfterwards(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	var mu sync.Mutex
	var calls []bool
	hooks := &sourceHooks{}
	hooks.set(func(s *sourceHooks) {
		s.verify = func(content bool) error {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, content)
			return nil
		}
	})
	if _, err := h.manager.Await(context.Background(), h.request(1, "user-a", hooks)); err != nil {
		t.Fatalf("Await: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 || !calls[0] || calls[1] {
		t.Fatalf("Verify calls (content flag) = %v, want [true false]", calls)
	}
}

func TestFreeSpaceAndReservations(t *testing.T) {
	reserve := reservation(1000)
	h := newHarness(t, harnessOptions{limits: func(l *limits) { l.minFree = 1 << 20 }})
	release := make(chan struct{})
	h.converter.set(func(f *fakeConverter) { f.block = release })

	// 剩余空间只够一个任务：预留量 + 保留的下限。
	h.free.Store(uint64(reserve) + 1<<20)
	first := h.startAwait(context.Background(), h.request(1, "user-a", nil))
	h.waitEntered(1)
	if _, err := h.manager.Await(context.Background(), h.request(2, "user-b", nil)); !errors.Is(err, ErrBusy) {
		t.Fatalf("second job error = %v, want ErrBusy (the first job's reservation must count)", err)
	}
	close(release)
	if received := receive(t, first); received.err != nil {
		t.Fatalf("first job: %v", received.err)
	}
	// 预留在任务结束时释放：第二个键现在可以开始。
	h.converter.set(func(f *fakeConverter) { f.block = nil })
	if _, err := h.manager.Await(context.Background(), h.request(2, "user-b", nil)); err != nil {
		t.Fatalf("second job after release: %v", err)
	}

	// 空间不足以满足预留量时拒绝启动。
	h.free.Store(uint64(reserve) + 1<<20 - 1)
	if _, err := h.manager.Await(context.Background(), h.request(3, "user-c", nil)); !errors.Is(err, ErrBusy) {
		t.Fatalf("low free space error = %v, want ErrBusy", err)
	}
	h.manager.mu.Lock()
	reserved := h.manager.reserved
	h.manager.mu.Unlock()
	if reserved != 0 {
		t.Fatalf("reserved = %d after all jobs ended, want 0", reserved)
	}
}

func TestOutputLargerThanTheCacheIsRejected(t *testing.T) {
	h := newHarness(t, harnessOptions{maxBytes: 500})
	h.converter.set(func(f *fakeConverter) { f.size = 600 })
	request := h.request(1, "user-a", nil)
	var failed *FailedError
	if _, err := h.manager.Await(context.Background(), request); !errors.As(err, &failed) || failed.Category != FailureOutputLimit {
		t.Fatalf("Await error = %v, want an output_limit failure", err)
	}
	if _, ok := h.cache.lookup(h.manager.Key(request.Source.Version)); ok {
		t.Fatal("an output larger than the cache was published")
	}
	if names := h.tempFiles(); len(names) != 0 {
		t.Fatalf("temporary files remain: %v", names)
	}
	// 转换器看到的上限不超过缓存上限。
	h.converter.mu.Lock()
	defer h.converter.mu.Unlock()
	if limit := h.converter.jobs[0].Limit; limit != 500 {
		t.Fatalf("Limit = %d, want the cache limit 500", limit)
	}
}

func TestAnEmptyOutputIsNotPublished(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	h.converter.set(func(f *fakeConverter) { f.size = 0 })
	request := h.request(1, "user-a", nil)
	var failed *FailedError
	if _, err := h.manager.Await(context.Background(), request); !errors.As(err, &failed) || failed.Category != FailureIO {
		t.Fatalf("Await error = %v, want an io failure", err)
	}
	if _, ok := h.cache.lookup(h.manager.Key(request.Source.Version)); ok {
		t.Fatal("an empty output was published")
	}
}

func TestJobTimeoutIsAFailure(t *testing.T) {
	h := newHarness(t, harnessOptions{limits: func(l *limits) { l.jobTimeout = 30 * time.Millisecond }})
	h.converter.set(func(f *fakeConverter) { f.block = make(chan struct{}) })
	var failed *FailedError
	if _, err := h.manager.Await(context.Background(), h.request(1, "user-a", nil)); !errors.As(err, &failed) || failed.Category != FailureTimeout {
		t.Fatalf("Await error = %v, want a timeout failure", err)
	}
	if names := h.tempFiles(); len(names) != 0 {
		t.Fatalf("temporary files remain: %v", names)
	}
}

func TestCloseCancelsRunningAndQueuedJobs(t *testing.T) {
	h := newHarness(t, harnessOptions{limits: func(l *limits) { l.maxRunning, l.maxQueued = 1, 2 }})
	h.converter.set(func(f *fakeConverter) { f.block = make(chan struct{}) })

	running := h.startAwait(context.Background(), h.request(1, "user-a", nil))
	h.waitEntered(1)
	queued := h.startAwait(context.Background(), h.request(2, "user-b", nil))
	waitUntil(t, "the second job to queue", func() bool { return h.queuedCount() == 1 })

	if err := h.manager.Close(5 * time.Second); err != nil {
		t.Fatalf("Close: %v", err)
	}
	for name, result := range map[string]<-chan awaitResult{"running": running, "queued": queued} {
		if received := receive(t, result); !errors.Is(received.err, ErrClosed) {
			t.Fatalf("%s waiter error = %v, want ErrClosed", name, received.err)
		}
	}
	if names := h.tempFiles(); len(names) != 0 {
		t.Fatalf("temporary files remain after Close: %v", names)
	}
	if _, err := h.manager.Await(context.Background(), h.request(3, "user-c", nil)); !errors.Is(err, ErrClosed) {
		t.Fatalf("Await after Close error = %v, want ErrClosed", err)
	}
	if err := h.manager.Close(time.Second); err != nil {
		t.Fatalf("a second Close: %v", err)
	}
}

func TestACanceledQueuedJobReleasesItsSlotAndReservation(t *testing.T) {
	h := newHarness(t, harnessOptions{limits: func(l *limits) { l.maxRunning, l.maxQueued = 1, 1 }})
	release := make(chan struct{})
	h.converter.set(func(f *fakeConverter) { f.block = release })

	running := h.startAwait(context.Background(), h.request(1, "user-a", nil))
	h.waitEntered(1)
	queuedCtx, cancelQueued := context.WithCancel(context.Background())
	queued := h.startAwait(queuedCtx, h.request(2, "user-b", nil))
	waitUntil(t, "the second job to queue", func() bool { return h.queuedCount() == 1 })
	if _, err := h.manager.Await(context.Background(), h.request(3, "user-c", nil)); !errors.Is(err, ErrBusy) {
		t.Fatalf("third job error = %v, want ErrBusy", err)
	}

	cancelQueued()
	receive(t, queued)
	h.clock.advance(10 * time.Second)
	waitUntil(t, "the queued job to be dropped", func() bool { return h.queuedCount() == 0 })
	h.manager.mu.Lock()
	reserved := h.manager.reserved
	h.manager.mu.Unlock()
	if reserved != reservation(1000) {
		t.Fatalf("reserved = %d, want only the running job's reservation %d", reserved, reservation(1000))
	}
	if h.converter.startedCount() != 1 {
		t.Fatal("a canceled queued job started converting")
	}
	close(release)
	if received := receive(t, running); received.err != nil {
		t.Fatalf("running job: %v", received.err)
	}
}

func TestOpenEntryOfAMissingKeyIsNotCached(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	if _, _, err := h.manager.OpenEntry(h.manager.Key(h.source(1, nil).Version)); !errors.Is(err, ErrNotCached) {
		t.Fatalf("OpenEntry error = %v, want ErrNotCached", err)
	}
	if count := h.converter.startedCount(); count != 0 {
		t.Fatal("OpenEntry must never start a conversion")
	}
}

func TestAwaitDoesNotLeakWaitersOrJobs(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	for n := 1; n <= 3; n++ {
		if _, err := h.manager.Await(context.Background(), h.request(n, "user-a", nil)); err != nil {
			t.Fatalf("Await %d: %v", n, err)
		}
	}
	h.manager.mu.Lock()
	defer h.manager.mu.Unlock()
	if len(h.manager.jobs) != 0 || len(h.manager.userKeys) != 0 || h.manager.running != 0 || h.manager.reserved != 0 || len(h.manager.queue) != 0 {
		t.Fatalf("manager state leaked: jobs=%d userKeys=%d running=%d reserved=%d queue=%d",
			len(h.manager.jobs), len(h.manager.userKeys), h.manager.running, h.manager.reserved, len(h.manager.queue))
	}
}

func TestStatsReportTheCache(t *testing.T) {
	h := newHarness(t, harnessOptions{maxBytes: 4096})
	if _, err := h.manager.Await(context.Background(), h.request(1, "user-a", nil)); err != nil {
		t.Fatalf("Await: %v", err)
	}
	stats := h.manager.Stats()
	if stats.Entries != 1 || stats.Bytes != 100 || stats.MaxBytes != 4096 || stats.Version != "fake-1" {
		t.Fatalf("Stats = %+v", stats)
	}
}

func TestOpenedSourceHandlesAreClosed(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	hooks := &sourceHooks{}
	var handle *os.File
	h.converter.set(func(f *fakeConverter) { f.onConvert = func(job ConvertJob) { handle = job.Source } })
	if _, err := h.manager.Await(context.Background(), h.request(1, "user-a", hooks)); err != nil {
		t.Fatalf("Await: %v", err)
	}
	if hooks.opened.Load() != 1 {
		t.Fatalf("Open ran %d times, want 1", hooks.opened.Load())
	}
	if handle == nil {
		t.Fatal("the converter did not receive the source handle")
	}
	if _, err := handle.Stat(); err == nil {
		t.Fatal("the source handle was left open after the job")
	}
}
