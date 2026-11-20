package runtime

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/migrations"
)

func TestC6PlanDigestCanonicalAndBoundToContent(t *testing.T) {
	a := []PlanItem{{Tool: "correct_release", Input: json.RawMessage(`{"b":2,"a":1}`), Position: 0}}
	b := []PlanItem{{Tool: "correct_release", Input: json.RawMessage(` { "a": 1, "b": 2 } `), Position: 0}}
	first, err := planDigest(a)
	if err != nil {
		t.Fatal(err)
	}
	same, err := planDigest(b)
	if err != nil || same != first || len(first) != 64 {
		t.Fatal(first, same, err)
	}
	for _, change := range []PlanItem{{Tool: "other", Input: a[0].Input}, {Tool: a[0].Tool, Input: json.RawMessage(`{"a":3,"b":2}`)}, {Tool: a[0].Tool, Input: a[0].Input, Position: 1}} {
		next, err := planDigest([]PlanItem{change})
		if err != nil || next == first {
			t.Fatal("摘要未绑定内容", err)
		}
	}
}
func TestC6ReplayStopsBeforeAwaitingConfirmation(t *testing.T) {
	user := Message{Role: "user", Content: json.RawMessage(`{"role":"user","content":[{"type":"text","text":"合成"}]}`)}
	pending := Message{Role: "assistant", Content: json.RawMessage(`{"role":"assistant","content":[{"type":"tool_call","tool_call":{"id":"c6","name":"correct_release","arguments":{}}}]}`), ToolCalls: []ToolCall{{Status: "awaiting_confirmation"}}}
	got, err := replayMessages([]Message{user, pending})
	if err != nil || len(got) != 1 || got[0].Role != provider.RoleUser {
		t.Fatalf("等待确认重放=%+v err=%v", got, err)
	}
}
func TestPostgreSQLStewardC6Migration54RoundTrip(t *testing.T) {
	db, s, user, _ := runtimeDatabase(t)
	ctx := context.Background()
	c, err := s.CreateConversation(ctx, user, "C6 旧会话", "model")
	mustStore(t, err)
	raw, _ := json.Marshal(provider.Message{Role: provider.RoleAssistant, Content: []provider.ContentBlock{{Type: provider.BlockToolCall, ToolCall: &provider.ToolCall{ID: "c6", Name: "correct_release", Arguments: json.RawMessage(`{}`)}}}})
	m, err := s.AppendMessage(ctx, user, c.ID, MessageInput{Role: "assistant", Content: raw, ToolCalls: []ToolCallInput{{Name: "correct_release", Input: json.RawMessage(`{}`)}}})
	mustStore(t, err)
	up, err := migrations.Files.ReadFile("0054_steward_plans.sql")
	mustStore(t, err)
	down, err := migrations.Files.ReadFile("0054_steward_plans.down.sql")
	mustStore(t, err)
	checkIndex := func(want bool) {
		t.Helper()
		var definition *string
		mustStore(t, db.QueryRow(`SELECT pg_get_indexdef(to_regclass('steward_plans_conversation_idx'))`).Scan(&definition))
		found := definition != nil && strings.HasSuffix(*definition, " USING btree (conversation_id)") && !strings.Contains(*definition, " WHERE ")
		if found != want {
			t.Fatalf("conversation_id 普通索引=%v，预期=%v", found, want)
		}
	}
	checkIndex(true)
	down55, err := migrations.Files.ReadFile("0055_steward_reviews.down.sql")
	mustStore(t, err)
	_, err = db.Exec(string(down55))
	mustStore(t, err)
	// 用旧版结构作为基线；上、下、再上并核对旧会话、旧调用保持。
	_, err = db.Exec(string(down))
	mustStore(t, err)
	checkIndex(false)
	baseline, err := postgrestest.Snapshot(ctx, db)
	mustStore(t, err)
	_, err = db.Exec(string(up))
	mustStore(t, err)
	checkIndex(true)
	plan, err := s.CreatePlan(ctx, user, c.ID, []PlanItem{{Tool: "correct_release", CallID: "c6", ToolCallID: m.ToolCalls[0].ID, Input: json.RawMessage(`{}`)}})
	mustStore(t, err)
	if plan.ID == "" {
		t.Fatal("未生成方案")
	}
	_, err = db.Exec(string(down))
	mustStore(t, err)
	checkIndex(false)
	var status string
	var code *string
	err = db.QueryRow(`SELECT status,error_code FROM steward_tool_calls WHERE id=$1`, m.ToolCalls[0].ID).Scan(&status, &code)
	mustStore(t, err)
	if status != "failed" || code == nil || *code != "not_executed" {
		t.Fatal(status, code)
	}
	// 回退有意把等待状态转换成未执行，其余旧数据逐行保持。
	_, err = db.Exec(`UPDATE steward_tool_calls SET status='running',error_code=NULL WHERE id=$1`, m.ToolCalls[0].ID)
	mustStore(t, err)
	after, err := postgrestest.Snapshot(ctx, db)
	mustStore(t, err)
	if diff := postgrestest.Diff(baseline, after); len(diff) != 0 {
		t.Fatal(diff)
	}
	_, err = db.Exec(string(up))
	mustStore(t, err)
	checkIndex(true)
	up55, err := migrations.Files.ReadFile("0055_steward_reviews.sql")
	mustStore(t, err)
	_, err = db.Exec(string(up55))
	mustStore(t, err)
	detail, err := s.GetConversation(ctx, user, c.ID)
	mustStore(t, err)
	if detail.Conversation.Title != "C6 旧会话" || len(detail.Messages) != 1 || !reflect.DeepEqual(detail.Conversation.AutoApprovedTools, []string{}) {
		t.Fatalf("再升级旧数据=%+v", detail)
	}
	var key string
	err = db.QueryRow(`SELECT idempotency_key::text FROM steward_tool_calls WHERE id=$1`, m.ToolCalls[0].ID).Scan(&key)
	mustStore(t, err)
	if key == "" {
		t.Fatal("未补幂等键")
	}
}

func TestC6ModeSystemPrompt(t *testing.T) {
	for _, tc := range []struct{ mode, suffix string }{{"assistant", "曲库修改直接调用对应的修改工具，不要先用文字征求确认。调用后系统会自动把它整理成方案，等用户在界面上确认后才执行；本会话已获用户免确认的工具会直接执行。"}, {"operator", "曲库修改在鉴权后直接执行，不询问用户。"}} {
		t.Run(tc.mode, func(t *testing.T) {
			got := systemPromptWithMode("合成管家", tc.mode)
			if got != systemPrompt+"用户称呼你为「合成管家」。"+tc.suffix {
				t.Fatal(got)
			}
			if tc.mode == "assistant" {
				for _, want := range []string{"直接调用对应的修改工具", "不要先用文字征求确认"} {
					if !strings.Contains(got, want) {
						t.Errorf("assistant 提示词缺少 %q：%s", want, got)
					}
				}
			}
		})
	}
}
func TestPostgreSQLStewardC6StartupKeepsAwaitingConfirmation(t *testing.T) {
	db, s, user, _ := runtimeDatabase(t)
	ctx := context.Background()
	c, err := s.CreateConversation(ctx, user, "待确认", "model")
	mustStore(t, err)
	mustStore(t, s.AcquireConversation(ctx, user, c.ID))
	raw, _ := json.Marshal(provider.Message{Role: provider.RoleAssistant, Content: []provider.ContentBlock{{Type: provider.BlockToolCall, ToolCall: &provider.ToolCall{ID: "c6-pending", Name: "correct_release", Arguments: json.RawMessage(`{}`)}}}})
	m, err := s.AppendMessage(ctx, user, c.ID, MessageInput{Role: "assistant", Content: raw, ToolCalls: []ToolCallInput{{Name: "correct_release", Input: json.RawMessage(`{}`)}}})
	mustStore(t, err)
	p, err := s.CreatePlan(ctx, user, c.ID, []PlanItem{{Tool: "correct_release", CallID: "c6-pending", ToolCallID: m.ToolCalls[0].ID, Input: json.RawMessage(`{}`)}})
	mustStore(t, err)
	_, err = s.ResetRunningConversations(ctx)
	mustStore(t, err)
	d, err := s.GetConversation(ctx, user, c.ID)
	mustStore(t, err)
	if d.Conversation.Status != "idle" || len(d.Plans) != 1 || d.Plans[0].ID != p.ID || d.Plans[0].Status != "pending" || d.Messages[0].ToolCalls[0].Status != "awaiting_confirmation" {
		t.Fatalf("启动恢复破坏待确认方案：%+v", d)
	}
	var n int
	mustStore(t, db.QueryRow(`SELECT count(*) FROM steward_messages WHERE role='tool'`).Scan(&n))
	if n != 0 {
		t.Fatal("启动恢复伪造了中断结果")
	}
}
