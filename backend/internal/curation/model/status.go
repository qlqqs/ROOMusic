package model

import (
	"context"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

type schedulingConditions struct {
	curation.ModelProgress
	targets []string
}

// 按待定项目标检查来源。没有注册网络搜索的调度器仍可处理发行项。
func (s *Scheduler) targetSources(ctx context.Context, p *schedulingConditions) error {
	for _, source := range []struct{ id, target string }{{"musicbrainz", "release"}, {"web_search", "artist"}} {
		err := s.sources.Check(ctx, source.id)
		if err == nil {
			p.targets = append(p.targets, source.target)
			continue
		}
		var failure *operations.Failure
		if !errors.As(err, &failure) {
			return err
		}
		switch failure.Code {
		case "scan_running":
			p.State = "waiting_for_scan"
			return nil
		case "curation_paused":
			p.State = "paused"
			return nil
		case "curation_disabled":
			p.State = "disabled"
			return nil
		case "not_found", "source_disabled":
			continue
		default:
			return err
		}
	}
	if len(p.targets) == 0 {
		reason := "source_disabled"
		p.State = "unavailable"
		p.UnavailableReason = &reason
	}
	return nil
}

func (s *Scheduler) conditions(ctx context.Context) (p schedulingConditions, actor string, err error) {
	settings, err := s.writer.Settings(ctx)
	if err != nil {
		return p, "", err
	}
	p.Model, p.DailyTokenLimit = settings.Model, settings.DailyTokenLimit
	now := s.options.Now()
	p.Today, err = s.writer.TodayUsage(ctx, now)
	if err != nil {
		return p, "", err
	}
	unavailable := func(reason string) { p.State = "unavailable"; p.UnavailableReason = &reason }
	switch {
	case !settings.Enabled:
		p.State = "disabled"
	case !settings.Active:
		unavailable("enabler_unavailable")
	case settings.Paused:
		p.State = "paused"
	default:
		actor = settings.EnabledBy.ID
		if err = s.targetSources(ctx, &p); err != nil || p.State != "" {
			return p, actor, err
		}
		if s.launcher == nil {
			return p, actor, operations.ErrUnavailable
		}
		model, reason, err := s.launcher.Availability(ctx, actor)
		if err != nil {
			return p, actor, err
		}
		if p.Model == "" {
			p.Model = model
		}
		s.mu.RLock()
		cooling := now.Before(s.providerUntil)
		s.mu.RUnlock()
		switch {
		case reason != "":
			unavailable(reason)
		case p.DailyTokenLimit != nil && p.Today.InputTokens+p.Today.OutputTokens >= *p.DailyTokenLimit:
			p.State = "limit_reached"
		case cooling:
			unavailable("provider_error")
		default:
			p.State = "idle"
		}
	}
	return p, actor, nil
}

func (s *Scheduler) ModelStatus(ctx context.Context) (curation.ModelProgress, error) {
	conditions, _, err := s.conditions(ctx)
	p := conditions.ModelProgress
	if err != nil {
		return p, err
	}
	var waitingReview bool
	err = s.db.QueryRowContext(ctx, `WITH visible AS (`+catalog.VisibleReleaseIDsSQL+`)
 SELECT count(*) FILTER (WHERE status='waiting_model'),count(*) FILTER (WHERE status='in_progress'),
 count(*) FILTER (WHERE status='waiting_human'),count(*) FILTER (WHERE status='shelved'),
 count(*) FILTER (WHERE status='in_progress' AND next_attempt_at IS NOT NULL)>0
 FROM curation_pending_items p WHERE `+curation.PendingTargetVisibleSQL).Scan(&p.Items.WaitingModel, &p.Items.InProgress, &p.Items.WaitingHuman, &p.Items.Shelved, &waitingReview)
	if p.State == "idle" {
		s.mu.RLock()
		running := s.running
		s.mu.RUnlock()
		switch {
		case running:
			p.State = "running"
		case waitingReview:
			p.State = "waiting_review"
		}
	}
	return p, err
}
