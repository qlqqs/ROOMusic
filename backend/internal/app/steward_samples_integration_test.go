package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/library"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

// 管家样本直接装配当前功能，不启动扫描、后台维护或任何现有实例。
func TestPostgreSQLStewardAPIResponseSamples(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := postgres.Open(ctx, postgrestest.NewSchema(t, "steward_samples_"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	base := filepath.Join("..", "..", "..", "temp")
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(base, "steward-samples-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	users, tokens := map[string]string{}, map[string]string{}
	for _, role := range []string{"admin", "user"} {
		id, token := uuid.New(), identity.GenerateSessionToken()
		if _, err := db.ExecContext(ctx, `INSERT INTO users(id,username,password_hash,role) VALUES($1,$2,'synthetic-only',$2)`, id, role); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,NOW()+INTERVAL '1 hour')`, identity.HashSessionToken(token), id); err != nil {
			t.Fatal(err)
		}
		users[role], tokens[role] = id, token
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mux := http.NewServeMux()
	registerSteward(ctx, mux, db, identity.NewGuard(db, false, ""), dir, logger, library.NewService(ctx, db, dir, logger), nil, nil, nil, nil)
	var mode atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			_, _ = io.WriteString(w, `{"data":[{"id":"synthetic-model"}]}`)
			return
		}
		switch mode.Load() {
		case 1:
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"error":{"message":"model does not support tools"}}`)
		case 3:
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"合成回答\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		case 4:
			w.WriteHeader(504)
			_, _ = io.WriteString(w, `{"error":{"message":"synthetic timeout"}}`)
		case 2:
			w.WriteHeader(401)
			_, _ = io.WriteString(w, `{"error":{"message":"synthetic-sample-model-key"}}`)
		default:
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"sample-call\",\"function\":{\"name\":\"roomusic_probe\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		}
	}))
	defer server.Close()
	call := func(role, method, path string, body any) *httptest.ResponseRecorder {
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, "http://roomusic.test/api/v1/"+path, bytes.NewReader(raw)).WithContext(ctx)
		r.Header.Set("Origin", "http://roomusic.test")
		r.Header.Set("X-Request-ID", sampleRequestID)
		if tokens[role] != "" {
			r.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: tokens[role]})
		}
		w := httptest.NewRecorder()
		w.Header().Set("X-Request-ID", sampleRequestID)
		mux.ServeHTTP(w, r)
		return w
	}
	samples := &apiSampleRecorder{scope: "steward", t: t, samples: map[string][]byte{}, forbidden: []string{dir, "synthetic-sample-model-key", tokens["admin"], tokens["user"], identity.SessionCookieName}}
	record := func(name string, w *httptest.ResponseRecorder, status int) {
		t.Helper() // 随机本地监听端口不属于接口合同，样本固定成合成地址。
		body := strings.ReplaceAll(w.Body.String(), server.URL, "http://127.0.0.1:12345")
		w.Body.Reset()
		_, _ = w.Body.WriteString(body)
		samples.record("steward/"+name, w, status)
	}
	record("unauthorized", call("", "GET", "me/steward/settings", nil), 401)
	record("settings-empty", call("admin", "GET", "me/steward/settings", nil), 200)
	record("not-configured", call("admin", "POST", "me/steward/conversations", map[string]string{}), 409)
	record("settings-name", call("admin", "PUT", "me/steward/settings", map[string]string{"display_name": "合成管家"}), 200)
	record("settings-saved", call("admin", "PUT", "me/steward/settings", map[string]string{"endpoint": server.URL, "key": "synthetic-sample-model-key", "model": "synthetic-model", "protocol": "openai", "review_model": "synthetic-review-model"}), 200)
	record("probe-supported", call("admin", "POST", "me/steward/settings/probe", map[string]string{}), 200)
	mode.Store(1)
	record("probe-unsupported", call("admin", "POST", "me/steward/settings/probe", map[string]string{}), 200)
	record("provider-no-tool-support", call("admin", "POST", "me/steward/conversations", map[string]string{}), 422)
	mode.Store(2)
	record("provider-unreachable", call("admin", "POST", "me/steward/settings/probe", map[string]string{}), 503)
	mode.Store(4)
	record("provider-timeout", call("admin", "POST", "me/steward/settings/probe", map[string]string{}), 503)
	mode.Store(0)
	if w := call("admin", "POST", "me/steward/settings/probe", map[string]string{}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	record("egress-denied", call("user", "POST", "me/steward/settings/probe", map[string]string{"endpoint": server.URL, "key": "synthetic-sample-model-key", "model": "synthetic-model"}), 403)
	record("egress-denied-settings", call("user", "PUT", "me/steward/settings", map[string]string{"endpoint": server.URL, "model": "synthetic-model", "protocol": "openai"}), 403)
	created := call("admin", "POST", "me/steward/conversations", map[string]string{"title": "合成对话"})
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &c); err != nil || c.ID == "" {
		t.Fatalf("create=%s err=%v", created.Body, err)
	}
	record("conversation-created", created, 201)
	path := "me/steward/conversations/" + c.ID
	record("conversations", call("admin", "GET", "me/steward/conversations", nil), 200)
	record("continue-nothing", call("admin", "POST", path+"/continue", nil), 409)
	// C5 的界面上下文只存在用户消息上，响应样本使用合成 UUID。
	store := runtime.NewStore(db)
	_, err = store.AppendMessage(ctx, users["admin"], c.ID, runtime.MessageInput{Role: "user",
		Content: json.RawMessage(`{"role":"user","content":[{"type":"text","text":"合成问题"}]}`),
		Context: json.RawMessage(`{"page":{"kind":"album","path":"/albums/11111111-1111-4111-8111-111111111111","id":"11111111-1111-4111-8111-111111111111","title":"合成专辑"},"playback":{"zone_id":"22222222-2222-4222-8222-222222222222","zone_name":"合成分区","status":"paused"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	record("conversation", call("admin", "GET", path, nil), 200)

	// C7：方案与最近审查使用真实持久化及 handler 生成样本。
	reviewConversation, err := store.CreateConversation(ctx, users["admin"], "合成审查方案", "synthetic-model")
	if err != nil {
		t.Fatal(err)
	}
	content, _ := json.Marshal(provider.Message{Role: provider.RoleAssistant, Content: []provider.ContentBlock{{Type: provider.BlockToolCall, ToolCall: &provider.ToolCall{ID: "sample-review-call", Name: "correct_release", Arguments: json.RawMessage(`{}`)}}}})
	message, err := store.AppendMessage(ctx, users["admin"], reviewConversation.ID, runtime.MessageInput{Role: "assistant", Content: content, ToolCalls: []runtime.ToolCallInput{{Name: "correct_release", Input: json.RawMessage(`{}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := store.CreatePlan(ctx, users["admin"], reviewConversation.ID, []runtime.PlanItem{{Tool: "correct_release", CallID: "sample-review-call", ToolCallID: message.ToolCalls[0].ID, Input: json.RawMessage(`{}`), Revertible: true, Preview: tool.Preview{Summary: "修正专辑标题", Target: "合成专辑", Changes: []tool.Change{{Field: "title", Before: "旧标题", After: "新标题"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	reviewPath := "me/steward/conversations/" + reviewConversation.ID
	record("conversation-plan", call("admin", "GET", reviewPath, nil), 200)
	review := runtime.Review{ID: uuid.New(), PlanID: plan.ID, PlanDigest: plan.Digest, EvidenceDigest: "synthetic-evidence-digest", PolicyVersion: 1, Model: "synthetic-review-model", Status: "completed", ExpiresAt: time.Now().UTC().Add(15 * time.Minute), Items: []runtime.ReviewItem{{ItemID: plan.Items[0].ID, Decision: "needs_human_confirmation", Risk: "medium", Reasons: []string{"证据不足，需要人工确认"}, Source: "model"}}}
	if err := store.SaveReview(ctx, users["admin"], reviewConversation.ID, review); err != nil {
		t.Fatal(err)
	}
	record("conversation-review-pending", call("admin", "GET", reviewPath, nil), 200)
	record("conversations-review-pending", call("admin", "GET", "me/steward/conversations", nil), 200)
	review.ID = uuid.New()
	review.Status = "unavailable"
	review.Items = []runtime.ReviewItem{}
	if err := store.SaveReview(ctx, users["admin"], reviewConversation.ID, review); err != nil {
		t.Fatal(err)
	}
	record("conversation-review-unavailable", call("admin", "GET", reviewPath, nil), 200)
	record("invalid-context", call("admin", "POST", path+"/messages", map[string]any{"text": "合成问题", "context": map[string]any{"page": map[string]any{"kind": "invalid"}}}), 400)

	record("conversation-renamed", call("admin", "PATCH", path, map[string]string{"title": "新标题"}), 200)
	record("conversation-model-updated", call("admin", "PATCH", path, map[string]string{"model": "synthetic-other-model"}), 200)
	record("conversation-model-default", call("admin", "PATCH", path, map[string]string{"model": ""}), 200)
	record("cancel-idle", call("admin", "POST", path+"/cancel", map[string]string{}), 200)
	if err := store.AcquireConversation(ctx, users["admin"], c.ID); err != nil {
		t.Fatal(err)
	}
	record("conversation-busy", call("admin", "POST", path+"/messages", map[string]string{"text": "合成问题"}), 409)
	if err := store.ReleaseConversation(ctx, users["admin"], c.ID); err != nil {
		t.Fatal(err)
	}
	record("conversation-deleted", call("admin", "DELETE", path, nil), 200)
	record("conversation-not-found", call("admin", "GET", path, nil), 404)
	record("instance-settings", call("admin", "GET", "admin/steward/settings", nil), 200)
	record("instance-members-disabled", call("admin", "PUT", "admin/steward/settings", map[string]bool{"members_enabled": false}), 200)
	record("disabled", call("user", "GET", "me/steward/settings", nil), 403)
	address := map[string]any{"host": "synthetic.invalid", "port": 12345}
	record("allowlist-added", call("admin", "POST", "admin/steward/egress-allowlist", address), 200)
	record("allowlist", call("admin", "GET", "admin/steward/egress-allowlist", nil), 200)
	record("allowlist-deleted", call("admin", "DELETE", "admin/steward/egress-allowlist", address), 200)
	record("settings-cleared-key", call("admin", "PUT", "me/steward/settings", map[string]any{"key": nil}), 200)
	mode.Store(3)
	record("probe-keyless-unconfirmed", call("admin", "POST", "me/steward/settings/probe", map[string]any{"key": nil}), 200)
	if len(samples.samples) != 36 {
		t.Fatalf("管家样本覆盖清单发生变化：%d", len(samples.samples))
	}
	samples.finish()
}
func TestStewardAPISampleOwnership(t *testing.T) {
	for _, scope := range []string{"", "personal", "scan-feedback", "steward"} {
		recorder := &apiSampleRecorder{scope: scope}
		if recorder.ownsSample("steward/settings-empty.json") != (scope == "steward") {
			t.Errorf("管家样本归属错误：scope=%q", scope)
		}
	}
}
