// Package program 按目标优先级调度版本化的匹配与补全。
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
	"github.com/qlqq/roomusic/backend/internal/sources"
)

const (
	LocalRuleVersion    int16 = 3
	MatchRuleVersion    int16 = 102
	PatrolPromptVersion int16 = 1

	// 资料主题与并入继续使用原来的版本和幂等键。
	resourceRuleVersion int16 = 101
	StoredRuleVersion         = resourceRuleVersion
)

type Options struct {
	CanonicalNames                               func(ctx context.Context, releaseID, trackID string) (SearchNames, error)
	Fingerprints                                 Fingerprints
	ConfirmScan                                  func(context.Context) error
	NamesPatrolled                               func(context.Context, string) (bool, error)
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

	mu                       sync.RWMutex
	state                    string
	lastPass, lastCompletion time.Time
	batchMu                  sync.Mutex
	policy                   *sources.QueuePolicy
	metrics                  map[string]*queueMetrics
	created                  time.Time
	projectionMu             sync.Mutex
	marker                   *curation.ProgramChanges
	projected                time.Time
	printMu                  sync.Mutex
	prints                   map[string]computedPrint
	batch                    curation.Batch
	batchStarted             time.Time
}

func NewWorker(db *sql.DB, writer *curation.Writer, sourceGate *curation.Sources, scanRunning func(context.Context) (bool, error), logger *slog.Logger, opts Options) *Worker {
	if writer != nil {
		if opts.NamesPatrolled == nil {
			opts.NamesPatrolled = writer.NamesPatrolled
		}
		if opts.CanonicalNames == nil {
			opts.CanonicalNames = func(ctx context.Context, releaseID, trackID string) (SearchNames, error) {
				title, artist, err := writer.CanonicalPatrolNames(ctx, releaseID, trackID)
				return SearchNames{Title: title, Artist: artist}, err
			}
		}
	}
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
	return &Worker{db: db, writer: writer, sources: sourceGate, scanRunning: scanRunning, logger: logger, opts: opts, state: "idle", policy: sources.NewQueuePolicy(opts.Now), metrics: make(map[string]*queueMetrics), created: opts.Now(), prints: make(map[string]computedPrint)}
}

var _ curation.ProgramStatus = (*Worker)(nil)

func (w *Worker) State() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.state
}

func (w *Worker) CurrentBatch() (string, time.Time, int, bool) {
	w.batchMu.Lock()
	defer w.batchMu.Unlock()
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
	if a := currentAttempt(ctx); a != nil && a.item.ReleaseID != "" {
		var stable bool
		if err := w.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM curation_stable_releases WHERE id=$1)`, a.item.ReleaseID).Scan(&stable); err != nil {
			return err
		}
		if !stable {
			return errReleaseChanged
		}
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
