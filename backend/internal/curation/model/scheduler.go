package model

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

var ErrStopped = errors.New("后台整理条件已变更")

type Options struct {
	Lookup        LookupQueue
	Now           func() time.Time
	PollInterval  time.Duration
	ReviewBackoff []time.Duration
	BatchSize     int
}

// Launcher 的实现负责管家会话、方案与运行生命周期。
type Launcher interface {
	Availability(context.Context, string) (model, reason string, err error)
	CreateConversation(ctx context.Context, actor, title, model string) (string, error)
	DeleteConversation(ctx context.Context, actor, id string) error
	PendingPlan(ctx context.Context, actor, conversation string) (string, error)
	RejectedSubmissions(ctx context.Context, actor, conversation string, items []curation.PendingItem) ([]curation.RejectedSubmission, error)
	ResolvePlans(ctx context.Context, conversation string) error
	Start(context.Context, RunParams) (Run, error)
}

type Run interface {
	Wait(context.Context) (string, error)
}

type RunParams struct {
	Items              []curation.PendingItem
	ProviderError      func(time.Duration)
	OnToolCalls        func(context.Context, int) error
	Run                curation.ModelRun
	Text, ReviewPlanID string
	BeforeStep         func(context.Context) error
	Usage              func(context.Context, int64, int64)
	Escalate           func(context.Context, curation.ModelException) (string, error)
}

type Scheduler struct {
	db                 *sql.DB
	writer             *curation.Writer
	sources            *curation.Sources
	launcher           Launcher
	options            Options
	logger             *slog.Logger
	mu                 sync.RWMutex
	running            int
	preparingSelection bool
	active             map[*modelWork][]string
	wake               chan struct{}
	workers            sync.WaitGroup
	providerUntil      time.Time
	providerBackoff    time.Duration
	activeBackoff      time.Duration
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
	if options.ReviewBackoff == nil {
		options.ReviewBackoff = []time.Duration{5 * time.Minute, 30 * time.Minute, 2 * time.Hour}
	}
	return &Scheduler{db: db, writer: writer, sources: sources, launcher: launcher, options: options, logger: logger, active: make(map[*modelWork][]string), wake: make(chan struct{}, 1)}
}

func (s *Scheduler) recover(ctx context.Context) error {
	if err := s.writer.CloseOpenBatches(ctx, "model"); err != nil {
		return err
	}
	if err := s.launcher.ResolvePlans(ctx, ""); err != nil {
		return err
	}
	items, err := s.writer.InterruptedSourceItems(ctx)
	if err != nil {
		return err
	}
	now := s.options.Now()
	for _, item := range items {
		next := now
		if item.ClaimSourceRecoveryAt != nil {
			next = curation.NextSourceRetry(now, *item.ClaimSourceRecoveryAt, item.SourceRetryCount)
		} else if !itemAtLimit(taskKind(item), item.ItemToolCalls, item.ItemTokens) {
			continue
		}
		if err := s.deferSourceItem(ctx, item, item.ConversationID, next); err != nil {
			return err
		}
	}
	return s.writer.RecoverInProgress(ctx)
}

type modelWork struct {
	actor, model, conversation, plan string
	items                            []curation.PendingItem
	retries                          int
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
	progress, actor, err := s.conditions(ctx, true)
	if err != nil || (progress.State != "idle" && progress.State != "limit_reached") {
		return false, err
	}
	s.mu.RLock()
	full := len(s.active) >= progress.Concurrency
	var occupied []string
	for _, keys := range s.active {
		occupied = append(occupied, keys...)
	}
	s.mu.RUnlock()
	if full {
		return false, nil
	}

	now := s.options.Now()
	if progress.State == "idle" {
		if err := s.writer.MaterializePatrol(ctx, program.PatrolPromptVersion); err != nil {
			return false, err
		}
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
	if !s.reserve(w, keys) {
		if w.conversation != "" && w.plan == "" {
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

func (s *Scheduler) reserve(w *modelWork, keys []string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.options.Now().Before(s.providerUntil) {
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
	if w.conversation == "" {
		s.preparingSelection = true
	}
	return true
}

func (s *Scheduler) process(ctx context.Context, w *modelWork) error {
	items, conversation := w.items, w.conversation
	kind := taskKind(items[0])
	var err error
	if conversation == "" {
		// 第一档先准备候选明细，来源等待期间仍可分派其他包。
		items, err = s.prepareSelection(ctx, items)
		s.mu.Lock()
		s.preparingSelection = false
		s.mu.Unlock()
		if err == nil {
			s.Wake()
		}
		if err != nil || len(items) == 0 {
			return err
		}
		now := s.options.Now()
		s.mu.RLock()
		cooling := now.Before(s.providerUntil)
		s.mu.RUnlock()
		if cooling {
			return nil
		}
		conversation, err = s.launcher.CreateConversation(ctx, w.actor, "自动整理 "+now.Format("01-02 15:04"), w.model)
		if err != nil {
			return err
		}
		items, err = s.writer.ClaimPreparedSelection(ctx, conversation, now, items)
		if err != nil || len(items) == 0 {
			return errors.Join(err, s.launcher.DeleteConversation(ctx, w.actor, conversation))
		}
	}
	if w.plan == "" && kind == SelectCandidate {
		ready := items[:0]
		for _, p := range items {
			if string(p.Candidates) == "[]" {
				if err := s.writer.AdvanceModelItem(ctx, p, conversation, string(SelectCandidate), "lookup", "第一档没有可核对的候选明细"); err != nil {
					return errors.Join(err, s.release(ctx, conversation, false, nil))
				}
			} else {
				ready = append(ready, p)
			}
		}
		items = ready
	}
	// 选择的一批共用同一次主调用。多步任务在同艺人会话内逐项启动。
	for len(items) > 0 {
		n := 1
		if w.plan != "" {
			n = len(items)
		} else if kind == SelectCandidate {
			n, err = selectionBatchSize(items)
			if err != nil {
				return errors.Join(err, s.release(ctx, conversation, false, nil))
			}
			if n == 0 {
				if err := s.writer.AdvanceModelItem(ctx, items[0], conversation, string(SelectCandidate), "lookup", "完整曲目与候选超过第一档消息大小上限"); err != nil {
					return errors.Join(err, s.release(ctx, conversation, false, nil))
				}
				items = items[1:]
				continue
			}
		}
		active, rest := items[:n], items[n:]
		if err := s.itemCheckpoint(ctx, w.actor, active); err != nil {
			if w.plan != "" {
				// 等待重审的项重新占用名额。日额度用尽时保留原方案。
				if errors.Is(err, ErrStopped) {
					return nil
				}
				return err
			}
			released := s.writer.ReleaseModelItems(ctx, conversation, itemIDs(items), false, nil)
			if errors.Is(err, ErrStopped) {
				return released
			}
			return errors.Join(err, released)
		}
		runCtx := ctx
		if kind == LookupCandidate {
			if s.options.Lookup == nil {
				return s.release(ctx, conversation, false, nil)
			}
			ready, err := s.options.Lookup.LookupTierReady(ctx, active[0].TargetID, s.options.Now())
			if err != nil || !ready {
				if releaseErr := s.writer.ReleaseModelItems(ctx, conversation, itemIDs(active), false, nil); releaseErr != nil {
					return errors.Join(err, releaseErr)
				}
				if err != nil {
					return errors.Join(err, s.release(ctx, conversation, false, nil))
				}
				items = rest
				continue
			}
		}
		if (kind == LookupCandidate || kind == WebFallback) && s.options.Lookup != nil {
			runCtx = s.options.Lookup.LookupTierContext(ctx)
		}
		reason, err := s.runBatch(runCtx, w.actor, w.model, conversation, w.plan, active, w.retries)
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

func (s *Scheduler) prepareSelection(ctx context.Context, items []curation.PendingItem) ([]curation.PendingItem, error) {
	if s.options.Lookup != nil {
		ctx = s.options.Lookup.LookupTierContext(ctx)
	}
	matcher := program.NewModelMatcher(s.db, s.writer, s.sources)
	for i := range items {
		if err := matcher.PrepareSelection(ctx, items[i:i+1]); err != nil {
			if !curation.IsSourceUnavailable(err) {
				return nil, err
			}
			var failures sourceFailures
			failures.record(curation.SourceRecoveryTime(err))
			return nil, s.writer.DeferSelection(ctx, items[i], failures.retryAt(s.options.Now(), items[i].SourceRetryCount))
		}
	}
	return items, nil
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

func (s *Scheduler) runBatch(ctx context.Context, actor, model, conversation, plan string, items []curation.PendingItem, retries int) (string, error) {
	settings, err := s.writer.Settings(ctx)
	if err != nil {
		return "internal_error", errors.Join(err, s.release(ctx, conversation, false, nil))
	}
	s.mu.Lock()
	if s.options.Now().Before(s.providerUntil) || s.running >= settings.ModelConcurrency {
		s.mu.Unlock()
		if plan != "" {
			return "waiting_review", nil
		}
		return "curation_stopped", s.release(ctx, conversation, false, nil)
	}
	s.running++
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.running--; s.mu.Unlock() }()
	var batch string
	var batchMu sync.Mutex
	kind := taskKind(items[0])
	params := RunParams{Items: items, ReviewPlanID: plan, Run: curation.ModelRun{TaskKind: string(kind), ActorID: actor, Model: model, ConversationID: conversation, Batch: func(ctx context.Context) (string, error) {
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
	if plan == "" {
		var err error
		params.Text, err = s.taskMessage(ctx, items)
		if err != nil {
			if (kind == LookupCandidate || kind == WebFallback) && curation.IsSourceUnavailable(err) {
				var failures sourceFailures
				failures.record(curation.SourceRecoveryTime(err))
				return "completed", s.deferSourceItem(ctx, items[0], conversation, failures.retryAt(s.options.Now(), items[0].SourceRetryCount))
			}
			return "internal_error", errors.Join(err, s.release(ctx, conversation, false, nil))
		}
	}
	if len(items) == 1 {
		params.Run.PendingItemID = items[0].ID
	}
	var failures sourceFailures
	var suggestionDeferred bool
	trackedID := ""
	p := items[0]
	calls, tokens := p.ItemToolCalls, p.ItemTokens
	if p.ClaimSourceRecoveryAt != nil {
		failures.record(*p.ClaimSourceRecoveryAt)
	}
	sourceRetryAt := func(now time.Time) time.Time {
		if itemAtLimit(kind, calls, tokens) {
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
	if kind == LookupCandidate || kind == WebFallback {
		trackedID = p.ID
		params.Run.SourceUnavailable = func(ctx context.Context, recovery time.Time) error {
			if recovery.IsZero() {
				recovery = s.options.Now()
			}
			failures.record(recovery)
			return s.writer.RecordClaimSourceFailure(ctx, p, conversation, recovery)
		}
		params.Run.DeferSuggestion = func() bool {
			suggestionDeferred = !sourceRetryAt(s.options.Now()).IsZero()
			return suggestionDeferred
		}
	}
	params.Run.Abandon = func(ctx context.Context, id, reason string) error {
		for _, item := range items {
			if id == item.ID {
				if err := s.checkRunConditions(ctx, actor, items); err != nil {
					return err
				}
				item.ItemToolCalls, item.ItemTokens = calls, tokens
				return s.abandon(ctx, item, model, conversation, reason, sourceRetryAt(s.options.Now()))
			}
		}
		return ErrStopped
	}
	var usageErr error
	startingItem := plan == ""
	params.BeforeStep = func(ctx context.Context) error {
		if suggestionDeferred {
			return ErrItemCompleted
		}
		if usageErr != nil {
			return usageErr
		}
		if err := s.checkRunConditions(ctx, actor, items); err != nil {
			return err
		}
		if startingItem {
			if err := s.itemCheckpoint(ctx, actor, items); err != nil {
				return err
			}
			startingItem = false
		}
		if itemLimit(kind, calls, tokens) || (itemAtLimit(kind, calls, tokens) && !sourceRetryAt(s.options.Now()).IsZero()) {
			return ErrItemLimit
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
		tokens += in + out
		err := s.writer.RecordItemUsage(ctx, s.options.Now(), trackedID, conversation, kind == WebFallback, in, out)
		if err != nil {
			usageErr = err
			s.logger.ErrorContext(ctx, "记录后台模型用量失败", "conversation_id", conversation, "error", err)
		}
	}
	params.OnToolCalls = func(ctx context.Context, n int) error {
		if usageErr != nil {
			return usageErr
		}
		if trackedID != "" && n > 0 {
			if err := s.writer.AddItemCalls(ctx, trackedID, conversation, n); err != nil {
				return err
			}
			calls += n
		}
		if itemLimit(kind, calls, tokens) {
			return ErrItemLimit
		}
		return nil
	}
	params.Escalate = func(ctx context.Context, e curation.ModelException) (string, error) {
		var target struct {
			ItemID    string `json:"item_id"`
			ReleaseID string `json:"release_id"`
		}
		if err := json.Unmarshal(e.Input, &target); err != nil {
			return "", err
		}
		for _, p := range items {
			matches := strings.EqualFold(target.ItemID, p.ID) || ((e.Tool == "correct_release" || e.Tool == "correct_track") && p.TargetType == "release" && strings.EqualFold(target.ReleaseID, p.TargetID))
			if matches {
				e.PendingItemID, e.ConversationID, e.Provider = p.ID, &conversation, model
				e.ExpectedClaimDigest = p.ModelClaimDigest()
				return s.writer.CreateException(ctx, e)
			}
		}
		return "", ErrStopped
	}
	var providerReported atomic.Bool
	params.ProviderError = func(delay time.Duration) {
		providerReported.Store(true)
		s.pauseProvider(delay)
	}
	r, err := s.launcher.Start(ctx, params)
	reason := "internal_error"
	// 运行由应用生命周期取消；等待其真正结束后才释放待定项和批次。
	if err == nil {
		reason, err = r.Wait(context.WithoutCancel(ctx))
	}
	now := s.options.Now()
	if itemAtLimit(kind, calls, tokens) && !sourceRetryAt(now).IsZero() {
		reason = "item_limit"
	} else if itemLimit(kind, calls, tokens) && reason != "provider_error" && reason != "cancelled" && reason != "review_unavailable" {
		reason = "item_limit"
	}
	if providerReported.Load() {
		if reason == "review_unavailable" {
			reason = "provider_error"
		}
	} else if reason == "provider_error" {
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
			finishErr = s.finish(cleanup, actor, model, conversation, reason, retries, now, items, sourceRetryAt(now))
			if finishErr == nil && trackedID != "" && (p.SourceRetryCount > 0 || failures.count > 0) {
				finishErr = s.writer.ClearResolvedSourceFailures(cleanup, trackedID, conversation)
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

func (s *Scheduler) finish(ctx context.Context, actor, model, id, reason string, retries int, now time.Time, items []curation.PendingItem, retryAt time.Time) error {
	if !retryAt.IsZero() && reason != "review_unavailable" && reason != "item_limit" {
		reason = "try_later"
	}
	switch reason {
	case "item_limit", "try_later":
		if retryAt.IsZero() {
			if err := s.checkRunConditions(ctx, actor, items); err != nil {
				if errors.Is(err, ErrStopped) {
					return s.release(ctx, id, false, nil)
				}
				return err
			}
		}
		if err := s.launcher.ResolvePlans(ctx, id); err != nil {
			return err
		}
		p, err := s.writer.PendingItem(ctx, items[0].ID)
		if err != nil {
			return err
		}
		if p.Status != "in_progress" || p.ConversationID != id {
			return nil
		}
		if p.ModelClaimDigest() != items[0].ModelClaimDigest() {
			return s.release(ctx, id, false, nil)
		}
		run := curation.ModelRun{ActorID: actor, ConversationID: id, PendingItemID: p.ID}
		err = s.abandon(curation.WithModelRun(ctx, &run), p, model, id, reason, retryAt)
		var failure *operations.Failure
		if errors.As(err, &failure) && failure.Code == "item_stale" {
			return s.release(ctx, id, false, nil)
		}
		return err
	case "review_unavailable":
		if retries < min(3, len(s.options.ReviewBackoff)) {
			items, err := s.writer.ClaimedItems(ctx, id)
			if err != nil {
				return err
			}
			if len(items) == 0 {
				return s.launcher.ResolvePlans(ctx, id)
			}
			if int(items[0].ReviewRetries) > retries {
				return nil
			}
			return s.writer.RetryClaimedReview(ctx, id, now.Add(s.options.ReviewBackoff[retries]))
		}
		next := now.Add(24 * time.Hour)
		return s.release(ctx, id, false, &next)
	case "completed", "step_limit", "token_limit":
		err := s.checkRunConditions(ctx, actor, items)
		if err == nil {
			return s.releaseUnhandled(ctx, actor, model, id, items)
		}
		if !errors.Is(err, ErrStopped) {
			return err
		}
	case "cancelled", "curation_stopped", "provider_error":
	default:
		s.logger.ErrorContext(ctx, "后台模型运行异常结束", "event", "curation.model.run_failed", "conversation_id", id, "reason", reason)
	}
	return s.release(ctx, id, false, nil)
}

func (s *Scheduler) releaseUnhandled(ctx context.Context, actor, model, conversation string, items []curation.PendingItem) error {
	claimed, err := s.writer.ClaimedItems(ctx, conversation)
	if err != nil {
		return err
	}
	current := make(map[string]string, len(claimed))
	for _, item := range claimed {
		current[item.ID] = item.ModelClaimDigest()
	}
	var pending []curation.PendingItem
	for _, item := range items {
		if current[item.ID] == item.ModelClaimDigest() {
			pending = append(pending, item)
		}
	}
	if len(pending) == 0 {
		return s.writer.ReleaseModelItems(ctx, conversation, itemIDs(items), false, nil)
	}
	if err := s.saveRejections(ctx, actor, conversation, pending); err != nil {
		return err
	}
	var reason string
	switch taskKind(pending[0]) {
	case SelectCandidate:
		reason = "第一档连续三次未得到可执行的决定"
	case LookupCandidate:
		reason = "第二档连续三次未得到可执行的决定"
	case WebFallback:
		reason = "联网档连续三次未得到可执行的决定"
	}
	if reason != "" {
		for _, item := range pending {
			if item.Attempts < 2 {
				continue
			}
			// 成功执行的决定已经释放领取，只有第三次未处理且证据仍相同的项才放弃本档。
			err := s.abandon(ctx, item, model, conversation, reason, time.Time{})
			var failure *operations.Failure
			if errors.As(err, &failure) && failure.Code == "item_stale" {
				err = s.writer.ReleaseModelItems(ctx, conversation, []string{item.ID}, false, nil)
			}
			if err != nil {
				return err
			}
		}
	}
	if err := s.writer.ReleaseModelItems(ctx, conversation, itemIDs(pending), true, nil); err != nil {
		return err
	}
	return s.writer.ReleaseModelItems(ctx, conversation, itemIDs(items), false, nil)
}

func (s *Scheduler) saveRejections(ctx context.Context, actor, conversation string, items []curation.PendingItem) error {
	rejected, err := s.launcher.RejectedSubmissions(ctx, actor, conversation, items)
	if err != nil {
		return err
	}
	for _, submission := range rejected {
		for _, item := range items {
			if item.ID == submission.ItemID {
				if err := s.writer.RecordRejectedModelTool(ctx, item, conversation, string(taskKind(item)), submission); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
