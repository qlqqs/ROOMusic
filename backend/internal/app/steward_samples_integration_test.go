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
	"github.com/qlqq/roomusic/backend/internal/steward/memory"
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
	supervisor := registerSteward(ctx, mux, db, identity.NewGuard(db, false, ""), dir, logger, library.NewService(ctx, db, dir, logger), nil, nil, nil, nil)
	var mode atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/search") {
			if mode.Load() == 6 {
				w.WriteHeader(403)
			}
			io.WriteString(w, `{"results":[]}`)
			return
		}
		if r.Method == "GET" {
			_, _ = io.WriteString(w, `{"data":[{"id":"synthetic-model"}]}`)
			return
		}
		switch mode.Load() {
		case 5:
			io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"合成联网回答\"}\n\n")
			io.WriteString(w, "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"web_search_call\",\"id\":\"sample-search\",\"status\":\"completed\",\"action\":{\"type\":\"search\",\"query\":\"合成音乐查询\",\"sources\":[{\"url\":\"https://example.org/music\",\"title\":\"合成来源\"}]}}}\n\n")
			io.WriteString(w, "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"output_index\":1,\"item\":{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"合成联网回答\",\"annotations\":[{\"type\":\"url_citation\",\"url\":\"https://example.org/music\",\"title\":\"合成来源\"}]}]}}\n\n")
			io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
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
	samples := &apiSampleRecorder{scope: "steward", t: t, samples: map[string][]byte{}, forbidden: []string{dir, "synthetic-sample-model-key", "synthetic-search-key-5678", tokens["admin"], tokens["user"], identity.SessionCookieName}}
	record := func(name string, w *httptest.ResponseRecorder, status int) {
		t.Helper() // 随机本地监听端口不属于接口合同，样本固定成合成地址。
		body := strings.ReplaceAll(w.Body.String(), server.URL, "http://127.0.0.1:12345")
		if strings.HasPrefix(name, "event-") {
			var event map[string]any
			if err := json.Unmarshal([]byte(body), &event); err != nil {
				t.Fatal(err)
			}
			seq, ok := event["seq"].(float64)
			if !ok || seq <= 0 || seq > 1<<53-1 || seq != float64(uint64(seq)) {
				t.Fatal("事件游标不是安全正整数", event["seq"])
			}
			// 单事件样本只展示载荷形状；启动时间游标归一为 1，真实顺序另由回放测试核对。
			event["seq"] = 1
			raw, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			body = string(raw)
		}
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
	// C8：只使用本机假搜索与假模型生成联网设置、搜索结果和 SSE 投影样本。
	record("search-not-configured", call("admin", "POST", "me/steward/settings/search-test", nil), 200)
	record("settings-search", call("admin", "PUT", "me/steward/settings", map[string]any{"search_source": "searxng", "searxng_url": server.URL, "search_key": "synthetic-search-key-5678"}), 200)
	record("search-empty", call("admin", "POST", "me/steward/settings/search-test", nil), 200)
	mode.Store(6)
	record("search-json-disabled", call("admin", "POST", "me/steward/settings/search-test", nil), 200)
	mode.Store(5)
	record("settings-responses-native", call("admin", "PUT", "me/steward/settings", map[string]any{"protocol": "responses", "search_source": "native", "overrides": map[string]string{"native_search": "supported", "tool_support": "supported"}}), 200)
	webCreated := call("admin", "POST", "me/steward/conversations", map[string]string{"title": "合成联网对话"})
	var webConversation struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(webCreated.Body.Bytes(), &webConversation); err != nil || webCreated.Code != 201 {
		t.Fatalf("联网对话：%s %v", webCreated.Body, err)
	}
	webPath := "me/steward/conversations/" + webConversation.ID
	stream := call("admin", "POST", webPath+"/messages", map[string]string{"text": "查合成资料"})
	if stream.Code != 200 {
		t.Fatalf("联网流：%s", stream.Body)
	}
	eventCount := 0
	for _, line := range strings.Split(stream.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event map[string]any
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) != nil {
			t.Fatal("无效 SSE")
		}
		kind, _ := event["type"].(string)
		if kind != "search" && kind != "citations" {
			continue
		}
		response := httptest.NewRecorder()
		response.Header().Set("Content-Type", "application/json")
		json.NewEncoder(response).Encode(event)
		record("event-"+kind, response, 200)
		eventCount++
	}
	if eventCount != 2 {
		t.Fatalf("联网事件数=%d：%s", eventCount, stream.Body)
	}
	record("conversation-web", call("admin", "GET", webPath, nil), 200)
	if w := call("admin", "PUT", "me/steward/settings", map[string]any{"protocol": "openai", "search_source": "none", "search_key": nil, "overrides": map[string]string{}}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	record("settings-cleared-key", call("admin", "PUT", "me/steward/settings", map[string]any{"key": nil}), 200)
	mode.Store(3)
	record("probe-keyless-unconfirmed", call("admin", "POST", "me/steward/settings/probe", map[string]any{"key": nil}), 200)
	// C10：只生成管家目录内变化的响应，使用合成记忆与历史。
	memoryPath := "me/steward/memories"
	record("memories-empty", call("admin", "GET", memoryPath, nil), 200)
	createdMemory := call("admin", "POST", memoryPath, map[string]string{"content": "回答使用繁体中文"})
	record("memory-created", createdMemory, 201)
	var remembered struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(createdMemory.Body.Bytes(), &remembered); err != nil {
		t.Fatal(err)
	}
	record("memory-updated", call("admin", "PATCH", memoryPath+"/"+remembered.ID, map[string]string{"content": "不喜欢现场录音版本"}), 200)
	record("memories", call("admin", "GET", memoryPath, nil), 200)
	record("memory-invalid-input", call("admin", "POST", memoryPath, map[string]string{"content": ""}), 400)
	record("memory-not-found", call("admin", "PATCH", memoryPath+"/"+uuid.New(), map[string]string{"content": "合成偏好"}), 404)
	memories := memory.NewStore(db)
	for i := 1; i < 50; i++ {
		if _, err := memories.Remember(ctx, users["admin"], nil, "合成偏好"); err != nil {
			t.Fatal(err)
		}
	}
	record("memory-limit-reached", call("admin", "POST", memoryPath, map[string]string{"content": "超过上限"}), 409)
	record("memory-deleted", call("admin", "DELETE", memoryPath+"/"+remembered.ID, nil), 204)
	record("memories-cleared", call("admin", "DELETE", memoryPath, nil), 204)
	memberMemory, err := memories.Remember(ctx, users["user"], nil, "合成成员偏好")
	if err != nil {
		t.Fatal(err)
	}
	record("memories-disabled-list", call("user", "GET", memoryPath, nil), 200)
	record("memory-disabled-create", call("user", "POST", memoryPath, map[string]string{"content": "合成"}), 403)
	record("memory-disabled-update", call("user", "PATCH", memoryPath+"/"+memberMemory.ID, map[string]string{"content": "合成"}), 403)
	record("memory-disabled-delete", call("user", "DELETE", memoryPath+"/"+memberMemory.ID, nil), 204)
	compactConversation, err := store.CreateConversation(ctx, users["admin"], "合成摘要会话", "synthetic-model")
	if err != nil {
		t.Fatal(err)
	}
	compactPath := "me/steward/conversations/" + compactConversation.ID
	record("events-unavailable", call("admin", "GET", compactPath+"/events", nil), 204)
	record("events-invalid-cursor", call("admin", "GET", compactPath+"/events?after=-1", nil), 400)
	if w := call("admin", "PUT", "admin/steward/settings", map[string]bool{"members_enabled": true}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	record("events-other-owner", call("user", "GET", compactPath+"/events", nil), 404)
	if w := call("admin", "PUT", "me/steward/settings", map[string]any{"protocol": "openai", "endpoint": server.URL, "key": "synthetic-sample-model-key", "model": "synthetic-model"}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	for i := 0; i < 8; i++ {
		for _, role := range []string{"user", "assistant"} {
			raw, _ := json.Marshal(provider.Message{Role: provider.Role(role), Content: []provider.ContentBlock{{Type: provider.BlockText, Text: "合成历史"}}})
			input := runtime.MessageInput{Role: role, Content: raw}
			if role == "assistant" {
				n := int64(120001)
				input.InputTokens = &n
			}
			if _, err := store.AppendMessage(ctx, users["admin"], compactConversation.ID, input); err != nil {
				t.Fatal(err)
			}
		}
	}
	compactedStream := call("admin", "POST", compactPath+"/messages", map[string]string{"text": "继续合成对话"})
	if compactedStream.Code != 200 {
		t.Fatal(compactedStream.Body)
	}
	foundCompacted := false
	for _, line := range strings.Split(compactedStream.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			t.Fatal(err)
		}
		if event["type"] != "history_compacted" {
			continue
		}
		response := httptest.NewRecorder()
		response.Header().Set("Content-Type", "application/json")
		json.NewEncoder(response).Encode(event)
		record("event-history-compacted", response, 200)
		foundCompacted = true
	}
	if !foundCompacted {
		t.Fatal("摘要事件未生成", compactedStream.Body)
	}
	record("conversation-compacted", call("admin", "GET", compactPath, nil), 200)
	record("settings-unread", call("admin", "GET", "me/steward/settings", nil), 200)
	record("conversation-read", call("admin", "POST", compactPath+"/read", nil), 200)
	record("conversation-read-state", call("admin", "GET", compactPath, nil), 200)

	gapConversation, err := store.CreateConversation(ctx, users["admin"], "合成日志省略", "")
	if err != nil {
		t.Fatal(err)
	}
	gapRun, err := supervisor.Start(ctx, runtime.RunParams{Caller: tool.Caller{User: identity.User{ID: users["admin"], Role: "admin"}, Source: tool.SourceChat}, ConversationID: gapConversation.ID, Text: "合成超长回答", Provider: c10SampleModel{}, Model: "synthetic-model"})
	if err != nil {
		t.Fatal(err)
	}
	gapSubscription := gapRun.Subscribe(0)
	for range gapSubscription.Events {
	}
	gapSubscription.Close()
	gapStream := call("admin", "GET", "me/steward/conversations/"+gapConversation.ID+"/events", nil)
	foundGap := false
	for _, line := range strings.Split(gapStream.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			t.Fatal(err)
		}
		if event["type"] != "history_gap" {
			continue
		}
		response := httptest.NewRecorder()
		response.Header().Set("Content-Type", "application/json")
		json.NewEncoder(response).Encode(event)
		record("event-history-gap", response, 200)
		foundGap = true
	}
	if !foundGap {
		t.Fatal("日志省略事件未生成")
	}
	for i := 0; i < 3; i++ {
		conversation, err := store.CreateConversation(ctx, users["admin"], "合成占用", "")
		if err != nil {
			t.Fatal(err)
		}
		started := make(chan struct{})
		if _, err := supervisor.Start(ctx, runtime.RunParams{Caller: tool.Caller{User: identity.User{ID: users["admin"], Role: "admin"}, Source: tool.SourceChat}, ConversationID: conversation.ID, Text: "等待", Provider: c10BlockingModel{started}, Model: "synthetic-model"}); err != nil {
			t.Fatal(err)
		}
		<-started
	}
	record("run-limit", call("admin", "POST", compactPath+"/messages", map[string]string{"text": "第四个运行"}), 409)
	if err := supervisor.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	record("shutting-down", call("admin", "POST", compactPath+"/messages", map[string]string{"text": "停止接收"}), 503)
	if len(samples.samples) != 68 {
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

// 合成超长输出经过实际 Runner 和接回接口，验证日志省略事件样本。
type c10SampleModel struct{}

func (c10SampleModel) Stream(context.Context, provider.Request) (provider.Stream, error) {
	return &c10SampleStream{events: []provider.Event{{Type: provider.EventText, Text: strings.Repeat("字", runtime.EventLogLimit/3+1)}, {Type: provider.EventDone, Finish: &provider.Finish{Reason: provider.FinishNormal}}}}, nil
}

type c10SampleStream struct{ events []provider.Event }

func (s *c10SampleStream) Next() (provider.Event, error) {
	if len(s.events) == 0 {
		return provider.Event{}, io.EOF
	}
	e := s.events[0]
	s.events = s.events[1:]
	return e, nil
}
func (*c10SampleStream) Close() error { return nil }
