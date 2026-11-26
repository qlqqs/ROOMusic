package model

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

type schedulingConditions struct {
	curation.ModelProgress
	sources  []string
	config   Configuration
	verified bool
	budget   int64
}

// 按待定项目标检查来源。没有注册网络搜索的调度器仍可处理发行项。
func (s *Scheduler) targetSources(ctx context.Context, p *schedulingConditions) error {
	for _, source := range []string{"musicbrainz"} {
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
	case SelectCandidate:
		return slices.Contains(p.sources, "musicbrainz")
	default:
		return true
	}
}

func (s *Scheduler) conditions(ctx context.Context, checkBudget bool) (p schedulingConditions, actor string, err error) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	settings, err := s.writer.Settings(ctx)
	if err != nil {
		return p, "", err
	}
	p.Model, p.DailyTokenLimit = settings.Model, settings.DailyTokenLimit
	p.Concurrency = settings.ModelConcurrency
	now := s.options.Now()
	if checkBudget {
		p.Today, err = s.writer.TodayUsage(ctx, now)
		if err != nil {
			return p, "", err
		}
	}
	p.budget = settings.MaxRequestTokens
	p.ContextHint = "使用本机模型时，请按模型服务实际加载的上下文长度设置单次最多发送。"
	if settings.EnabledBy == nil || settings.Enabled && !settings.Active || s.launcher == nil {
		reason := "not_configured"
		p.UnavailableReason = &reason
		p.State = "disabled"
		if settings.Enabled {
			reason = "enabler_unavailable"
			p.State = "unavailable"
		}
		return p, "", nil
	}
	actor = settings.EnabledBy.ID
	p.config, err = s.launcher.Availability(ctx, actor, settings.Model)
	if err != nil {
		return p, actor, err
	}
	p.Model = p.config.Model
	state, err := s.writer.SyncModelConfig(ctx, p.config.Key, p.config.ModelKey)
	if err != nil {
		return p, actor, err
	}
	p.verified = state.Verified
	reason := p.config.Reason
	if reason == "tool_unsupported" && state.Verified {
		reason = ""
	}
	p.Configured = reason != "not_configured"
	if reason != "" {
		p.UnavailableReason = &reason
		p.State = "unavailable"
		if p.Configured {
			kind := "configuration"
			p.PauseKind = &kind
			detail := "模型不支持工具调用"
			if reason == "key_needs_reentry" {
				detail = "模型密钥需要重新输入"
			}
			p.ReasonDetail = &detail
			p.CanRetry = reason == "tool_unsupported"
			if state.Pause != nil {
				p.PauseKind, p.ReasonDetail, p.RetryAt = state.Pause, state.Detail, state.RetryAt
			}
		}
	} else {
		s.mu.RLock()
		cooling := now.Before(s.providerUntil)
		s.mu.RUnlock()
		p.State = "idle"
		p.CanRetry = true
		p.PauseKind, p.ReasonDetail, p.RetryAt = state.Pause, state.Detail, state.RetryAt
		if state.Pause != nil && *state.Pause == "temporary" && state.RetryAt != nil && !state.RetryAt.After(now) {
			p.PauseKind, p.ReasonDetail, p.RetryAt = nil, nil, nil
		}
		if p.PauseKind != nil && checkBudget {
			p.State = "unavailable"
			r := *p.PauseKind
			if r == "temporary" {
				r = "provider_error"
				p.CanRetry = false
			}
			p.UnavailableReason = &r
		} else if checkBudget && cooling {
			p.State = "unavailable"
			r := "provider_error"
			p.UnavailableReason = &r
		}
	}
	if checkBudget && p.Today.AtLimit(p.DailyTokenLimit) {
		p.State = "limit_reached"
		kind := "daily_limit"
		p.PauseKind = &kind
		retry := curation.NextBudgetDay(now)
		p.RetryAt = &retry
		p.CanRetry = false
	}
	if !settings.Enabled {
		p.State = "disabled"
		p.CanRetry = false
	} else if settings.Paused {
		p.State = "paused"
		p.CanRetry = false
	} else if p.State == "idle" || p.State == "limit_reached" {
		if err = s.targetSources(ctx, &p); err != nil {
			return p, actor, err
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
	err = s.db.QueryRowContext(ctx, `WITH visible AS (`+catalog.VisibleReleaseIDsSQL+`)
 SELECT count(*) FILTER (WHERE status IN ('waiting_model','waiting_route')),count(*) FILTER (WHERE status='in_progress'),
 0,count(*) FILTER (WHERE status='shelved')
 FROM curation_pending_items p WHERE `+curation.PendingTargetVisibleSQL).Scan(&p.Items.WaitingModel, &p.Items.InProgress, &p.Items.WaitingHuman, &p.Items.Shelved)
	s.mu.RLock()
	p.Running = s.running
	if s.probing {
		p.Running++
	}
	p.CanRetry = p.CanRetry && s.slotsUsed() < p.Concurrency
	s.mu.RUnlock()
	if p.State == "idle" && p.Running > 0 {
		p.State = "running"
	}
	return p, err
}
func (s *Scheduler) RetryModel(ctx context.Context) (curation.ModelProgress, error) {
	p, actor, err := s.conditions(ctx, true)
	if err != nil {
		return p.ModelProgress, err
	}
	if p.CanRetry {
		if err = s.probe(ctx, p, actor, true); err != nil {
			return p.ModelProgress, err
		}
	}
	return s.ModelStatus(ctx)
}

func (s *Scheduler) probe(ctx context.Context, p schedulingConditions, actor string, manual bool) error {
	s.mu.Lock()
	if s.probing || s.slotsUsed() >= p.Concurrency {
		s.mu.Unlock()
		return nil
	}
	s.probing = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.probing = false; s.mu.Unlock(); s.Wake() }()
	now := s.options.Now()
	if !manual {
		ok, err := s.writer.StartQuotaProbe(ctx, p.config.Key, now)
		if err != nil || !ok {
			return err
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	in, out, f, err := s.launcher.Probe(probeCtx, actor, p.Model, manual)
	if in+out > 0 {
		if usageErr := s.writer.AddModelUsage(ctx, now, in, out); usageErr != nil {
			return usageErr
		}
	}
	if err != nil {
		return err
	}
	if f != nil {
		hardPause := p.PauseKind != nil && (*p.PauseKind == "configuration" || *p.PauseKind == "empty_replies")
		if f.Class == "empty_reply" || f.Class == "request_rejected" || manual && hardPause && f.Class != "configuration" {
			return s.writer.RecordProbeDetail(ctx, p.config.Key, f.Message)
		}
		return s.writer.SetModelFailure(ctx, p.config.Key, *f, now)
	}
	return s.writer.ModelProbeSucceeded(ctx, p.config.Key, manual)
}
