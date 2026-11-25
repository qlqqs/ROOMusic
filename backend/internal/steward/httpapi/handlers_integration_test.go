package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/memory"
	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
	"github.com/qlqq/roomusic/backend/internal/steward/secret"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

const apiSyntheticKey = "synthetic-http-key-4321"

type lockedLog struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedLog) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

type apiFixture struct {
	db            *sql.DB
	store         *runtime.Store
	handler       http.Handler
	api           *Handlers
	logs          *lockedLog
	users, tokens map[string]string
	keyPath       string
	registry      *tool.Registry
}

func newAPIFixture(t *testing.T) *apiFixture {
	t.Helper()
	scoped := postgrestest.NewSchema(t, "steward_api_")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, err := postgres.Open(ctx, scoped)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	base := filepath.Join("..", "..", "..", "..", "temp")
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(base, "steward-http-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	f := &apiFixture{db: db, store: runtime.NewStore(db), logs: &lockedLog{}, users: map[string]string{}, tokens: map[string]string{}, keyPath: filepath.Join(dir, "instance.key")}
	for _, role := range []string{"admin", "member", "other"} {
		id, token := uuid.New(), identity.GenerateSessionToken()
		dbRole := "user"
		if role == "admin" {
			dbRole = "admin"
		}
		if _, err := db.Exec(`INSERT INTO users(id,username,password_hash,role) VALUES($1,$2,'synthetic-only',$3)`, id, "synthetic-"+role, dbRole); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '1 hour')`, identity.HashSessionToken(token), id); err != nil {
			t.Fatal(err)
		}
		f.users[role] = id
		f.tokens[role] = token
	}
	registry := tool.NewRegistry()
	f.registry = registry
	if err := registry.Register(tool.Definition{Name: "synthetic_lookup", Description: "合成工具", MinRole: tool.RoleMember, Scope: tool.ScopeRead, Untrusted: true, Input: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`), Output: json.RawMessage(`{"type":"object"}`), Handler: func(context.Context, tool.Caller, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"value":"synthetic-tool-output"}`), nil
	}}); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewJSONHandler(f.logs, nil))
	memories := memory.NewStore(db)
	for _, definition := range memories.StewardTools() {
		if err := registry.Register(definition); err != nil {
			t.Fatal(err)
		}
	}
	f.api = New(identity.NewGuard(db, false, "").WithLogger(logger), f.store, secret.New(f.keyPath), runtime.NewRunner(f.store, registry, logger).WithMemories(memories), logger).WithMemories(memories)
	t.Cleanup(func() {
		if err := f.api.supervisor.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	mux := http.NewServeMux()
	f.api.Register(mux)
	f.handler = httpserver.Middleware(logger, mux)
	return f
}
func (f *apiFixture) request(role, method, path string, body any) *httptest.ResponseRecorder {
	return f.requestTimeout(role, method, path, body, 5*time.Second)
}
func (f *apiFixture) requestTimeout(role, method, path string, body any, timeout time.Duration) *httptest.ResponseRecorder {
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	r := httptest.NewRequest(method, "http://roomusic.test/api/v1/"+path, bytes.NewReader(raw)).WithContext(ctx)
	r.Header.Set("Origin", "http://roomusic.test")
	r.Header.Set("Content-Type", "application/json")
	if token := f.tokens[role]; token != "" {
		r.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: token})
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}
func apiStatus(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status=%d want=%d body=%s", w.Code, status, w.Body)
	}
	if code != "" {
		var body httpserver.APIError
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Error.Code != code {
			t.Fatalf("code want=%s body=%s err=%v", code, w.Body, err)
		}
	}
}
func (f *apiFixture) configure(t *testing.T, role, endpoint, protocol string) {
	t.Helper()
	apiStatus(t, f.request(role, "PUT", "me/steward/settings", map[string]any{"endpoint": endpoint, "protocol": protocol, "model": "synthetic-model", "key": apiSyntheticKey}), 200, "")
}
func (f *apiFixture) conversation(t *testing.T, role string) string {
	t.Helper()
	w := f.request(role, "POST", "me/steward/conversations", map[string]any{"title": "合成会话"})
	apiStatus(t, w, 201, "")
	var c conversationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	return c.ID
}
func assertPrivateLogs(t *testing.T, f *apiFixture) {
	t.Helper()
	for _, value := range []string{apiSyntheticKey, "synthetic-message-body", "synthetic-tool-output", "synthetic-answer"} {
		if strings.Contains(f.logs.String(), value) {
			t.Errorf("log contains %q", value)
		}
	}
}

func TestPostgreSQLStewardHTTPAuthenticationAndMemberSwitch(t *testing.T) {
	f := newAPIFixture(t)
	apiStatus(t, f.request("", "GET", "me/steward/settings", nil), 401, "unauthorized")
	apiStatus(t, f.request("member", "PUT", "admin/steward/settings", map[string]any{"members_enabled": false}), 403, "forbidden")
	apiStatus(t, f.request("admin", "PUT", "admin/steward/settings", map[string]any{"members_enabled": false}), 200, "")
	for _, tc := range []struct {
		method, path string
		body         any
	}{{"GET", "me/steward/settings", nil}, {"PUT", "me/steward/settings", map[string]string{"display_name": "合成"}}, {"GET", "me/steward/conversations", nil}, {"POST", "me/steward/conversations", map[string]string{}}, {"POST", "me/steward/settings/probe", map[string]string{}}} {
		apiStatus(t, f.request("member", tc.method, tc.path, tc.body), 403, "steward_disabled")
	}
	apiStatus(t, f.request("admin", "GET", "me/steward/settings", nil), 200, "")
	apiStatus(t, f.request("admin", "GET", "me/steward/conversations", nil), 200, "")
	apiStatus(t, f.request("admin", "PUT", "admin/steward/settings", map[string]any{"members_enabled": true}), 200, "")
	apiStatus(t, f.request("member", "GET", "me/steward/settings", nil), 200, "")
}
func TestPostgreSQLStewardHTTPSettingsPrivacyNamesAndIsolation(t *testing.T) {
	f := newAPIFixture(t)
	for _, tc := range []struct {
		name   string
		status int
	}{{strings.Repeat("名", 20), 200}, {strings.Repeat("名", 21), 400}, {"", 200}} {
		w := f.request("member", "PUT", "me/steward/settings", map[string]string{"display_name": tc.name})
		apiStatus(t, w, tc.status, "")
		if tc.status == 200 {
			var s settingsResponse
			if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil || s.DisplayName != tc.name {
				t.Fatalf("name=%+v err=%v", s, err)
			}
		}
	}
	f.allowEndpoint(t, "http://127.0.0.1:1")
	f.configure(t, "member", "http://127.0.0.1:1", "openai")
	w := f.request("member", "GET", "me/steward/settings", nil)
	apiStatus(t, w, 200, "")
	var settings settingsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &settings); err != nil {
		t.Fatal(err)
	}
	if !settings.KeySaved || settings.KeyLastFour != "4321" || settings.KeyNeedsReentry || strings.Contains(w.Body.String(), apiSyntheticKey) || strings.Contains(w.Body.String(), "ciphertext") {
		t.Fatalf("unsafe settings=%s", w.Body)
	}
	for _, role := range []string{"admin", "other"} {
		w := f.request(role, "GET", "me/steward/settings", nil)
		apiStatus(t, w, 200, "")
		var s settingsResponse
		_ = json.Unmarshal(w.Body.Bytes(), &s)
		if s.KeySaved || s.Endpoint != "" || s.KeyLastFour != "" {
			t.Fatalf("%s read member settings: %s", role, w.Body)
		}
	}
	old, err := f.store.GetUserSettings(context.Background(), f.users["member"])
	if err != nil {
		t.Fatal(err)
	}
	apiStatus(t, f.request("member", "PUT", "me/steward/settings", map[string]string{"display_name": "新名字"}), 200, "")
	current, err := f.store.GetUserSettings(context.Background(), f.users["member"])
	if err != nil || !bytes.Equal(old.Key.Ciphertext, current.Key.Ciphertext) {
		t.Fatal("omitted key did not preserve ciphertext")
	}
	if err := os.Remove(f.keyPath); err != nil {
		t.Fatal(err)
	}
	f.api.secrets = secret.New(f.keyPath)
	w = f.request("member", "GET", "me/steward/settings", nil)
	apiStatus(t, w, 200, "")
	_ = json.Unmarshal(w.Body.Bytes(), &settings)
	if !settings.KeyNeedsReentry || settings.DisplayName != "新名字" || settings.Endpoint != old.Endpoint {
		t.Fatalf("lost instance key=%s", w.Body)
	}
	assertPrivateLogs(t, f)
}
func TestPostgreSQLStewardHTTPDefaultModeSettings(t *testing.T) {
	f := newAPIFixture(t)
	const path = "me/steward/settings"
	for _, patch := range []map[string]string{{"default_mode": "operator"}, {"display_name": "x"}} {
		apiStatus(t, f.request("admin", "PUT", path, patch), 200, "")
		w := f.request("admin", "GET", path, nil)
		apiStatus(t, w, 200, "")
		var settings settingsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &settings); err != nil {
			t.Fatal(err)
		}
		if settings.DefaultMode != "operator" {
			t.Fatalf("after PUT %v: default_mode=%q want=operator", patch, settings.DefaultMode)
		}
	}
	apiStatus(t, f.request("admin", "PUT", path, map[string]string{"default_mode": "bogus"}), 400, "invalid_input")
}
func TestPostgreSQLStewardHTTPDefaultModeConversation(t *testing.T) {
	f := newAPIFixture(t)
	f.configure(t, "admin", "http://127.0.0.1:1", "openai")
	apiStatus(t, f.request("admin", "PUT", "me/steward/settings", map[string]string{"default_mode": "steward"}), 200, "")
	for _, tc := range []struct {
		body map[string]string
		want string
	}{
		{map[string]string{}, "steward"},
		{map[string]string{"mode": "assistant"}, "assistant"},
	} {
		w := f.request("admin", "POST", "me/steward/conversations", tc.body)
		apiStatus(t, w, 201, "")
		var conversation conversationResponse
		if err := json.Unmarshal(w.Body.Bytes(), &conversation); err != nil {
			t.Fatal(err)
		}
		if conversation.Mode != tc.want {
			t.Fatalf("after POST %v: mode=%q want=%q", tc.body, conversation.Mode, tc.want)
		}
	}
}
func TestPostgreSQLStewardHTTPConversationOwnershipCRUD(t *testing.T) {
	f := newAPIFixture(t)
	f.allowEndpoint(t, "http://127.0.0.1:1")
	f.configure(t, "member", "http://127.0.0.1:1", "openai")
	id := f.conversation(t, "member")
	path := "me/steward/conversations/" + id
	for _, role := range []string{"admin", "other"} {
		for _, tc := range []struct {
			method, suffix string
			body           any
		}{{"GET", "", nil}, {"PATCH", "", map[string]string{"title": "入侵"}}, {"DELETE", "", nil}, {"POST", "/messages", map[string]string{"text": "synthetic-message-body"}}, {"POST", "/cancel", map[string]string{}}} {
			apiStatus(t, f.request(role, tc.method, path+tc.suffix, tc.body), 404, "not_found")
		}
		w := f.request(role, "GET", "me/steward/conversations", nil)
		apiStatus(t, w, 200, "")
		if strings.Contains(w.Body.String(), id) {
			t.Fatal("foreign conversation listed")
		}
	}
	apiStatus(t, f.request("member", "PATCH", path, map[string]string{"title": "新标题"}), 200, "")
	w := f.request("member", "GET", path, nil)
	apiStatus(t, w, 200, "")
	if !strings.Contains(w.Body.String(), "新标题") {
		t.Fatal("rename not persisted")
	}
	apiStatus(t, f.request("member", "DELETE", path, nil), 200, "")
	apiStatus(t, f.request("member", "GET", path, nil), 404, "not_found")
}
func TestPostgreSQLStewardHTTPNotConfiguredAndBusyAreJSON(t *testing.T) {
	f := newAPIFixture(t)
	c, err := f.store.CreateConversation(context.Background(), f.users["member"], "", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	path := "me/steward/conversations/" + c.ID + "/messages"
	w := f.request("member", "POST", path, map[string]string{"text": "synthetic-message-body"})
	apiStatus(t, w, 409, "steward_not_configured")
	if strings.Contains(w.Header().Get("Content-Type"), "event-stream") {
		t.Fatal("not configured started SSE")
	}
	if err := f.store.AcquireConversation(context.Background(), f.users["member"], c.ID); err != nil {
		t.Fatal(err)
	}
	w = f.request("member", "POST", path, map[string]string{"text": "synthetic-message-body"})
	apiStatus(t, w, 409, "conversation_busy")
	if strings.Contains(w.Header().Get("Content-Type"), "event-stream") {
		t.Fatal("busy started SSE")
	}
	assertPrivateLogs(t, f)
}

func wireEvent(name, raw string) string {
	prefix := ""
	if name != "" {
		prefix = "event: " + name + "\n"
	}
	return prefix + "data: " + raw + "\n\n"
}
func apiModelResponse(protocol string, step int, probe bool) string {
	name := "synthetic_lookup"
	if probe {
		name = "roomusic_probe"
	}
	if protocol == "openai" {
		if step <= 2 || probe {
			return wireEvent("", `{"choices":[{"index":0,"delta":{"content":"synthetic-answer","tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"`+name+`","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`) + wireEvent("", "[DONE]")
		}
		return wireEvent("", `{"choices":[{"index":0,"delta":{"content":"synthetic-answer"},"finish_reason":"stop"}]}`) + wireEvent("", `{"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3}}`) + wireEvent("", "[DONE]")
	}
	body := wireEvent("message_start", `{"type":"message_start","message":{}}`)
	if step <= 2 || probe {
		body += wireEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"synthetic-answer"}}`) + wireEvent("content_block_stop", `{"type":"content_block_stop","index":0}`) + wireEvent("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"c1","name":"`+name+`","input":{}}}`) + wireEvent("content_block_stop", `{"type":"content_block_stop","index":1}`) + wireEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`)
	} else {
		body += wireEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"synthetic-answer"}}`) + wireEvent("content_block_stop", `{"type":"content_block_stop","index":0}`) + wireEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":7,"output_tokens":3}}`)
	}
	return body + wireEvent("message_stop", `{"type":"message_stop"}`)
}
func TestPostgreSQLStewardHTTPProbeSavesResultAndEgressAllowlist(t *testing.T) {
	for _, tc := range []struct {
		name      string
		protocol  provider.Protocol
		available bool
	}{
		{"responses_tools", provider.ProtocolResponses, true},
		{"responses_not_found", provider.ProtocolOpenAI, false},
		{"responses_no_tool_call", provider.ProtocolOpenAI, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAPIFixture(t)
			var hits atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				if r.Method == "GET" {
					_, _ = io.WriteString(w, `{"data":[{"id":"synthetic-model"}]}`)
				} else if r.URL.Path == "/v1/responses" {
					if !tc.available {
						http.NotFound(w, r)
						return
					}
					raw, _ := io.ReadAll(r.Body)
					if tc.protocol == provider.ProtocolResponses && bytes.Contains(raw, []byte("roomusic_probe")) {
						_, _ = io.WriteString(w, wireEvent("response.output_item.done", `{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"item-1","call_id":"call-1","name":"roomusic_probe","arguments":"{}"}}`))
					}
					_, _ = io.WriteString(w, wireEvent("response.completed", `{"type":"response.completed","response":{"status":"completed"}}`))
				} else {
					if r.URL.Path != "/v1/chat/completions" || tc.protocol == provider.ProtocolResponses {
						t.Errorf("意外的回退请求：%s", r.URL.Path)
					}
					_, _ = io.WriteString(w, apiModelResponse("openai", 1, true))
				}
			}))
			defer s.Close()
			for _, endpoint := range []string{s.URL, "http://10.2.3.4:1234"} {
				w := f.request("member", "POST", "me/steward/settings/probe", map[string]string{"endpoint": endpoint, "model": "synthetic-model", "key": apiSyntheticKey})
				apiStatus(t, w, 403, "egress_denied")
			}
			if hits.Load() != 0 {
				t.Fatal("denied endpoint received request")
			}
			u, _ := url.Parse(s.URL)
			port, _ := strconv.Atoi(u.Port())
			entry := map[string]any{"host": u.Hostname(), "port": port}
			apiStatus(t, f.request("admin", "POST", "admin/steward/egress-allowlist", entry), 200, "")
			apiStatus(t, f.request("admin", "GET", "admin/steward/egress-allowlist", nil), 200, "")
			w := f.request("member", "POST", "me/steward/settings/probe", map[string]string{"endpoint": s.URL, "model": "synthetic-model", "key": apiSyntheticKey})
			apiStatus(t, w, 200, "")
			var settings settingsResponse
			if err := json.Unmarshal(w.Body.Bytes(), &settings); err != nil {
				t.Fatal(err)
			}
			var probe provider.ProbeResult
			if err := json.Unmarshal(settings.ProbeResult, &probe); err != nil || settings.Protocol != string(tc.protocol) || probe.Protocol != tc.protocol || probe.ToolSupport != provider.ToolSupportSupported || probe.ResponsesAvailable != tc.available || settings.ProbedAt == nil {
				t.Fatalf("probe=%+v err=%v body=%s", probe, err, w.Body)
			}
			if !tc.available {
				if len(probe.Failures) != 1 || probe.Failures[0].Step != "responses" || probe.Failures[0].Protocol != provider.ProtocolResponses || probe.Failures[0].Error.StatusCode != http.StatusNotFound {
					t.Fatalf("回退报告未正确保存 Responses 失败：%s", settings.ProbeResult)
				}
			} else if len(probe.Failures) != 0 {
				t.Fatalf("完整结束的 Responses 不应记失败：%s", settings.ProbeResult)
			}
			stored, err := f.store.GetUserSettings(context.Background(), f.users["member"])
			if err != nil || stored.Protocol != string(tc.protocol) || stored.Probe == nil || !stored.Probe.ProbedAt.Equal(*settings.ProbedAt) {
				t.Fatalf("probe not saved: %+v %v", stored, err)
			}
			var storedProbe provider.ProbeResult
			if err := json.Unmarshal(stored.Probe.Result, &storedProbe); err != nil || !reflect.DeepEqual(storedProbe, probe) {
				t.Fatalf("已保存的探测报告不一致：%s %v", stored.Probe.Result, err)
			}
			apiStatus(t, f.request("member", "PUT", "me/steward/settings", map[string]any{"overrides": map[string]string{"model": "manual-model", "tool_support": "unconfirmed"}}), 200, "")
			apiStatus(t, f.request("member", "PUT", "me/steward/settings", map[string]string{"display_name": "合成"}), 200, "")
			stored, err = f.store.GetUserSettings(context.Background(), f.users["member"])
			if err != nil || !bytes.Contains(stored.Overrides, []byte("manual-model")) || stored.Probe == nil {
				t.Fatal("ordinary save overwrote probe/manual override")
			}
			apiStatus(t, f.request("admin", "DELETE", "admin/steward/egress-allowlist", entry), 200, "")
			assertPrivateLogs(t, f)
		})
	}
}
func TestPostgreSQLStewardHTTPBothProtocolsStreamAndPersist(t *testing.T) {
	for _, protocol := range []string{"openai", "messages"} {
		t.Run(protocol, func(t *testing.T) {
			f := newAPIFixture(t)
			var requests atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				step := int(requests.Add(1))
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				if r.Header.Get("Authorization") != "Bearer "+apiSyntheticKey || strings.Contains(string(raw), apiSyntheticKey) {
					t.Error("model authentication boundary")
				}
				if step > 1 && !bytes.Contains(raw, []byte("roomusic-source")) {
					t.Error("tool result missing source wrapper")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, apiModelResponse(protocol, step, false))
			}))
			defer s.Close()
			f.configure(t, "admin", s.URL, protocol)
			id := f.conversation(t, "admin")
			w := f.request("admin", "POST", "me/steward/conversations/"+id+"/messages", map[string]string{"text": "synthetic-message-body"})
			apiStatus(t, w, 200, "")
			if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
				t.Fatalf("not SSE: %s", w.Body)
			}
			var events []runtime.Event
			for _, line := range strings.Split(w.Body.String(), "\n") {
				if strings.HasPrefix(line, "data: ") {
					var e runtime.Event
					if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e); err != nil {
						t.Fatal(err)
					}
					events = append(events, e)
				}
			}
			var kinds []runtime.EventType
			for _, e := range events {
				kinds = append(kinds, e.Type)
			}
			want := []runtime.EventType{runtime.EventText, runtime.EventToolStarted, runtime.EventToolFinished, runtime.EventText, runtime.EventToolStarted, runtime.EventToolFinished, runtime.EventText, runtime.EventUsage, runtime.EventDone}
			if !reflect.DeepEqual(kinds, want) || events[len(events)-1].End.Reason != runtime.EndCompleted {
				t.Fatalf("events=%v body=%s", kinds, w.Body)
			}
			detail, err := f.store.GetConversation(context.Background(), f.users["admin"], id)
			if err != nil || len(detail.Messages) != 6 || detail.Conversation.Status != "idle" || requests.Load() != 3 {
				t.Fatalf("detail=%+v err=%v requests=%d", detail, err, requests.Load())
			}
			for i, mi := range []int{1, 3} {
				call := detail.Messages[mi].ToolCalls[0]
				event := events[2+i*3].ToolFinished
				if call.Status != event.Status || !bytes.Equal(call.Output, event.Output) || call.Truncated != event.Truncated || call.Untrusted != event.Untrusted {
					t.Fatal("SSE tool result differs from persistence")
				}
			}
			if detail.Messages[5].InputTokens == nil || *detail.Messages[5].InputTokens != 7 {
				t.Fatal("usage not persisted")
			}
			read := f.request("admin", "GET", "me/steward/conversations/"+id, nil)
			apiStatus(t, read, 200, "")
			if !strings.Contains(read.Body.String(), "synthetic-message-body") || !strings.Contains(read.Body.String(), "synthetic-tool-output") {
				t.Fatal("conversation read incomplete")
			}
			assertPrivateLogs(t, f)
		})
	}
}
func TestPostgreSQLStewardHTTPCancelEndsRunningRequest(t *testing.T) {
	f := newAPIFixture(t)
	started, released := make(chan struct{}), make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(released)
	}))
	defer s.Close()
	f.configure(t, "admin", s.URL, "openai")
	id := f.conversation(t, "admin")
	path := "me/steward/conversations/" + id
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		result <- f.request("admin", "POST", path+"/messages", map[string]string{"text": "synthetic-message-body"})
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("model never started")
	}
	apiStatus(t, f.request("admin", "POST", path+"/messages", map[string]string{"text": "synthetic-message-body"}), 409, "conversation_busy")
	w := f.request("admin", "POST", path+"/cancel", map[string]string{})
	apiStatus(t, w, 200, "")
	if !strings.Contains(w.Body.String(), `"cancelled":true`) {
		t.Fatal(w.Body.String())
	}
	select {
	case w := <-result:
		apiStatus(t, w, 200, "")
		if !strings.Contains(w.Body.String(), `"reason":"cancelled"`) {
			t.Fatal(w.Body.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled run did not finish")
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("model connection retained")
	}
	d, err := f.store.GetConversation(context.Background(), f.users["admin"], id)
	if err != nil || d.Conversation.Status != "idle" {
		t.Fatalf("state=%+v %v", d, err)
	}
	assertPrivateLogs(t, f)
}

// 编译期确保测试夹具使用标准 handler，不依赖运行中的应用实例。
var _ http.Handler = (*http.ServeMux)(nil)

func (f *apiFixture) replaceRunner(runner *runtime.Runner) {
	f.api.runner = runner
	f.api.WithSupervisor(runtime.NewSupervisor(context.Background(), runner, f.api.logger))
}
