package model

import (
	"context"
	"errors"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

type TaskKind string

const (
	localQueue      TaskKind = "local"
	LocalCleanup    TaskKind = localQueue
	SelectCandidate TaskKind = "select"
	VerifyEvidence  TaskKind = "verify"
	ArtistWeb       TaskKind = "artist_web"
)

// QueueOrder 让各任务提供自己的就绪判据。按本地辅助、识别、核对依次取项。
type QueueOrder struct {
	Ready func(context.Context, TaskKind, time.Time) (bool, error)
}

func (q QueueOrder) NextModelKind(ctx context.Context, now time.Time) (TaskKind, error) {
	for _, kind := range []TaskKind{localQueue, SelectCandidate, VerifyEvidence} {
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
	PreparationContext(context.Context) context.Context
}

func (s *Scheduler) nextKind(ctx context.Context, now time.Time, conditions schedulingConditions, excluded []string) (TaskKind, string, error) {
	kinds, err := s.writer.ReadyModelKinds(ctx, now, []string{"release", "artist"}, excluded)
	if err != nil {
		return "", "", err
	}
	order := QueueOrder{Ready: func(_ context.Context, kind TaskKind, _ time.Time) (bool, error) {
		return kinds[string(kind)] && conditions.allows(kind), nil
	}}
	kind, err := order.NextModelKind(ctx, now)
	return kind, "", err
}

func (s *Scheduler) nextWork(ctx context.Context, actor string, progress schedulingConditions, now time.Time, excluded []string) (*modelWork, error) {
	if curation.ArtistWebActor(ctx) != "" {
		w := &modelWork{actor: actor, model: progress.Model, config: progress.config.Key, budget: progress.budget, verified: progress.verified}
		if progress.State != "idle" || !progress.allows(ArtistWeb) {
			return nil, nil
		}
		ready, err := s.writer.PeekModelKind(ctx, now, 1, string(ArtistWeb), excluded)
		if err != nil || len(ready) == 0 {
			return nil, err
		}
		if err = curation.CheckArtistWebInput(ctx, s.db, ready[0]); err != nil {
			var f *operations.Failure
			if !errors.As(err, &f) || f.Code != "item_stale" {
				return nil, err
			}
			_, err = s.db.ExecContext(ctx, `UPDATE curation_pending_items SET status='resolved',resolution='input_changed',resolved_at=now(),next_attempt_at=NULL,updated_at=now()
        WHERE id=$1 AND updated_at=$2 AND status IN ('waiting_model','waiting_route')`, ready[0].ID, ready[0].UpdatedAt)
			if err == nil {
				s.Wake()
			}
			return nil, err
		}
		point, err := s.writer.BeforeModelKind(ctx, actor, now, string(ArtistWeb))
		if err != nil || point.Reason != "" {
			return nil, err
		}
		w.conversation, err = s.launcher.CreateConversation(ctx, actor, "整理艺人", progress.Model)
		if err != nil {
			return nil, err
		}
		w.items, err = s.writer.ClaimPreparedKind(ctx, w.conversation, now, string(ArtistWeb), ready)
		if err != nil || len(w.items) == 0 {
			return nil, errors.Join(err, s.launcher.DeleteConversation(ctx, actor, w.conversation))
		}
		return w, nil
	}
	if progress.State == "limit_reached" {
		return nil, nil
	}
	kind, _, err := s.nextKind(ctx, now, progress, excluded)
	if err != nil || kind == "" {
		return nil, err
	}
	w := &modelWork{actor: actor, model: progress.Model, config: progress.config.Key, budget: progress.budget, verified: progress.verified, queueKind: string(kind)}
	w.items, err = s.writer.PeekModelKind(ctx, now, s.options.BatchSize, string(kind), excluded)
	if err != nil || len(w.items) == 0 {
		return nil, err
	}
	return w, nil
}
