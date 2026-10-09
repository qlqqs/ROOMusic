package derivation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// 默认上限（PRD B3、B6、B7）。
const (
	// DefaultMaxCacheBytes 是缓存的默认上限：10 GiB。
	DefaultMaxCacheBytes int64 = 10 << 30
	// MinMaxCacheBytes 是允许配置的缓存上限下限：256 MiB。
	MinMaxCacheBytes int64 = 256 << 20

	defaultMaxRunning     = 2
	defaultMaxQueued      = 16
	defaultMaxKeysPerUser = 3
	defaultGrace          = 10 * time.Second
	defaultJobTimeout     = 10 * time.Minute
	defaultFailureMemory  = 10 * time.Minute
	// defaultMinFree 是缓存目录所在文件系统必须保留的空间；低于它不启动新转换。
	defaultMinFree int64 = 1 << 30
	// reserveSlack 是预留量在“源大小 × 2”之上的余量。
	reserveSlack int64 = 16 << 20
	// maxRememberedFailures 限制失败记忆的条数，防止它无界增长。
	maxRememberedFailures = 1024
)

// limits 是管理器的运行上限，生产使用默认值，测试可以缩小。
type limits struct {
	maxRunning     int
	maxQueued      int
	maxKeysPerUser int
	grace          time.Duration
	jobTimeout     time.Duration
	failureMemory  time.Duration
	minFree        int64
}

func defaultLimits() limits {
	return limits{
		maxRunning:     defaultMaxRunning,
		maxQueued:      defaultMaxQueued,
		maxKeysPerUser: defaultMaxKeysPerUser,
		grace:          defaultGrace,
		jobTimeout:     defaultJobTimeout,
		failureMemory:  defaultFailureMemory,
		minFree:        defaultMinFree,
	}
}

// Config 是 Open 的参数。
type Config struct {
	// Dir 是缓存目录（<数据目录>/media-cache）；不存在时创建。调用方负责保证它与音乐根目录互不包含。
	Dir string
	// MaxBytes 是缓存上限。
	MaxBytes int64
	// FFmpegPath 是 ffmpeg 可执行文件的路径，空则在 PATH 中查找。
	FFmpegPath string
	Logger     *slog.Logger
}

// Source 描述要转换的源文件：它的版本事实，以及“它仍是扫描时的那个文件”的核对办法。回调由调用方提供，
// 因为哪个文件才是扫描时的那个、目录是否仍被允许属于调用方（playback）的策略；本包只在转换前后调用它们。
type Source struct {
	Version SourceVersion
	// Open 安全地只读打开源文件并完成与扫描记录的核对（普通文件、大小与修改时间、容器）；调用方负责关闭返回的文件。
	// 任务自己重新打开，不借用第一个请求的句柄，所以不受该请求生命周期的影响。
	Open func(ctx context.Context) (*os.File, error)
	// Verify 核对已打开的句柄仍是扫描时的那个文件：转换前 content 为真，要对句柄重新计算内容指纹并核对；
	// 转换后 content 为假，只需确认大小与修改时间没有变化（转换期间被改写则失败）。
	// 回调返回的错误原样交给等待者，不计入转换失败记忆。
	Verify func(ctx context.Context, file *os.File, content bool) error
}

// Request 是一次等待转换完成的请求。
type Request struct {
	Source Source
	// User 标识发起请求的成员，用于限制每个成员同时等待的不同转换个数。
	User string
	// TrackID 只用于日志。
	TrackID string
}

// Entry 是缓存中一个完成品。
type Entry struct {
	Key  string
	Size int64
}

// Stats 是缓存的当前状态。
type Stats struct {
	Entries  int
	Bytes    int64
	MaxBytes int64
	Version  string
}

type failure struct {
	err     *FailedError
	expires time.Time
}

// job 是一个键上的转换任务。waiters、graceStop、graceGen、running、finished 由 Manager.mu 保护；
// done 关闭之前写入 entry 与 err，之后只读。
type job struct {
	key     string
	request Request
	reserve int64
	base    context.Context
	cancel  context.CancelCauseFunc

	waiters   int
	graceStop func() bool
	graceGen  int
	running   bool
	finished  bool

	done  chan struct{}
	entry Entry
	err   error
}

// Manager 管理派生任务与缓存：按键合并任务，请求在等待期间登记为等待者，最后一个等待者离开并过了宽限期后取消转换；
// 同时转换、排队与每个成员等待的键都有上限；失败短期记忆；关停时在有限时间内结束全部转换。
// 任务的上下文来自管理器的生命周期，不来自任何一个请求。
type Manager struct {
	cache     *Cache
	converter Converter
	logger    *slog.Logger
	limits    limits
	clock     clock
	freeSpace func(dir string) (uint64, error)

	lifecycle context.Context
	shutdown  context.CancelCauseFunc
	jobsDone  sync.WaitGroup

	mu       sync.Mutex
	closed   bool
	jobs     map[string]*job
	queue    []*job
	running  int
	reserved int64
	userKeys map[string]map[string]int
	failures map[string]failure
}

// Open 打开缓存、定位并自检 ffmpeg，返回可用的管理器。ffmpeg 不可用时返回 *UnavailableError（缓存目录此时已建立）。
func Open(ctx context.Context, config Config) (*Manager, error) {
	cache, err := openCache(config.Dir, config.MaxBytes, realClock{})
	if err != nil {
		return nil, err
	}
	ffmpeg, err := ProbeFFmpeg(ctx, config.FFmpegPath, filepath.Join(config.Dir, tempDirName))
	if err != nil {
		_ = cache.close()
		return nil, err
	}
	return newManager(cache, ffmpeg, config.Logger, defaultLimits(), realClock{}, freeSpace), nil
}

func newManager(cache *Cache, converter Converter, logger *slog.Logger, limits limits, clk clock, space func(string) (uint64, error)) *Manager {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	lifecycle, shutdown := context.WithCancelCause(context.Background())
	return &Manager{
		cache: cache, converter: converter, logger: logger, limits: limits, clock: clk, freeSpace: space,
		lifecycle: lifecycle, shutdown: shutdown,
		jobs: map[string]*job{}, userKeys: map[string]map[string]int{}, failures: map[string]failure{},
	}
}

// Version 返回转换器的版本串。
func (m *Manager) Version() string { return m.converter.Version() }

// Key 返回源文件版本对应的缓存键（含转换器版本与转换策略版本）。
func (m *Manager) Key(version SourceVersion) string {
	return deriveKey(m.converter.Version(), version)
}

// Stats 返回缓存的条目数、总字节数与上限。
func (m *Manager) Stats() Stats {
	entries, bytes := m.cache.stats()
	return Stats{Entries: entries, Bytes: bytes, MaxBytes: m.cache.max, Version: m.converter.Version()}
}

// OpenEntry 以只读方式打开缓存中的派生文件并记录一次使用；没有该条目时返回 ErrNotCached。它从不启动转换。
func (m *Manager) OpenEntry(key string) (*os.File, int64, error) {
	return m.cache.open(key)
}

// Await 确保该源文件版本的派生文件存在：命中缓存立即返回；否则启动或加入该键的任务，并把本请求登记为等待者，
// 直到任务结束或 ctx 结束（等待者随之注销）。ctx 结束时返回 ctx 的错误，任务继续运行，直到没有等待者且过了宽限期。
// 错误：ErrBusy（上限已满）、ErrClosed、*FailedError（转换失败，含失败记忆）、Source 回调返回的错误、ctx 的错误。
func (m *Manager) Await(ctx context.Context, request Request) (Entry, error) {
	key := m.Key(request.Source.Version)
	if entry, ok := m.hit(key); ok {
		return entry, nil
	}
	running, entry, err := m.join(key, request)
	if err != nil {
		return Entry{}, err
	}
	if running == nil {
		m.cache.touch(key)
		return entry, nil
	}
	defer m.release(running, request.User)
	select {
	case <-running.done:
		return running.entry, running.err
	case <-ctx.Done():
		return Entry{}, ctx.Err()
	}
}

func (m *Manager) hit(key string) (Entry, bool) {
	size, ok := m.cache.lookup(key)
	if !ok {
		return Entry{}, false
	}
	m.cache.touch(key)
	return Entry{Key: key, Size: size}, true
}

// join 在锁内决定请求的去向：失败记忆内直接失败；缓存里已有完成品则返回该条目（任务为 nil）；否则加入已有任务或
// 在上限允许时创建任务，并把请求登记为等待者。
func (m *Manager) join(key string, request Request) (*job, Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, Entry{}, ErrClosed
	}
	if recorded, ok := m.failures[key]; ok {
		if m.clock.now().Before(recorded.expires) {
			return nil, Entry{}, recorded.err
		}
		delete(m.failures, key)
	}
	running := m.jobs[key]
	if running == nil {
		// 完成品先发布、任务后移出，所以此刻没有任务而缓存里有，就是刚刚完成的。
		if size, ok := m.cache.lookup(key); ok {
			return nil, Entry{Key: key, Size: size}, nil
		}
	}
	if !m.userMayWaitLocked(request.User, key) {
		return nil, Entry{}, ErrBusy
	}
	if running == nil {
		var err error
		if running, err = m.admitLocked(key, request); err != nil {
			return nil, Entry{}, err
		}
	}
	m.addWaiterLocked(running, request.User)
	return running, Entry{}, nil
}

// userMayWaitLocked 判断成员是否还能再等待一个键：已经在等这个键的不算新增。
func (m *Manager) userMayWaitLocked(user, key string) bool {
	keys := m.userKeys[user]
	return keys[key] > 0 || len(keys) < m.limits.maxKeysPerUser
}

func (m *Manager) addWaiterLocked(running *job, user string) {
	running.waiters++
	keys := m.userKeys[user]
	if keys == nil {
		keys = map[string]int{}
		m.userKeys[user] = keys
	}
	keys[running.key]++
	if running.graceStop != nil {
		running.graceStop()
		running.graceStop = nil
		running.graceGen++
	}
}

// release 注销一个等待者；最后一个离开时开始宽限期计时，期间有新的等待者就继续转换，否则取消。
func (m *Manager) release(running *job, user string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	running.waiters--
	if keys := m.userKeys[user]; keys != nil {
		if keys[running.key]--; keys[running.key] <= 0 {
			delete(keys, running.key)
		}
		if len(keys) == 0 {
			delete(m.userKeys, user)
		}
	}
	if running.waiters == 0 && !running.finished {
		running.graceGen++
		generation := running.graceGen
		running.graceStop = m.clock.afterFunc(m.limits.grace, func() { m.graceExpired(running, generation) })
	}
}

// graceExpired 在宽限期结束时取消仍然没有等待者的任务；期间有新等待者加入或任务已结束则什么都不做。
func (m *Manager) graceExpired(running *job, generation int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if running.finished || running.waiters > 0 || running.graceGen != generation {
		return
	}
	running.graceStop = nil
	// 立即移出任务表：之后的新请求不能加入一个正在被取消的任务，而是创建新任务。
	if m.jobs[running.key] == running {
		delete(m.jobs, running.key)
	}
	running.cancel(errNoWaiters)
	if running.running {
		return
	}
	// 还在排队：没有协程，直接收尾。
	m.removeQueuedLocked(running)
	m.reserved -= running.reserve
	running.finished = true
	running.err = context.Canceled
	close(running.done)
	m.logCanceled(running, "no_waiters")
}

func (m *Manager) removeQueuedLocked(target *job) {
	for index, queued := range m.queue {
		if queued == target {
			m.queue = append(m.queue[:index], m.queue[index+1:]...)
			return
		}
	}
}

// admitLocked 在上限允许时创建任务：同时运行与排队的个数、缓存目录所在文件系统的剩余空间（扣除已预留的）。
// 预留量 = 源大小 × 2 + 16 MiB，任务结束时释放。
func (m *Manager) admitLocked(key string, request Request) (*job, error) {
	if m.running >= m.limits.maxRunning && len(m.queue) >= m.limits.maxQueued {
		return nil, ErrBusy
	}
	reserve := reservation(request.Source.Version.Size)
	free, err := m.freeSpace(m.cache.dir)
	if err != nil {
		return nil, &FailedError{Category: FailureIO, Err: fmt.Errorf("read free space: %w", err)}
	}
	available := int64(math.MaxInt64)
	if free < math.MaxInt64 {
		available = int64(free)
	}
	if available-m.reserved < reserve+m.limits.minFree {
		return nil, ErrBusy
	}
	base, cancel := context.WithCancelCause(m.lifecycle)
	created := &job{key: key, request: request, reserve: reserve, base: base, cancel: cancel, done: make(chan struct{})}
	m.jobs[key] = created
	m.reserved += reserve
	if m.running < m.limits.maxRunning {
		m.startLocked(created)
	} else {
		m.queue = append(m.queue, created)
	}
	return created, nil
}

// reservation 返回源大小 × 2 + 16 MiB（饱和运算，不溢出）。
func reservation(size int64) int64 {
	size = max(size, 0)
	if size > (math.MaxInt64-reserveSlack)/2 {
		return math.MaxInt64
	}
	return size*2 + reserveSlack
}

func (m *Manager) startLocked(started *job) {
	m.running++
	started.running = true
	m.jobsDone.Add(1)
	go m.run(started)
}

func (m *Manager) startQueuedLocked() {
	for !m.closed && m.running < m.limits.maxRunning && len(m.queue) > 0 {
		next := m.queue[0]
		m.queue = m.queue[1:]
		m.startLocked(next)
	}
}

func (m *Manager) run(running *job) {
	defer m.jobsDone.Done()
	started := m.clock.now()
	ctx, stop := context.WithTimeout(running.base, m.limits.jobTimeout)
	entry, err := m.execute(ctx, running)
	stop()
	m.finish(running, entry, err, started)
}

// execute 是任务的全部工作：重新安全打开源文件 → 转换前核对内容指纹 → 转换并核对 → 转换后核对源未变化 → 发布。
// 任何一步失败或被取消都不会留下可命中的文件：临时文件在返回前删除。
func (m *Manager) execute(ctx context.Context, running *job) (Entry, error) {
	source := running.request.Source
	file, err := source.Open(ctx)
	if err != nil {
		return Entry{}, m.classify(ctx, err)
	}
	defer file.Close()
	if err := source.Verify(ctx, file, true); err != nil {
		return Entry{}, m.classify(ctx, err)
	}
	tempName, tempPath, err := m.cache.newTemp()
	if err != nil {
		return Entry{}, &FailedError{Category: FailureIO, Err: err}
	}
	published := false
	defer func() {
		if !published {
			m.cache.discard(tempName)
		}
	}()
	version := source.Version
	err = m.converter.Convert(ctx, ConvertJob{
		Source: file,
		Output: tempPath,
		Limit:  min(running.reserve, m.cache.max),
		Expect: Expect{BitDepth: version.BitDepth, SampleRate: version.SampleRate, Channels: version.Channels},
	})
	if err != nil {
		return Entry{}, m.classify(ctx, err)
	}
	if err := source.Verify(ctx, file, false); err != nil {
		return Entry{}, m.classify(ctx, err)
	}
	size, err := m.cache.publish(tempName, running.key)
	if err != nil {
		if errors.Is(err, errOutputTooLarge) {
			return Entry{}, &FailedError{Category: FailureOutputLimit, Err: err}
		}
		return Entry{}, &FailedError{Category: FailureIO, Err: err}
	}
	published = true
	return Entry{Key: running.key, Size: size}, nil
}

// classify 统一任务错误的类别：任务的 context 已结束（被取消或超时）时，不论哪一步返回了什么错误，
// 都按取消或超时处理；否则原样返回（*FailedError 或 Source 回调的错误）。
func (m *Manager) classify(ctx context.Context, err error) error {
	cause := context.Cause(ctx)
	switch {
	case cause == nil:
		return err
	case errors.Is(cause, errNoWaiters):
		return &canceledError{reason: "no_waiters"}
	case errors.Is(cause, context.DeadlineExceeded):
		return &FailedError{Category: FailureTimeout, Err: cause}
	default:
		return &canceledError{reason: "shutdown"}
	}
}

// finish 收尾：先把任务移出任务表（完成品已在此之前发布），释放预留，记忆失败，再唤醒等待者并启动排队的任务。
func (m *Manager) finish(running *job, entry Entry, err error, started time.Time) {
	var failed *FailedError
	var canceled *canceledError
	m.mu.Lock()
	if m.jobs[running.key] == running {
		delete(m.jobs, running.key)
	}
	m.running--
	m.reserved -= running.reserve
	running.finished = true
	if running.graceStop != nil {
		running.graceStop()
		running.graceStop = nil
	}
	switch {
	case errors.As(err, &failed):
		m.rememberFailureLocked(running.key, failed)
	case errors.As(err, &canceled):
		// 只有关停时才可能还有等待者：无人等待的取消没有人接收结果。
		err = context.Canceled
		if canceled.reason == "shutdown" {
			err = ErrClosed
		}
	}
	running.entry, running.err = entry, err
	close(running.done)
	m.startQueuedLocked()
	m.mu.Unlock()

	switch {
	case err == nil:
		m.logger.Info("playback derivation completed", "event", "playback.derivation.completed",
			"track_id", running.request.TrackID, "duration_ms", m.clock.now().Sub(started).Milliseconds(), "output_bytes", entry.Size)
	case failed != nil:
		m.logger.Warn("playback derivation failed", "event", "playback.derivation.failed",
			"track_id", running.request.TrackID, "category", failed.Category)
	case canceled != nil:
		m.logCanceled(running, canceled.reason)
	}
}

func (m *Manager) logCanceled(canceled *job, reason string) {
	m.logger.Info("playback derivation canceled", "event", "playback.derivation.canceled",
		"track_id", canceled.request.TrackID, "reason", reason)
}

// rememberFailureLocked 记住失败：该键在记忆期内的请求直接得到同一个失败，不再重试。条数有上限，先清掉已过期的。
func (m *Manager) rememberFailureLocked(key string, failed *FailedError) {
	now := m.clock.now()
	if len(m.failures) >= maxRememberedFailures {
		for recordedKey, recorded := range m.failures {
			if !now.Before(recorded.expires) {
				delete(m.failures, recordedKey)
			}
		}
	}
	if len(m.failures) >= maxRememberedFailures {
		for recordedKey := range m.failures {
			delete(m.failures, recordedKey)
			break
		}
	}
	m.failures[key] = failure{err: failed, expires: now.Add(m.limits.failureMemory)}
}

// Close 关停管理器：不再接受新请求，取消全部排队与运行中的任务（ffmpeg 进程组被终止，临时文件被删除），
// 最多等待 wait 让它们结束。超时返回 context.DeadlineExceeded（残留的临时文件由下次启动清理）。可重复调用。
func (m *Manager) Close(wait time.Duration) error {
	m.mu.Lock()
	if !m.closed {
		m.closed = true
		for _, queued := range m.queue {
			if m.jobs[queued.key] == queued {
				delete(m.jobs, queued.key)
			}
			m.reserved -= queued.reserve
			queued.finished = true
			queued.err = ErrClosed
			close(queued.done)
			m.logCanceled(queued, "shutdown")
		}
		m.queue = nil
		m.shutdown(ErrClosed)
	}
	m.mu.Unlock()

	finished := make(chan struct{})
	go func() {
		m.jobsDone.Wait()
		close(finished)
	}()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-finished:
		_ = m.cache.close()
		return nil
	case <-timer.C:
		return context.DeadlineExceeded
	}
}
