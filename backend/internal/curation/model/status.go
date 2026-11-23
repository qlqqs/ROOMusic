package model

import (
	"context"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

func (s *Scheduler) conditions(ctx context.Context) (p curation.ModelProgress, actor string, err error) {
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
		if err = s.sources.Check(ctx, "musicbrainz"); err != nil {
			var f *operations.Failure
			if !errors.As(err, &f) {
				return p, actor, err
			}
			switch f.Code {
			case "scan_running":
				p.State = "waiting_for_scan"
			case "curation_paused":
				p.State = "paused"
			case "curation_disabled":
				p.State = "disabled"
			case "source_disabled":
				unavailable("source_disabled")
			default:
				return p, actor, err
			}
			return p, actor, nil
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
	p, _, err := s.conditions(ctx)
	if err != nil {
		return p, err
	}
	var waitingReview bool
	err = s.db.QueryRowContext(ctx, `WITH visible AS (`+catalog.VisibleReleaseIDsSQL+`)
 SELECT count(*) FILTER (WHERE status='waiting_model'),count(*) FILTER (WHERE status='in_progress'),
 count(*) FILTER (WHERE status='waiting_human'),count(*) FILTER (WHERE status='shelved'),
 count(*) FILTER (WHERE status='in_progress' AND next_attempt_at IS NOT NULL)>0
 FROM curation_pending_items p JOIN visible v ON v.id=p.target_id`).Scan(&p.Items.WaitingModel, &p.Items.InProgress, &p.Items.WaitingHuman, &p.Items.Shelved, &waitingReview)
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
