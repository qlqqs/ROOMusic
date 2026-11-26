package library

import (
	"context"
	"database/sql"
	"errors"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
)

const watchQuiet = 2 * time.Second

type directoryBatch struct {
	removedTrees map[string]bool
	directories  map[string]bool
	cookies      map[uint32]bool
	due          time.Time
	whole        bool
}

type watchedRoot struct {
	root    registeredRoot
	mode    string
	network bool
	cancel  context.CancelFunc
	pending []*directoryBatch
}

type directoryWatcher struct {
	mu            sync.Mutex
	roots         map[string]*watchedRoot
	wake, refresh chan struct{}
	clock         ScanSchedulerClock
	system        watchSystem
	workers       sync.WaitGroup
}

func newDirectoryWatcher() directoryWatcher {
	return directoryWatcher{roots: map[string]*watchedRoot{}, wake: make(chan struct{}, 1), refresh: make(chan struct{}, 1), clock: scanSchedulerClock{}, system: defaultWatchSystem()}
}

// WithDirectoryWatchClock 在启动前注入局部调度时钟，每日整点计时器独立。
func (s *Service) WithDirectoryWatchClock(clock ScanSchedulerClock) *Service {
	s.watch.clock = clock
	return s
}

func wakeWatch(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (s *Service) directoryWriting(id, dir string) bool {
	w := &s.watch
	w.mu.Lock()
	defer w.mu.Unlock()
	if root := w.roots[id]; root != nil {
		for _, batch := range root.pending {
			if (batch.whole || batch.directories[albumDirectory(dir)]) && w.clock.Now().Before(batch.due) {
				return true
			}
		}
	}
	return false
}

func (s *Service) watchMode(id string, revision int64) *string {
	w := &s.watch
	w.mu.Lock()
	defer w.mu.Unlock()
	if r := w.roots[id]; r != nil && r.root.revision == revision && r.mode != "" {
		mode := r.mode
		return &mode
	}
	return nil
}

func (w *directoryWatcher) record(root *watchedRoot, event watchEvent) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.roots[root.root.ID] != root {
		return
	}
	batch := &directoryBatch{removedTrees: map[string]bool{}, directories: map[string]bool{}, cookies: map[uint32]bool{}, due: w.clock.Now().Add(watchQuiet), whole: event.overflow}
	for _, dir := range event.dirs {
		batch.directories[dir] = true
		batch.directories[albumDirectory(dir)] = true
		if folderArtworkSubdirectoryNames[strings.ToLower(filepath.Base(dir))] {
			batch.directories[ingest.DirectoryOf(dir)] = true
		}
	}
	if event.removedTree != "" {
		batch.removedTrees[event.removedTree] = true
	}
	if event.move != 0 {
		batch.cookies[event.move] = true
	}
	keep := root.pending[:0]
	for _, old := range root.pending {
		merge := old.whole || batch.whole || old.cookies[event.move]
		if !merge {
			for dir := range batch.directories {
				if old.directories[dir] {
					merge = true
					break
				}
			}
		}
		if !merge {
			keep = append(keep, old)
			continue
		}
		batch.whole = batch.whole || old.whole
		maps.Copy(batch.removedTrees, old.removedTrees)
		maps.Copy(batch.directories, old.directories)
		maps.Copy(batch.cookies, old.cookies)
	}
	root.pending = append(keep, batch)
	wakeWatch(w.wake)
}

// 各读循环只拥有自己的 fd。根被停用、改址或重建时，旧回调因不再对应当前登记而失效。
func (s *Service) attachWatch(ctx context.Context, r *watchedRoot, opened []string) {
	w := &s.watch
	watchCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	root := r.root
	w.workers.Add(1)
	go func() {
		defer w.workers.Done()
		ready := func() {
			w.mu.Lock()
			if w.roots[root.ID] == r {
				r.mode = "realtime"
			}
			w.mu.Unlock()
			wakeWatch(w.wake)
		}
		err := w.system.watch(watchCtx, root, ready, func(event watchEvent) { w.record(r, event) }, opened)
		if watchCtx.Err() != nil {
			return
		}
		w.mu.Lock()
		if w.roots[root.ID] == r {
			r.mode = "scheduled"
			r.network = errors.Is(err, errNetworkMount)
			r.pending = nil
		}
		w.mu.Unlock()
		if s.logger != nil {
			reason := "registration_failed"
			if errors.Is(err, errNetworkMount) {
				reason = "network_mount"
			}
			s.logger.Info("曲库目录切换至定时扫描", "event", "library.scan.watch_fallback", "root_id", root.ID, "reason", reason)
		}
		wakeWatch(w.refresh)
	}()
}

func (s *Service) refreshWatches(ctx context.Context) error {
	roots, err := s.loadRegisteredRoots(ctx, s.database)
	if err != nil {
		return err
	}
	w := &s.watch
	active := map[string]bool{}
	w.mu.Lock()
	for _, root := range roots {
		if !root.baseline.Valid {
			continue
		}
		active[root.ID] = true
		prev := w.roots[root.ID]
		var opened []string
		if prev != nil {
			reprobe := prev.mode == "scheduled" && !prev.network && root.baseline.Time.After(prev.root.baseline.Time)
			if root.revision == prev.root.revision && slices.Equal(root.Exclusions, prev.root.Exclusions) && !reprobe {
				prev.root = root
				continue
			}
			for _, path := range prev.root.Exclusions {
				if !slices.Contains(root.Exclusions, path) {
					opened = append(opened, path)
				}
			}
			prev.cancel()
		}
		current := &watchedRoot{root: root}
		if prev != nil && root.Path == prev.root.Path {
			current.pending = prev.pending
		}
		w.roots[root.ID] = current
		s.attachWatch(ctx, current, opened)
	}
	for id, r := range w.roots {
		if !active[id] {
			r.cancel()
			delete(w.roots, id)
		}
	}
	w.mu.Unlock()
	return nil
}

func (s *Service) startRootPoll(ctx context.Context, r registeredRoot, now time.Time) (StartResult, error) {
	return s.StartScan(ctx, StartOptions{Mode: ScanModeIncremental, Trigger: ScanTriggerScheduled, prepare: func(ctx context.Context, tx *sql.Tx) (scanTargets, error) {
		result, err := tx.ExecContext(ctx, `UPDATE library_roots SET last_poll_started_at=$3 WHERE id=$1 AND revision=$2 AND status='active'
		AND watch_baseline_at IS NOT NULL AND last_poll_started_at+poll_interval_minutes*interval '1 minute'<=$3`, r.ID, r.revision, now)
		if err != nil {
			return nil, err
		}
		n, err := result.RowsAffected()
		if err != nil || n == 0 {
			return nil, err
		}
		return scanTargets{r.ID: {revision: r.revision}}, nil
	}})
}

func (s *Service) startWatchBatches(ctx context.Context, r registeredRoot, batches []*directoryBatch) (StartResult, error) {
	w := &s.watch
	return s.StartScan(ctx, StartOptions{Mode: ScanModeIncremental, Trigger: ScanTriggerScheduled, prepare: func(ctx context.Context, tx *sql.Tx) (scanTargets, error) {
		var active bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM library_roots WHERE id=$1 AND revision=$2 AND status='active')`, r.ID, r.revision).Scan(&active); err != nil || !active {
			return nil, err
		}
		w.mu.Lock()
		defer w.mu.Unlock()
		current := w.roots[r.ID]
		if current == nil || current.mode != "realtime" || current.root.revision != r.revision {
			return nil, nil
		}
		dirs := map[string]bool{}
		trees := map[string]bool{}
		for _, batch := range batches {
			if !slices.Contains(current.pending, batch) || w.clock.Now().Before(batch.due) {
				return nil, nil
			}
			if batch.whole {
				return scanTargets{r.ID: {revision: r.revision}}, nil
			}
			maps.Copy(dirs, batch.directories)
			maps.Copy(trees, batch.removedTrees)
		}
		return scanTargets{r.ID: {revision: r.revision, directories: slices.Sorted(maps.Keys(dirs)), removedTrees: slices.Sorted(maps.Keys(trees))}}, nil
	}})
}

func (s *Service) handleWatchedRoot(ctx context.Context, id string, now time.Time) time.Duration {
	w := &s.watch
	delay := scanSchedulePollInterval
	w.mu.Lock()
	current := w.roots[id]
	if current == nil {
		w.mu.Unlock()
		return delay
	}
	root, mode := current.root, current.mode
	var due []*directoryBatch
	for _, batch := range current.pending {
		if !batch.due.After(now) {
			due = append(due, batch)
		} else {
			delay = min(delay, batch.due.Sub(now))
		}
	}
	w.mu.Unlock()
	if mode == "scheduled" {
		if !root.lastPoll.Valid {
			var anchor time.Time
			err := s.database.QueryRowContext(ctx, `UPDATE library_roots SET last_poll_started_at=coalesce(last_poll_started_at,$3)
			 WHERE id=$1 AND revision=$2 AND status='active' RETURNING last_poll_started_at`, id, root.revision, now).Scan(&anchor)
			if err != nil {
				return delay
			}
			root.lastPoll = sql.NullTime{Time: anchor, Valid: true}
			w.mu.Lock()
			current.root.lastPoll = root.lastPoll
			w.mu.Unlock()
		}
		next := root.lastPoll.Time.Add(time.Duration(root.interval) * time.Minute)
		if next.After(now) {
			return min(delay, next.Sub(now))
		}
		result, err := s.startRootPoll(ctx, root, now)
		if err == nil && (result.Started || result.Scan.ID == "") {
			wakeWatch(w.refresh)
		}
		return time.Second
	}
	if mode != "realtime" || len(due) == 0 {
		return delay
	}
	result, err := s.startWatchBatches(ctx, root, due)
	if err != nil || !result.Started {
		return min(delay, time.Second)
	}
	w.mu.Lock()
	current.pending = slices.DeleteFunc(current.pending, func(batch *directoryBatch) bool { return slices.Contains(due, batch) })
	w.mu.Unlock()
	return delay
}

// RunDirectoryWatcher 与每日调度共用读库间隔。文件事件只唤醒内存队列，
// 根设置或扫描终态另行唤醒刷新，避免持续拷贝触发持续查询。
func (s *Service) RunDirectoryWatcher(ctx context.Context) {
	w := &s.watch
	defer func() {
		w.mu.Lock()
		for _, root := range w.roots {
			root.cancel()
		}
		clear(w.roots)
		w.mu.Unlock()
		w.workers.Wait()
	}()
	var refreshAt time.Time
	for ctx.Err() == nil {
		now := w.clock.Now()
		if !refreshAt.After(now) {
			if err := s.refreshWatches(ctx); err != nil && ctx.Err() == nil && s.logger != nil {
				s.logger.Warn("无法刷新曲库目录监听配置")
			}
			refreshAt = now.Add(scanSchedulePollInterval)
		}
		w.mu.Lock()
		ids := slices.Sorted(maps.Keys(w.roots))
		w.mu.Unlock()
		delay := refreshAt.Sub(now)
		for _, id := range ids {
			delay = min(delay, s.handleWatchedRoot(ctx, id, now))
		}
		timer := w.clock.NewTimer(max(0, delay))
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-w.refresh:
			timer.Stop()
			refreshAt = time.Time{}
		case <-w.wake:
			timer.Stop()
		case <-timer.C():
		}
	}
}

// SavePollInterval 保存根自己的间隔，独立于每日全库扫描设置和目录操作版本。
func (s *Service) SavePollInterval(ctx context.Context, id string, minutes int) error {
	switch minutes {
	case 15, 60, 240, 720, 1440:
	default:
		return &RootOperationError{Code: "invalid_input", Message: "定时扫描间隔无效"}
	}
	result, err := s.database.ExecContext(ctx, `UPDATE library_roots SET poll_interval_minutes=$2 WHERE id=$1`, id, minutes)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return &RootOperationError{Code: "not_found", Message: "目录不存在"}
	}
	wakeWatch(s.watch.refresh)
	return nil
}
