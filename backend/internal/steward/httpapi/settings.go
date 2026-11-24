package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	NativeSearch *provider.NativeSearchSupport `json:"native_search,omitempty"`
	// Protocol 是 openai、messages 或 responses 协议。
	Protocol *string `json:"protocol,omitempty"`
	// Model 是对话模型名称。
	Model *string `json:"model,omitempty"`
	// ToolSupport 是工具能力的三态结论。
	ToolSupport *provider.ToolSupport `json:"tool_support,omitempty"`
}

type settingsInput struct {
	SearchSource *string         `json:"search_source"`
	SearXNGURL   *string         `json:"searxng_url"`
	SearchKey    json.RawMessage `json:"search_key"`
	// Endpoint 是用户填写的 HTTP(S) 基础地址。
	Endpoint *string `json:"endpoint"`
	// Key 是只写的模型密钥，省略保留，显式 null 清除。
	Key json.RawMessage `json:"key"`
	// Protocol 是 openai、messages 或 responses 协议。
	Protocol *string `json:"protocol"`
	// Model 是对话模型名称。
	Model       *string `json:"model"`
	ReviewModel *string `json:"review_model"`
	// DisplayName 是去掉首尾空白后的管家名字，空值表示默认。
	DisplayName *string `json:"display_name"`
	// Overrides 保存用户显式填写的覆盖项。
	Overrides *manualOverrides `json:"overrides"`
}

type settingsResponse struct {
	UnreadConversationCount int64  `json:"unread_conversation_count"`
	SearchSource            string `json:"search_source"`
	SearXNGURL              string `json:"searxng_url"`
	SearchKeySaved          bool   `json:"search_key_saved"`
	SearchKeyLastFour       string `json:"search_key_last_four"`
	SearchKeyNeedsReentry   bool   `json:"search_key_needs_reentry"`
	// Endpoint 是用户填写的 HTTP(S) 基础地址。
	Endpoint string `json:"endpoint"`
	// KeyLastFour 是已保存密钥的末四位。
	KeyLastFour string `json:"key_last_four"`
	// KeySaved 表示是否存在已保存的密钥密文。
	KeySaved bool `json:"key_saved"`
	// KeyNeedsReentry 表示密文无法解密，需要重新填写密钥。
	KeyNeedsReentry bool `json:"key_needs_reentry"`
	// Protocol 是 openai、messages 或 responses 协议。
	Protocol string `json:"protocol"`
	// Model 是对话模型名称。
	Model       string `json:"model"`
	ReviewModel string `json:"review_model"`
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
	result := settingsResponse{Endpoint: settings.Endpoint, Protocol: settings.Protocol, Model: settings.Model, ReviewModel: settings.ReviewModel,
		DisplayName: settings.DisplayName, Overrides: settings.Overrides, SearchSource: settings.SearchSource, SearXNGURL: settings.SearXNGURL}
	if result.SearchSource == "" {
		result.SearchSource = "none"
	}
	if result.Overrides == nil {
		result.Overrides = json.RawMessage(`{}`)
	}
	result.KeyLastFour, result.KeySaved, result.KeyNeedsReentry = h.keyView(userID, modelKeyPurpose, settings.Key)
	result.SearchKeyLastFour, result.SearchKeySaved, result.SearchKeyNeedsReentry = h.keyView(userID, secret.SearchKeyPurpose, settings.SearchKey)
	if settings.Probe != nil {
		result.ProbeResult, result.ProbedAt = settings.Probe.Result, &settings.Probe.ProbedAt
		var probe provider.ProbeResult
		if json.Unmarshal(result.ProbeResult, &probe) == nil && probe.NativeSearch == "" {
			probe.NativeSearch = provider.NativeSearchUnknown
			result.ProbeResult, _ = json.Marshal(probe)
		}
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
	h.writeSettings(w, r, user.ID, settings)
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
	cancelRuns := false
	saved, err := h.store.UpdateUserSettings(r.Context(), user.ID, func(current runtime.UserSettings) (runtime.UserSettingsInput, error) {
		update, err := h.mergeSettings(user.ID, current, input)
		if err != nil {
			return update, err
		}
		cancelRuns = modelSettingsChanged(current, update)
		if input.SearchSource != nil && *input.SearchSource == "native" && effectiveNativeSearch(current, update) != provider.NativeSearchSupported {
			return update, runtime.ErrInvalidInput
		}
		endpoints := []string{update.Endpoint}
		if input.SearXNGURL != nil || update.SearchSource == "searxng" {
			endpoints = append(endpoints, update.SearXNGURL)
		}
		for _, endpoint := range endpoints {
			if endpoint == "" {
				continue
			}
			client, err := h.outboundClient(r.Context(), user)
			if err != nil {
				return update, err
			}
			defer client.CloseIdleConnections()
			if err := preflight(r.Context(), client, endpoint); err != nil {
				return update, &settingsOutboundError{err}
			}
		}
		return update, nil
	})
	if err != nil {
		h.writeSettingsError(w, r, err)
		return
	}
	if cancelRuns {
		h.supervisor.CancelUser(user.ID)
		h.supervisor.CancelBackground(user.ID)
	}
	h.writeSettings(w, r, user.ID, saved)
}

func validSettingsInput(input *settingsInput) bool {
	if input.SearchSource != nil && *input.SearchSource != "none" && *input.SearchSource != "searxng" && *input.SearchSource != "tavily" && *input.SearchSource != "native" {
		return false
	}
	if input.SearXNGURL != nil {
		*input.SearXNGURL = strings.TrimSpace(*input.SearXNGURL)
		if *input.SearXNGURL != "" && !validEndpoint(*input.SearXNGURL) {
			return false
		}
	}
	if _, _, err := parseKey(input.SearchKey); err != nil {
		return false
	}
	if input.Endpoint != nil {
		*input.Endpoint = strings.TrimSpace(*input.Endpoint)
		if *input.Endpoint != "" && !validEndpoint(*input.Endpoint) {
			return false
		}
	}
	if input.Protocol != nil && *input.Protocol != "" && !validProtocol(*input.Protocol) {
		return false
	}
	if input.ReviewModel != nil {
		*input.ReviewModel = strings.TrimSpace(*input.ReviewModel)
		if !validText(*input.ReviewModel) {
			return false
		}
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
		if o.NativeSearch != nil && !provider.ValidNativeSearch(*o.NativeSearch) {
			return false
		}
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

func (h *Handlers) encryptKey(userID, purpose, key string) (*runtime.EncryptedKey, error) {
	ciphertext, err := h.secrets.Seal(userID, purpose, []byte(key))
	if err != nil {
		return nil, err
	}
	return &runtime.EncryptedKey{Ciphertext: ciphertext, LastFour: secret.LastFour(key)}, nil
}

func settingsUpdate(settings runtime.UserSettings) runtime.UserSettingsInput {
	return runtime.UserSettingsInput{Endpoint: settings.Endpoint, Protocol: settings.Protocol, Model: settings.Model, ReviewModel: settings.ReviewModel,
		DisplayName: settings.DisplayName, SearchSource: settings.SearchSource, SearXNGURL: settings.SearXNGURL}
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

// ModelUnavailable 的 Reason 可供自动整理进度直接展示。
type ModelUnavailable struct{ Reason string }

func (e *ModelUnavailable) Error() string { return e.Reason }

var errInvalidModelConfig = errors.New("管家模型设置格式无效")

func (h *Handlers) configured(w http.ResponseWriter, r *http.Request, user identity.User) (runtime.UserSettings, string, bool) {
	settings, key, err := h.resolveModelConfig(r.Context(), user.ID)
	var unavailable *ModelUnavailable
	switch {
	case err == nil:
		return settings, key, true
	case errors.As(err, &unavailable):
		if unavailable.Reason == "tool_unsupported" {
			writeProblem(w, r, providerProblem(provider.ErrorToolUnsupported))
		} else {
			writeProblem(w, r, notConfiguredProblem)
		}
	case errors.Is(err, errInvalidModelConfig):
		h.logFailure(r, "internal")
		writeProblem(w, r, internalProblem)
	default:
		h.writeStoreError(w, r, err)
	}
	return settings, "", false
}

func (h *Handlers) resolveModelConfig(ctx context.Context, userID string) (runtime.UserSettings, string, error) {
	settings, err := h.store.GetUserSettings(ctx, userID)
	if err != nil {
		return settings, "", err
	}
	return modelConfig(settings, userID, h.secrets)
}

func modelConfig(settings runtime.UserSettings, userID string, secrets *secret.Store) (runtime.UserSettings, string, error) {
	settings, support, err := effectiveSettings(settings)
	if err != nil {
		return settings, "", errInvalidModelConfig
	}
	if !settings.IsConfigured() {
		return settings, "", &ModelUnavailable{Reason: "not_configured"}
	}
	var key []byte
	if settings.Key != nil {
		key, err = secrets.Open(userID, modelKeyPurpose, settings.Key.Ciphertext)
		if err != nil {
			return settings, "", &ModelUnavailable{Reason: "key_needs_reentry"}
		}
	}
	// 默认协议仍为 Chat Completions；自带搜索及其手动覆盖生效时使用 Responses。
	if settings.SearchSource == "native" && settings.Protocol == string(provider.ProtocolOpenAI) && effectiveNativeSearch(settings, settingsUpdate(settings)) == provider.NativeSearchSupported {
		settings.Protocol = string(provider.ProtocolResponses)
		// Chat Completions 的工具结论不能当作 Responses 的结论；显式覆盖仍优先。
		var overrides manualOverrides
		if settings.Overrides != nil {
			if err := json.Unmarshal(settings.Overrides, &overrides); err != nil {
				return settings, "", errInvalidModelConfig
			}
		}
		support = provider.ToolSupportUnconfirmed
		if overrides.ToolSupport != nil {
			support = *overrides.ToolSupport
		}
	}
	if support == provider.ToolSupportUnsupported {
		return settings, "", &ModelUnavailable{Reason: "tool_unsupported"}
	}
	return settings, string(key), nil
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
	if input.ReviewModel != nil {
		update.ReviewModel = *input.ReviewModel
	}
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
	var err error
	var keyChanged bool
	update.Key, update.ClearKey, keyChanged, err = h.mergeKey(userID, modelKeyPurpose, current.Key, input.Key)
	if err != nil {
		return update, err
	}
	update.SearchKey, update.ClearSearchKey, _, err = h.mergeKey(userID, secret.SearchKeyPurpose, current.SearchKey, input.SearchKey)
	if err != nil {
		return update, err
	}
	if input.SearchSource != nil {
		update.SearchSource = *input.SearchSource
	}
	if input.SearXNGURL != nil {
		update.SearXNGURL = *input.SearXNGURL
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
			probe.NativeSearch = provider.NativeSearchUnknown
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
		if input.Overrides == nil || input.Overrides.NativeSearch == nil {
			overrides.NativeSearch = nil
		}
		if input.Model != nil {
			overrides.Model = nil
		}
		update.Overrides, _ = json.Marshal(overrides)
	}
	return update, nil
}

// 两类密钥共用输入语义、加解密和展示路径，仅用途标签不同。
func (h *Handlers) mergeKey(userID, purpose string, current *runtime.EncryptedKey, raw json.RawMessage) (*runtime.EncryptedKey, bool, bool, error) {
	key, clear, err := parseKey(raw)
	if err != nil {
		return nil, false, false, err
	}
	if key == nil {
		return nil, clear, clear && current != nil, nil
	}
	changed := true
	if current != nil {
		old, err := h.secrets.Open(userID, purpose, current.Ciphertext)
		changed = err != nil || string(old) != *key
	}
	if !changed {
		return nil, false, false, nil
	}
	encrypted, err := h.encryptKey(userID, purpose, *key)
	if err != nil {
		return nil, false, false, &settingsSecretError{err}
	}
	return encrypted, false, changed, nil
}

func (h *Handlers) keyView(userID, purpose string, key *runtime.EncryptedKey) (string, bool, bool) {
	if key == nil {
		return "", false, false
	}
	_, err := h.secrets.Open(userID, purpose, key.Ciphertext)
	return key.LastFour, true, err != nil
}

func effectiveNativeSearch(current runtime.UserSettings, update runtime.UserSettingsInput) provider.NativeSearchSupport {
	probe := current.Probe
	if update.ClearProbe {
		probe = nil
	}
	if update.Probe != nil {
		probe = update.Probe
	}
	support := provider.NativeSearchUnknown
	if probe != nil {
		var result provider.ProbeResult
		if json.Unmarshal(probe.Result, &result) == nil && result.NativeSearch != "" {
			support = result.NativeSearch
		}
	}
	raw := current.Overrides
	if update.Overrides != nil {
		raw = update.Overrides
	}
	var overrides manualOverrides
	if json.Unmarshal(raw, &overrides) == nil && overrides.NativeSearch != nil {
		support = *overrides.NativeSearch
	}
	return support
}

func (h *Handlers) writeSettings(w http.ResponseWriter, r *http.Request, userID string, settings runtime.UserSettings) {
	count, err := h.store.CountUnreadConversations(r.Context(), userID)
	if err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	response := h.settingsView(userID, settings)
	response.UnreadConversationCount = count
	httpserver.WriteJSON(w, 200, response)
}

func modelSettingsChanged(current runtime.UserSettings, update runtime.UserSettingsInput) bool {
	return current.Endpoint != update.Endpoint || update.Key != nil || (update.ClearKey && current.Key != nil)
}
