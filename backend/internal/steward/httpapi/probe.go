package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
)

type probeInput struct {
	// Endpoint 是用户填写的 HTTP(S) 基础地址。
	Endpoint *string `json:"endpoint"`
	// Key 是只写密钥，省略保留，显式 null 清除。
	Key json.RawMessage `json:"key"`
	// Model 是用于探测的对话模型。
	Model *string `json:"model"`
}

func (h *Handlers) probe(w http.ResponseWriter, r *http.Request, user identity.User) {
	var input probeInput
	if !decodeInput(w, r, &input) {
		return
	}
	patch := settingsInput{Endpoint: input.Endpoint, Key: input.Key, Model: input.Model}
	if !validSettingsInput(&patch) {
		invalidInput(w, r)
		return
	}
	result := provider.ProbeResult{Models: []provider.Model{}, ToolSupport: provider.ToolSupportUnconfirmed}
	var failure *problem
	probedAt := time.Now().UTC()
	saved, err := h.store.UpdateUserSettings(r.Context(), user.ID, func(current runtime.UserSettings) (runtime.UserSettingsInput, error) {
		update, err := h.mergeSettings(user.ID, current, patch)
		if err != nil {
			return update, err
		}
		if update.Endpoint == "" {
			failure = &notConfiguredProblem
			return update, runtime.ErrInvalidInput
		}
		key, clearKey, err := parseKey(input.Key)
		if err != nil {
			return update, err
		}
		var plaintext string
		if key != nil {
			plaintext = *key
		} else if !clearKey && !update.ClearKey && current.Key != nil {
			decoded, err := h.secrets.Open(user.ID, modelKeyPurpose, current.Key.Ciphertext)
			if err != nil {
				failure = &notConfiguredProblem
				return update, err
			}
			plaintext = string(decoded)
		}
		client, err := h.outboundClient(r.Context(), user)
		if err != nil {
			return update, err
		}
		defer client.CloseIdleConnections()
		ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
		defer cancel()
		if err := preflight(ctx, client, update.Endpoint); err != nil {
			p := outboundProblem(err)
			failure = &p
			kind := provider.ErrorUnreachable
			if p.code == "egress_denied" {
				kind = provider.ErrorEgressDenied
			}
			if p.code == "provider_timeout" {
				kind = provider.ErrorTimeout
			}
			result.Failures = []provider.ProbeFailure{{Step: "endpoint", Error: &provider.Error{Kind: kind, Message: p.message}}}
			return update, err
		}
		result, err = provider.Probe(ctx, client, update.Endpoint, plaintext, update.Model)
		probedAt = time.Now().UTC()
		if err != nil {
			p := outboundProblem(err)
			failure = &p
			return update, err
		}
		// 更早的协议尝试失败不影响最终成功；明确不支持工具是有效结论，其他工具步骤失败不保存。
		for _, step := range result.Failures {
			if step.Step == "tools" && step.Error.Kind != provider.ErrorToolUnsupported {
				p := providerProblem(step.Error.Kind)
				failure = &p
				return update, step.Error
			}
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return update, err
		}
		update.Probe = &runtime.ProbeResult{Result: raw, ProbedAt: probedAt}
		update.Overrides = json.RawMessage(`{}`)
		update.Protocol = string(result.Protocol)
		return update, nil
	})
	if failure != nil {
		httpserver.WriteJSON(w, failure.status, struct {
			httpserver.APIError
			// ProbeResult 是本次失败的探测报告，未保存到设置。
			ProbeResult provider.ProbeResult `json:"probe_result"`
			// ProbedAt 是本次探测时间。
			ProbedAt time.Time `json:"probed_at"`
		}{httpserver.APIError{Error: httpserver.APIErrorDetail{Code: failure.code, Message: failure.message}, RequestID: w.Header().Get("X-Request-ID")}, result, probedAt})
		return
	}
	if err != nil {
		h.writeSettingsError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, 200, h.settingsView(user.ID, saved))
}
