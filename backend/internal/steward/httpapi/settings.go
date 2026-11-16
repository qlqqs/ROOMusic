package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
	"github.com/qlqq/roomusic/backend/internal/steward/secret"
)

const modelKeyPurpose = "steward.model"

type manualOverrides struct {
	// Protocol 是 openai 或 messages 协议。
	Protocol *string `json:"protocol,omitempty"`
	// Model 是对话模型名称。
	Model *string `json:"model,omitempty"`
	// ToolSupport 是工具能力的三态结论。
	ToolSupport *provider.ToolSupport `json:"tool_support,omitempty"`
}

type settingsInput struct {
	// Endpoint 是用户填写的 HTTP(S) 基础地址。
	Endpoint *string `json:"endpoint"`
	// Key 是只写的模型密钥，省略保留，显式 null 清除。
	Key json.RawMessage `json:"key"`
	// Protocol 是 openai 或 messages 协议。
	Protocol *string `json:"protocol"`
	// Model 是对话模型名称。
	Model *string `json:"model"`
	// DisplayName 是去掉首尾空白后的管家名字，空值表示默认。
	DisplayName *string `json:"display_name"`
	// Overrides 保存用户显式填写的覆盖项。
	Overrides *manualOverrides `json:"overrides"`
}

type settingsResponse struct {
	// Endpoint 是用户填写的 HTTP(S) 基础地址。
	Endpoint string `json:"endpoint"`
	// KeyLastFour 是已保存密钥的末四位。
	KeyLastFour string `json:"key_last_four"`
	// KeySaved 表示是否存在已保存的密钥密文。
	KeySaved bool `json:"key_saved"`
	// KeyNeedsReentry 表示密文无法解密，需要重新填写密钥。
	KeyNeedsReentry bool `json:"key_needs_reentry"`
	// Protocol 是 openai 或 messages 协议。
	Protocol string `json:"protocol"`
	// Model 是对话模型名称。
	Model string `json:"model"`
	// DisplayName 是去掉首尾空白后的管家名字，空值表示默认。
	DisplayName string `json:"display_name"`
	// ProbeResult 是最近一次探测报告。
	ProbeResult json.RawMessage `json:"probe_result"`
	// ProbedAt 是最近一次探测时间。
	ProbedAt *time.Time `json:"probed_at"`
	// Overrides 保存用户显式填写的覆盖项。
	Overrides json.RawMessage `json:"overrides"`
	// UpdatedAt 是最近修改时间。
	UpdatedAt *time.Time `json:"updated_at"`
}

func (h *Handlers) settingsView(userID string, settings runtime.UserSettings) settingsResponse {
	result := settingsResponse{Endpoint: settings.Endpoint, Protocol: settings.Protocol, Model: settings.Model,
		DisplayName: settings.DisplayName, Overrides: settings.Overrides}
	if result.Overrides == nil {
		result.Overrides = json.RawMessage(`{}`)
	}
	if settings.Key != nil {
		result.KeyLastFour, result.KeySaved = settings.Key.LastFour, true
		_, err := h.secrets.Open(userID, modelKeyPurpose, settings.Key.Ciphertext)
		result.KeyNeedsReentry = err != nil
	}
	if settings.Probe != nil {
		result.ProbeResult, result.ProbedAt = settings.Probe.Result, &settings.Probe.ProbedAt
	}
	if !settings.UpdatedAt.IsZero() {
		result.UpdatedAt = &settings.UpdatedAt
	}
	return result
}

func (h *Handlers) getSettings(w http.ResponseWriter, r *http.Request, user identity.User) {
	settings, err := h.store.GetUserSettings(r.Context(), user.ID)
	if err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, 200, h.settingsView(user.ID, settings))
}

func (h *Handlers) putSettings(w http.ResponseWriter, r *http.Request, user identity.User) {
	var input settingsInput
	if !decodeInput(w, r, &input) {
		return
	}
	if !validSettingsInput(&input) {
		invalidInput(w, r)
		return
	}
	saved, err := h.store.UpdateUserSettings(r.Context(), user.ID, func(current runtime.UserSettings) (runtime.UserSettingsInput, error) {
		update, err := h.mergeSettings(user.ID, current, input)
		if err != nil {
			return update, err
		}
		if update.Endpoint != "" {
			client, err := h.outboundClient(r.Context(), user)
			if err != nil {
				return update, err
			}
			defer client.CloseIdleConnections()
			if err := preflight(r.Context(), client, update.Endpoint); err != nil {
				return update, &settingsOutboundError{err}
			}
		}
		return update, nil
	})
	if err != nil {
		h.writeSettingsError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, 200, h.settingsView(user.ID, saved))
}

func validSettingsInput(input *settingsInput) bool {
	if input.Endpoint != nil {
		*input.Endpoint = strings.TrimSpace(*input.Endpoint)
		if *input.Endpoint != "" && !validEndpoint(*input.Endpoint) {
			return false
		}
	}
	if input.Protocol != nil && *input.Protocol != "" && !validProtocol(*input.Protocol) {
		return false
	}
	if input.Model != nil {
		*input.Model = strings.TrimSpace(*input.Model)
		if !validText(*input.Model) {
			return false
		}
	}
	if _, _, err := parseKey(input.Key); err != nil {
		return false
	}
	if input.DisplayName != nil {
		*input.DisplayName = strings.TrimSpace(*input.DisplayName)
		if !validText(*input.DisplayName) || utf8.RuneCountInString(*input.DisplayName) > 20 {
			return false
		}
	}
	if input.Overrides != nil {
		o := input.Overrides
		if o.Protocol != nil && !validProtocol(*o.Protocol) {
			return false
		}
		if o.Model != nil {
			*o.Model = strings.TrimSpace(*o.Model)
			if *o.Model == "" || !validText(*o.Model) {
				return false
			}
		}
		if o.ToolSupport != nil && *o.ToolSupport != provider.ToolSupportSupported && *o.ToolSupport != provider.ToolSupportUnsupported && *o.ToolSupport != provider.ToolSupportUnconfirmed {
			return false
		}
	}
	return true
}

func validKey(key string) bool {
	// 密钥用于 HTTP 认证头；空值及控制字符不能构成有效凭据。
	if strings.TrimSpace(key) == "" {
		return false
	}
	for _, c := range key {
		if c < 32 || c == 127 {
			return false
		}
	}
	return true
}

func (h *Handlers) encryptKey(userID, key string) (*runtime.EncryptedKey, error) {
	ciphertext, err := h.secrets.Seal(userID, modelKeyPurpose, []byte(key))
	if err != nil {
		return nil, err
	}
	return &runtime.EncryptedKey{Ciphertext: ciphertext, LastFour: secret.LastFour(key)}, nil
}

func settingsUpdate(settings runtime.UserSettings) runtime.UserSettingsInput {
	return runtime.UserSettingsInput{Endpoint: settings.Endpoint, Protocol: settings.Protocol, Model: settings.Model,
		DisplayName: settings.DisplayName}
}

func effectiveSettings(settings runtime.UserSettings) (runtime.UserSettings, provider.ToolSupport, error) {
	support := provider.ToolSupportUnconfirmed
	if settings.Probe != nil {
		var probe provider.ProbeResult
		if err := json.Unmarshal(settings.Probe.Result, &probe); err != nil {
			return settings, support, err
		}
		support = probe.ToolSupport
	}
	var overrides manualOverrides
	if settings.Overrides != nil {
		if err := json.Unmarshal(settings.Overrides, &overrides); err != nil {
			return settings, support, err
		}
	}
	if overrides.Protocol != nil {
		settings.Protocol = *overrides.Protocol
	}
	if overrides.Model != nil {
		settings.Model = *overrides.Model
	}
	if overrides.ToolSupport != nil {
		support = *overrides.ToolSupport
	}
	return settings, support, nil
}

func (h *Handlers) configured(w http.ResponseWriter, r *http.Request, user identity.User) (runtime.UserSettings, string, bool) {
	settings, err := h.store.GetUserSettings(r.Context(), user.ID)
	if err != nil {
		h.writeStoreError(w, r, err)
		return settings, "", false
	}
	settings, support, err := effectiveSettings(settings)
	if err != nil {
		h.logFailure(r, "internal")
		writeProblem(w, r, internalProblem)
		return settings, "", false
	}
	if !settings.IsConfigured() {
		writeProblem(w, r, notConfiguredProblem)
		return settings, "", false
	}
	var key []byte
	if settings.Key != nil {
		key, err = h.secrets.Open(user.ID, modelKeyPurpose, settings.Key.Ciphertext)
		if err != nil {
			writeProblem(w, r, notConfiguredProblem)
			return settings, "", false
		}
	}
	if support == provider.ToolSupportUnsupported {
		writeProblem(w, r, providerProblem(provider.ErrorToolUnsupported))
		return settings, "", false
	}
	return settings, string(key), true
}

func parseKey(raw json.RawMessage) (*string, bool, error) {
	if raw == nil {
		return nil, false, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, true, nil
	}
	var key string
	if json.Unmarshal(raw, &key) != nil || !validKey(key) {
		return nil, false, runtime.ErrInvalidInput
	}
	return &key, false, nil
}

func endpointAuthority(endpoint string) string {
	u, _ := url.Parse(endpoint)
	if u == nil {
		return ""
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	return strings.ToLower(strings.TrimSuffix(u.Hostname(), ".")) + ":" + port
}

func (h *Handlers) mergeSettings(userID string, current runtime.UserSettings, input settingsInput) (runtime.UserSettingsInput, error) {
	update := settingsUpdate(current)
	if input.Endpoint != nil {
		update.Endpoint = *input.Endpoint
	}
	if input.Protocol != nil {
		update.Protocol = *input.Protocol
	}
	if input.Model != nil {
		update.Model = *input.Model
	}
	if input.DisplayName != nil {
		update.DisplayName = *input.DisplayName
	}
	if input.Overrides != nil {
		update.Overrides, _ = json.Marshal(input.Overrides)
	}
	key, clearKey, err := parseKey(input.Key)
	if err != nil {
		return update, err
	}
	update.ClearKey = clearKey
	keyChanged := clearKey && current.Key != nil
	if key != nil {
		keyChanged = true
		if current.Key != nil {
			old, openErr := h.secrets.Open(userID, modelKeyPurpose, current.Key.Ciphertext)
			keyChanged = openErr != nil || string(old) != *key
		}
		update.Key, err = h.encryptKey(userID, *key)
		if err != nil {
			return update, &settingsSecretError{err}
		}
	}
	if endpointAuthority(current.Endpoint) != endpointAuthority(update.Endpoint) && input.Key == nil && current.Key != nil {
		update.ClearKey, keyChanged = true, true
		// 改换认证目标后，未显式提交协议时需重新确认。
		if input.Protocol == nil {
			update.Protocol = ""
		}
	}
	before, _, err := effectiveSettings(current)
	if err != nil {
		return update, err
	}
	candidate := current
	candidate.Protocol, candidate.Model = update.Protocol, update.Model
	if update.Overrides != nil {
		candidate.Overrides = update.Overrides
	}
	after, _, err := effectiveSettings(candidate)
	if err != nil {
		return update, err
	}
	if current.Endpoint != update.Endpoint || current.Protocol != update.Protocol || before.Protocol != after.Protocol || keyChanged {
		update.ClearProbe = true
		if input.Overrides == nil {
			update.Overrides = json.RawMessage(`{}`)
		}
	} else if current.Model != update.Model || before.Model != after.Model {
		if current.Probe != nil {
			var probe provider.ProbeResult
			if err := json.Unmarshal(current.Probe.Result, &probe); err != nil {
				return update, err
			}
			probe.ToolSupport = provider.ToolSupportUnconfirmed
			failures := probe.Failures[:0]
			for _, failure := range probe.Failures {
				if failure.Step != "tools" {
					failures = append(failures, failure)
				}
			}
			probe.Failures = failures
			raw, err := json.Marshal(probe)
			if err != nil {
				return update, err
			}
			update.Probe = &runtime.ProbeResult{Result: raw, ProbedAt: current.Probe.ProbedAt}
		}
		var overrides manualOverrides
		if err := json.Unmarshal(candidate.Overrides, &overrides); err != nil && candidate.Overrides != nil {
			return update, err
		}
		overrides.ToolSupport = nil
		if input.Model != nil {
			overrides.Model = nil
		}
		update.Overrides, _ = json.Marshal(overrides)
	}
	return update, nil
}
