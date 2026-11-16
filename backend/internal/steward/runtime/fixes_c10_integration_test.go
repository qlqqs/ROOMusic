package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/steward/provider"
)

type c10QueryFailureStore struct{ *Store }

func (s c10QueryFailureStore) GetConversation(context.Context, string, string) (ConversationDetail, error) {
	return ConversationDetail{}, syntheticStoreError
}

func TestPostgreSQLStewardC10F9PreflightUnread(t *testing.T) {
	db, store, user, _ := runtimeDatabase(t)
	ctx := context.Background()
	for _, unread := range []bool{false, true} {
		for _, kind := range []string{"nothing", "query", "expired_review", "expired_decision"} {
			t.Run(kind+map[bool]string{false: "_read", true: "_unread"}[unread], func(t *testing.T) {
				c, err := store.CreateConversation(ctx, user, "前置拒绝合成", "model")
				mustStore(t, err)
				p := runParams(nil)
				p.Caller.User.ID = user
				p.ConversationID = c.ID
				p.Continue = true
				var rs RunStore = store
				want := ErrNothingToContinue
				if kind == "query" {
					rs = c10QueryFailureStore{store}
					want = syntheticStoreError
				}
				if kind == "expired_review" || kind == "expired_decision" {
					_, err = db.Exec(`UPDATE steward_conversations SET mode='steward' WHERE id=$1`, c.ID)
					mustStore(t, err)
					raw := json.RawMessage(`{"role":"assistant","content":[{"type":"tool_call","tool_call":{"id":"synthetic","name":"correct_release","arguments":{}}}]}`)
					m, err := store.AppendMessage(ctx, user, c.ID, MessageInput{Role: "assistant", Content: raw, ToolCalls: []ToolCallInput{{Name: "correct_release", Input: json.RawMessage(`{}`)}}})
					mustStore(t, err)
					plan, err := store.CreatePlan(ctx, user, c.ID, []PlanItem{{Tool: "correct_release", CallID: "synthetic", ToolCallID: m.ToolCalls[0].ID, Input: json.RawMessage(`{}`)}})
					mustStore(t, err)
					_, err = db.Exec(`UPDATE steward_plans SET expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, plan.ID)
					mustStore(t, err)
					want = ErrPlanExpired
					if kind == "expired_review" {
						p.ReviewPlanID = plan.ID
					} else {
						p.Decision = &PlanDecision{PlanID: plan.ID, Digest: plan.Digest}
					}
				}
				if unread {
					_, err = db.Exec(`UPDATE steward_conversations SET last_run_finished_at='2026-01-02Z',last_read_at='2026-01-01Z' WHERE id=$1`, c.ID)
				} else {
					_, err = db.Exec(`UPDATE steward_conversations SET last_run_finished_at='2026-01-01Z',last_read_at='2026-01-02Z' WHERE id=$1`, c.ID)
				}
				mustStore(t, err)
				before, err := store.GetConversation(ctx, user, c.ID)
				mustStore(t, err)
				supervisor := NewSupervisor(ctx, NewRunner(rs, runtimeRegistry(t), slog.Default()))
				defer supervisor.Shutdown(ctx)
				if _, err := supervisor.Start(ctx, p); !errors.Is(err, want) {
					t.Fatalf("错误=%v，期望=%v", err, want)
				}
				after, err := store.GetConversation(ctx, user, c.ID)
				mustStore(t, err)
				if after.Conversation.Unread != unread || after.Conversation.LastRunFinishedAt == nil || !after.Conversation.LastRunFinishedAt.Equal(*before.Conversation.LastRunFinishedAt) || after.Conversation.Status == "running" {
					t.Fatalf("前置拒绝改变未读：%+v", after.Conversation)
				}
				if sub := supervisor.Subscribe(c.ID, 0); sub != nil {
					sub.Close()
					t.Fatal("前置拒绝保留日志")
				}
			})
		}
	}
}

func TestPostgreSQLStewardC10F9Completion(t *testing.T) {
	_, store, user, _ := runtimeDatabase(t)
	ctx := context.Background()
	for _, kind := range []string{"release", "complete", "cancel", "model_error", "storage_error", "awaiting_confirmation"} {
		t.Run(kind, func(t *testing.T) {
			c, err := store.CreateConversation(ctx, user, "结束时间合成", "model")
			mustStore(t, err)
			mustStore(t, store.MarkConversationRead(ctx, user, c.ID))
			if kind == "release" {
				mustStore(t, store.AcquireConversation(ctx, user, c.ID))
				mustStore(t, store.ReleaseConversation(ctx, user, c.ID))
			} else {
				runCtx, cancel := context.WithCancel(ctx)
				defer cancel()
				model := &fakeProvider{next: func(context.Context, int, provider.Request) ([]provider.Event, error) {
					switch kind {
					case "cancel":
						cancel()
						return nil, context.Canceled
					case "model_error":
						return nil, errors.New("合成模型失败")
					}
					return []provider.Event{modelDone(provider.FinishNormal)}, nil
				}}
				p := runParams(model)
				p.Caller.User.ID = user
				p.ConversationID = c.ID
				var rs RunStore = store
				if kind == "storage_error" {
					rs = c10AppendFailureStore{store}
				}
				if kind == "awaiting_confirmation" {
					raw := json.RawMessage(`{"role":"assistant","content":[{"type":"tool_call","tool_call":{"id":"synthetic","name":"correct_release","arguments":{}}}]}`)
					m, err := store.AppendMessage(ctx, user, c.ID, MessageInput{Role: "assistant", Content: raw, ToolCalls: []ToolCallInput{{Name: "correct_release", Input: json.RawMessage(`{}`)}}})
					mustStore(t, err)
					_, err = store.CreatePlan(ctx, user, c.ID, []PlanItem{{Tool: "correct_release", CallID: "synthetic", ToolCallID: m.ToolCalls[0].ID, Input: json.RawMessage(`{}`)}})
					mustStore(t, err)
					p.Continue = true
				}
				err = NewRunner(rs, runtimeRegistry(t), slog.Default()).Run(runCtx, p, func(Event) {})
				if kind == "storage_error" {
					if !errors.Is(err, syntheticStoreError) {
						t.Fatal(err)
					}
				} else {
					mustStore(t, err)
				}
			}
			d, err := store.GetConversation(ctx, user, c.ID)
			mustStore(t, err)
			want := kind != "release"
			if d.Conversation.Unread != want || (d.Conversation.LastRunFinishedAt != nil) != want || d.Conversation.Status == "running" {
				t.Fatalf("结束时间错误：%+v", d.Conversation)
			}
		})
	}
}

type c10AppendFailureStore struct{ *Store }

func (s c10AppendFailureStore) AppendMessage(ctx context.Context, user, id string, input MessageInput) (Message, error) {
	if input.Role == "assistant" {
		return Message{}, syntheticStoreError
	}
	return s.Store.AppendMessage(ctx, user, id, input)
}

func TestPostgreSQLStewardC10F11SummaryAfterMainFailure(t *testing.T) {
	_, store, user, _ := runtimeDatabase(t)
	ctx := context.Background()
	c, err := store.CreateConversation(ctx, user, "摘要重试合成", "model")
	mustStore(t, err)
	for _, m := range c10History(t, 120001) {
		_, err := store.AppendMessage(ctx, user, c.ID, MessageInput{Role: m.Role, Content: m.Content, InputTokens: m.InputTokens})
		mustStore(t, err)
	}
	summaryCalls := 0
	model := &fakeProvider{next: func(_ context.Context, _ int, r provider.Request) ([]provider.Event, error) {
		if r.System == SummarySystemPrompt {
			summaryCalls++
			return []provider.Event{{Type: provider.EventText, Text: "合成摘要"}, modelDone(provider.FinishNormal)}, nil
		}
		return nil, errors.New("合成主请求失败")
	}}
	run := func(continuation bool) {
		t.Helper()
		p := runParams(model)
		p.Caller.User.ID = user
		p.ConversationID = c.ID
		p.Continue = continuation
		mustStore(t, NewRunner(store, runtimeRegistry(t), slog.Default()).Run(ctx, p, func(Event) {}))
	}
	run(false)
	d, err := store.GetConversation(ctx, user, c.ID)
	mustStore(t, err)
	if summaryCalls != 1 || d.Summary.UpdatedAt == nil {
		t.Fatal("首次摘要未保存")
	}
	for _, m := range d.Messages {
		if m.Role == "assistant" && !m.CreatedAt.Before(*d.Summary.UpdatedAt) {
			t.Fatal("旧主消息时间不早于摘要")
		}
	}
	run(false)
	run(true)
	if summaryCalls != 1 {
		t.Fatal("无新用量仍重复摘要", summaryCalls)
	}
	// 有可压缩历史时，未知、等于阈值、早于/等于摘要时间均不触发。
	for _, kind := range []string{"unknown", "equal_threshold", "same_time", "older_time", "new_high"} {
		tokens := int64(120001)
		var input *int64 = &tokens
		if kind == "unknown" {
			input = nil
		}
		if kind == "equal_threshold" {
			tokens = 120000
		}
		m, err := store.AppendMessage(ctx, user, c.ID, MessageInput{Role: "assistant", Content: json.RawMessage(`{"role":"assistant","content":[{"type":"text","text":"合成新回答"}]}`), InputTokens: input})
		mustStore(t, err)
		if kind == "same_time" || kind == "older_time" {
			at := *d.Summary.UpdatedAt
			if kind == "older_time" {
				at = at.Add(-time.Second)
			}
			_, err = store.db.Exec(`UPDATE steward_messages SET created_at=$2 WHERE id=$1`, m.ID, at)
			mustStore(t, err)
		}
		run(false)
		want := 1
		if kind == "new_high" {
			want = 2
		}
		if summaryCalls != want {
			t.Fatalf("%s 摘要调用=%d，期望=%d", kind, summaryCalls, want)
		}
	}
}
