package model

import (
	"context"
	"time"
)

type TaskKind string

const (
	localQueue      TaskKind = "local"
	LocalCleanup    TaskKind = localQueue
	SelectCandidate TaskKind = "select"
	VerifyEvidence  TaskKind = "verify"
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
