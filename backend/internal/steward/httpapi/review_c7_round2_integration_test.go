package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	catalogtools "github.com/qlqq/roomusic/backend/internal/catalog/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

// 第二轮专用端口不改变上一轮共享夹具；证据读取沿用已发布的目录接口。
func c7Round2Evidence(f *c7API) runtime.ReviewEvidenceReader {
	return func(ctx context.Context, caller tool.Caller, item runtime.PlanItem) (runtime.ReviewEvidenceFacts, error) {
		if !catalogtools.SupportsReviewEvidence(item.Tool) {
			return runtime.ReviewEvidenceFacts{}, runtime.ErrReviewEvidenceUnsupported
		}
		targets, err := catalogtools.ReadReviewTargets(ctx, catalog.NewStore(f.db), governance.NewStore(f.db), item.Input)
		if err != nil {
			return runtime.ReviewEvidenceFacts{}, err
		}
		raw, err := json.Marshal(targets)
		return runtime.ReviewEvidenceFacts{Targets: raw}, err
	}
}

func c7Round2Endpoint(t *testing.T, f *c7API, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	f.allowEndpoint(t, server.URL)
	f.configure(t, "admin", server.URL, "openai")
	f.endpoint = server.URL
}

func c7Round2NoReviewOrExecution(t *testing.T, f *c7API, id string, w *httptest.ResponseRecorder) {
	t.Helper()
	started, finished, done := 0, 0, 0
	for _, e := range c7Events(t, w) {
		switch e.Type {
		case runtime.EventReviewStarted:
			started++
		case runtime.EventReviewFinished:
			finished++
		case runtime.EventDone:
			done++
		case runtime.EventToolStarted:
			if started > 0 {
				t.Error("失败或取消后开始执行条目")
			}
		}
	}
	if started != 1 || finished != 0 || done != 1 {
		t.Fatalf("审查没有完整收尾：%s", w.Body)
	}
	var count int
	if err := f.db.QueryRow(`SELECT count(*) FROM steward_reviews`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("审查被意外保存：%d %v", count, err)
	}
	p := f.latest(t, "admin", id)
	if p.Status != "pending" || p.Review != nil {
		t.Fatalf("方案被处理：%+v", p)
	}
	for _, item := range p.Items {
		if item.Decision != "pending" || item.Result != nil || item.OperationID != nil {
			t.Fatalf("条目被处理：%+v", item)
		}
	}
	f.unchanged(t)
}

func TestPostgreSQLStewardHTTPC7Round2OpenAIStop(t *testing.T) {
	f, id := newC7API(t, "openai", "admin")
	c7Round2Endpoint(t, f, func(w http.ResponseWriter, r *http.Request) {
		recorder := httptest.NewRecorder()
		f.serveModel(recorder, r)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, strings.ReplaceAll(recorder.Body.String(), `"finish_reason":"tool_calls"`, `"finish_reason":"stop"`))
	})
	assertDone(t, f.send("admin", id), runtime.EndCompleted)
	p := f.latest(t, "admin", id)
	if p.Review == nil || p.Review.Status != "completed" || p.Items[0].Decision != "accepted" || p.Items[0].OperationID == nil || f.count(t) != 1 || f.title(t, f.releases[0]) != "新标题" {
		t.Fatalf("stop 未接受唯一审查调用：%+v", p)
	}
	f.assertReviewWire(t, 1, "synthetic-model")
}

func TestPostgreSQLStewardHTTPC7Round2MixedEvidence(t *testing.T) {
	for _, changed := range []string{"human_target", "approved_target"} {
		t.Run(changed, func(t *testing.T) {
			f, id := newC7API(t, "openai", "admin")
			f.calls = append(f.calls, c6Correct(f.releases[1], 1, "第二标题"))
			f.decisions = []string{"approved", "needs_human_confirmation"}
			store := &c7MutateReviewStore{Store: f.store, afterSave: func(review runtime.Review) error {
				index := 1
				if changed == "approved_target" {
					index = 0
				}
				_, err := f.db.Exec(`UPDATE releases SET artist='第二轮外部修改' WHERE id=$1`, f.releases[index])
				return err
			}}
			f.api.runner = runtime.NewRunner(store, f.registry, f.api.logger).WithReviewEvidence(c7Round2Evidence(f))
			w := f.send("admin", id)
			want, count, decision := runtime.EndAwaitingConfirmation, 1, "accepted"
			if changed == "approved_target" {
				want, count, decision = runtime.EndReviewUnavailable, 0, "pending"
			}
			assertDone(t, w, want)
			p := f.latest(t, "admin", id)
			if p.Review == nil || p.Review.Status != "completed" || p.Items[0].Decision != decision || p.Items[1].Decision != "pending" || p.Items[1].Result != nil || f.count(t) != count || f.title(t, f.releases[1]) != "原名" {
				t.Fatalf("混合方案证据绑定错误：%+v", p)
			}
			if count == 1 && (p.Items[0].OperationID == nil || f.title(t, f.releases[0]) != "新标题") {
				t.Fatal("人工项目标变化阻止了批准项")
			}
			if count == 0 && (p.Items[0].OperationID != nil || f.title(t, f.releases[0]) != "原名") {
				t.Fatal("批准项自身证据变化仍执行")
			}
		})
	}
}

func TestPostgreSQLStewardHTTPC7Round2ReconcileBeforeDecline(t *testing.T) {
	for _, next := range []string{"rejected_by_review", "declined_by_user"} {
		t.Run(next, func(t *testing.T) {
			f, id := newC7API(t, "openai", "admin")
			f.api.runner = runtime.NewRunner(&c6FailPlanResultOnce{Store: f.store, fail: true}, f.registry, f.api.logger).WithReviewEvidence(c7Round2Evidence(f))
			w := f.send("admin", id)
			if end := assertDone(t, w, ""); end.ErrorCode != "database_unavailable" {
				t.Fatalf("没有制造提交后断开：%s", w.Body)
			}
			p := f.latest(t, "admin", id)
			item := p.Items[0]
			if f.count(t) != 1 || item.Decision != "pending" || item.Result != nil || item.OperationID != nil {
				t.Fatalf("注入点错误：%+v", p)
			}
			var key string
			if err := f.db.QueryRow(`SELECT idempotency_key::text FROM steward_plan_items WHERE id=$1`, item.ID).Scan(&key); err != nil {
				t.Fatal(err)
			}
			recorded, err := f.store.LookupOperationResult(context.Background(), f.users["admin"], key)
			if err != nil || recorded.ID == "" || recorded.Output == nil {
				t.Fatalf("未按原键提交操作：%+v %v", recorded, err)
			}
			// 对账阶段禁止再次进入执行器，即使幂等层能吞掉重复调用也应使测试失败。
			registry := tool.NewRegistry()
			for _, definition := range f.registry.List(tool.Caller{User: identity.User{Role: "admin"}}) {
				name := definition.Name
				definition.Preview = func(ctx context.Context, caller tool.Caller, raw json.RawMessage) (tool.Preview, error) {
					return f.registry.PreviewCall(ctx, caller, name, raw)
				}
				definition.Handler = func(context.Context, tool.Caller, json.RawMessage) (json.RawMessage, error) {
					t.Error("拒绝前对账重复调用执行器")
					return nil, errors.New("第二轮禁止重复执行")
				}
				if err := registry.Register(definition); err != nil {
					t.Fatal(err)
				}
			}
			f.api.runner = runtime.NewRunner(f.store, registry, f.api.logger).WithReviewEvidence(c7Round2Evidence(f))
			if next == "rejected_by_review" {
				f.decisions = []string{"rejected"}
				w = f.request("admin", "POST", c6Path(id)+"/plans/"+p.ID+"/review", nil)
			} else {
				w = f.decision("admin", id, p, []string{}, nil)
			}
			assertDone(t, w, runtime.EndCompleted)
			p = f.latest(t, "admin", id)
			item = p.Items[0]
			if p.Status != "applied" || item.Decision != "accepted" || item.OperationID == nil || *item.OperationID != recorded.ID || f.count(t) != 1 || f.title(t, f.releases[0]) != "新标题" {
				t.Fatalf("已提交操作被当作未执行：%+v", p)
			}
			var got, want any
			if err := json.Unmarshal(item.Result, &got); err != nil {
				t.Fatal(err)
			}
			bound := tool.BoundResult(recorded.Output, item.Untrusted)
			if err := json.Unmarshal(bound.Output, &want); err != nil {
				t.Fatal(err)
			}
			var status string
			var code *string
			if err := f.db.QueryRow(`SELECT status,error_code FROM steward_tool_calls WHERE id=$1`, item.ToolCallID).Scan(&status, &code); err != nil {
				t.Fatal(err)
			}
			if status != "succeeded" || code != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("对账未恢复真实结果：%s want=%s status=%s code=%v", item.Result, bound.Output, status, code)
			}
			if next == "rejected_by_review" && (p.Review.Items[0].Decision != "rejected" || p.Review.ID == "") {
				t.Fatal("没有实际执行拒绝审查")
			}
		})
	}
}

func TestPostgreSQLStewardHTTPC7Round2ReviewMode(t *testing.T) {
	for _, mode := range []string{"assistant", "operator"} {
		t.Run(mode, func(t *testing.T) {
			f, id := newC7API(t, "openai", "admin")
			f.decisions = []string{"needs_human_confirmation"}
			assertDone(t, f.send("admin", id), runtime.EndAwaitingConfirmation)
			p := f.latest(t, "admin", id)
			c6SQL(t, f.c6API, `UPDATE steward_conversations SET mode=$2 WHERE id=$1`, id, mode)
			apiStatus(t, f.request("admin", "POST", c6Path(id)+"/plans/"+p.ID+"/review", nil), 409, "review_not_applicable")
			f.assertReviewWire(t, 1, "synthetic-model")
			f.unchanged(t)
			// 绕开 HTTP 预检，验证 runtime 占用会话后仍拒绝。
			err := f.api.runner.Run(context.Background(), runtime.RunParams{Caller: tool.Caller{User: identity.User{ID: f.users["admin"], Role: "admin"}}, ConversationID: id, ReviewPlanID: p.ID}, func(e runtime.Event) { t.Errorf("非 steward 重审产生事件：%+v", e) })
			if !errors.Is(err, runtime.ErrReviewNotApplicable) {
				t.Fatalf("运行时允许非 steward 重审：%v", err)
			}
		})
	}
}

func TestPostgreSQLStewardHTTPC7Round2CancelReview(t *testing.T) {
	f, id := newC7API(t, "openai", "admin")
	started := make(chan struct{})
	c7Round2Endpoint(t, f, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		if body["tool_choice"] != nil {
			close(started)
			<-r.Context().Done()
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		f.serveModel(w, r)
	})
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { result <- f.send("admin", id) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("审查请求未开始")
	}
	apiStatus(t, f.request("admin", "POST", c6Path(id)+"/cancel", nil), 200, "")
	select {
	case w := <-result:
		assertDone(t, w, runtime.EndCancelled)
		c7Round2NoReviewOrExecution(t, f, id, w)
	case <-time.After(5 * time.Second):
		t.Fatal("审查取消未及时结束")
	}
}

const c7Round2StorageSentinel = "C7_ROUND2_PRIVATE_DATABASE_ERROR"

type c7Round2FailReviewStore struct{ *runtime.Store }

func (s *c7Round2FailReviewStore) SaveReview(context.Context, string, string, runtime.Review) error {
	return errors.New(c7Round2StorageSentinel)
}

func TestPostgreSQLStewardHTTPC7Round2ReviewSaveFailure(t *testing.T) {
	f, id := newC7API(t, "openai", "admin")
	f.api.runner = runtime.NewRunner(&c7Round2FailReviewStore{f.store}, f.registry, f.api.logger).WithReviewEvidence(c7Round2Evidence(f))
	w := f.send("admin", id)
	if end := assertDone(t, w, ""); end.ErrorCode != "database_unavailable" || end.Message == "" || end.ErrorKind != "" {
		t.Fatalf("保存失败未统一映射：%s", w.Body)
	}
	c7Round2NoReviewOrExecution(t, f, id, w)
	if strings.Contains(w.Body.String(), c7Round2StorageSentinel) || strings.Contains(f.logs.String(), c7Round2StorageSentinel) {
		t.Fatal("数据库错误原文泄露")
	}
}

func TestPostgreSQLStewardHTTPC7Round2UnavailableLogCategories(t *testing.T) {
	for _, category := range []string{"upstream_error", "invalid_result", "truncated", "timeout"} {
		t.Run(category, func(t *testing.T) {
			f, id := newC7API(t, "openai", "admin")
			const sentinel = "C7_ROUND2_PRIVATE_UPSTREAM_ERROR"
			c7Round2Endpoint(t, f, func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				var body map[string]any
				_ = json.Unmarshal(raw, &body)
				if body["tool_choice"] == nil {
					r.Body = io.NopCloser(bytes.NewReader(raw))
					f.serveModel(w, r)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				switch category {
				case "upstream_error":
					http.Error(w, sentinel, 500)
				case "invalid_result":
					_, _ = io.WriteString(w, wireEvent("", `{"choices":[{"index":0,"delta":{"content":"`+sentinel+`"},"finish_reason":"stop"}]}`)+wireEvent("", "[DONE]"))
				case "truncated":
					_, _ = io.WriteString(w, wireEvent("", `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"log-test","function":{"name":"submit_review","arguments":"{\"`+sentinel+`\":"}}]},"finish_reason":null}]}`))
				case "timeout":
					<-r.Context().Done()
				}
			})
			client := &http.Client{Timeout: 80 * time.Millisecond}
			p, err := provider.NewOpenAI(client, f.endpoint, apiSyntheticKey)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var events []runtime.Event
			err = f.api.runner.Run(ctx, runtime.RunParams{Caller: tool.Caller{User: identity.User{ID: f.users["admin"], Role: "admin"}, Source: tool.SourceChat}, ConversationID: id, Text: c7Message, Model: "synthetic-model", Provider: p}, func(e runtime.Event) { events = append(events, e) })
			if err != nil || len(events) == 0 || events[len(events)-1].End == nil || events[len(events)-1].End.Reason != runtime.EndReviewUnavailable {
				t.Fatalf("不可用未关闭：%+v %v", events, err)
			}
			count := 0
			for _, line := range strings.Split(f.logs.String(), "\n") {
				var entry map[string]any
				if strings.TrimSpace(line) == "" {
					continue
				}
				if err := json.Unmarshal([]byte(line), &entry); err != nil {
					t.Fatal(err)
				}
				if entry["event"] == "steward.review.finished" {
					count++
					if entry["reason_category"] != category || entry["status"] != "unavailable" {
						t.Errorf("类别错误：%s", line)
					}
				}
			}
			if count != 1 || strings.Contains(f.logs.String(), sentinel) {
				t.Fatalf("日志缺失、重复或泄露：%s", f.logs.String())
			}
			f.unchanged(t)
		})
	}
}

func TestPostgreSQLStewardHTTPC7Round2DecisionAutoApprove(t *testing.T) {
	for _, mode := range []string{"steward", "assistant"} {
		t.Run(mode, func(t *testing.T) {
			f, id := newC7API(t, "openai", "admin")
			f.decisions = []string{"needs_human_confirmation"}
			assertDone(t, f.send("admin", id), runtime.EndAwaitingConfirmation)
			if mode == "assistant" {
				c6SQL(t, f.c6API, `UPDATE steward_conversations SET mode='assistant' WHERE id=$1`, id)
			}
			before := f.latest(t, "admin", id)
			w := f.decision("admin", id, before, []string{before.Items[0].ID}, []string{"correct_release"})
			if mode == "assistant" {
				assertDone(t, w, runtime.EndCompleted)
				if f.count(t) != 1 {
					t.Fatal("assistant 免确认被拒绝")
				}
				return
			}
			apiStatus(t, w, 400, "invalid_input")
			// 事务接口也必须拒绝，不能只靠 HTTP 预检。
			err := f.store.DecidePlan(context.Background(), f.users["admin"], id, runtime.PlanDecision{PlanID: before.ID, Digest: before.Digest, AcceptedItemIDs: []string{before.Items[0].ID}, AutoApproveTools: []string{"correct_release"}})
			if !errors.Is(err, runtime.ErrInvalidInput) {
				t.Fatalf("事务接受了免确认：%v", err)
			}
			after := f.latest(t, "admin", id)
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("非法输入改变方案：before=%+v after=%+v", before, after)
			}
			f.unchanged(t)
		})
	}
}

func TestPostgreSQLStewardHTTPC7Round2PendingItemCount(t *testing.T) {
	f, id := newC7API(t, "openai", "admin")
	counts := func(role string) map[string]int {
		t.Helper()
		w := f.request(role, "GET", "me/steward/conversations", nil)
		apiStatus(t, w, 200, "")
		var page struct {
			Items []struct {
				ID    string `json:"id"`
				Count *int   `json:"pending_item_count"`
			} `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		result := map[string]int{}
		for _, item := range page.Items {
			if item.Count == nil {
				t.Fatalf("缺少 pending_item_count：%s", w.Body)
			}
			result[item.ID] = *item.Count
		}
		return result
	}
	if got, ok := counts("admin")[id]; !ok || got != 0 {
		t.Fatal("无方案未返回 0")
	}
	f.calls = append(f.calls, c6Correct(f.releases[1], 1, "第二标题"), c6Correct(f.releases[2], 1, "第三标题"))
	f.decisions = []string{"needs_human_confirmation"}
	assertDone(t, f.send("admin", id), runtime.EndAwaitingConfirmation)
	if counts("admin")[id] != 3 {
		t.Fatal("待确认项未全部计数")
	}
	// 重审仅处理一项，其余两项继续等待人工确认。
	f.decisions = []string{"approved", "needs_human_confirmation", "needs_human_confirmation"}
	p := f.latest(t, "admin", id)
	assertDone(t, f.request("admin", "POST", c6Path(id)+"/plans/"+p.ID+"/review", nil), runtime.EndAwaitingConfirmation)
	if counts("admin")[id] != 2 {
		t.Fatal("部分处理后未减少")
	}
	if _, ok := counts("other")[id]; ok {
		t.Fatal("其他用户列出了本人的待确认会话")
	}
	other, err := f.store.CreateConversation(context.Background(), f.users["other"], "其他用户会话", "synthetic-model")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := counts("other")[other.ID]; !ok || got != 0 {
		t.Fatal("其他用户无方案会话错误")
	}
	if _, ok := counts("admin")[other.ID]; ok {
		t.Fatal("本人列表包含其他用户会话")
	}
	c6SQL(t, f.c6API, `UPDATE steward_plans SET expires_at=now()-interval '1 second' WHERE id=$1`, p.ID)
	if counts("admin")[id] != 0 {
		t.Fatal("过期方案仍计数")
	}
}
