package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/maintenance"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

// 用真实 app 装配重审已有方案，未登记证据方式的工具不能混入模型请求。
func TestPostgreSQLStewardAppC7Round2UnsupportedEvidence(t *testing.T) {
	f := newC3AppFixture(t)
	ctx := context.Background()
	if err := maintenance.Initialize(ctx, f.db); err != nil {
		t.Fatal(err)
	}
	root, group, release, medium := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := f.db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO library_roots(id,path,status) VALUES($1,'/c7-round2-synthetic','active')`, root)
	exec(`INSERT INTO release_groups(id) VALUES($1)`, group)
	exec(`INSERT INTO releases(id,group_id,title,artist,source_root_id,relative_directory,candidate_anchor,candidate_kind) VALUES($1,$2,'第二轮原名','合成艺人',$3,'album',$4,'ordinary_directory')`, release, group, root, release)
	exec(`INSERT INTO media(id,release_id,position) VALUES($1,$2,1)`, medium, release)
	exec(`INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,source_kind,source_status,observed_at) VALUES($1,$2,1,'合成曲目','合成艺人',$3,'01.flac','physical','present',now())`, uuid.New(), medium, root)
	store := runtime.NewStore(f.db)
	conversation, err := store.CreateConversation(ctx, f.users["admin"].ID, "第二轮证据策略", "synthetic-model", "steward")
	if err != nil {
		t.Fatal(err)
	}
	const unsupported = "c7_tool_without_evidence"
	inputs := []json.RawMessage{json.RawMessage(`{"release_id":"` + release + `","expected_revision":1,"changes":{"title":{"mode":"set","value":"第二轮新名"}},"basis":{"source":"external","note":"合成依据"}}`), json.RawMessage(`{}`)}
	content, _ := json.Marshal(provider.Message{Role: provider.RoleAssistant, Content: []provider.ContentBlock{
		{Type: provider.BlockToolCall, ToolCall: &provider.ToolCall{ID: "supported", Name: "correct_release", Arguments: inputs[0]}},
		{Type: provider.BlockToolCall, ToolCall: &provider.ToolCall{ID: "unsupported", Name: unsupported, Arguments: inputs[1]}},
	}})
	message, err := store.AppendMessage(ctx, f.users["admin"].ID, conversation.ID, runtime.MessageInput{Role: "assistant", Content: content, ToolCalls: []runtime.ToolCallInput{{Name: "correct_release", Input: inputs[0]}, {Name: unsupported, Input: inputs[1]}}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := store.CreatePlan(ctx, f.users["admin"].ID, conversation.ID, []runtime.PlanItem{
		{Tool: "correct_release", CallID: "supported", ToolCallID: message.ToolCalls[0].ID, Input: inputs[0], Revertible: true, Preview: tool.Preview{Target: "合成发行", Summary: "修正标题"}},
		{Tool: unsupported, Position: 1, CallID: "unsupported", ToolCallID: message.ToolCalls[1].ID, Input: inputs[1], Revertible: true, Preview: tool.Preview{Target: "合成目标", Summary: "证据未登记"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		requests <- string(raw)
		args, _ := json.Marshal(map[string]any{"items": []any{map[string]any{"item_id": plan.Items[0].ID, "decision": "needs_human_confirmation", "risk": "medium", "reasons": []string{"证据不足"}}}})
		body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "review", "function": map[string]any{"name": "submit_review", "arguments": string(args)}}}}, "finish_reason": "tool_calls"}}})
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: "+string(body)+"\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	w := f.request("admin", "PUT", "me/steward/settings", "", map[string]string{"endpoint": server.URL, "model": "synthetic-model", "protocol": "openai"})
	if w.Code != 200 {
		t.Fatalf("settings: %d %s", w.Code, w.Body)
	}
	w = f.request("admin", "POST", "me/steward/conversations/"+conversation.ID+"/plans/"+plan.ID+"/review", "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"reason":"awaiting_confirmation"`) {
		t.Fatalf("review: %d %s", w.Code, w.Body)
	}
	if len(requests) != 1 {
		t.Fatalf("请求数=%d", len(requests))
	}
	raw := <-requests
	if strings.Contains(raw, unsupported) || strings.Contains(raw, plan.Items[1].ID) {
		t.Fatalf("模型收到未登记项：%s", raw)
	}
	if !strings.Contains(raw, plan.Items[0].ID) {
		t.Fatal("可送审项未送入模型")
	}
	detail, err := store.GetConversation(ctx, f.users["admin"].ID, conversation.ID)
	if err != nil || len(detail.Plans) != 1 || detail.Plans[0].Review == nil {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
	p := detail.Plans[0]
	if len(p.Review.Items) != 2 || p.Review.Items[1].ItemID != plan.Items[1].ID || p.Review.Items[1].Decision != "needs_human_confirmation" || p.Review.Items[1].Source != "policy" || p.Review.Items[0].Source != "model" {
		t.Fatalf("未生成策略结论：%+v", p.Review)
	}
	var count int
	if err := f.db.QueryRow(`SELECT count(*) FROM operations`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("转人工仍执行：%d %v", count, err)
	}
	for _, item := range p.Items {
		if item.Decision != "pending" || item.Result != nil || item.OperationID != nil {
			t.Fatalf("转人工项被处理：%+v", item)
		}
	}
}
