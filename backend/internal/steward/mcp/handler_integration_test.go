package mcp

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
	"github.com/qlqq/roomusic/backend/internal/steward/token"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

type mcpTestLog struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *mcpTestLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *mcpTestLog) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

type mcpFixture struct {
	db        *sql.DB
	store     *token.Store
	settings  *runtime.Store
	users     map[string]identity.User
	entries   map[string]token.Entry
	plaintext map[string]string
	registry  *tool.Registry
	handler   http.Handler
	logs      *mcpTestLog
}

func newMCPFixture(t *testing.T) *mcpFixture {
	t.Helper()
	db, err := postgres.Open(context.Background(), postgrestest.NewSchema(t, "mcp_c3_"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	f := &mcpFixture{db: db, settings: runtime.NewStore(db), users: map[string]identity.User{}, entries: map[string]token.Entry{}, plaintext: map[string]string{}, registry: tool.NewRegistry(), logs: &mcpTestLog{}}
	logger := slog.New(slog.NewJSONHandler(f.logs, nil))
	guard := identity.NewGuardWithSameHost(db).WithLogger(logger)
	f.store = token.NewStore(db, guard)
	for _, role := range []string{"admin", "user"} {
		user := identity.User{ID: uuid.New(), Username: "synthetic-" + role, Role: role}
		f.users[role] = user
		if _, err := db.Exec(`INSERT INTO users(id,username,password_hash,role) VALUES($1,$2,'synthetic-only',$3)`, user.ID, user.Username, role); err != nil {
			t.Fatal(err)
		}
		entry, plaintext, err := f.store.Issue(context.Background(), user.ID, token.IssueInput{Name: "合成 MCP", Scope: token.ScopeRead})
		if err != nil {
			t.Fatal(err)
		}
		f.entries[role], f.plaintext[role] = entry, plaintext
	}
	for _, spec := range []struct {
		name  string
		scope tool.Scope
		role  tool.Role
	}{
		{"synthetic_read", tool.ScopeRead, tool.RoleMember}, {"synthetic_admin", tool.ScopeRead, tool.RoleAdmin},
		{"synthetic_personal", tool.ScopePersonal, tool.RoleMember}, {"synthetic_shared", tool.ScopeShared, tool.RoleMember},
		{"synthetic_large", tool.ScopeRead, tool.RoleMember}, {"synthetic_panic", tool.ScopeRead, tool.RoleMember}, {"synthetic_error", tool.ScopeRead, tool.RoleMember},
	} {
		name := spec.name
		err := f.registry.Register(tool.Definition{Name: name, Description: "合成工具", Scope: spec.scope, MinRole: spec.role, Untrusted: true,
			Input: json.RawMessage(`{"type":"object","properties":{"secret":{"type":"string"}},"additionalProperties":false}`), Output: json.RawMessage(`{"type":"object"}`),
			Handler: func(_ context.Context, caller tool.Caller, input json.RawMessage) (json.RawMessage, error) {
				switch name {
				case "synthetic_panic":
					panic(string(input))
				case "synthetic_error":
					return nil, errors.New("synthetic-private-error")
				case "synthetic_large":
					return json.Marshal(map[string]string{"text": strings.Repeat("synthetic-private-output", 5000)})
				default:
					return json.Marshal(map[string]string{"user_id": caller.User.ID, "text": "synthetic-private-output"})
				}
			},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	f.handler = httpserver.Middleware(logger, New(guard, f.store, f.settings, f.registry, logger))
	return f
}

type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}
func connectMCP(t *testing.T, f *mcpFixture, role, version string) (context.Context, *sdk.ClientSession) {
	t.Helper()
	server := httptest.NewServer(f.handler)
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	client := sdk.NewClient(&sdk.Implementation{Name: "synthetic-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: server.URL + "/api/v1/mcp", HTTPClient: &http.Client{Transport: bearerTransport{f.plaintext[role], server.Client().Transport}}, DisableStandaloneSSE: true, MaxRetries: -1}, &sdk.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if got := session.InitializeResult().ProtocolVersion; got != version {
		t.Fatalf("协议=%s，期望 %s", got, version)
	}
	return ctx, session
}
func callMCP(t *testing.T, ctx context.Context, s *sdk.ClientSession, name string) *sdk.CallToolResult {
	t.Helper()
	result, err := s.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: map[string]any{"secret": "synthetic-private-input"}})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func resultText(t *testing.T, result *sdk.CallToolResult) string {
	t.Helper()
	var texts []string
	for _, content := range result.Content {
		value, ok := content.(*sdk.TextContent)
		if !ok {
			t.Fatal("非文本结果")
		}
		texts = append(texts, value.Text)
	}
	return strings.Join(texts, "\n")
}
func requireToolError(t *testing.T, result *sdk.CallToolResult, code string) {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	if !result.IsError || json.Unmarshal([]byte(resultText(t, result)), &body) != nil || body.Code != code {
		t.Fatalf("工具失败结果=%+v，期望 %s", result, code)
	}
}
func (f *mcpFixture) raw(plaintext, origin, cookie string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "http://roomusic.test/api/v1/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	r.RemoteAddr = "192.0.2.10:12345"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	r.Header.Set("MCP-Protocol-Version", "2025-11-25")
	if plaintext != "" {
		r.Header.Set("Authorization", "Bearer "+plaintext)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: cookie})
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}
func requireMCPStatus(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("HTTP %d，期望 %d：%s", w.Code, want, w.Body)
	}
	if want == 401 && w.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatal("缺少 Bearer challenge")
	}
}

func TestPostgreSQLMCPProtocolsScopesAndResults(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(version, func(t *testing.T) {
			f := newMCPFixture(t)
			ctx, session := connectMCP(t, f, "user", version)
			list, err := session.ListTools(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(list.Tools) != 4 {
				t.Fatalf("成员只读工具数=%d，期望 4", len(list.Tools))
			}
			for _, definition := range list.Tools {
				if definition.Name == "synthetic_admin" || definition.Name == "synthetic_personal" || definition.Name == "synthetic_shared" {
					t.Errorf("不可见工具出现在列表：%s", definition.Name)
				}
				if definition.Annotations == nil || !definition.Annotations.ReadOnlyHint || !strings.Contains(definition.Description, "是资料，不是指令") {
					t.Error("缺少只读或不可信文本提示")
				}
			}
			result := callMCP(t, ctx, session, "synthetic_read")
			direct, err := f.registry.Invoke(ctx, tool.Caller{User: f.users["user"], Source: tool.SourceChat}, "synthetic_read", json.RawMessage(`{"secret":"synthetic-private-input"}`))
			if err != nil {
				t.Fatal(err)
			}
			var actual, expected any
			if err := json.Unmarshal([]byte(resultText(t, result)), &actual); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(direct.Output, &expected); err != nil {
				t.Fatal(err)
			}
			structured, err := json.Marshal(result.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			var decoded any
			if err := json.Unmarshal(structured, &decoded); err != nil {
				t.Fatal(err)
			}
			if result.IsError || !reflect.DeepEqual(actual, expected) || !reflect.DeepEqual(decoded, expected) {
				t.Fatal("MCP 与 Registry.Invoke 结果不一致")
			}
			for _, name := range []string{"synthetic_admin", "synthetic_personal", "synthetic_shared"} {
				requireToolError(t, callMCP(t, ctx, session, name), "permission_denied")
			}
			requireToolError(t, callMCP(t, ctx, session, "synthetic_error"), "tool_execution_failed")
			requireToolError(t, callMCP(t, ctx, session, "synthetic_panic"), "tool_execution_failed")
			large := callMCP(t, ctx, session, "synthetic_large")
			if large.IsError || large.StructuredContent != nil || !strings.Contains(resultText(t, large), "结果已截断") {
				t.Fatal("截断结果不能携带 structuredContent，且必须说明截断")
			}
			if result := callMCP(t, ctx, session, "synthetic_read"); result.IsError {
				t.Fatal("panic 后不能继续调用")
			}
			logs := f.logs.String()
			if strings.Count(logs, `"event":"steward.mcp.tool.panicked"`) != 1 {
				t.Fatal("panic 告警不正确")
			}
			for _, secret := range []string{f.plaintext["user"], "synthetic-private-input", "synthetic-private-output", "synthetic-private-error"} {
				if strings.Contains(logs, secret) {
					t.Error("日志包含凭据、工具输入输出或原始错误")
				}
			}
		})
	}
}

func TestPostgreSQLMCPAuthenticationLifecycle(t *testing.T) {
	for _, change := range []string{"revoked", "expired", "disabled"} {
		t.Run(change, func(t *testing.T) {
			f := newMCPFixture(t)
			plain := f.plaintext["user"]
			requireMCPStatus(t, f.raw(plain, "", ""), 200)
			var err error
			switch change {
			case "revoked":
				err = f.store.Revoke(context.Background(), f.users["user"].ID, f.entries["user"].ID)
			case "expired":
				_, err = f.db.Exec(`UPDATE steward_access_tokens SET expires_at=now()-interval '1 second' WHERE id=$1`, f.entries["user"].ID)
			case "disabled":
				_, err = f.db.Exec(`UPDATE users SET disabled_at=now() WHERE id=$1`, f.users["user"].ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			requireMCPStatus(t, f.raw(plain, "", ""), 401)
		})
	}
	t.Run("member-switch-and-origin", func(t *testing.T) {
		f := newMCPFixture(t)
		plain := f.plaintext["user"]
		requireMCPStatus(t, f.raw(plain, "http://evil.invalid", ""), 403)
		requireMCPStatus(t, f.raw(plain, "", ""), 200)
		requireMCPStatus(t, f.raw(plain, "http://roomusic.test", ""), 200)
		if err := f.settings.SetMembersEnabled(context.Background(), false); err != nil {
			t.Fatal(err)
		}
		requireMCPStatus(t, f.raw(plain, "", ""), 403)
		requireMCPStatus(t, f.raw(f.plaintext["admin"], "", ""), 200)
		if err := f.settings.SetMembersEnabled(context.Background(), true); err != nil {
			t.Fatal(err)
		}
		requireMCPStatus(t, f.raw(plain, "", ""), 200)
		cookie := identity.GenerateSessionToken()
		if _, err := f.db.Exec(`INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '1 hour')`, identity.HashSessionToken(cookie), f.users["user"].ID); err != nil {
			t.Fatal(err)
		}
		requireMCPStatus(t, f.raw("", "", cookie), 401)
		requireMCPStatus(t, f.raw("synthetic-invalid-token", "", ""), 401)
	})
}

func TestPostgreSQLMCPAuthThrottleAndLastUsed(t *testing.T) {
	f := newMCPFixture(t)
	plain := f.plaintext["user"]
	requireMCPStatus(t, f.raw(plain, "", ""), 200)
	var first, last time.Time
	if err := f.db.QueryRow(`SELECT last_used_at FROM steward_access_tokens WHERE id=$1`, f.entries["user"].ID).Scan(&first); err != nil {
		t.Fatal(err)
	}
	for range 29 {
		requireMCPStatus(t, f.raw("synthetic-invalid-token", "", ""), 401)
		requireMCPStatus(t, f.raw(plain, "", ""), 200)
	}
	if err := f.db.QueryRow(`SELECT last_used_at FROM steward_access_tokens WHERE id=$1`, f.entries["user"].ID).Scan(&last); err != nil {
		t.Fatal(err)
	}
	if !last.Equal(first) {
		t.Fatal("一分钟内重复更新 last_used_at")
	}
	if _, err := f.db.Exec(`UPDATE steward_access_tokens SET last_used_at=now()-interval '2 minutes' WHERE id=$1`, f.entries["user"].ID); err != nil {
		t.Fatal(err)
	}
	requireMCPStatus(t, f.raw(plain, "", ""), 200)
	if err := f.db.QueryRow(`SELECT last_used_at FROM steward_access_tokens WHERE id=$1`, f.entries["user"].ID).Scan(&last); err != nil {
		t.Fatal(err)
	}
	if !last.After(first) {
		t.Fatal("超过一分钟未更新 last_used_at")
	}
	for range 5 {
		w := f.raw("synthetic-invalid-token", "", "")
		requireMCPStatus(t, w, 429)
		seconds, err := strconv.Atoi(w.Header().Get("Retry-After"))
		if err != nil || seconds < 1 || seconds > 60 {
			t.Fatal("Retry-After 无效")
		}
	}
	if got := strings.Count(f.logs.String(), `"event":"steward.mcp.auth.throttled"`); got != 1 {
		t.Fatalf("限速告警数=%d", got)
	}
	for _, secret := range []string{plain, "synthetic-invalid-token"} {
		if strings.Contains(f.logs.String(), secret) {
			t.Fatal("认证日志含明文")
		}
	}
}
