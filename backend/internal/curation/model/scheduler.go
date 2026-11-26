package model

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/sources"
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
	ResolvePlans(ctx context.Context, conversation string) error
	Start(context.Context, RunParams) (Run, error)
}

type Run interface {
	Wait(context.Context) (string, error)
}

type RunParams struct {
	Items              []curation.PendingItem
	OnToolCalls        func(context.Context, int) error
	Run                curation.ModelRun
	Text, ReviewPlanID string
	BeforeStep         func(context.Context) error
	Usage              func(context.Context, int64, int64)
	Escalate           func(context.Context, curation.ModelException) (string, error)
}

type Scheduler struct {
	db              *sql.DB
	writer          *curation.Writer
	sources         *curation.Sources
	launcher        Launcher
	options         Options
	logger          *slog.Logger
	mu              sync.RWMutex
	running         bool
	providerUntil   time.Time
	providerBackoff time.Duration
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
	return &Scheduler{db: db, writer: writer, sources: sources, launcher: launcher, options: options, logger: logger}
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

func (s *Scheduler) Run(ctx context.Context) {
	recovered := false
	for ctx.Err() == nil {
		var err error
		if !recovered {
			err = s.recover(ctx)
			recovered = err == nil
		}
		if recovered {
			err = s.poll(ctx)
		}
		if err != nil && ctx.Err() == nil {
			s.logger.ErrorContext(ctx, "后台整理调度失败", "event", "curation.model.failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.options.PollInterval):
		}
	}
}

func (s *Scheduler) poll(ctx context.Context) error {
	progress, actor, err := s.conditions(ctx, true)
	if err != nil || (progress.State != "idle" && progress.State != "limit_reached") {
		return err
	}
	now := s.options.Now()
	var conversation string
	var retries int
	err = s.db.QueryRowContext(ctx, `SELECT conversation_id,review_retries FROM curation_pending_items
 WHERE status='in_progress' AND next_attempt_at<=$1 ORDER BY next_attempt_at,created_at,id LIMIT 1`, now).Scan(&conversation, &retries)
	var items []curation.PendingItem
	var plan string
	switch {
	case err == nil:
		plan, err = s.launcher.PendingPlan(ctx, actor, conversation)
		if err != nil {
			return err
		}
		if plan == "" {
			return s.release(ctx, conversation, false, nil)
		}
		items, err = s.writer.ClaimedItems(ctx, conversation)
	case errors.Is(err, sql.ErrNoRows):
		if !progress.allows(WebFallback) {
			if err := s.finishDisabledWeb(ctx, actor, progress.Model, now); err != nil {
				return err
			}
		}
		var checkpoint curation.ModelItemCheckpoint
		checkpoint, err = s.writer.BeforeModelItem(ctx, actor, now)
		if err != nil || checkpoint.Reason != "" {
			return err
		}
		if err = s.writer.MaterializePatrol(ctx, program.PatrolPromptVersion); err != nil {
			return err
		}
		if err = s.writer.MaterializeVerificationItems(ctx); err != nil {
			return err
		}
		var kind TaskKind
		var releaseID string
		kind, releaseID, err = s.nextKind(ctx, now, progress)
		if err != nil || kind == "" {
			return err
		}
		if kind == SelectCandidate {
			items, err = s.prepareSelection(ctx, now)
			if err != nil || len(items) == 0 {
				return err
			}
		}
		conversation, err = s.launcher.CreateConversation(ctx, actor, "自动整理 "+now.Format("01-02 15:04"), progress.Model)
		if err != nil {
			return err
		}
		if kind == SelectCandidate {
			items, err = s.writer.ClaimPreparedSelection(ctx, conversation, now, items)
		} else {
			items, err = s.writer.ClaimModelKind(ctx, conversation, now, s.options.BatchSize, []string{"release", "artist"}, string(kind), releaseID)
		}
		if err != nil || len(items) == 0 {
			return errors.Join(err, s.launcher.DeleteConversation(ctx, actor, conversation))
		}
	}
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return s.release(ctx, conversation, false, nil)
	}
	kind := taskKind(items[0])
	if !progress.allows(kind) {
		return s.release(ctx, conversation, false, nil)
	}
	if plan == "" && kind == SelectCandidate {
		ready := items[:0]
		for _, p := range items {
			if string(p.Candidates) == "[]" {
				if err := s.writer.AdvanceModelItem(ctx, p, conversation, "lookup"); err != nil {
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
		if plan != "" {
			n = len(items)
		} else if kind == SelectCandidate {
			n, err = selectionBatchSize(items)
			if err != nil {
				return errors.Join(err, s.release(ctx, conversation, false, nil))
			}
			if n == 0 {
				if err := s.writer.AdvanceModelItem(ctx, items[0], conversation, "lookup"); err != nil {
					return errors.Join(err, s.release(ctx, conversation, false, nil))
				}
				items = items[1:]
				continue
			}
		}
		active, rest := items[:n], items[n:]
		if plan == "" {
			if err := s.itemCheckpoint(ctx, actor, active); err != nil {
				released := s.writer.ReleaseModelItems(ctx, conversation, itemIDs(items), false, nil)
				if errors.Is(err, ErrStopped) {
					return released
				}
				return errors.Join(err, released)
			}
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
			runCtx = s.options.Lookup.LookupTierContext(ctx)
		}
		reason, err := s.runBatch(runCtx, actor, progress.Model, conversation, plan, active, retries)
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

func (s *Scheduler) prepareSelection(ctx context.Context, now time.Time) ([]curation.PendingItem, error) {
	items, err := s.writer.SelectionItems(ctx, now, s.options.BatchSize)
	if err != nil {
		return nil, err
	}
	if s.options.Lookup != nil {
		ctx = s.options.Lookup.LookupTierContext(ctx)
	}
	matcher := program.NewModelMatcher(s.db, s.writer, s.sources)
	for i := range items {
		if err := matcher.PrepareSelection(ctx, items[i:i+1]); err != nil {
			if !curation.IsSourceUnavailable(err) {
				return nil, err
			}
			// 队列来源给出恢复时刻，普通后台读取失败会缓存十分钟。
			next := s.options.Now().Add(10 * time.Minute)
			var pause *sources.QueuePause
			if errors.As(err, &pause) {
				next = pause.RetryAt
			}
			return nil, s.writer.DeferSelection(ctx, items[i], next)
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
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.running = false; s.mu.Unlock() }()
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
			return "internal_error", errors.Join(err, s.release(ctx, conversation, false, nil))
		}
	}
	if len(items) == 1 {
		params.Run.PendingItemID = items[0].ID
	}
	params.Run.Abandon = func(ctx context.Context, id, reason string) error {
		for _, p := range items {
			if id == p.ID {
				if err := s.checkRunConditions(ctx, actor, items); err != nil {
					return err
				}
				return s.abandon(ctx, p, model, conversation, reason)
			}
		}
		return ErrStopped
	}
	var usageErr error
	trackedID := ""
	if kind == LookupCandidate || kind == WebFallback {
		trackedID = items[0].ID
	}
	calls, tokens := items[0].ItemToolCalls, items[0].ItemTokens
	startingItem := plan == ""
	params.BeforeStep = func(ctx context.Context) error {
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
		if itemLimit(kind, calls, tokens) {
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
	r, err := s.launcher.Start(ctx, params)
	reason := "internal_error"
	// 运行由应用生命周期取消；等待其真正结束后才释放待定项和批次。
	if err == nil {
		reason, err = r.Wait(context.WithoutCancel(ctx))
	}
	now := s.options.Now()
	if itemLimit(kind, calls, tokens) && reason != "provider_error" && reason != "cancelled" && reason != "review_unavailable" {
		reason = "item_limit"
	}
	s.recordEnd(reason, now)
	// 收尾遇到暂时的数据库故障时，先完成本会话，再领取新项。
	itemsFinished := false
	for {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		var finishErr error
		if !itemsFinished {
			finishErr = s.finish(cleanup, actor, model, conversation, reason, retries, now, items)
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

func (s *Scheduler) recordEnd(reason string, now time.Time) {
	s.mu.Lock()
	if reason == "provider_error" {
		s.providerBackoff = min(6*time.Hour, max(10*time.Minute, s.providerBackoff*2))
		s.providerUntil = now.Add(s.providerBackoff)
	} else {
		s.providerBackoff = 0
		s.providerUntil = time.Time{}
	}
	s.mu.Unlock()
}

func (s *Scheduler) finish(ctx context.Context, actor, model, id, reason string, retries int, now time.Time, items []curation.PendingItem) error {
	switch reason {
	case "item_limit":
		if err := s.checkRunConditions(ctx, actor, items); err != nil {
			if errors.Is(err, ErrStopped) {
				return s.release(ctx, id, false, nil)
			}
			return err
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
		err = s.abandon(curation.WithModelRun(ctx, &run), p, model, id, "item_limit")
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
			return s.writer.ReleaseModelItems(ctx, id, itemIDs(items), true, nil)
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
