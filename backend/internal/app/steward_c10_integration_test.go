package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
	"github.com/qlqq/roomusic/backend/internal/steward/token"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

type c10BlockingModel struct{ started chan struct{} }

func (p c10BlockingModel) Stream(ctx context.Context, _ provider.Request) (provider.Stream, error) {
	close(p.started)
	<-ctx.Done()
	return nil, ctx.Err()
}
func c10AppRun(t *testing.T, a *integrationTestApplication, user integrationTestUser, role string) (string, *runtime.Subscription) {
	t.Helper()
	store := runtime.NewStore(a.database)
	c, err := store.CreateConversation(context.Background(), user.id, "取消钩子合成会话", "")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	run, err := a.app.steward.Start(context.Background(), runtime.RunParams{Caller: tool.Caller{User: identity.User{ID: user.id, Role: role}, Source: tool.SourceChat}, ConversationID: c.ID, Text: "等待取消", Model: "synthetic", Provider: c10BlockingModel{started}})
	if err != nil {
		t.Fatal(err)
	}
	sub := run.Subscribe(0)
	t.Cleanup(sub.Close)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("模型未启动")
	}
	return c.ID, sub
}
func TestPostgreSQLStewardC10CancellationHooks(t *testing.T) {
	for _, name := range []string{"logout", "disable_http", "revoke_http", "change_password", "reset_password", "disable_tool", "revoke_tool", "members_switch", "endpoint", "key_change", "key_clear", "probe_save"} {
		t.Run(name, func(t *testing.T) {
			a := newIntegrationTestApplication(t)
			t.Cleanup(func() {
				if err := a.app.ShutdownSteward(context.Background()); err != nil {
					t.Error(err)
				}
			})
			admin := a.createUser(t, "admin")
			user := a.createUser(t, "user")
			call := func(actor *integrationTestUser, method, path string, body any) *httptest.ResponseRecorder {
				return a.request(t, method, "/api/v1/"+path, body, actor, nil)
			}
			must := func(w *httptest.ResponseRecorder) {
				t.Helper()
				if w.Code != 200 {
					t.Fatalf("HTTP %d: %s", w.Code, w.Body)
				}
			}
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"data":[{"id":"synthetic"}]}`) }))
			defer endpoint.Close()
			parts := strings.Split(strings.TrimPrefix(endpoint.URL, "http://"), ":")
			var port int
			if err := json.Unmarshal([]byte(parts[1]), &port); err != nil {
				t.Fatal(err)
			}
			must(call(&admin, "POST", "admin/steward/egress-allowlist", map[string]any{"host": parts[0], "port": port}))
			must(call(&user, "PUT", "me/steward/settings", map[string]any{"endpoint": endpoint.URL, "protocol": "openai", "model": "synthetic", "key": "synthetic-c10-key"}))
			if name == "change_password" {
				hash, err := identity.HashPassword("synthetic-old-123")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := a.database.Exec(`UPDATE users SET password_hash=$2 WHERE id=$1`, user.id, hash); err != nil {
					t.Fatal(err)
				}
			}
			id, sub := c10AppRun(t, a, user, "user")
			_, other := c10AppRun(t, a, admin, "admin")
			switch name {
			case "logout":
				must(call(&user, "POST", "auth/logout", nil))
			case "disable_http":
				must(call(&admin, "PATCH", "users/"+user.id, map[string]bool{"disabled": true}))
			case "revoke_http":
				must(call(&admin, "POST", "users/"+user.id+"/sessions/revoke", nil))
			case "change_password":
				must(call(&user, "POST", "auth/password", map[string]string{"current_password": "synthetic-old-123", "new_password": "synthetic-new-123"}))
			case "reset_password":
				must(call(&admin, "POST", "users/"+strings.ToUpper(user.id)+"/password", map[string]string{"password": "synthetic-reset-123"}))
			case "members_switch":
				must(call(&admin, "PUT", "admin/steward/settings", map[string]bool{"members_enabled": false}))
			case "endpoint":
				must(call(&user, "PUT", "me/steward/settings", map[string]string{"endpoint": endpoint.URL + "/v2"}))
			case "key_change":
				must(call(&user, "PUT", "me/steward/settings", map[string]string{"key": "synthetic-changed-key"}))
			case "key_clear":
				must(call(&user, "PUT", "me/steward/settings", map[string]any{"key": nil}))
			case "probe_save":
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == "GET" {
						io.WriteString(w, `{"data":[{"id":"synthetic"}]}`)
						return
					}
					io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"probe\",\"function\":{\"name\":\"roomusic_probe\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
				}))
				defer server.Close()
				parts := strings.Split(strings.TrimPrefix(server.URL, "http://"), ":")
				var port int
				json.Unmarshal([]byte(parts[1]), &port)
				must(call(&admin, "POST", "admin/steward/egress-allowlist", map[string]any{"host": parts[0], "port": port}))
				must(call(&user, "POST", "me/steward/settings/probe", map[string]string{"endpoint": server.URL, "key": "synthetic-probed-key"}))
			case "disable_tool", "revoke_tool":
				guard := identity.NewGuardWithSameHost(a.database)
				_, plain, err := token.NewStore(a.database, guard).Issue(context.Background(), admin.id, token.IssueInput{Name: "C10 合成", Scope: token.ScopeReadWrite})
				if err != nil {
					t.Fatal(err)
				}
				server := httptest.NewServer(a.handler)
				defer server.Close()
				client := sdk.NewClient(&sdk.Implementation{Name: "c10-test", Version: "1"}, nil)
				session, err := client.Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: server.URL + "/api/v1/mcp", HTTPClient: &http.Client{Transport: c3BearerTransport{plain, server.Client().Transport}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer session.Close()
				nameTool := "revoke_member_sessions"
				input := map[string]any{"member_id": user.id}
				if name == "disable_tool" {
					nameTool = "set_member_disabled"
					input["disabled"] = true
				}
				c9MCPCall(t, session, nameTool, input, "")
			}
			timeout := time.NewTimer(3 * time.Second)
			defer timeout.Stop()
			finished := false
			for !finished {
				select {
				case e, ok := <-sub.Events:
					if !ok {
						t.Fatal("缺少取消事件")
					}
					if e.Type == runtime.EventDone {
						if e.End == nil || e.End.Reason != runtime.EndCancelled {
							t.Fatal(e)
						}
						finished = true
					}
				case <-timeout.C:
					t.Fatal("取消钩子未终止运行")
				}
			}
			select {
			case e := <-other.Events:
				t.Fatalf("取消钩子误伤管理员：%+v", e)
			default:
			}
			detail, err := runtime.NewStore(a.database).GetConversation(context.Background(), user.id, id)
			if err != nil || detail.Conversation.Status != "idle" {
				t.Fatal(detail, err)
			}
		})
	}
}

func TestPostgreSQLStewardC10F6StaleLogout(t *testing.T) {
	for _, kind := range []string{"revoked", "expired"} {
		t.Run(kind, func(t *testing.T) {
			a := newIntegrationTestApplication(t)
			t.Cleanup(func() {
				if err := a.app.ShutdownSteward(context.Background()); err != nil {
					t.Error(err)
				}
			})
			old := a.createUser(t, "user")
			logout := func(actor *integrationTestUser) {
				t.Helper()
				w := a.request(t, "POST", "/api/v1/auth/logout", nil, actor, nil)
				if w.Code != 200 || !strings.Contains(w.Body.String(), `"logged_out":true`) {
					t.Fatalf("%d %s", w.Code, w.Body)
				}
				if len(w.Result().Cookies()) == 0 || w.Result().Cookies()[0].MaxAge >= 0 {
					t.Fatal("未清 Cookie")
				}
			}
			if kind == "revoked" {
				logout(&old)
			} else {
				if _, err := a.database.Exec(`UPDATE sessions SET expires_at=NOW()-INTERVAL '1 second' WHERE token_hash=$1`, identity.HashSessionToken(old.token)); err != nil {
					t.Fatal(err)
				}
			}
			current := integrationTestUser{id: old.id, token: identity.GenerateSessionToken()}
			if _, err := a.database.Exec(`INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,NOW()+INTERVAL '1 hour')`, identity.HashSessionToken(current.token), current.id); err != nil {
				t.Fatal(err)
			}
			store := runtime.NewStore(a.database)
			c, err := store.CreateConversation(context.Background(), current.id, "旧 Cookie 合成", "")
			if err != nil {
				t.Fatal(err)
			}
			observed := make(chan context.Context, 1)
			run, err := a.app.steward.Start(context.Background(), runtime.RunParams{Caller: tool.Caller{User: identity.User{ID: current.id, Role: "user"}, Source: tool.SourceChat}, ConversationID: c.ID, Text: "等待", Model: "synthetic", Provider: c10LogoutModel{observed}})
			if err != nil {
				t.Fatal(err)
			}
			sub := run.Subscribe(0)
			defer sub.Close()
			runContext := <-observed
			logout(&old)
			if runContext.Err() != nil {
				t.Fatal("旧 Cookie 同步取消新运行", runContext.Err())
			}
			select {
			case e := <-sub.Events:
				t.Fatalf("旧 Cookie 取消新运行：%+v", e)
			default:
			}
			// 再次读正常受保护接口，证明新 Cookie 仍有效。
			w := a.request(t, "GET", "/api/v1/me/steward/conversations", nil, &current, nil)
			if w.Code != 200 {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			logout(&current)
			timer := time.NewTimer(3 * time.Second)
			defer timer.Stop()
			for {
				select {
				case e, ok := <-sub.Events:
					if !ok {
						t.Fatal("有效 Cookie 登出无 done")
					}
					if e.Type == runtime.EventDone {
						if e.End == nil || e.End.Reason != runtime.EndCancelled {
							t.Fatal(e)
						}
						return
					}
				case <-timer.C:
					t.Fatal("有效 Cookie 未取消")
				}
			}
		})
	}
}

type c10LogoutModel struct{ observed chan context.Context }

func (p c10LogoutModel) Stream(ctx context.Context, _ provider.Request) (provider.Stream, error) {
	p.observed <- ctx
	<-ctx.Done()
	return nil, ctx.Err()
}
