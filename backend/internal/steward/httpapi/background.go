package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/secret"
	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func (h *Handlers) OnModelSettingsChanged(wake func()) { h.modelWake = wake }

// BackgroundParams 由调度器提供本次整理批次、首条消息或待重审方案。
type BackgroundParams struct {
	RequestBudget int64
	Verified      bool

	ClaimDigests     map[string]string
	UserID           string
	ItemID           string
	ConversationID   string
	Model            string
	Text             string
	SystemSuffix     string
	Selection        func(string) ([]provider.ToolCall, error)
	StepLimit        int
	WebSearchEnabled func(context.Context) bool
	BeforeRequest    func(context.Context) error
	BeforeStep       func(context.Context) error
	Usage            func(context.Context, runtime.UsageKind, provider.Usage)
	Escalate         func(context.Context, runtime.PlanItem, runtime.Review, runtime.ReviewItem) (string, error)
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

// BackgroundModelIdentity 的两个摘要只在后台比较，不对外公开。
func (h *Handlers) BackgroundModelIdentity(ctx context.Context, actor, override string) (model, configKey, modelKey, reason string, err error) {
	raw, err := h.store.GetUserSettings(ctx, actor)
	if err != nil {
		return "", "", "", "", err
	}
	settings, _, err := backgroundModelConfig(raw, actor, h.secrets, override, false)
	var unavailable *ModelUnavailable
	if errors.As(err, &unavailable) {
		reason = unavailable.Reason
	} else if err != nil {
		return "", "", "", "", err
	}
	hash := func(v any) string { data, _ := json.Marshal(v); return fmt.Sprintf("%x", sha256.Sum256(data)) }
	modelKey = hash([]string{settings.Endpoint, settings.Protocol, settings.Model})
	var cipher []byte
	if raw.Key != nil {
		cipher = raw.Key.Ciphertext
	}
	var overrides manualOverrides
	if len(raw.Overrides) > 0 {
		if err = json.Unmarshal(raw.Overrides, &overrides); err != nil {
			return "", "", "", "", err
		}
	}
	_, support, err := effectiveSettings(raw)
	configKey = hash([]any{actor, modelKey, cipher, support, overrides.ToolSupport})
	if err != nil {
		return "", "", "", "", err
	}
	if reason == "tool_unsupported" && overrides.ToolSupport != nil {
		reason = "tool_unsupported_override"
	}
	return settings.Model, configKey, modelKey, reason, nil
}

func (h *Handlers) curationConfig(ctx context.Context, actor, override string, verified bool) (identity.User, runtime.UserSettings, string, error) {
	user, err := h.guard.EnabledUser(ctx, actor)
	if errors.Is(err, sql.ErrNoRows) || err == nil && user.Role != "admin" {
		return user, runtime.UserSettings{}, "", &ModelUnavailable{Reason: "enabler_unavailable"}
	}
	if err != nil {
		return user, runtime.UserSettings{}, "", err
	}
	raw, err := h.store.GetUserSettings(ctx, actor)
	if err != nil {
		return user, raw, "", err
	}
	settings, key, err := backgroundModelConfig(raw, actor, h.secrets, override, verified)
	return user, settings, key, err
}

// 后台在建连前的预检也使用相同的配置、超时及连接失败分类。
func backgroundOutboundFailure(err error) error {
	var outbound *settingsOutboundError
	if !errors.As(err, &outbound) {
		return err
	}
	var failure *provider.Error
	if errors.As(outbound.error, &failure) {
		return failure
	}
	problem := outboundProblem(outbound.error)
	kind := provider.ErrorUnreachable
	switch problem.code {
	case "egress_denied":
		kind = provider.ErrorEgressDenied
	case "provider_timeout":
		kind = provider.ErrorTimeout
	}
	return &provider.Error{Kind: kind, Code: problem.code, Message: problem.message}
}

func (h *Handlers) ProbeCurationModel(ctx context.Context, actor, model string, tools bool) (provider.Usage, error) {
	user, settings, key, err := h.curationConfig(ctx, actor, model, true)
	if err != nil {
		return provider.Usage{}, err
	}
	p, closeClient, err := h.openModel(ctx, user, settings, key)
	if err != nil {
		return provider.Usage{}, backgroundOutboundFailure(err)
	}
	defer closeClient()
	support, u, err := provider.ProbeModel(provider.SingleAttempt(ctx), p, settings.Model, tools)
	if err == nil && support != provider.ToolSupportSupported {
		err = &provider.Error{Class: "empty_reply", Message: "模型没有给出预期的测试回复"}
	}
	return u, err
}

func (h *Handlers) EstimateCuration(ctx context.Context, r *runtime.Runner, p BackgroundParams) (int64, error) {
	user, settings, _, err := h.curationConfig(ctx, p.UserID, p.Model, p.Verified)
	if err != nil {
		return 0, err
	}
	return r.EstimateBackground(ctx, runtime.RunParams{Caller: tool.Caller{User: user, Source: tool.SourceCuration, Mode: "steward"},
		Model: settings.Model, Protocol: provider.Protocol(settings.Protocol), Endpoint: settings.Endpoint, DisplayName: settings.DisplayName,
		Selection: p.Selection, SystemSuffix: p.SystemSuffix, RequestBudget: p.RequestBudget, Text: p.Text})
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

// BackgroundSearchReadiness 与运行入口使用相同的配置、密钥和搜索能力判定。
func BackgroundSearchReadiness(settings runtime.UserSettings, userID string, secrets *secret.Store) (modelReady, searchReady bool) {
	_, _, err := modelConfig(settings, userID, secrets)
	switch settings.SearchSource {
	case "searxng":
		searchReady = settings.SearXNGURL != ""
	case "tavily":
		if settings.SearchKey != nil {
			_, keyErr := secrets.Open(userID, secret.SearchKeyPurpose, settings.SearchKey.Ciphertext)
			searchReady = keyErr == nil
		}
	case "native":
		searchReady = nativeSearchEnabled(settings)
	}
	return err == nil, searchReady
}

func nativeSearchEnabled(settings runtime.UserSettings) bool {
	return settings.SearchSource == "native" && effectiveNativeSearch(settings, settingsUpdate(settings)) == provider.NativeSearchSupported
}

// StartBackground 启动独立于管理员登录会话的自动整理运行。
func (h *Handlers) StartBackground(ctx context.Context, p BackgroundParams) (*runtime.Run, error) {
	user, settings, key, err := h.curationConfig(ctx, p.UserID, p.Model, p.Verified)
	if err != nil {
		return nil, err
	}
	model, closeClient, err := h.openModel(ctx, user, settings, key)
	if err != nil {
		return nil, backgroundOutboundFailure(err)
	}
	if p.Model == "" {
		p.Model = settings.Model
	}
	run, err := h.supervisor.Start(ctx, runtime.RunParams{
		RequestBudget: p.RequestBudget, Background: true, BackgroundItemID: p.ItemID, BackgroundClaims: p.ClaimDigests, Selection: p.Selection, StepLimit: p.StepLimit, ConversationID: p.ConversationID, Text: p.Text,
		Caller:   tool.Caller{User: user, Source: tool.SourceCuration, Mode: "steward"},
		Provider: model, Model: p.Model, Protocol: provider.Protocol(settings.Protocol),
		Endpoint: settings.Endpoint, DisplayName: settings.DisplayName, SkipMemory: true, SystemSuffix: p.SystemSuffix,
		BeforeRequest: p.BeforeRequest, BeforeStep: p.BeforeStep, Usage: p.Usage, Finished: closeClient,
		Check: func(ctx context.Context) error { return h.checkModelKey(ctx, p.UserID, settings) },
	})
	if err != nil {
		closeClient()
	}
	return run, err
}
