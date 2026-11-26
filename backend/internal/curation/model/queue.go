package model

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type TaskKind string

const (
	PatrolAndLocal  TaskKind = "patrol_local"
	SelectCandidate TaskKind = "select"
	VerifyEvidence  TaskKind = "verify"
	LookupCandidate TaskKind = "lookup"
	WebFallback     TaskKind = "web"
)

// QueueOrder 让各任务提供自己的就绪判据。第二档暂不可启动时继续检查联网任务。
type QueueOrder struct {
	Ready func(context.Context, TaskKind, time.Time) (bool, error)
}

func (q QueueOrder) NextModelKind(ctx context.Context, now time.Time) (TaskKind, error) {
	for _, kind := range []TaskKind{PatrolAndLocal, SelectCandidate, VerifyEvidence, LookupCandidate, WebFallback} {
		ready, err := q.Ready(ctx, kind, now)
		if err != nil {
			return "", err
		}
		if ready {
			return kind, nil
		}
	}
	return "", nil
}

type LookupQueue interface {
	LookupTierReady(context.Context, string, time.Time) (bool, error)
	LookupTierContext(context.Context) context.Context
}

func (s *Scheduler) nextKind(ctx context.Context, now time.Time, conditions schedulingConditions, excluded []string) (TaskKind, string, error) {
	kinds, err := s.writer.ReadyModelKinds(ctx, now, []string{"release", "artist"}, excluded)
	if err != nil {
		return "", "", err
	}
	// 第一档准备缺失明细时可能等待来源；其余名额可继续领取无需该来源的任务。
	s.mu.RLock()
	if s.preparingSelection {
		delete(kinds, string(SelectCandidate))
	}
	s.mu.RUnlock()
	var releaseID string
	order := QueueOrder{Ready: func(ctx context.Context, kind TaskKind, now time.Time) (bool, error) {
		if !kinds[string(kind)] || !conditions.allows(kind) || (kind == WebFallback && conditions.WebLimitReached) {
			return false, nil
		}
		if kind != LookupCandidate {
			return true, nil
		}
		if s.options.Lookup == nil {
			return false, nil
		}
		ids, err := s.writer.LookupModelTargets(ctx, now, excluded)
		if err != nil {
			return false, err
		}
		for _, id := range ids {
			ready, err := s.options.Lookup.LookupTierReady(ctx, id, now)
			if err != nil {
				return false, err
			}
			if ready {
				releaseID = id
				return true, nil
			}
		}
		return false, nil
	}}
	kind, err := order.NextModelKind(ctx, now)
	return kind, releaseID, err
}

// 到期的重审优先；只要包内有一项目标已被占用，就继续寻找其他会话。
func (s *Scheduler) nextWork(ctx context.Context, actor string, progress schedulingConditions, now time.Time, excluded []string) (*modelWork, error) {
	w := &modelWork{actor: actor, model: progress.Model}
	err := s.db.QueryRowContext(ctx, `SELECT conversation_id,review_retries FROM curation_pending_items p
 WHERE status='in_progress' AND next_attempt_at<=$1 AND $3
 AND (NOT $4 OR (p.route IS DISTINCT FROM 'web' AND p.kind<>'artist_identity')) AND NOT EXISTS (
 SELECT FROM curation_pending_items q WHERE q.conversation_id=p.conversation_id AND q.id=ANY($2::uuid[]))
 ORDER BY next_attempt_at,created_at,id LIMIT 1`, now, excluded, progress.State == "idle", progress.WebLimitReached).Scan(&w.conversation, &w.retries)
	if err == nil {
		w.plan, err = s.launcher.PendingPlan(ctx, actor, w.conversation)
		if err != nil {
			return nil, err
		}
		if w.plan == "" {
			return nil, s.release(ctx, w.conversation, false, nil)
		}
		w.items, err = s.writer.ClaimedItems(ctx, w.conversation)
		if err != nil {
			return nil, err
		}
		if len(w.items) == 0 {
			return nil, s.release(ctx, w.conversation, false, nil)
		}
		return w, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if !progress.allows(WebFallback) {
		if err := s.finishDisabledWeb(ctx, actor, progress.Model, now, excluded); err != nil {
			return nil, err
		}
	}
	if progress.State == "limit_reached" {
		return nil, nil
	}
	kind, releaseID, err := s.nextKind(ctx, now, progress, excluded)
	if err != nil || kind == "" {
		return nil, err
	}
	if kind == SelectCandidate {
		w.items, err = s.writer.SelectionItems(ctx, now, s.options.BatchSize, excluded)
		if err != nil || len(w.items) == 0 {
			return nil, err
		}
		return w, nil
	}
	w.conversation, err = s.launcher.CreateConversation(ctx, actor, "自动整理 "+now.Format("01-02 15:04"), progress.Model)
	if err != nil {
		return nil, err
	}
	batchSize := s.options.BatchSize
	if kind == VerifyEvidence {
		// 核对逐项调用，互不相关的判断可占用不同的并发名额。
		batchSize = 1
	}
	w.items, err = s.writer.ClaimModelKind(ctx, w.conversation, now, batchSize, []string{"release", "artist"}, string(kind), releaseID, excluded...)
	if err != nil || len(w.items) == 0 {
		return nil, errors.Join(err, s.launcher.DeleteConversation(ctx, actor, w.conversation))
	}
	return w, nil
}
