package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/qlqq/roomusic/backend/internal/steward/memory"
	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func TestSupervisorBackgroundSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		s := NewSupervisor(ctx, c10Executor(c10Waiting))
		defer s.Shutdown(ctx)

		start := func(id, user, role string, background bool, check func(context.Context) error) (*Run, error) {
			params := runParams(nil)
			params.ConversationID, params.Caller.User.ID, params.Caller.User.Role = id, user, role
			params.Background, params.Check = background, check
			return s.Start(ctx, params)
		}
		mustStart := func(id, user, role string, background bool, check func(context.Context) error) *Run {
			t.Helper()
			run, err := start(id, user, role, background, check)
			if err != nil {
				t.Fatal(err)
			}
			return run
		}
		waitCancelled := func(run *Run) {
			t.Helper()
			wait, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			if end, err := run.Wait(wait); err != nil || end.Reason != EndCancelled {
				t.Fatalf("%s: end=%+v err=%v", run.ConversationID, end, err)
			}
		}

		chat1 := mustStart("chat-1", "owner", "admin", false, nil)
		chat2 := mustStart("chat-2", "owner", "admin", false, nil)
		chat3 := mustStart("chat-3", "owner", "admin", false, nil)
		background := mustStart("background", "owner", "admin", true, nil)
		if _, err := start("other-background", "other", "admin", true, nil); !errors.Is(err, ErrBackgroundBusy) {
			t.Fatalf("第二个后台运行：%v", err)
		}
		s.CancelBackground("other")
		synctest.Wait()
		c10Finished(t, background, false)
		s.CancelBackground("owner")
		waitCancelled(background)
		for _, run := range []*Run{chat1, chat2, chat3} {
			c10Finished(t, run, false)
		}
		background = mustStart("replacement", "owner", "admin", true, nil)
		s.CancelUser("owner")
		for _, run := range []*Run{chat1, chat2, chat3} {
			waitCancelled(run)
		}
		c10Finished(t, background, false)
		s.CancelBackground("owner")
		waitCancelled(background)

		// 使用成员身份直接进入 Supervisor，验证 CancelMembers 的后台排除条件。
		background = mustStart("member-background", "member", "user", true, nil)
		memberChat := mustStart("member-chat", "member", "user", false, nil)
		s.CancelMembers()
		waitCancelled(memberChat)
		c10Finished(t, background, false)
		s.CancelBackground("member")
		waitCancelled(background)

		checks := 0
		background = mustStart("recheck-background", "owner", "admin", true, func(context.Context) error {
			checks++
			if checks > 1 {
				return ErrRunInvalidated
			}
			return nil
		})
		chat1 = mustStart("recheck-chat-1", "owner", "admin", false, nil)
		chat2 = mustStart("recheck-chat-2", "owner", "admin", false, nil)
		chat3 = mustStart("recheck-chat-3", "owner", "admin", false, nil)
		if _, err := start("chat-4", "owner", "admin", false, nil); !errors.Is(err, ErrRunLimit) {
			t.Fatalf("第四个对话运行：%v", err)
		}
		time.Sleep(60 * time.Second)
		waitCancelled(background)
		for _, run := range []*Run{chat1, chat2, chat3} {
			c10Finished(t, run, false)
		}
	})
}

type backgroundRunStore struct {
	c10SummaryStore
}

// 使用内存会话夹具保存方案和审查，让 Runner 走完正常的重读与结果回放。
func (s *backgroundRunStore) CreatePlan(_ context.Context, _, _ string, items []PlanItem, background ...bool) (Plan, error) {
	digest, err := planDigest(items)
	if err != nil {
		return Plan{}, err
	}
	for i := range items {
		items[i].ID = fmt.Sprintf("item-%d", i+1)
		items[i].PlanID = "plan"
		items[i].Decision = "pending"
	}
	plan := Plan{ID: "plan", Digest: digest, Status: "pending", ExpiresAt: time.Now().Add(time.Hour), Items: items, Background: background[0]}
	s.detail.Plans = append(s.detail.Plans, plan)
	return plan, nil
}

func (*backgroundRunStore) DecidePlan(context.Context, string, string, PlanDecision) error {
	return errors.New("测试未发起人工决策")
}

func (*backgroundRunStore) PendingExecutions(context.Context, string, string) ([]PlanItem, error) {
	return nil, nil
}

func (s *backgroundRunStore) FinishPlanItem(ctx context.Context, user, conversation string, item PlanItem, result ToolCallResult, input MessageInput) error {
	plan := &s.detail.Plans[0]
	pending := 0
	for i := range plan.Items {
		if plan.Items[i].ID == item.ID {
			item.Result = result.Output
			plan.Items[i] = item
		}
		if plan.Items[i].Decision == "pending" {
			pending++
		}
	}
	if pending == 0 {
		plan.Status = "resolved"
	}
	_, err := s.FinishToolCallMessage(ctx, user, conversation, item.ToolCallID, result, input)
	return err
}

func (s *backgroundRunStore) SaveReview(_ context.Context, _, _ string, review Review) error {
	s.detail.Plans[0].Review = &review
	return nil
}

func (*backgroundRunStore) LookupOperationResult(context.Context, string, string) (RecordedOperation, error) {
	return RecordedOperation{}, nil
}

func backgroundSharedRegistry(t *testing.T) *tool.Registry {
	t.Helper()
	registry := tool.NewRegistry()
	err := registry.Register(tool.Definition{
		Name: "synthetic_change", MinRole: tool.RoleAdmin, Scope: tool.ScopeShared, Revertible: true,
		Input: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`), Output: json.RawMessage(`{"type":"object"}`),
		Handler: func(context.Context, tool.Caller, json.RawMessage) (json.RawMessage, error) {
			t.Error("未经批准的工具被执行")
			return json.RawMessage(`{}`), nil
		},
		Preview: func(context.Context, tool.Caller, json.RawMessage) (tool.Preview, error) {
			return tool.Preview{Target: "合成发行"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func backgroundCalls(count int) []provider.Event {
	events := make([]provider.Event, 0, count+1)
	for i := 1; i <= count; i++ {
		call := modelCall("synthetic_change", `{}`)
		call.ToolCall.ID = fmt.Sprintf("call-%d", i)
		events = append(events, call)
	}
	return append(events, modelDone(provider.FinishToolCalls))
}

func TestBackgroundTwentyFirstSharedCallRejected(t *testing.T) {
	for _, tc := range []struct {
		name       string
		background bool
		items      int
		end        EndReason
	}{
		{"后台", true, 20, EndCompleted},
		{"对话", false, 21, EndAwaitingConfirmation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &backgroundRunStore{}
			store.detail.Conversation.Mode = "steward"
			p := &fakeProvider{next: func(_ context.Context, n int, _ provider.Request) ([]provider.Event, error) {
				if n == 1 {
					return backgroundCalls(21), nil
				}
				return []provider.Event{modelDone(provider.FinishNormal)}, nil
			}}
			params := runParams(p)
			params.Caller.User.Role, params.Background = "admin", tc.background
			params.Escalate = func(_ context.Context, item PlanItem, _ Review, _ ReviewItem) (string, error) {
				return item.ID + "-exception", nil
			}
			var end RunEnd
			var twentyFirst *ToolFinished
			err := NewRunner(store, backgroundSharedRegistry(t), slog.New(slog.DiscardHandler)).Run(context.Background(), params, func(event Event) {
				if event.End != nil {
					end = *event.End
				}
				if event.ToolFinished != nil && event.ToolFinished.CallID == "call-21" {
					twentyFirst = event.ToolFinished
				}
			})
			if err != nil || end.Reason != tc.end {
				t.Fatalf("end=%+v err=%v", end, err)
			}
			if len(store.detail.Plans) != 1 {
				t.Fatalf("plans=%+v", store.detail.Plans)
			}
			plan := store.detail.Plans[0]
			if plan.Background != tc.background || len(plan.Items) != tc.items {
				t.Fatalf("background=%v items=%d", plan.Background, len(plan.Items))
			}
			if tc.background {
				if twentyFirst == nil || twentyFirst.ErrorCode != "too_many_proposals" {
					t.Fatalf("第 21 项结果=%+v", twentyFirst)
				}
				for _, item := range plan.Items {
					if item.CallID == "call-21" {
						t.Fatal("第 21 项进入后台方案")
					}
				}
			} else {
				if twentyFirst != nil || plan.Review == nil || len(plan.Review.Items) != 21 {
					t.Fatalf("第 21 项结果=%+v review=%+v", twentyFirst, plan.Review)
				}
				for _, result := range plan.Review.Items {
					if result.Decision != "needs_human_confirmation" || result.Source != "policy" || !strings.Contains(strings.Join(result.Reasons, ""), "20") {
						t.Fatalf("对话方案审查=%+v", result)
					}
				}
			}
		})
	}
}

func TestBackgroundBeforeStepStopsAfterToolAndSkipsMemory(t *testing.T) {
	store := &memoryRunStore{}
	store.detail.Conversation.Mode = "steward"
	memories := &c10Memories{users: map[string][]memory.Item{"synthetic-user": {{Content: "合成个人记忆"}}}}
	stop := false
	p := &fakeProvider{next: func(_ context.Context, n int, req provider.Request) ([]provider.Event, error) {
		if n != 1 || strings.Contains(req.System, "合成个人记忆") {
			t.Fatalf("请求 %d system=%q", n, req.System)
		}
		stop = true
		return []provider.Event{modelCall("lookup", `{"query":"合成"}`), modelDone(provider.FinishToolCalls)}, nil
	}}
	params := runParams(p)
	params.Background, params.SkipMemory = true, true
	params.BeforeStep = func(context.Context) error {
		if stop {
			return ErrRunStopped
		}
		return nil
	}

	var end RunEnd
	var finished *ToolFinished
	err := NewRunner(store, runtimeRegistry(t), slog.New(slog.DiscardHandler)).WithMemories(memories).Run(context.Background(), params, func(event Event) {
		if event.End != nil {
			end = *event.End
		}
		if event.ToolFinished != nil {
			finished = event.ToolFinished
		}
	})
	if err != nil || end.Reason != EndCurationStopped || finished == nil || finished.Status != "succeeded" {
		t.Fatalf("end=%+v tool=%+v err=%v", end, finished, err)
	}
	if len(memories.reads) != 0 || len(p.requests) != 1 {
		t.Fatalf("记忆读取=%v 模型请求=%d", memories.reads, len(p.requests))
	}
	assertReleased(t, store, p)
}

func Test后台审查未采用时记录用量和建议(t *testing.T) {
	store := &backgroundRunStore{}
	store.detail.Conversation.Mode = "steward"
	store.detail.Messages = c10History(t, summaryInputThreshold+1)
	p := &fakeProvider{next: func(_ context.Context, n int, req provider.Request) ([]provider.Event, error) {
		switch n {
		case 1:
			if req.System != SummarySystemPrompt {
				t.Fatalf("首次调用应为摘要，system=%q", req.System)
			}
			return []provider.Event{{Type: provider.EventText, Text: "合成摘要"}, modelUsage(31, 7), modelDone(provider.FinishNormal)}, nil
		case 2:
			return append([]provider.Event{modelUsage(50, 2), modelUsage(51, 8)}, backgroundCalls(2)...), nil
		case 3:
			if req.ForceTool != "submit_review" {
				t.Fatalf("第三次调用应为审查，force=%q", req.ForceTool)
			}
			return []provider.Event{
				modelCall("submit_review", `{"items":[{"item_id":"item-1","decision":"needs_human_confirmation","risk":"high","reasons":["需要人工核对"]},{"item_id":"item-2","decision":"rejected","risk":"low","reasons":["证据不支持"]}]}`),
				modelUsage(23, 4), modelDone(provider.FinishToolCalls),
			}, nil
		case 4:
			var outputs []string
			for _, message := range req.Messages {
				for _, block := range message.Content {
					if block.ToolResult != nil {
						outputs = append(outputs, block.ToolResult.Content)
					}
				}
			}
			joined := strings.Join(outputs, "\n")
			for _, id := range []string{"exception-item-1", "exception-item-2"} {
				if !strings.Contains(joined, id) {
					t.Errorf("续跑模型未收到例外 %s，tool results=%s", id, joined)
				}
			}
			return []provider.Event{{Type: provider.EventUsage, Usage: &provider.Usage{OutputTokens: new(int64)}}, modelDone(provider.FinishNormal)}, nil
		default:
			t.Fatalf("额外模型请求 %d", n)
			return nil, nil
		}
	}}
	params := runParams(p)
	params.Background, params.Caller.User.Role = true, "admin"
	type usageEntry struct {
		kind    UsageKind
		in, out int64
	}
	var usages []usageEntry
	params.Usage = func(_ context.Context, kind UsageKind, usage provider.Usage) {
		if usage.InputTokens == nil || usage.OutputTokens == nil {
			t.Fatalf("用量 %s 缺字段：%+v", kind, usage)
		}
		usages = append(usages, usageEntry{kind, *usage.InputTokens, *usage.OutputTokens})
	}
	escalations := map[string]string{}
	params.Escalate = func(_ context.Context, item PlanItem, review Review, result ReviewItem) (string, error) {
		if review.ID == "" || result.ItemID != item.ID || len(result.Reasons) != 1 {
			t.Fatalf("例外 item=%+v review=%+v result=%+v", item, review, result)
		}
		escalations[item.ID] = result.Decision
		return "exception-" + item.ID, nil
	}

	var end RunEnd
	runner := NewRunner(store, backgroundSharedRegistry(t), slog.New(slog.DiscardHandler)).WithReviewEvidence(func(context.Context, tool.Caller, PlanItem) (ReviewEvidenceFacts, error) {
		return ReviewEvidenceFacts{Targets: json.RawMessage(`[]`)}, nil
	})
	err := runner.Run(context.Background(), params, func(event Event) {
		if event.End != nil {
			end = *event.End
		}
	})
	if err != nil || end.Reason != EndCompleted || len(p.requests) != 4 || store.saved != 1 {
		t.Fatalf("end=%+v err=%v requests=%d summaries=%d", end, err, len(p.requests), store.saved)
	}
	if want := []usageEntry{{UsageSummary, 31, 7}, {UsageMain, 51, 8}, {UsageReview, 23, 4}, {UsageMain, 0, 0}}; !reflect.DeepEqual(usages, want) {
		t.Errorf("用量=%+v，期望 %+v", usages, want)
	}
	if want := map[string]string{"item-1": "needs_human_confirmation", "item-2": "rejected"}; !reflect.DeepEqual(escalations, want) {
		t.Errorf("Escalate=%v，期望 %v", escalations, want)
	}
	plan := store.detail.Plans[0]
	for _, item := range plan.Items {
		var output struct {
			Code        string `json:"code"`
			ExceptionID string `json:"exception_id"`
		}
		if err := json.Unmarshal(item.Result, &output); err != nil || item.Decision != "rejected_by_review" || output.Code != "rejected_by_review" || output.ExceptionID != "exception-"+item.ID {
			t.Errorf("方案项 %s decision=%s result=%s err=%v", item.ID, item.Decision, item.Result, err)
		}
	}
}
