package model

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
)

var ErrStopped = errors.New("后台整理条件已变更")

type Options struct {
	Lookup       LookupQueue
	Now          func() time.Time
	PollInterval time.Duration
	BatchSize    int
}

// Launcher 的实现负责管家会话、方案与运行生命周期。
type Launcher interface {
	Availability(context.Context, string, string) (Configuration, error)
	OnSettingsChanged(func())
	Estimate(context.Context, RunParams) (int64, error)
	Probe(context.Context, string, string, bool) (int64, int64, *curation.ModelFailure, error)
	CreateConversation(ctx context.Context, actor, title, model string) (string, error)
	DeleteConversation(ctx context.Context, actor, id string) error
	ResolvePlans(ctx context.Context, conversation string) error
	Start(context.Context, RunParams) (Run, error)
}

type Configuration struct{ Model, Key, ModelKey, Reason string }
type RunResult struct {
	Reason  string
	Failure *curation.ModelFailure
}

type Run interface {
	Wait(context.Context) (RunResult, error)
}

type RunParams struct {
	Budget               int64
	Verified             bool
	Items                []curation.PendingItem
	ProviderError        func(time.Duration)
	Run                  curation.ModelRun
	Text                 string
	BeforeRequest        func(context.Context) error
	BeforeStep           func(context.Context) error
	Usage                func(context.Context, int64, int64)
	RemainingItemBudget  func() (int, int64)
	ChargeEstimatedUsage func(context.Context, int64) error
	OnToolCalls          func(context.Context, int) error
}

type Scheduler struct {
	configMu sync.Mutex
	probing  bool
	db       *sql.DB
	writer   *curation.Writer
	sources  *curation.Sources
	launcher Launcher
	options  Options
	logger   *slog.Logger
	mu       sync.RWMutex
	running  int

	active          map[*modelWork][]string
	wake            chan struct{}
	workers         sync.WaitGroup
	providerUntil   time.Time
	providerBackoff time.Duration
	activeBackoff   time.Duration
}

func NewScheduler(db *sql.DB, writer *curation.Writer, sources *curation.Sources, launcher Launcher, logger *slog.Logger, options Options) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.PollInterval <= 0 {
		options.PollInterval = time.Minute
	}
	if options.BatchSize <= 0 {
		options.BatchSize = 5
	}

	s := &Scheduler{db: db, writer: writer, sources: sources, launcher: launcher, options: options, logger: logger, active: make(map[*modelWork][]string), wake: make(chan struct{}, 1)}
	if launcher != nil {
		launcher.OnSettingsChanged(s.Wake)
	}
	return s
}

func (s *Scheduler) recover(ctx context.Context) error {
	if err := s.writer.CloseOpenBatches(ctx, "model"); err != nil {
		return err
	}
	if err := s.launcher.ResolvePlans(ctx, ""); err != nil {
		return err
	}
	return s.writer.RecoverInProgress(ctx)
}

type modelWork struct {
	queueKind                  string
	config                     string
	verified                   bool
	budget                     int64
	text                       string
	actor, model, conversation string
	items                      []curation.PendingItem
}

func (s *Scheduler) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Scheduler) Run(ctx context.Context) {
	defer s.workers.Wait()
	recovered := false
	for ctx.Err() == nil {
		var err error
		if !recovered {
			err = s.recover(ctx)
			recovered = err == nil
		}
		if recovered {
			var started bool
			started, err = s.dispatch(ctx)
			if started {
				continue
			}
		}
		if err != nil && ctx.Err() == nil {
			s.logger.ErrorContext(ctx, "后台整理调度失败", "event", "curation.model.failed", "error", err)
		}
		delay := s.options.PollInterval
		s.mu.RLock()
		if remaining := s.providerUntil.Sub(s.options.Now()); remaining > 0 {
			delay = min(delay, remaining)
		}
		s.mu.RUnlock()
		if recovered && err == nil {
			now := s.options.Now()
			var next *time.Time
			if e := s.db.QueryRowContext(ctx, `SELECT min(t) FROM (
            SELECT retry_at AS t FROM curation_model_state WHERE retry_at>$1
            UNION ALL SELECT min(next_attempt_at) FROM curation_pending_items
             WHERE status IN ('waiting_model','waiting_route','in_progress') AND next_attempt_at>$1) deadlines`, now).Scan(&next); e == nil && next != nil {
				delay = min(delay, next.Sub(now))
			}
			delay = min(delay, curation.NextBudgetDay(now).Sub(now))
		}
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-time.After(delay):
		}
	}
}

// 单一领取循环按包预留目标键；候选准备、模型运行和收尾可在不同包之间并行。
func (s *Scheduler) dispatch(ctx context.Context) (bool, error) {
	now := s.options.Now()
	manual, err := s.writer.ReadyArtistWebActor(ctx, now)
	if err != nil {
		return false, err
	}
	if manual != "" {
		active, err := s.dispatchFor(curation.WithArtistWebActor(ctx, manual))
		if err != nil || active {
			return active, err
		}
	}
	return s.dispatchFor(ctx)
}

func (s *Scheduler) dispatchFor(ctx context.Context) (bool, error) {
	progress, actor, err := s.conditions(ctx, true)
	if err == nil && progress.modelBusy {
		return false, nil
	}
	if err == nil && progress.PauseKind != nil && *progress.PauseKind == "provider_quota" && progress.RetryAt != nil && !progress.RetryAt.After(s.options.Now()) && !progress.Today.AtLimit(progress.DailyTokenLimit) && progress.Configured && progress.State == "unavailable" {
		err = s.probe(ctx, progress, actor, false)
		return false, err
	}
	if err != nil || (progress.State != "idle" && progress.State != "limit_reached") {
		return false, err
	}
	s.mu.RLock()
	full := s.slotsUsed() >= progress.Concurrency
	var occupied []string
	for _, keys := range s.active {
		occupied = append(occupied, keys...)
	}
	s.mu.RUnlock()
	if full {
		return false, nil
	}

	now := s.options.Now()
	if progress.State == "idle" && curation.ArtistWebActor(ctx) == "" {
		if err := s.writer.MaterializeVerificationItems(ctx); err != nil {
			return false, err
		}
	}
	excluded, err := s.writer.ConflictingModelItems(ctx, occupied, now)
	if err != nil {
		return false, err
	}
	w, err := s.nextWork(ctx, actor, progress, now, excluded)
	if err != nil || w == nil {
		return false, err
	}
	if !progress.allows(taskKind(w.items[0])) {
		return false, s.release(ctx, w.conversation, false, nil)
	}
	keys, err := s.writer.ModelTargetKeys(ctx, itemIDs(w.items))
	if err != nil {
		if w.conversation != "" {
			err = errors.Join(err, s.release(ctx, w.conversation, false, nil))
		}
		return false, err
	}
	if !s.reserve(w, keys, progress.Concurrency) {
		if w.conversation != "" {
			return false, s.release(ctx, w.conversation, false, nil)
		}
		return false, nil
	}
	s.workers.Go(func() {
		err := s.process(ctx, w)
		if err != nil && ctx.Err() == nil {
			s.logger.ErrorContext(ctx, "后台整理处理失败", "event", "curation.model.failed", "error", err)
		}
		s.mu.Lock()
		delete(s.active, w)
		s.mu.Unlock()
		if err == nil {
			s.Wake()
		}
	})
	return true, nil
}

// 调用方持有 mu，探测与后台包共用并发上限。
func (s *Scheduler) slotsUsed() int {
	n := len(s.active)
	if s.probing {
		n++
	}
	return n
}

func (s *Scheduler) reserve(w *modelWork, keys []string, concurrency int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.slotsUsed() >= concurrency || s.options.Now().Before(s.providerUntil) {
		return false
	}
	// 领取查询期间可能新增待定项或更正署名，按最终目标复核。
	for _, held := range s.active {
		for _, key := range keys {
			if slices.Contains(held, key) {
				return false
			}
		}
	}
	s.active[w] = keys
	return true
}

func (s *Scheduler) process(ctx context.Context, w *modelWork) error {
	items, conversation := w.items, w.conversation
	kind := taskKind(items[0])
	var err error
	if conversation == "" {
		items, err = s.prepareBatch(ctx, w)
		if err != nil || len(items) == 0 {
			return err
		}
		live, actor, err := s.conditions(ctx, true)
		if err != nil || actor != w.actor || live.config.Key != w.config || live.budget != w.budget || live.State != "idle" || !live.allows(kind) {
			return err
		}
		now := s.options.Now()
		conversation, err = s.launcher.CreateConversation(ctx, w.actor, "自动整理 "+now.Format("01-02 15:04"), w.model)
		if err != nil {
			return err
		}
		prepared := len(items)
		items, err = s.writer.ClaimPreparedKind(ctx, conversation, now, w.queueKind, items)
		if err != nil || len(items) == 0 || (w.queueKind == string(localQueue) && len(items) != prepared) {
			return errors.Join(err, s.writer.ReleaseClaimed(ctx, conversation, false, nil), s.launcher.DeleteConversation(ctx, w.actor, conversation))
		}
		w.text, err = firstMessage(items)
		if err != nil {
			return errors.Join(err, s.release(ctx, conversation, false, nil))
		}
	}
	for len(items) > 0 {
		n := 1
		if w.queueKind != "" {
			n = len(items)
		}
		active, rest := items[:n], items[n:]
		if err := s.itemCheckpoint(ctx, w.actor, active); err != nil {
			released := s.writer.ReleaseModelItems(ctx, conversation, itemIDs(items), false, nil)
			if errors.Is(err, ErrStopped) {
				return released
			}
			return errors.Join(err, released)
		}
		reason, err := s.runBatch(ctx, w, conversation, active)
		if err != nil || reason != "completed" {
			if len(rest) > 0 {
				err = errors.Join(err, s.writer.ReleaseModelItems(ctx, conversation, itemIDs(rest), false, nil))
			}
			return err
		}
		items = rest
	}
	return nil
}

// 资料读取经过来源队列：可命中缓存，缺失时按真实来源恢复时刻退回该项。
func (s *Scheduler) prepareBatch(ctx context.Context, w *modelWork) ([]curation.PendingItem, error) {
	if s.options.Lookup != nil {
		ctx = s.options.Lookup.PreparationContext(ctx)
	}
	matcher := program.NewModelMatcher(s.db, s.writer, s.sources)
	var ready []curation.PendingItem
	for _, p := range w.items {
		if err := s.prepareItem(ctx, matcher, &p); err != nil {
			if !curation.IsSourceUnavailable(err) {
				return nil, err
			}
			retry := curation.NextSourceRetry(s.options.Now(), curation.SourceRecoveryTime(err), p.SourceRetryCount)
			if err = s.writer.DeferSelection(ctx, p, retry); err != nil {
				return nil, err
			}
			continue
		}
		ready = append(ready, p)
	}
	if len(ready) == 0 {
		return nil, nil
	}
	if w.queueKind == string(localQueue) {
		if len(ready) != len(w.items) {
			return nil, nil
		}
		fits, err := s.batchFits(ctx, w, ready)
		if err != nil || fits {
			return ready, err
		}
		return nil, s.writer.StopLongInputs(ctx, w.config, w.budget, ready)
	}
	var batch []curation.PendingItem
	for _, p := range ready {
		fits, err := s.batchFits(ctx, w, []curation.PendingItem{p})
		if err != nil {
			return nil, err
		}
		if !fits {
			if taskKind(p) == SelectCandidate {
				var candidates []program.Candidate
				if err = json.Unmarshal(p.Candidates, &candidates); err != nil {
					return nil, err
				}
				slices.SortStableFunc(candidates, func(a, b program.Candidate) int { return cmp.Compare(b.Score, a.Score) })
				for n := min(3, len(candidates)-1); n > 0 && !fits; n-- {
					p.Candidates, err = json.Marshal(candidates[:n])
					if err != nil {
						return nil, err
					}
					fits, err = s.batchFits(ctx, w, []curation.PendingItem{p})
					if err != nil {
						return nil, err
					}
				}
			}
			if !fits {
				if err = s.writer.StopLongInputs(ctx, w.config, w.budget, []curation.PendingItem{p}); err != nil {
					return nil, err
				}
				continue
			}
		}
		proposed := append(batch, p)
		if len(batch) > 0 {
			fits, err = s.batchFits(ctx, w, proposed)
			if err != nil {
				return nil, err
			}
		}
		if fits {
			batch = proposed
		}
	}
	return batch, nil
}

func (s *Scheduler) batchFits(ctx context.Context, w *modelWork, items []curation.PendingItem) (bool, error) {
	text, err := firstMessage(items)
	if err != nil {
		return false, err
	}
	run := curation.ModelRun{ActorID: w.actor, Model: w.model, TaskKind: string(taskKind(items[0]))}
	if len(items) == 1 {
		run.PendingItemID = items[0].ID
	}
	size, err := s.launcher.Estimate(ctx, RunParams{Run: run, Items: items, Text: text, Budget: w.budget, Verified: w.verified})
	return size <= w.budget, err
}

func (s *Scheduler) checkRunConditions(ctx context.Context, actor string, items []curation.PendingItem) error {
	progress, enabler, err := s.conditions(ctx, false)
	if err != nil {
		return err
	}
	if enabler != actor || progress.State != "idle" || !progress.allows(taskKind(items[0])) {
		return ErrStopped
	}
	return nil
}

func (s *Scheduler) runBatch(ctx context.Context, w *modelWork, conversation string, items []curation.PendingItem) (string, error) {
	actor, model := w.actor, w.model
	ctx = curation.WithModelConfig(ctx, w.config)
	settings, err := s.writer.Settings(ctx)
	if err != nil {
		return "internal_error", errors.Join(err, s.release(ctx, conversation, false, nil))
	}
	s.mu.Lock()
	if s.options.Now().Before(s.providerUntil) || s.running >= settings.ModelConcurrency {
		s.mu.Unlock()
		return "curation_stopped", s.release(ctx, conversation, false, nil)
	}
	s.running++
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.running--; s.mu.Unlock() }()
	var batch string
	var batchMu sync.Mutex
	kind := taskKind(items[0])
	params := RunParams{Budget: w.budget, Verified: w.verified, Items: items, Run: curation.ModelRun{TaskKind: string(kind), ActorID: actor, Model: model, ConversationID: conversation, Batch: func(ctx context.Context) (string, error) {
		batchMu.Lock()
		defer batchMu.Unlock()
		if batch == "" {
			b, err := s.writer.OpenBatch(ctx, "model")
			if err != nil {
				return "", err
			}
			batch = b.ID
		}
		return batch, nil
	}}}
	if w.text != "" {
		params.Text = w.text
	} else {
		var err error
		params.Text, err = s.taskMessage(ctx, items)
		if err != nil {
			return "internal_error", errors.Join(err, s.release(ctx, conversation, false, nil))
		}
	}
	if len(items) == 1 {
		params.Run.PendingItemID = items[0].ID
	}
	var failures sourceFailures
	p := items[0]
	calls, tokens := p.ItemToolCalls, p.ItemTokens
	if kind == ArtistWeb {
		params.RemainingItemBudget = func() (int, int64) { return curation.ArtistWebCallLimit - calls, curation.ArtistWebTokenLimit - tokens }
		params.ChargeEstimatedUsage = func(ctx context.Context, n int64) error {
			if err := s.writer.AddEstimatedItemTokens(ctx, p.ID, conversation, n); err != nil {
				return err
			}
			tokens += n
			return nil
		}
	}
	if p.ClaimSourceRecoveryAt != nil {
		failures.record(*p.ClaimSourceRecoveryAt)
	}
	sourceRetryAt := func(now time.Time) time.Time {
		if kind == ArtistWeb && artistAtLimit(calls, tokens) {
			// 当前请求可能已用完额度，还没有机会从工具取得来源错误。
			if recovery := curation.MatchSourceRecoveryTime(ctx); recovery.After(failures.recovery) {
				failures.record(recovery)
			}
			if failures.count == 0 && p.SourceRetryCount > 0 {
				return now
			}
		}
		return failures.retryAt(now, p.SourceRetryCount)
	}
	if kind == ArtistWeb {
		params.Run.SourceUnavailable = func(ctx context.Context, recovery time.Time) error {
			if recovery.IsZero() {
				recovery = s.options.Now()
			}
			failures.record(recovery)
			return s.writer.RecordClaimSourceFailure(ctx, p, conversation, recovery)
		}
		params.Run.DeferSuggestion = func() bool {
			return !sourceRetryAt(s.options.Now()).IsZero()
		}
	}
	if kind == ArtistWeb {
		params.Run.Abandon = func(ctx context.Context, id, reason string) error {
			if id != p.ID {
				return ErrStopped
			}
			if err := s.checkRunConditions(ctx, actor, items); err != nil {
				return err
			}
			if artistAtLimit(calls, tokens) {
				return s.writer.ShelvePendingItem(ctx, id, conversation, "item_limit")
			}
			if at := sourceRetryAt(s.options.Now()); !at.IsZero() {
				return s.writer.ReleaseModelItems(ctx, conversation, []string{id}, false, &at)
			}
			return s.writer.FinishModelItem(ctx, p, conversation, "no_result")
		}
	}
	var usageErr error
	firstRequest := true
	params.BeforeRequest = func(ctx context.Context) error {
		// 在途答案可落地，下一次实际出站前再检查全局暂停。
		live, enabler, err := s.conditions(ctx, firstRequest || kind == ArtistWeb)
		if err != nil {
			return err
		}
		if enabler != actor || live.config.Key != w.config || live.State != "idle" || live.PauseKind != nil || !live.allows(kind) {
			return ErrStopped
		}
		if kind == ArtistWeb {
			ok, err := s.writer.ArtistWebRunning(ctx, *p.ArtistWebGeneration, p.ID)
			if err != nil {
				return err
			}
			if !ok {
				return ErrStopped
			}
			if artistAtLimit(calls, tokens) {
				return ErrItemLimit
			}
		}
		firstRequest = false
		return nil
	}
	params.BeforeStep = func(ctx context.Context) error {
		if usageErr != nil {
			return usageErr
		}
		if err := s.checkRunConditions(ctx, actor, items); err != nil {
			return err
		}
		if len(items) == 1 {
			current, err := s.writer.PendingItem(ctx, items[0].ID)
			if err != nil {
				return err
			}
			if current.Status != "in_progress" || current.ConversationID != conversation {
				return ErrItemCompleted
			}
		}
		return nil
	}
	params.Usage = func(ctx context.Context, in, out int64) {
		id := ""
		if kind == ArtistWeb {
			id = p.ID
			tokens += in + out
		}
		err := s.writer.RecordItemUsage(ctx, s.options.Now(), id, conversation, in, out)
		if err != nil {
			usageErr = err
			s.logger.ErrorContext(ctx, "记录后台模型用量失败", "conversation_id", conversation, "error", err)
		}
	}
	if kind == ArtistWeb {
		params.OnToolCalls = func(ctx context.Context, n int) error {
			if usageErr != nil {
				return usageErr
			}
			if calls+n > curation.ArtistWebCallLimit {
				return ErrItemLimit
			}
			if n > 0 {
				if err := s.writer.AddItemCalls(ctx, p.ID, conversation, n); err != nil {
					return err
				}
				calls += n
			}
			return nil
		}
	}
	var providerReported atomic.Bool
	params.ProviderError = func(delay time.Duration) {
		providerReported.Store(true)
		s.pauseProvider(delay)
	}
	r, err := s.launcher.Start(ctx, params)
	var failure *curation.ModelFailure
	reason := "internal_error"
	// 运行由应用生命周期取消；等待其真正结束后才释放待定项和批次。
	if err == nil {
		var result RunResult
		result, err = r.Wait(context.WithoutCancel(ctx))
		reason, failure = result.Reason, result.Failure
	} else if errors.As(err, &failure) {
		err = nil
		reason = "provider_error"
	}
	now := s.options.Now()
	if kind == ArtistWeb && artistAtLimit(calls, tokens) {
		reason = "item_limit"
	}
	if !providerReported.Load() && reason == "provider_error" && failure == nil {
		s.pauseProvider(-1)
	} else if err == nil && reason == "completed" {
		s.mu.Lock()
		// 在途项成功仅清零下一轮退避，当前冷却期仍按原间隔合并失败。
		s.providerBackoff = 0
		s.mu.Unlock()
	}
	// 收尾遇到暂时的数据库故障时，先完成本会话，再领取新项。
	itemsFinished := false
	for {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		var finishErr error
		if !itemsFinished {
			if err == nil && kind == ArtistWeb && (reason == "item_limit" || !sourceRetryAt(now).IsZero()) {
				// 最后一个回答可能已经提交候选，只处置仍属于本次领取的项。
				current, readErr := s.writer.PendingItem(cleanup, p.ID)
				finishErr = readErr
				if readErr == nil && current.Status == "in_progress" && current.ConversationID == conversation {
					if current.ModelClaimDigest() != p.ModelClaimDigest() {
						finishErr = s.release(cleanup, conversation, false, nil)
					} else if reason == "item_limit" {
						finishErr = s.writer.ShelvePendingItem(cleanup, p.ID, conversation, "item_limit")
					} else {
						delay := sourceRetryAt(now)
						finishErr = s.writer.ReleaseModelItems(cleanup, conversation, []string{p.ID}, false, &delay)
					}
				}
			} else if err == nil && failure != nil {
				switch failure.Class {
				case "temporary", "provider_quota", "configuration":
					finishErr = s.writer.SetModelFailure(cleanup, w.config, *failure, now)
					if finishErr == nil {
						finishErr = s.release(cleanup, conversation, false, nil)
					}
				default:
					finishErr = s.writer.FailModelItems(cleanup, w.config, conversation, items, *failure, now)
					if finishErr == nil {
						finishErr = s.release(cleanup, conversation, false, nil)
					}
				}
			} else if err == nil && (reason == "completed" || reason == "step_limit" || reason == "token_limit") {
				finishErr = s.checkRunConditions(cleanup, actor, items)
				if finishErr == nil {
					finishErr = s.writer.FailModelItems(cleanup, w.config, conversation, items, curation.ModelFailure{Class: "empty_reply"}, now)
				} else if errors.Is(finishErr, ErrStopped) {
					finishErr = nil
				}
				if finishErr == nil {
					finishErr = s.release(cleanup, conversation, false, nil)
				}
			} else {
				finishErr = s.release(cleanup, conversation, false, nil)
			}
			if finishErr == nil && kind == ArtistWeb && (p.SourceRetryCount > 0 || failures.count > 0) {
				finishErr = s.writer.ClearResolvedSourceFailures(cleanup, p.ID, conversation)
			}
			itemsFinished = finishErr == nil
		}
		if itemsFinished && batch != "" {
			finishErr = s.writer.CloseBatch(cleanup, batch)
		}
		cancel()
		if finishErr == nil || ctx.Err() != nil {
			return reason, errors.Join(err, usageErr, finishErr)
		}
		s.logger.ErrorContext(ctx, "后台整理收尾失败", "conversation_id", conversation, "error", finishErr)
		select {
		case <-ctx.Done():
			return reason, errors.Join(err, usageErr, finishErr)
		case <-time.After(s.options.PollInterval):
		}
	}
}

func (s *Scheduler) release(ctx context.Context, id string, count bool, next *time.Time) error {
	if err := s.launcher.ResolvePlans(ctx, id); err != nil {
		return err
	}
	return s.writer.ReleaseClaimed(ctx, id, count, next)
}

func (s *Scheduler) pauseProvider(delay time.Duration) {
	s.mu.Lock()
	now := s.options.Now()
	if !now.Before(s.providerUntil) {
		s.activeBackoff = 0
	}
	if delay < 0 {
		// 同一冷却期内，在途请求的失败只延后恢复时刻。
		if s.activeBackoff == 0 {
			s.providerBackoff = min(10*time.Minute, max(5*time.Second, s.providerBackoff*2))
			s.activeBackoff = s.providerBackoff
		}
		delay = s.activeBackoff
	}
	until := now.Add(delay)
	if until.After(s.providerUntil) {
		s.providerUntil = until
	}
	s.mu.Unlock()
	s.Wake()
}
