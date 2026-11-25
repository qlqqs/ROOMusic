package model

import (
	"context"
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

func (s *Scheduler) nextKind(ctx context.Context, now time.Time, conditions schedulingConditions) (TaskKind, string, error) {
	kinds, err := s.writer.ReadyModelKinds(ctx, now, []string{"release", "artist"})
	if err != nil {
		return "", "", err
	}
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
		ids, err := s.writer.LookupModelTargets(ctx, now)
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
