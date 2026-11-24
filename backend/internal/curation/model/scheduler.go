package model

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
)

var ErrStopped = errors.New("后台整理条件已变更")

type Options struct {
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
	progress, actor, err := s.conditions(ctx)
	if err != nil || progress.State != "idle" {
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
		var due bool
		err = s.db.QueryRowContext(ctx, `WITH visible AS (`+catalog.VisibleReleaseIDsSQL+`) SELECT EXISTS(
 SELECT 1 FROM curation_pending_items p WHERE `+curation.PendingTargetVisibleSQL+`
 AND p.status='waiting_model' AND p.target_type=ANY($2::text[]) AND (p.next_attempt_at IS NULL OR p.next_attempt_at<=$1))`, now, progress.targets).Scan(&due)
		if err != nil || !due {
			return err
		}
		conversation, err = s.launcher.CreateConversation(ctx, actor, "自动整理 "+now.Format("01-02 15:04"), progress.Model)
		if err != nil {
			return err
		}
		items, err = s.writer.ClaimPendingItems(ctx, conversation, now, s.options.BatchSize, progress.targets...)
		if err != nil || len(items) == 0 {
			return errors.Join(err, s.launcher.DeleteConversation(ctx, actor, conversation))
		}
	}
	if err != nil {
		return err
	}
	if !batchSourcesReady(items, progress.targets) {
		return s.release(ctx, conversation, false, nil)
	}
	return s.runBatch(ctx, actor, progress.Model, conversation, plan, items, retries)
}

func firstMessage(items []curation.PendingItem) string {
	var msg strings.Builder
	msg.WriteString("请先调用 read_pending_items 读取本批证据与候选，再逐项核对。\n")
	for _, item := range items {
		fmt.Fprintf(&msg, "item_id=%s kind=%s conflicts=%s\n", item.ID, item.Kind, item.Conflicts)
	}
	return msg.String()
}

func batchSourcesReady(items []curation.PendingItem, targets []string) bool {
	for _, p := range items {
		if !slices.Contains(targets, p.TargetType) {
			return false
		}
	}
	return true
}

func (s *Scheduler) checkRunConditions(ctx context.Context, actor string, items []curation.PendingItem) error {
	progress, enabler, err := s.conditions(ctx)
	if err != nil {
		return err
	}
	if enabler != actor || progress.State != "idle" || !batchSourcesReady(items, progress.targets) {
		return ErrStopped
	}
	return nil
}

func (s *Scheduler) runBatch(ctx context.Context, actor, model, conversation, plan string, items []curation.PendingItem, retries int) error {
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.running = false; s.mu.Unlock() }()
	var batch string
	var batchMu sync.Mutex
	params := RunParams{ReviewPlanID: plan, Run: curation.ModelRun{ActorID: actor, Model: model, ConversationID: conversation, Batch: func(ctx context.Context) (string, error) {
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
		params.Text = firstMessage(items)
	}
	var usageErr error
	params.BeforeStep = func(ctx context.Context) error {
		if usageErr != nil {
			return usageErr
		}
		return s.checkRunConditions(ctx, actor, items)
	}
	params.Usage = func(ctx context.Context, in, out int64) {
		if err := s.writer.AddModelUsage(ctx, s.options.Now(), in, out); err != nil {
			usageErr = err
			s.logger.ErrorContext(ctx, "记录后台模型用量失败", "conversation_id", conversation, "error", err)
		}
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
			var matches bool
			switch e.Tool {
			case "resolve_artist_identity":
				matches = p.Kind == "artist_identity" && strings.EqualFold(target.ItemID, p.ID)
			case "resolve_release_match", "merge_releases":
				matches = p.TargetType == "release" && strings.EqualFold(target.ItemID, p.ID)
			case "correct_release", "correct_track":
				matches = p.TargetType == "release" && strings.EqualFold(target.ReleaseID, p.TargetID)
			}
			if matches {
				e.PendingItemID, e.ConversationID, e.Provider = p.ID, &conversation, model
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
	s.recordEnd(reason, now)
	// 收尾遇到暂时的数据库故障时，先完成本会话，再领取新项。
	itemsFinished := false
	for {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		var finishErr error
		if !itemsFinished {
			finishErr = s.finish(cleanup, actor, conversation, reason, retries, now, items)
			itemsFinished = finishErr == nil
		}
		if itemsFinished && batch != "" {
			finishErr = s.writer.CloseBatch(cleanup, batch)
		}
		cancel()
		if finishErr == nil || ctx.Err() != nil {
			return errors.Join(err, usageErr, finishErr)
		}
		s.logger.ErrorContext(ctx, "后台整理收尾失败", "conversation_id", conversation, "error", finishErr)
		select {
		case <-ctx.Done():
			return errors.Join(err, usageErr, finishErr)
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

func (s *Scheduler) finish(ctx context.Context, actor, id, reason string, retries int, now time.Time, items []curation.PendingItem) error {
	switch reason {
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
			return s.writer.ReleaseClaimed(ctx, id, true, nil)
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
