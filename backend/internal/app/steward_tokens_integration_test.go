package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	governancetools "github.com/qlqq/roomusic/backend/internal/catalog/governance/stewardtools"
	catalogtools "github.com/qlqq/roomusic/backend/internal/catalog/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/identity"
	identityapi "github.com/qlqq/roomusic/backend/internal/identity/httpapi"
	"github.com/qlqq/roomusic/backend/internal/library"
	librarytools "github.com/qlqq/roomusic/backend/internal/library/stewardtools"
	listeningtools "github.com/qlqq/roomusic/backend/internal/listening/stewardtools"
	operationtools "github.com/qlqq/roomusic/backend/internal/operations/stewardtools"
	personaltools "github.com/qlqq/roomusic/backend/internal/personal/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	stewardapi "github.com/qlqq/roomusic/backend/internal/steward/httpapi"
	"github.com/qlqq/roomusic/backend/internal/steward/memory"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
	"github.com/qlqq/roomusic/backend/internal/steward/secret"
	"github.com/qlqq/roomusic/backend/internal/steward/token"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
	"github.com/qlqq/roomusic/backend/internal/steward/web"
	zonestools "github.com/qlqq/roomusic/backend/internal/zones/stewardtools"
)

type c3AppFixture struct {
	db       *sql.DB
	mux      *http.ServeMux
	users    map[string]identity.User
	cookies  map[string]string
	tokens   *token.Store
	registry *tool.Registry
}

func newC3AppFixture(t *testing.T) *c3AppFixture {
	t.Helper()
	ctx := context.Background()
	db, err := postgres.Open(ctx, postgrestest.NewSchema(t, "steward_c3_app_"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	base := filepath.Join("..", "..", "..", "temp")
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(base, "steward-c3-app-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	f := &c3AppFixture{db: db, mux: http.NewServeMux(), users: map[string]identity.User{}, cookies: map[string]string{}, registry: tool.NewRegistry()}
	for _, role := range []string{"admin", "user"} {
		user := identity.User{ID: uuid.New(), Username: "synthetic-" + role, Role: role}
		cookie := identity.GenerateSessionToken()
		f.users[role], f.cookies[role] = user, cookie
		if _, err := db.Exec(`INSERT INTO users(id,username,password_hash,role) VALUES($1,$2,'synthetic-only',$3)`, user.ID, user.Username, user.Role); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '1 hour')`, identity.HashSessionToken(cookie), user.ID); err != nil {
			t.Fatal(err)
		}
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	guard := identity.NewGuardWithSameHost(db)
	f.tokens = token.NewStore(db, guard)
	scans := library.NewService(ctx, db, dir, logger)
	registerSteward(ctx, f.mux, db, guard, newInstanceSecrets(dir), logger, scans, nil, nil, nil, nil)
	identityapi.New(db, guard, nil, logger).Register(f.mux)
	// 对照注册表使用同一组业务定义，HTTP 一侧必须经过真实 registerSteward 装配。
	definitions := catalogtools.StewardTools(catalog.NewStore(db), governance.NewStore(db))
	definitions = append(definitions, librarytools.StewardTools(scans)...)
	definitions = append(definitions, operationtools.StewardTools(db)...)
	definitions = append(definitions, governancetools.StewardTools(db, nil)...)
	definitions = append(definitions, operationtools.RevertTools(db)...)
	definitions = append(definitions, zonestools.StewardTools(nil, catalog.NewStore(db))...)
	definitions = append(definitions, listeningtools.StewardTools(nil)...)
	definitions = append(definitions, personaltools.StewardTools(nil)...)
	definitions = append(definitions, memory.NewStore(db).StewardTools()...)
	webSettings := runtime.NewStore(db)
	definitions = append(definitions, web.New(webSettings, secret.New(filepath.Join(dir, "steward", "instance.key")), func(ctx context.Context, user identity.User) (*http.Client, error) {
		return stewardapi.OutboundClient(ctx, user, webSettings)
	}, logger).StewardTools()...)
	for _, definition := range definitions {
		if err := f.registry.Register(definition); err != nil {
			t.Fatal(err)
		}
	}
	return f
}
func (f *c3AppFixture) request(role, method, path, bearer string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(method, "http://roomusic.test/api/v1/"+path, bytes.NewReader(raw))
	r.Header.Set("Origin", "http://roomusic.test")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Request-ID", sampleRequestID)
	if f.cookies[role] != "" {
		r.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: f.cookies[role]})
	}
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	return w
}

type c3BearerTransport struct {
	plain string
	base  http.RoundTripper
}

func (b c3BearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.plain)
	return b.base.RoundTrip(r)
}

func TestPostgreSQLMCPRealRegistryProtocolsAndIsolation(t *testing.T) {
	f := newC3AppFixture(t)
	server := httptest.NewServer(f.mux)
	defer server.Close()
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		for _, tc := range []struct {
			role  string
			scope token.Scope
		}{
			{"admin", token.ScopeRead},
			{"admin", token.ScopeReadWrite},
			{"user", token.ScopeRead},
			{"user", token.ScopeReadWrite},
		} {
			role, scope := tc.role, tc.scope
			t.Run(version+"/"+role+"/"+string(scope), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				_, plain, err := f.tokens.Issue(ctx, f.users[role].ID, token.IssueInput{Name: "合成协议测试", Scope: scope})
				if err != nil {
					t.Fatal(err)
				}
				client := sdk.NewClient(&sdk.Implementation{Name: "synthetic-app-test", Version: "1"}, nil)
				session, err := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: server.URL + "/api/v1/mcp", HTTPClient: &http.Client{Transport: c3BearerTransport{plain, server.Client().Transport}}, DisableStandaloneSSE: true, MaxRetries: -1}, &sdk.ClientSessionOptions{ProtocolVersion: version})
				if err != nil {
					t.Fatal(err)
				}
				defer session.Close()
				if session.InitializeResult().ProtocolVersion != version {
					t.Fatal("未使用指定协议")
				}
				listed, err := session.ListTools(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				caller := tool.Caller{User: f.users[role], Source: tool.SourceChat}
				all := f.registry.List(caller)
				if role == "admin" && len(all) != 56 {
					t.Fatalf("实际登记工具数=%d，期望 56", len(all))
				}
				readWrite := scope == token.ScopeReadWrite
				var want []tool.Definition
				for _, d := range all {
					if d.Scope == tool.ScopeRead || (d.Scope == tool.ScopePersonal && d.ReadOnly) ||
						(readWrite && (d.Scope == tool.ScopePersonal || d.Scope == tool.ScopeShared)) {
						want = append(want, d)
					}
				}
				names := map[string]bool{}
				for _, definition := range listed.Tools {
					names[definition.Name] = true
				}
				if wantVisible := role == "admin" && readWrite; names["correct_track"] != wantVisible {
					t.Errorf("correct_track 可见=%t，期望 %t", names["correct_track"], wantVisible)
				}
				if len(listed.Tools) != len(want) {
					t.Fatalf("可见工具数=%d，注册表=%d", len(listed.Tools), len(want))
				}
				for _, definition := range want {
					if !names[definition.Name] {
						t.Errorf("缺少 %s", definition.Name)
					}
				}
				direct, err := f.registry.Invoke(ctx, caller, "list_albums", json.RawMessage(`{}`))
				if err != nil {
					t.Fatal(err)
				}
				result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "list_albums", Arguments: map[string]any{}})
				if err != nil {
					t.Fatal(err)
				}
				var expected, actual any
				if err := json.Unmarshal(direct.Output, &expected); err != nil {
					t.Fatal(err)
				}
				if len(result.Content) != 1 {
					t.Fatal("工具结果不完整")
				}
				text, ok := result.Content[0].(*sdk.TextContent)
				if !ok {
					t.Fatal("结果不是文本")
				}
				if err := json.Unmarshal([]byte(text.Text), &actual); err != nil {
					t.Fatal(err)
				}
				if result.IsError || !reflect.DeepEqual(expected, actual) {
					t.Fatal("真实工具两个入口输出不同")
				}
				if role == "user" {
					for _, definition := range f.registry.List(tool.Caller{User: f.users["admin"]}) {
						if definition.MinRole != tool.RoleAdmin {
							continue
						}
						if names[definition.Name] {
							t.Errorf("成员能看到 %s", definition.Name)
						}
						_, directErr := f.registry.Invoke(ctx, caller, definition.Name, json.RawMessage(`{}`))
						var denied *tool.Error
						if !errors.As(directErr, &denied) || denied.Code != tool.CodePermissionDenied {
							t.Fatal("注册表未拒绝管理员工具")
						}
						result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: definition.Name, Arguments: map[string]any{}})
						if err != nil {
							t.Fatal(err)
						}
						if !result.IsError || len(result.Content) != 1 || !strings.Contains(result.Content[0].(*sdk.TextContent).Text, `"code":"permission_denied"`) {
							t.Fatal("MCP 未返回 permission_denied")
						}
					}
				}
				if w := f.request(role, "GET", "auth/me", "", nil); w.Code != 200 {
					t.Fatalf("测试 Cookie 无效：%d", w.Code)
				}
				if w := f.request(role, "POST", "mcp", "", nil); w.Code != 401 || w.Header().Get("WWW-Authenticate") != "Bearer" {
					t.Fatalf("Cookie 进入 MCP：%d", w.Code)
				}
				for _, endpoint := range []struct{ method, path string }{
					{"GET", "auth/me"}, {"GET", "users"},
					{"GET", "me/steward/settings"}, {"GET", "admin/steward/settings"},
					{"GET", "me/steward/tokens"}, {"POST", "me/steward/tokens"}, {"DELETE", "me/steward/tokens/" + uuid.New()},
				} {
					w := f.request("", endpoint.method, endpoint.path, plain, map[string]any{"name": "合成", "scope": "read"})
					if w.Code != 401 {
						t.Fatalf("Bearer 访问 %s：%d", endpoint.path, w.Code)
					}
				}
			})
		}
	}
}

func TestPostgreSQLStewardTokenAPIResponseSamples(t *testing.T) {
	f := newC3AppFixture(t)
	samples := &apiSampleRecorder{scope: "steward/tokens", t: t, samples: map[string][]byte{}, forbidden: []string{f.cookies["admin"], f.cookies["user"], identity.SessionCookieName}}
	created := f.request("user", "POST", "me/steward/tokens", "", map[string]any{"name": "合成客户端", "scope": "read"})
	if created.Code != 201 {
		t.Fatalf("签发：%d %s", created.Code, created.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(created.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	id, plain := body["id"].(string), body["token"].(string)
	// 随机凭据只用于本次隔离测试；持久样本用 32 个固定合成字节生成，保留实际格式和末四位关系。
	synthetic := "rmt_" + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	normalize := func(w *httptest.ResponseRecorder) {
		value := strings.ReplaceAll(w.Body.String(), `"token":"`+plain+`"`, `"token":"`+synthetic+`"`)
		value = strings.ReplaceAll(value, `"tail":"`+plain[len(plain)-4:]+`"`, `"tail":"`+synthetic[len(synthetic)-4:]+`"`)
		w.Body.Reset()
		w.Body.WriteString(value)
	}
	normalize(created)
	samples.record("steward/tokens/issued", created, 201)
	listed := f.request("user", "GET", "me/steward/tokens", "", nil)
	normalize(listed)
	samples.record("steward/tokens/list", listed, 200)
	revoked := f.request("user", "DELETE", "me/steward/tokens/"+id, "", nil)
	samples.record("steward/tokens/revoked", revoked, 204)
	if len(samples.samples) != 3 {
		t.Fatal("令牌样本覆盖不完整")
	}
	samples.finish()
}

func TestStewardTokenAPISampleOwnership(t *testing.T) {
	for _, scope := range []string{"", "steward", "steward/tokens", "personal"} {
		r := &apiSampleRecorder{scope: scope}
		if r.ownsSample("steward/tokens/issued.json") != (scope == "steward/tokens") {
			t.Errorf("令牌样本归属：%s", scope)
		}
		if r.ownsSample("steward/settings-empty.json") != (scope == "steward") {
			t.Errorf("C2 样本归属：%s", scope)
		}
	}
}
