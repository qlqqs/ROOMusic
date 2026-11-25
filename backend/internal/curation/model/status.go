package model

import (
	"context"
	"errors"
	"slices"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

type schedulingConditions struct {
	curation.ModelProgress
	sources []string
}

// 按待定项目标检查来源。没有注册网络搜索的调度器仍可处理发行项。
func (s *Scheduler) targetSources(ctx context.Context, p *schedulingConditions) error {
	for _, source := range []string{"musicbrainz", "web_search"} {
		err := s.sources.Check(ctx, source)
		if err == nil {
			p.sources = append(p.sources, source)
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
		case "not_found", "source_disabled", "search_unconfigured":
			continue
		default:
			return err
		}
	}
	// 库内核对也受扫描闸门约束，即使两个出站源都关闭。
	if len(p.sources) == 0 {
		running, err := s.sources.ScanRunning(ctx)
		if err != nil {
			return err
		}
		if running {
			p.State = "waiting_for_scan"
		}
	}
	return nil
}

func (p schedulingConditions) allows(kind TaskKind) bool {
	switch kind {
	case SelectCandidate, LookupCandidate:
		return slices.Contains(p.sources, "musicbrainz")
	case WebFallback:
		return slices.Contains(p.sources, "web_search")
	default:
		return true
	}
}

func (s *Scheduler) conditions(ctx context.Context, checkBudget bool) (p schedulingConditions, actor string, err error) {
	settings, err := s.writer.Settings(ctx)
	if err != nil {
		return p, "", err
	}
	p.Model, p.DailyTokenLimit, p.WebDailyTokenLimit = settings.Model, settings.DailyTokenLimit, settings.WebDailyTokenLimit
	now := s.options.Now()
	if checkBudget {
		p.Today, p.WebToday, err = s.writer.DailyUsage(ctx, now)
		p.WebLimitReached = p.WebToday.AtLimit(p.WebDailyTokenLimit)
		if p.WebLimitReached {
			retry := curation.NextBudgetDay(now)
			p.WebRetryAt = &retry
		}
		if err != nil {
			return p, "", err
		}
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
		case checkBudget && p.Today.AtLimit(p.DailyTokenLimit):
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
	conditions, _, err := s.conditions(ctx, true)
	p := conditions.ModelProgress
	if err != nil {
		return p, err
	}
	var waitingReview bool
	err = s.db.QueryRowContext(ctx, `WITH visible AS (`+catalog.VisibleReleaseIDsSQL+`)
 SELECT count(*) FILTER (WHERE status IN ('waiting_model','waiting_route')),count(*) FILTER (WHERE status='in_progress'),
 0,count(*) FILTER (WHERE status='shelved'),
 count(*) FILTER (WHERE status='in_progress' AND next_attempt_at IS NOT NULL)>0
 FROM curation_pending_items p WHERE `+curation.PendingTargetVisibleSQL).Scan(&p.Items.WaitingModel, &p.Items.InProgress, &p.Items.WaitingHuman, &p.Items.Shelved, &waitingReview)
	if p.State == "idle" || p.State == "limit_reached" {
		s.mu.RLock()
		running := s.running
		s.mu.RUnlock()
		switch {
		case running:
			p.State = "running"
		case waitingReview && p.State == "idle":
			p.State = "waiting_review"
		}
	}
	return p, err
}
