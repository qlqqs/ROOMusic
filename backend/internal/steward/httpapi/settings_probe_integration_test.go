package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
)

func TestPostgreSQLStewardHTTPProbeUnsupportedSavesAndOverrideAllowsMessages(t *testing.T) {
	f := newAPIFixture(t)
	var unsupported atomic.Bool
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `{"data":[{"id":"probed-model"}]}`)
		} else if unsupported.Load() {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"model does not support tools"}}`)
		} else {
			_, _ = io.WriteString(w, apiModelResponse("openai", 3, false))
		}
	}))
	defer s.Close()
	f.configure(t, "admin", s.URL, "openai")
	id := f.conversation(t, "admin")
	path := "me/steward/conversations/" + id + "/messages"
	unsupported.Store(true)
	w := f.request("admin", "POST", "me/steward/settings/probe", map[string]string{
		"endpoint": s.URL + "/changed", "key": "synthetic-probed-key-5678", "model": "probed-model",
	})
	apiStatus(t, w, 200, "")
	var settings settingsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &settings); err != nil {
		t.Fatal(err)
	}
	var probe provider.ProbeResult
	if err := json.Unmarshal(settings.ProbeResult, &probe); err != nil {
		t.Fatal(err)
	}
	if settings.Endpoint != s.URL+"/changed" || settings.Protocol != "openai" || settings.Model != "probed-model" || !settings.KeySaved || settings.KeyLastFour != "5678" || settings.ProbedAt == nil || settings.UpdatedAt == nil {
		t.Fatalf("不支持工具的有效探测没有保存设置：%s", w.Body)
	}
	if probe.Protocol != provider.ProtocolOpenAI || probe.ToolSupport != provider.ToolSupportUnsupported || len(probe.Models) != 1 || probe.Models[0].ID != "probed-model" || len(probe.Failures) != 2 || probe.Failures[0].Step != "tools" || probe.Failures[0].Error.Kind != provider.ErrorToolUnsupported || probe.Failures[1].Step != "responses" || probe.Failures[1].Error.Kind != provider.ErrorToolUnsupported {
		t.Fatalf("不支持工具的报告不完整：%s", settings.ProbeResult)
	}
	read := f.request("admin", "GET", "me/steward/settings", nil)
	apiStatus(t, read, 200, "")
	if !bytes.Equal(w.Body.Bytes(), read.Body.Bytes()) {
		t.Fatalf("探测结果未持久化：保存=%s 读取=%s", w.Body, read.Body)
	}
	beforeHits := hits.Load()
	blocked := f.request("admin", "POST", path, map[string]string{"text": "synthetic-message-body"})
	apiStatus(t, blocked, 422, "provider_no_tool_support")
	if strings.Contains(blocked.Header().Get("Content-Type"), "event-stream") || hits.Load() != beforeHits {
		t.Fatal("工具能力不支持时仍然启动模型请求")
	}
	detail, err := f.store.GetConversation(context.Background(), f.users["admin"], id)
	if err != nil || len(detail.Messages) != 0 || detail.Conversation.Status != "idle" {
		t.Fatalf("被拦截的消息改变会话：%+v %v", detail, err)
	}
	apiStatus(t, f.request("admin", "PUT", "me/steward/settings", map[string]any{"overrides": map[string]string{"tool_support": "supported"}}), 200, "")
	read = f.request("admin", "GET", "me/steward/settings", nil)
	apiStatus(t, read, 200, "")
	var overridden settingsResponse
	if err := json.Unmarshal(read.Body.Bytes(), &overridden); err != nil {
		t.Fatal(err)
	}
	var overrides map[string]string
	if err := json.Unmarshal(overridden.Overrides, &overrides); err != nil || overrides["tool_support"] != "supported" || !bytes.Equal(settings.ProbeResult, overridden.ProbeResult) || overridden.ProbedAt == nil || !overridden.ProbedAt.Equal(*settings.ProbedAt) {
		t.Fatalf("手动覆盖未保存或改写原探测报告：%s %v", read.Body, err)
	}
	unsupported.Store(false)
	assertDone(t, f.request("admin", "POST", path, map[string]string{"text": "synthetic-message-body"}), runtime.EndCompleted)
	if hits.Load() <= beforeHits {
		t.Fatal("手动确认支持工具后没有请求模型")
	}
	assertPrivateLogs(t, f)
}

func TestPostgreSQLStewardHTTPSettingsProtocolOverridePersistsAndControlsWire(t *testing.T) {
	for _, change := range []string{"initial", "override_only", "endpoint", "protocol", "key"} {
		t.Run(change, func(t *testing.T) {
			f := newAPIFixture(t)
			var hits atomic.Int32
			wantPath := "/v1/messages"
			if change == "endpoint" {
				wantPath = "/changed/messages"
			}
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != wantPath {
					t.Errorf("覆盖协议未控制请求路径：%s %s，期望 POST %s", r.Method, r.URL.Path, wantPath)
				}
				var body struct {
					Model    string `json:"model"`
					System   string `json:"system"`
					Messages []struct {
						Content []struct {
							Type string `json:"type"`
							Text string `json:"text"`
						} `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("请求不是 Messages 格式：%v", err)
				} else if body.Model != "override-model" || body.System == "" || len(body.Messages) != 1 || len(body.Messages[0].Content) != 1 || body.Messages[0].Content[0].Type != "text" || body.Messages[0].Content[0].Text != "synthetic-message-body" {
					t.Errorf("Messages 请求内容错误：%+v", body)
				}
				_, _ = io.WriteString(w, apiModelResponse("messages", 3, false))
			}))
			defer s.Close()
			if change != "initial" {
				f.configure(t, "admin", s.URL, "openai")
				if _, err := f.db.Exec(`UPDATE steward_user_settings SET probe_result='{"tool_support":"unsupported"}',probed_at=now(),overrides='{"tool_support":"unsupported"}' WHERE user_id=$1`, f.users["admin"]); err != nil {
					t.Fatal(err)
				}
			}
			wantOverrides := map[string]string{"protocol": "messages", "model": "override-model", "tool_support": "supported"}
			patch := map[string]any{"overrides": wantOverrides}
			switch change {
			case "initial":
				patch["endpoint"], patch["protocol"], patch["model"], patch["key"] = s.URL, "openai", "synthetic-model", apiSyntheticKey
			case "endpoint":
				patch["endpoint"] = s.URL + "/changed"
			case "protocol":
				patch["protocol"] = ""
			case "key":
				patch["key"] = "synthetic-replacement-key"
			}
			w := f.request("admin", "PUT", "me/steward/settings", patch)
			apiStatus(t, w, 200, "")
			read := f.request("admin", "GET", "me/steward/settings", nil)
			apiStatus(t, read, 200, "")
			if !bytes.Equal(w.Body.Bytes(), read.Body.Bytes()) {
				t.Fatalf("覆盖项未持久化：保存=%s 读取=%s", w.Body, read.Body)
			}
			var settings settingsResponse
			if err := json.Unmarshal(read.Body.Bytes(), &settings); err != nil {
				t.Fatal(err)
			}
			var got map[string]string
			if err := json.Unmarshal(settings.Overrides, &got); err != nil || !reflect.DeepEqual(got, wantOverrides) {
				t.Fatalf("本次提交的覆盖项被清除：%s %v", read.Body, err)
			}
			if string(settings.ProbeResult) != "null" || settings.ProbedAt != nil {
				t.Fatalf("协议变化后仍保留旧探测：%s", read.Body)
			}
			path := "me/steward/conversations/" + f.conversation(t, "admin") + "/messages"
			assertDone(t, f.request("admin", "POST", path, map[string]string{"text": "synthetic-message-body"}), runtime.EndCompleted)
			if hits.Load() == 0 {
				t.Fatal("没有向假模型发送对话请求")
			}
			assertPrivateLogs(t, f)
		})
	}
}

func TestPostgreSQLStewardHTTPProbeErrorsDoNotSaveSettings(t *testing.T) {
	for _, failure := range []string{"authentication_models", "authentication_tools", "timeout_models", "timeout_tools", "connection", "egress_denied"} {
		t.Run(failure, func(t *testing.T) {
			f := newAPIFixture(t)
			var failing atomic.Bool
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if failing.Load() && ((strings.HasSuffix(failure, "_models") && r.Method == http.MethodGet) || (strings.HasSuffix(failure, "_tools") && r.Method == http.MethodPost)) {
					status := http.StatusUnauthorized
					if strings.HasPrefix(failure, "timeout") {
						status = http.StatusGatewayTimeout
					}
					w.WriteHeader(status)
					_, _ = io.WriteString(w, `{"error":{"message":"synthetic probe failure"}}`)
				} else if r.Method == http.MethodGet {
					_, _ = io.WriteString(w, `{"data":[{"id":"synthetic-model"}]}`)
				} else {
					_, _ = io.WriteString(w, apiModelResponse("openai", 1, true))
				}
			}))
			defer s.Close()
			entry := f.allowEndpoint(t, s.URL)
			f.configure(t, "member", s.URL, "openai")
			apiStatus(t, f.request("member", "POST", "me/steward/settings/probe", map[string]any{}), 200, "")
			apiStatus(t, f.request("member", "PUT", "me/steward/settings", map[string]any{"overrides": map[string]string{"tool_support": "supported"}}), 200, "")
			before := f.request("member", "GET", "me/steward/settings", nil)
			apiStatus(t, before, 200, "")
			failing.Store(true)
			if failure == "connection" {
				s.Close()
			}
			wantStatus, wantCode := 503, "provider_unreachable"
			if strings.HasPrefix(failure, "timeout") {
				wantCode = "provider_timeout"
			}
			if failure == "egress_denied" {
				apiStatus(t, f.request("admin", "DELETE", "admin/steward/egress-allowlist", entry), 200, "")
				wantStatus, wantCode = 403, "egress_denied"
			}
			apiStatus(t, f.request("member", "POST", "me/steward/settings/probe", map[string]any{"endpoint": s.URL + "/changed", "key": nil, "model": "changed-model"}), wantStatus, wantCode)
			after := f.request("member", "GET", "me/steward/settings", nil)
			apiStatus(t, after, 200, "")
			if !bytes.Equal(before.Body.Bytes(), after.Body.Bytes()) {
				t.Fatalf("失败探测改写已存设置、密钥、覆盖项或探测：之前=%s 之后=%s", before.Body, after.Body)
			}
			assertPrivateLogs(t, f)
		})
	}
}
