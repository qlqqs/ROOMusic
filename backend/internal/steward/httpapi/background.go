package httpapi

import (
	"context"
	"database/sql"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

// BackgroundParams 由调度器提供本次整理批次、首条消息或待重审方案。
type BackgroundParams struct {
	UserID         string
	ConversationID string
	Model          string
	Text           string
	ReviewPlanID   string
	SystemSuffix   string
	BeforeStep     func(context.Context) error
	Usage          func(context.Context, runtime.UsageKind, provider.Usage)
	Escalate       func(context.Context, runtime.PlanItem, runtime.Review, runtime.ReviewItem) (string, error)
}

func (h *Handlers) backgroundConfig(ctx context.Context, userID string) (identity.User, runtime.UserSettings, string, error) {
	user, err := h.guard.EnabledUser(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && user.Role != "admin") {
		return user, runtime.UserSettings{}, "", &ModelUnavailable{Reason: "enabler_unavailable"}
	}
	if err != nil {
		return user, runtime.UserSettings{}, "", err
	}
	settings, key, err := h.resolveModelConfig(ctx, userID)
	return user, settings, key, err
}

// BackgroundAvailability 检查开启者与有效模型配置，不发起模型请求。
func (h *Handlers) BackgroundAvailability(ctx context.Context, userID string) (model, reason string, err error) {
	_, settings, _, err := h.backgroundConfig(ctx, userID)
	var unavailable *ModelUnavailable
	if errors.As(err, &unavailable) {
		return settings.Model, unavailable.Reason, nil
	}
	return settings.Model, "", err
}

// StartBackground 启动独立于管理员登录会话的自动整理运行。
func (h *Handlers) StartBackground(ctx context.Context, p BackgroundParams) (*runtime.Run, error) {
	user, settings, key, err := h.backgroundConfig(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	model, closeClient, err := h.openModel(ctx, user, settings, key)
	if err != nil {
		return nil, err
	}
	if p.Model == "" {
		p.Model = settings.Model
	}
	run, err := h.supervisor.Start(ctx, runtime.RunParams{
		Background: true, ConversationID: p.ConversationID, Text: p.Text, ReviewPlanID: p.ReviewPlanID,
		Caller:   tool.Caller{User: user, Source: tool.SourceCuration, Mode: "steward"},
		Provider: model, Model: p.Model, ReviewModel: settings.ReviewModel, Protocol: provider.Protocol(settings.Protocol),
		Endpoint: settings.Endpoint, DisplayName: settings.DisplayName, SkipMemory: true, SystemSuffix: p.SystemSuffix,
		BeforeStep: p.BeforeStep, Usage: p.Usage, Escalate: p.Escalate, Finished: closeClient,
		Check: func(ctx context.Context) error { return h.checkModelKey(ctx, p.UserID, settings) },
	})
	if err != nil {
		closeClient()
	}
	return run, err
}
