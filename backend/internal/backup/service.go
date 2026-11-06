package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
)

type Option func(*Service)

func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }
func WithRunner(runner Runner) Option       { return func(s *Service) { s.runner = runner } }
func WithDatabaseAccess(access DatabaseAccess) Option {
	return func(s *Service) { s.database = access }
}
func WithWaitingNotification(notify func()) Option { return func(s *Service) { s.onWaiting = notify } }

// Service 的状态只在 mu 下读写；进程内预约先于跨进程锁，Wait 后不再接受新工作。
type Service struct {
	settings                  config.Backup
	logger                    *slog.Logger
	runner                    Runner
	database                  DatabaseAccess
	now                       func() time.Time
	onWaiting                 func()
	mu                        sync.Mutex
	retentionMu               sync.Mutex // 串行化规则保存与自动清理，不阻塞手动删除。
	running                   *Running
	lastFailure               *Failure
	retryAt                   time.Time
	lifecycle                 context.Context
	started, closing, startup bool
	idle                      chan struct{}
	wake                      chan struct{}
	workers                   sync.WaitGroup
}

func NewService(settings config.Backup, logger *slog.Logger, options ...Option) *Service {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if settings.Location == nil {
		settings.Location = time.UTC
	}
	if settings.KeepDays == 0 {
		settings.KeepDays = 14
	}
	idle := make(chan struct{})
	close(idle)
	s := &Service{settings: settings, logger: logger.With("module", "backup"), runner: ExecRunner{}, database: postgresAccess{}, now: time.Now, lifecycle: context.Background(), idle: idle, wake: make(chan struct{}, 1)}
	for _, option := range options {
		option(s)
	}
	s.readState()
	return s
}
func (s *Service) Start(lifecycle context.Context, db *sql.DB) {
	s.mu.Lock()
	if s.started || s.closing {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.startup = true
	s.lifecycle = lifecycle
	s.workers.Add(1)
	s.mu.Unlock()
	go func() { defer s.workers.Done(); s.cleanPartials(); s.schedule(lifecycle, db) }()
}
func (s *Service) Wait(ctx context.Context) error {
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
	s.notify()
	done := make(chan struct{})
	go func() { s.workers.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Service) Overview(ctx context.Context) (Overview, error) {
	if ctx.Err() != nil {
		return Overview{}, ctx.Err()
	}
	files, err := s.listFiles()
	directory := s.directoryStatus()
	if err != nil {
		files = []File{}
	}
	status := Status{Directory: directory}
	if len(files) > 0 {
		at := files[0].CreatedAt
		status.LastSuccessAt = &at
	}
	s.mu.Lock()
	if s.running != nil {
		running := *s.running
		status.Running = &running
	}
	if s.lastFailure != nil && (status.LastSuccessAt == nil || s.lastFailure.At.After(*status.LastSuccessAt)) {
		f := *s.lastFailure
		f.At = f.At.UTC()
		status.LastFailure = &f
	}
	if s.started && !s.closing {
		last := time.Time{}
		if status.LastSuccessAt != nil {
			last = *status.LastSuccessAt
		}
		plan := Plan(PlanInput{Now: s.now(), Location: s.settings.Location, Hour: s.settings.Hour, LastSuccess: last, RetryAt: s.retryAt, Startup: s.startup})
		next := plan.Next.UTC()
		status.NextScheduledAt = &next
	}
	s.mu.Unlock()
	total := len(files)
	if total > 500 {
		files = files[:500]
	}
	var retention *Retention
	if rule, err := s.effectiveRetention(); err == nil {
		retention = &rule
	}
	return Overview{Status: status, Retention: retention, Backups: files, Total: total}, nil
}
func (s *Service) reserve(ctx context.Context, source Source, wait bool) error {
	for {
		s.mu.Lock()
		if s.closing || s.lifecycle.Err() != nil {
			s.mu.Unlock()
			return failure("prepare", "killed")
		}
		if s.running == nil {
			s.running = &Running{Source: source, StartedAt: s.now().UTC()}
			s.idle = make(chan struct{})
			s.workers.Add(1)
			s.mu.Unlock()
			return nil
		}
		idle := s.idle
		s.mu.Unlock()
		if !wait {
			return ErrBusy
		}
		select {
		case <-ctx.Done():
			return failure("prepare", "killed")
		case <-idle:
		}
	}
}
func (s *Service) finish(source Source) {
	s.mu.Lock()
	s.running = nil
	close(s.idle)
	s.mu.Unlock()
	s.workers.Done()
	if source != SourceScheduled {
		s.notify()
	}
}
func (s *Service) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Service) StartManual(ctx context.Context, db *sql.DB) (Status, bool, error) {
	if err := s.reserve(ctx, SourceManual, false); err != nil {
		if errors.Is(err, ErrBusy) {
			overview, e := s.Overview(ctx)
			return overview.Status, false, e
		}
		return Status{}, false, err
	}
	release, err := s.acquire(ctx, db, false)
	if err != nil {
		if !errors.Is(err, ErrBusy) {
			s.recordFailure(SourceManual, err)
		}
		s.finish(SourceManual)
		return Status{}, false, err
	}
	overview, err := s.Overview(ctx)
	if err != nil {
		release()
		s.finish(SourceManual)
		return Status{}, false, err
	}
	s.mu.Lock()
	lifecycle := s.lifecycle
	s.mu.Unlock()
	go func() { defer s.finish(SourceManual); defer release(); _, _ = s.run(lifecycle, db, SourceManual, 0) }()
	return overview.Status, true, nil
}
func (s *Service) RunNow(ctx context.Context, db *sql.DB, source Source) (File, error) {
	return s.runReserved(ctx, db, source, 0, true)
}
func (s *Service) BackupBeforeMigration(ctx context.Context, db *sql.DB, change postgres.MigrationChange) (string, error) {
	file, err := s.runReserved(ctx, db, SourcePreMigration, change.FromVersion, true)
	return file.Name, err
}
func (s *Service) runReserved(ctx context.Context, db *sql.DB, source Source, version int64, wait bool) (File, error) {
	if !validSource(source) {
		return File{}, failure("prepare", "invalid_source")
	}
	if err := s.reserve(ctx, source, wait); err != nil {
		return File{}, err
	}
	defer s.finish(source)
	release, err := s.acquire(ctx, db, wait)
	if err != nil {
		if !errors.Is(err, ErrBusy) {
			s.recordFailure(source, err)
		}
		return File{}, err
	}
	defer release()
	return s.run(ctx, db, source, version)
}
func (s *Service) run(ctx context.Context, db *sql.DB, source Source, version int64) (file File, err error) {
	started := s.now()
	s.logger.Info("开始备份", "event", "backup.started", "source", source)
	defer func() {
		if err != nil {
			s.recordFailure(source, err)
		}
	}()
	if s.settings.CreateDirectory {
		if e := os.MkdirAll(s.settings.Directory, 0700); e != nil {
			return File{}, failure("directory", "not_writable")
		}
	}
	if status := s.directoryStatus(); status != "ready" {
		return File{}, failure("directory", status)
	}
	serverMajor, err := s.database.ServerMajor(ctx, db)
	if err != nil {
		return File{}, failure("prepare", "connect_failed")
	}
	address, env, err := s.prepare(ctx, serverMajor)
	if err != nil {
		return File{}, err
	}
	partial, err := os.CreateTemp(s.settings.Directory, fmt.Sprintf(".roomusic-%s-%s-%d.partial.*", started.Format("20060102-150405"), source, os.Getpid()))
	if err != nil {
		return File{}, failure("directory", "not_writable")
	}
	defer partial.Close()
	defer os.Remove(partial.Name())
	if err = s.command(ctx, "pg_dump", "pg_dump", []string{"--format=custom", "--no-password", "--lock-wait-timeout=60s", "--dbname=" + address}, env, nil, partial); err != nil {
		return File{}, err
	}
	if err = s.verify(ctx, partial, env); err != nil {
		return File{}, err
	}
	// 固定时钟与真实时钟都通过最终文件的 mtime 派生成功时间。
	now := s.now()
	if os.Chtimes(partial.Name(), now, now) != nil {
		return File{}, failure("publish", "other")
	}
	file, err = s.publish(ctx, partial, source, version)
	if err != nil {
		return File{}, err
	}
	s.mu.Lock()
	s.lastFailure = nil
	s.retryAt = time.Time{}
	s.mu.Unlock()
	s.writeState()
	s.logger.Info("备份完成", "event", "backup.completed", "source", source, "file", file.Name, "bytes", file.SizeBytes, "duration_ms", s.now().Sub(started).Milliseconds())
	s.retain()
	return file, nil
}
func (s *Service) recordFailure(source Source, err error) {
	classified := &Error{Stage: "prepare", Reason: "other", ExitCode: -1}
	var known *Error
	if errors.As(err, &known) {
		classified = known
	}
	at := s.now().UTC()
	s.mu.Lock()
	s.lastFailure = &Failure{At: at, Stage: classified.Stage, Source: source}
	s.retryAt = at.Add(time.Hour)
	s.mu.Unlock()
	s.logger.Error("备份没有成功", "event", "backup.failed", "source", source, "stage", classified.Stage, "exit_code", classified.ExitCode, "reason", classified.Reason)
	s.writeState()
}
