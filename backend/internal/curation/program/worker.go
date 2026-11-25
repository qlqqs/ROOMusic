// Package program 在可见发行上顺序执行版本化的匹配、补全与重试。
package program

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

const (
	LocalRuleVersion    int16 = 2
	MatchRuleVersion    int16 = 102
	PatrolPromptVersion int16 = 1

	// 资料主题与并入继续使用原来的版本和幂等键。
	resourceRuleVersion int16 = 101
	StoredRuleVersion         = resourceRuleVersion
)

type Options struct {
	CanonicalNames                               func(ctx context.Context, releaseID, trackID string) (SearchNames, error)
	Fingerprints                                 Fingerprints
	Now                                          func() time.Time
	After                                        func(time.Duration) <-chan time.Time
	PollInterval, ScanInterval, FullPassInterval time.Duration
}

type Worker struct {
	db          *sql.DB
	writer      *curation.Writer
	sources     *curation.Sources
	scanRunning func(context.Context) (bool, error)
	logger      *slog.Logger
	opts        Options

	mu           sync.RWMutex
	state        string
	lastPass     time.Time
	batch        curation.Batch
	batchStarted time.Time
}

func NewWorker(db *sql.DB, writer *curation.Writer, sources *curation.Sources, scanRunning func(context.Context) (bool, error), logger *slog.Logger, opts Options) *Worker {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.After == nil {
		opts.After = time.After
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = time.Minute
	}
	if opts.ScanInterval <= 0 {
		opts.ScanInterval = 30 * time.Second
	}
	if opts.FullPassInterval <= 0 {
		opts.FullPassInterval = 6 * time.Hour
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{db: db, writer: writer, sources: sources, scanRunning: scanRunning, logger: logger, opts: opts, state: "idle"}
}

var _ curation.ProgramStatus = (*Worker)(nil)

func (w *Worker) State() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.state
}

func (w *Worker) CurrentBatch() (string, time.Time, int, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.batch.ID, w.batch.OpenedAt, int(w.batch.Operations), w.batch.ID != ""
}

func (w *Worker) LastPassFinishedAt() *time.Time {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.lastPass.IsZero() {
		return nil
	}
	t := w.lastPass
	return &t
}

func (w *Worker) setState(s string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.state = s
}

func (w *Worker) wait(ctx context.Context, delay time.Duration) {
	select {
	case <-ctx.Done():
	case <-w.opts.After(delay):
	}
}

func failureCode(err error) string {
	var f *operations.Failure
	if errors.As(err, &f) {
		return f.Code
	}
	return ""
}

func (w *Worker) ready(ctx context.Context) error {
	settings, err := w.writer.Settings(ctx)
	if err != nil {
		return err
	}
	if !settings.Active {
		return operations.Conflict("curation_disabled", "自动整理未启用")
	}
	if settings.Paused {
		return operations.Conflict("curation_paused", "自动整理已暂停")
	}
	running, err := w.scanRunning(ctx)
	if err != nil {
		return err
	}
	if running {
		return operations.Conflict("scan_running", "曲库扫描进行中")
	}
	return nil
}

func (w *Worker) rest(ctx context.Context, err error) {
	state, delay := "idle", w.opts.PollInterval
	switch failureCode(err) {
	case "curation_disabled":
		state = "disabled"
	case "curation_paused":
		state = "paused"
	case "scan_running":
		state, delay = "waiting_for_scan", w.opts.ScanInterval
	default:
		w.logger.ErrorContext(ctx, "自动整理处理失败", "event", "curation.program.failed", "error", err)
	}
	w.setState(state)
	w.wait(ctx, delay)
}

var errReleaseChanged = errors.New("发行轮的输入已变化")

// Run 先处理发行与封面，再处理歌词。数据库失败从页首重读，已完成单元由摘要跳过。
func (w *Worker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		if err := w.writer.CloseOpenBatches(ctx, "program"); err != nil {
			if ctx.Err() != nil {
				return
			}
			w.rest(ctx, err)
			continue
		}
		break
	}
	for ctx.Err() == nil {
		err := w.ready(ctx)
		if err == nil {
			err = w.runPasses(ctx)
		}
		if ctx.Err() != nil {
			return
		}
		if closeErr := w.closeBatch(ctx); closeErr != nil {
			err = closeErr
		}
		if err != nil && !errors.Is(err, errReleaseChanged) {
			w.rest(ctx, err)
		}
	}
}

func releaseMarker(c curation.ProgramChanges) curation.ProgramChanges {
	c.ArtistSettings = sql.NullTime{}
	c.Operation = sql.NullTime{}
	c.LatestDue = sql.NullTime{}
	c.Due = 0
	return c
}

func (w *Worker) changes(ctx context.Context) (curation.ProgramChanges, error) {
	return curation.LoadProgramChanges(ctx, w.db, w.opts.Now())
}

func (w *Worker) runPasses(ctx context.Context) error {
	started := w.opts.Now()
	start, err := w.changes(ctx)
	if err != nil {
		return err
	}
	step, err := w.fingerprintStep(ctx)
	if err != nil {
		return err
	}
	w.setState("running")
	if err := w.walk(ctx, false, nil, step != nil); err != nil {
		return err
	}
	if err := w.closeBatch(ctx); err != nil {
		return err
	}
	base, err := w.changes(ctx)
	if err != nil {
		return err
	}
	// 成功处理到期项会减少计数；只把新到期或其他输入的变化视为打断。
	if base.ReleaseDue < start.ReleaseDue {
		start.ReleaseDue = base.ReleaseDue
	}
	if !base.LatestReleaseDue.Time.After(started) {
		start.LatestReleaseDue = base.LatestReleaseDue
	}
	if releaseMarker(base) != releaseMarker(start) {
		return errReleaseChanged
	}
	lastCheck := w.opts.Now()
	checkPriority := func() error {
		now := w.opts.Now()
		if now.Sub(lastCheck) < w.opts.PollInterval {
			return nil
		}
		lastCheck = now
		changes, err := w.changes(ctx)
		if err != nil {
			return err
		}
		if releaseMarker(changes) != releaseMarker(base) {
			return errReleaseChanged
		}
		return nil
	}
	if err := w.walk(ctx, true, checkPriority, false); err != nil {
		return err
	}
	if err := w.closeBatch(ctx); err != nil {
		return err
	}
	if err := w.walkArtistMedia(ctx, checkPriority); err != nil {
		return err
	}
	if err := w.closeBatch(ctx); err != nil {
		return err
	}
	if err := w.writer.AdvanceReleaseMergeQueues(ctx, resourceRuleVersion); err != nil {
		return err
	}
	idle, err := w.changes(ctx)
	if err != nil {
		return err
	}
	if releaseMarker(idle) != releaseMarker(base) {
		return errReleaseChanged
	}
	finished := w.opts.Now()
	w.mu.Lock()
	w.lastPass, w.state = finished, "idle"
	w.mu.Unlock()
	for ctx.Err() == nil {
		w.wait(ctx, w.opts.PollInterval)
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := w.ready(ctx); err != nil {
			return err
		}
		changes, err := w.changes(ctx)
		if err != nil {
			return err
		}
		if changes != idle || idle.LatestDue.Time.After(started) || w.opts.Now().Sub(finished) >= w.opts.FullPassInterval {
			break
		}
	}
	return ctx.Err()
}
