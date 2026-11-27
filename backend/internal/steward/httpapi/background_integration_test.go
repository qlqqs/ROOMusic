package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func TestPostgreSQLStewardCurationConversationHiddenAndLocked(t *testing.T) {
	f := newAPIFixture(t)
	ctx := context.Background()
	owner := f.users["admin"]
	other := f.users["other"]
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := f.db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE users SET role='admin' WHERE id=$1`, other)
	chat, err := f.store.CreateConversation(ctx, owner, "对话", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	curation, err := f.store.CreateCurationConversation(ctx, owner, "自动整理", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	otherCuration, err := f.store.CreateCurationConversation(ctx, other, "另一位管理员的整理", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE steward_conversations SET last_run_finished_at=now() WHERE id IN ($1,$2,$3)`, chat.ID, curation.ID, otherCuration.ID)

	group, release := uuid.New(), uuid.New()
	exec(`INSERT INTO release_groups(id) VALUES($1)`, group)
	exec(`INSERT INTO releases(id,group_id,title,artist) VALUES($1,$2,'合成发行','合成艺人')`, release, group)
	exec(`INSERT INTO curation_pending_items(target_type,target_id,kind,status,rule_version,input_digest,evidence,conversation_id,claimed_at)
		VALUES('release',$1,'release_match','in_progress',1,repeat('a',64),'{}',$2,now()),
		('release',$1,'recording_match','waiting_model',1,repeat('b',64),'{}',$2,NULL)`, release, curation.ID)

	checkPage := func(role, path string, expected runtime.Conversation, counts map[string]int) {
		t.Helper()
		w := f.request(role, "GET", path, nil)
		apiStatus(t, w, 200, "")
		var page struct {
			Items []struct {
				ID         string         `json:"id"`
				Kind       string         `json:"kind"`
				Mode       string         `json:"mode"`
				Unread     bool           `json:"unread"`
				ItemCounts map[string]int `json:"item_counts"`
			} `json:"items"`
			Pagination struct {
				Total int `json:"total"`
			} `json:"pagination"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Pagination.Total != 1 {
			t.Fatalf("page=%s err=%v", w.Body, err)
		}
		item := page.Items[0]
		if item.ID != expected.ID || item.Kind != expected.Kind || item.Mode != expected.Mode || !item.Unread || !reflect.DeepEqual(item.ItemCounts, counts) {
			t.Fatalf("会话列表 %s：%s", path, w.Body)
		}
	}
	checkPage("admin", "me/steward/conversations", chat, nil)
	checkPage("admin", "admin/curation/conversations", curation, map[string]int{"in_progress": 1, "waiting_model": 1})
	checkPage("other", "admin/curation/conversations", otherCuration, map[string]int{})
	apiStatus(t, f.request("member", "GET", "admin/curation/conversations", nil), 403, "forbidden")

	settings := f.request("admin", "GET", "me/steward/settings", nil)
	apiStatus(t, settings, 200, "")
	var unread struct {
		Count int `json:"unread_conversation_count"`
	}
	if err := json.Unmarshal(settings.Body.Bytes(), &unread); err != nil || unread.Count != 1 {
		t.Fatalf("未读会话数=%d err=%v body=%s", unread.Count, err, settings.Body)
	}

	path := c6Path(curation.ID)
	apiStatus(t, f.request("admin", "PATCH", path, map[string]string{"mode": "assistant"}), 409, "curation_conversation")
	apiStatus(t, f.request("admin", "DELETE", path, nil), 409, "conversation_busy")

	call := &provider.ToolCall{ID: "curation-proposal", Name: "correct_release", Arguments: json.RawMessage(`{}`)}
	content, err := json.Marshal(provider.Message{Role: provider.RoleAssistant, Content: []provider.ContentBlock{{Type: provider.BlockToolCall, ToolCall: call}}})
	if err != nil {
		t.Fatal(err)
	}
	message, err := f.store.AppendMessage(ctx, owner, curation.ID, runtime.MessageInput{
		Role: "assistant", Content: content, ToolCalls: []runtime.ToolCallInput{{Name: call.Name, Input: call.Arguments}},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := f.store.CreatePlan(ctx, owner, curation.ID, []runtime.PlanItem{
		{Tool: call.Name, ToolCallID: message.ToolCalls[0].ID, Input: call.Arguments, Revertible: true},
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	planPath := path + "/plans/" + plan.ID
	apiStatus(t, f.request("admin", "POST", planPath+"/decision", runtime.PlanDecision{
		Digest: plan.Digest, AcceptedItemIDs: []string{plan.Items[0].ID},
	}), 409, "curation_plan")
	apiStatus(t, f.request("admin", "POST", planPath+"/review", nil), 409, "curation_plan")
}

func TestPostgreSQLStewardReviewV1ApprovalRejected(t *testing.T) {
	f, id := newC7API(t, "openai", "admin")
	// 在 SaveReview 持久化后、执行前模拟升级遗留的第 1 版批准。
	store := &c7MutateReviewStore{Store: f.store, afterSave: func(review runtime.Review) error {
		_, err := f.db.Exec(`UPDATE steward_reviews SET policy_version=1 WHERE id=$1`, review.ID)
		return err
	}}
	f.replaceRunner(runtime.NewRunner(store, f.registry, f.api.logger).WithReviewEvidence(c7Round2Evidence(f)))

	assertDone(t, f.send("admin", id), runtime.EndReviewUnavailable)
	plan := f.latest(t, "admin", id)
	if plan.Review == nil || plan.Review.PolicyVersion != 1 || plan.Review.Status != "completed" ||
		len(plan.Review.Items) != 1 || plan.Review.Items[0].Decision != "approved" {
		t.Fatalf("第 1 版批准未保存：%+v", plan.Review)
	}
	if plan.Status != "pending" || len(plan.Items) != 1 || plan.Items[0].Decision != "pending" ||
		plan.Items[0].OperationID != nil || f.count(t) != 0 || f.title(t, f.releases[0]) != "原名" {
		t.Fatalf("旧版批准改变了发行或方案：%+v", plan)
	}
}

func TestPostgreSQLStewardBackgroundAvailabilityAndStart(t *testing.T) {
	f := newAPIFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	owner := f.users["admin"]
	conversation, err := f.store.CreateCurationConversation(ctx, owner, "自动整理", "")
	if err != nil {
		t.Fatal(err)
	}
	params := BackgroundParams{UserID: owner, ConversationID: conversation.ID, Text: "处理合成待定项"}
	assertUnavailable := func(want string) {
		t.Helper()
		if _, reason, err := f.api.BackgroundAvailability(ctx, owner); err != nil || reason != want {
			t.Fatalf("availability reason=%q want=%q err=%v", reason, want, err)
		}
		run, err := f.api.StartBackground(ctx, params)
		var unavailable *ModelUnavailable
		if run != nil || !errors.As(err, &unavailable) || unavailable.Reason != want {
			t.Fatalf("start run=%v err=%v want=%q", run, err, want)
		}
	}
	assertUnavailable("not_configured")

	called := make(chan tool.Caller, 1)
	if err := f.registry.Register(tool.Definition{
		Name: "synthetic_curation_lookup", Scope: tool.ScopeRead, MinRole: tool.RoleAdmin,
		Input: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`), Output: json.RawMessage(`{"type":"object"}`),
		Handler: func(_ context.Context, caller tool.Caller, _ json.RawMessage) (json.RawMessage, error) {
			called <- caller
			return json.RawMessage(`{"found":true}`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	var modelHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model != "synthetic-model" {
			t.Errorf("后台模型请求 model=%q err=%v", body.Model, err)
			http.Error(w, "invalid request", 400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if modelHits.Add(1) == 1 {
			io.WriteString(w, c7Calls("openai", []c6Call{{name: "synthetic_curation_lookup", input: map[string]any{}}}, ""))
		} else {
			io.WriteString(w, apiModelResponse("openai", 3, false))
		}
	}))
	defer server.Close()
	f.allowEndpoint(t, server.URL)
	f.configure(t, "admin", server.URL, "openai")
	if _, err := f.db.ExecContext(ctx, `UPDATE steward_user_settings SET key_ciphertext='synthetic-invalid-ciphertext' WHERE user_id=$1`, owner); err != nil {
		t.Fatal(err)
	}
	assertUnavailable("key_needs_reentry")
	f.configure(t, "admin", server.URL, "openai")
	if model, reason, err := f.api.BackgroundAvailability(ctx, owner); err != nil || model != "synthetic-model" || reason != "" || modelHits.Load() != 0 {
		t.Fatalf("availability model=%q reason=%q err=%v requests=%d", model, reason, err, modelHits.Load())
	}

	run, err := f.api.StartBackground(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if end, err := run.Wait(ctx); err != nil || end.Reason != runtime.EndCompleted || modelHits.Load() != 2 {
		t.Fatalf("end=%+v err=%v requests=%d", end, err, modelHits.Load())
	}
	select {
	case caller := <-called:
		if caller.Source != tool.SourceCuration || caller.Mode != "steward" || caller.User.ID != owner {
			t.Fatalf("后台工具调用身份=%+v", caller)
		}
	default:
		t.Fatal("后台模型未调用合成工具")
	}
	detail, err := f.store.GetConversation(ctx, owner, conversation.ID)
	if err != nil || detail.Conversation.Kind != "curation" || detail.Conversation.Mode != "steward" {
		t.Fatalf("会话=%+v err=%v", detail.Conversation, err)
	}
}
