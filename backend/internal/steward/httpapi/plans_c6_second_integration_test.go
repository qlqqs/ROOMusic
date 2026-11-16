package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	governancetools "github.com/qlqq/roomusic/backend/internal/catalog/governance/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/identity"
	operationtools "github.com/qlqq/roomusic/backend/internal/operations/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func c6Invoke(t *testing.T, f *c6API, id string, call c6Call) tool.Result {
	t.Helper()
	raw, err := json.Marshal(call.input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.registry.Invoke(context.Background(), tool.Caller{User: identity.User{ID: f.users["admin"], Role: "admin"}, Source: tool.SourceChat, ConversationID: id, Mode: "operator", SharedExecution: true, IdempotencyKey: uuid.New()}, call.name, raw)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func c6RevertCall(t *testing.T, f *c6API, id string) c6Call {
	t.Helper()
	for _, d := range operationtools.RevertTools(f.db, governance.CorrectionOperations()) {
		if err := f.registry.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	result := c6Invoke(t, f, id, c6Correct(f.releases[0], 1, "撤销目标"))
	var operation struct {
		ID string `json:"operation_id"`
	}
	if err := json.Unmarshal(result.Output, &operation); err != nil || operation.ID == "" {
		t.Fatalf("操作结果=%s err=%v", result.Output, err)
	}
	return c6Call{"revert_operation", map[string]any{"operation_id": operation.ID}}
}

func TestPostgreSQLStewardHTTPC6RevertRegistryRuntime(t *testing.T) {
	f, id := newC6API(t, "admin", "assistant")
	f.calls = []c6Call{c6RevertCall(t, f, id)}
	p := f.plan(t, "admin", id)
	if len(p.Items) != 1 || p.Items[0].Revertible || len(p.Items[0].Changes) != 1 || p.Items[0].Changes[0].Before != "撤销目标" || p.Items[0].Changes[0].After != "原名" {
		t.Fatalf("撤销预览=%+v", p)
	}
	if f.title(t, f.releases[0]) != "撤销目标" || f.count(t) != 1 {
		t.Fatal("预览执行了撤销")
	}
	assertDone(t, f.decision("admin", id, p, []string{p.Items[0].ID}, nil), runtime.EndCompleted)
	if f.title(t, f.releases[0]) != "原名" || f.count(t) != 2 {
		t.Fatal("注册表→运行时→确认未执行撤销")
	}
	f.origin(t, id, p.ID, "assistant", 1)
	var count int
	if err := f.db.QueryRow(`SELECT count(*) FROM operations WHERE reverts_operation_id IS NOT NULL`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("撤销记录=%d err=%v", count, err)
	}
}

func TestPostgreSQLStewardHTTPC6D7AutoApproval(t *testing.T) {
	for _, name := range []string{"irreversible_rejected", "unaccepted_tool_rejected", "assistant_existing_name", "operator_direct"} {
		t.Run(name, func(t *testing.T) {
			mode := "assistant"
			if name == "operator_direct" {
				mode = "operator"
			}
			f, id := newC6API(t, "admin", mode)
			revert := c6RevertCall(t, f, id)
			f.calls = []c6Call{revert, c6Correct(f.releases[1], 1, "可撤销修改")}
			if name == "assistant_existing_name" || name == "operator_direct" {
				c6SQL(t, f, `UPDATE steward_conversations SET auto_approved_tools=ARRAY['revert_operation'] WHERE id=$1`, id)
				f.calls = f.calls[:1]
				if name == "operator_direct" {
					assertDone(t, f.request("admin", "POST", c6Path(id)+"/messages", map[string]string{"text": "撤销"}), runtime.EndCompleted)
					if f.title(t, f.releases[0]) != "原名" || f.count(t) != 2 {
						t.Fatal("operator 未直接执行不可撤销工具")
					}
					f.origin(t, id, "", "operator", 2)
				} else {
					p := f.plan(t, "admin", id)
					if len(p.Items) != 1 || p.Items[0].Revertible || f.title(t, f.releases[0]) != "撤销目标" || f.count(t) != 1 {
						t.Fatalf("旧名单绕过不可撤销工具确认：%+v", p)
					}
				}
				return
			}
			p := f.plan(t, "admin", id)
			accepted := []string{p.Items[0].ID, p.Items[1].ID}
			tools := []string{"correct_release", "revert_operation"}
			if name == "unaccepted_tool_rejected" {
				// correct_release 存在于方案且可撤销，但本次没有任何一项被接受。
				accepted = []string{p.Items[0].ID}
				tools = []string{"correct_release"}
			}
			before, err := postgrestest.Snapshot(context.Background(), f.db)
			if err != nil {
				t.Fatal(err)
			}
			apiStatus(t, f.decision("admin", id, p, accepted, tools), 400, "invalid_input")
			after, err := postgrestest.Snapshot(context.Background(), f.db)
			if err != nil {
				t.Fatal(err)
			}
			if diff := postgrestest.Diff(before, after); len(diff) != 0 {
				t.Fatalf("D7 拒绝未整体回滚：%v", diff)
			}
		})
	}
}

func TestPostgreSQLStewardHTTPC6NewMessageReconcilesOnly(t *testing.T) {
	for _, lookupFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("lookup_failure_%v", lookupFails), func(t *testing.T) {
			f, id := newC6API(t, "admin", "assistant")
			f.calls = append(f.calls, c6Correct(f.releases[2], 1, "不选择"))
			p := f.plan(t, "admin", id)
			f.api.runner = runtime.NewRunner(&c6FailPlanResultOnce{Store: f.store, fail: true}, f.registry, f.api.logger)
			w := f.decision("admin", id, p, []string{p.Items[0].ID, p.Items[1].ID}, nil)
			if !strings.Contains(w.Body.String(), "database_unavailable") || f.count(t) != 1 {
				t.Fatalf("未制造提交后中断：%s", w.Body)
			}
			before, err := f.store.GetConversation(context.Background(), f.users["admin"], id)
			if err != nil {
				t.Fatal(err)
			}
			var keys []string
			f.store.WithOperationLookup(func(ctx context.Context, tx *sql.Tx, user, key string) (runtime.RecordedOperation, error) {
				keys = append(keys, key)
				if lookupFails && len(keys) == 2 {
					return runtime.RecordedOperation{}, fmt.Errorf("C6 合成查询失败")
				}
				return c6OperationLookup(ctx, tx, user, key)
			})
			if lookupFails {
				snapshot, err := postgrestest.Snapshot(context.Background(), f.db)
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.store.AppendMessage(context.Background(), f.users["admin"], id, runtime.MessageInput{Role: "user", Content: json.RawMessage(`{"role":"user","content":[{"type":"text","text":"对账失败要回滚"}]}`)})
				if err == nil {
					t.Fatal("查询失败仍追加消息")
				}
				after, err := postgrestest.Snapshot(context.Background(), f.db)
				if err != nil {
					t.Fatal(err)
				}
				if diff := postgrestest.Diff(snapshot, after); len(diff) != 0 {
					t.Fatalf("首项收尾、历史或新消息未回滚：%v", diff)
				}
			} else {
				assertDone(t, f.request("admin", "POST", c6Path(id)+"/messages", map[string]string{"text": "跳过剩余修改，回答新问题"}), runtime.EndCompleted)
				after, err := f.store.GetConversation(context.Background(), f.users["admin"], id)
				if err != nil {
					t.Fatal(err)
				}
				items := after.Plans[0].Items
				var operation string
				if err := f.db.QueryRow(`SELECT id::text FROM operations`).Scan(&operation); err != nil {
					t.Fatal(err)
				}
				if items[0].OperationID == nil || *items[0].OperationID != operation || !strings.Contains(string(items[0].Result), operation) {
					t.Fatalf("原操作未对账：%+v", items[0])
				}
				for i, code := range map[int]string{1: "not_executed", 2: "declined_by_user"} {
					if items[i].OperationID != nil || !strings.Contains(string(items[i].Result), code) {
						t.Fatalf("项%d=%+v", i, items[i])
					}
				}
				if f.count(t) != 1 || f.title(t, f.releases[1]) != "原名" || f.title(t, f.releases[2]) != "原名" {
					t.Fatal("普通新消息执行了旧批准项")
				}
				if len(after.Messages) != len(before.Messages)+5 {
					t.Fatalf("历史未补齐：before=%d after=%d", len(before.Messages), len(after.Messages))
				}
				f.wireMu.Lock()
				last := f.wire[len(f.wire)-1]
				f.wireMu.Unlock()
				for _, value := range []string{operation, "not_executed", "declined_by_user", "跳过剩余修改"} {
					if !strings.Contains(last, value) {
						t.Errorf("模型历史缺少 %s", value)
					}
				}
			}
			want := []string{before.Plans[0].Items[0].IdempotencyKey, before.Plans[0].Items[1].IdempotencyKey}
			if !reflect.DeepEqual(keys, want) {
				t.Fatalf("查询键=%v want=%v", keys, want)
			}
		})
	}
}

func TestPostgreSQLStewardHTTPC6DirectCommitReconciliation(t *testing.T) {
	for _, mode := range []string{"operator", "assistant"} {
		for _, uncertain := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/still_uncertain_%v", mode, uncertain), func(t *testing.T) {
				f, id := newC6API(t, "admin", mode)
				var keys []string
				for _, d := range governancetools.StewardTools(f.db, nil) {
					if d.Name != "correct_release" {
						continue
					}
					original := d.Handler
					d.Name = "c6_uncertain_write"
					d.Handler = func(ctx context.Context, caller tool.Caller, input json.RawMessage) (json.RawMessage, error) {
						keys = append(keys, caller.IdempotencyKey)
						raw, err := original(ctx, caller, input)
						if err != nil {
							return nil, err
						}
						if len(keys) == 1 || uncertain {
							return nil, &tool.Error{Code: "database_unavailable", Message: "C6 合成提交后失败"}
						}
						return raw, nil
					}
					if err := f.registry.Register(d); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "assistant" {
					c6SQL(t, f, `UPDATE steward_conversations SET auto_approved_tools=ARRAY['c6_uncertain_write','correct_release'] WHERE id=$1`, id)
				}
				first := c6Correct(f.releases[0], 1, "只执行一次")
				first.name = "c6_uncertain_write"
				f.calls = []c6Call{first, c6Correct(f.releases[1], 1, "后续调用")}
				w := f.request("admin", "POST", c6Path(id)+"/messages", map[string]string{"text": "直接执行并对账"})
				if len(keys) != 2 || keys[0] == "" || keys[0] != keys[1] {
					t.Fatalf("未按原键对账：%v 响应=%s", keys, w.Body)
				}
				var stored string
				if err := f.db.QueryRow(`SELECT idempotency_key::text FROM steward_tool_calls WHERE tool_name='c6_uncertain_write'`).Scan(&stored); err != nil || stored != keys[0] {
					t.Fatalf("持久键=%s 调用键=%v err=%v", stored, keys, err)
				}
				if uncertain {
					apiStatus(t, w, 200, "")
					if !strings.Contains(w.Body.String(), `"error_code":"database_unavailable"`) || strings.Contains(w.Body.String(), "event: tool_finished") || f.hits.Load() != 1 || f.count(t) != 1 || f.title(t, f.releases[1]) != "原名" {
						t.Fatalf("不确定后仍续跑：hits=%d operations=%d %s", f.hits.Load(), f.count(t), w.Body)
					}
					var outputs int
					if err := f.db.QueryRow(`SELECT count(*) FROM steward_messages WHERE role='tool'`).Scan(&outputs); err != nil || outputs != 0 {
						t.Fatalf("不确定时保存了确定结果：%d err=%v", outputs, err)
					}
				} else {
					assertDone(t, w, runtime.EndCompleted)
					if f.count(t) != 2 || f.hits.Load() != 2 || f.title(t, f.releases[0]) != "只执行一次" || f.title(t, f.releases[1]) != "后续调用" {
						t.Fatalf("对账成功后续跑错误：%s", w.Body)
					}
				}
			})
		}
	}
}

func TestPostgreSQLStewardHTTPC6NULInputBoundary(t *testing.T) {
	for _, mode := range []string{"assistant", "operator"} {
		for _, literal := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/literal_%v", mode, literal), func(t *testing.T) {
				f, id := newC6API(t, "admin", mode)
				title := "含\x00字符"
				if literal {
					title = `字面量\u0000文本`
				}
				f.calls = []c6Call{c6Correct(f.releases[0], 1, title)}
				if mode == "assistant" && literal {
					p := f.plan(t, "admin", id)
					if p.Items[0].Changes[0].After != title {
						t.Fatalf("字面量被改写：%+v", p)
					}
					assertDone(t, f.decision("admin", id, p, []string{p.Items[0].ID}, nil), runtime.EndCompleted)
				} else {
					w := f.request("admin", "POST", c6Path(id)+"/messages", map[string]string{"text": "字符边界"})
					assertDone(t, w, runtime.EndCompleted)
					if !literal && (!strings.Contains(w.Body.String(), "validation_failed") || strings.Contains(w.Body.String(), "event: plan")) {
						t.Fatalf("NUL 未按工具输入错误拒绝：%s", w.Body)
					}
				}
				if literal {
					if f.title(t, f.releases[0]) != title || f.count(t) != 1 {
						t.Fatal("字面量未放行")
					}
				} else {
					f.unchanged(t)
				}
			})
		}
	}
}

func TestPostgreSQLStewardHTTPC6PlanResultKeepsTruncated(t *testing.T) {
	for _, reconcile := range []bool{false, true} {
		t.Run(fmt.Sprintf("reconcile_%v", reconcile), func(t *testing.T) {
			f, id := newC6API(t, "admin", "assistant")
			raw, _ := json.Marshal(map[string]string{"text": strings.Repeat("合成内容", 20000)})
			if err := f.registry.Register(tool.Definition{Name: "c6_large_result", Scope: tool.ScopeShared, MinRole: tool.RoleAdmin, Revertible: true, Untrusted: true, Input: json.RawMessage(`{"type":"object"}`), Output: json.RawMessage(`{}`), Preview: func(context.Context, tool.Caller, json.RawMessage) (tool.Preview, error) {
				return tool.Preview{Target: "合成目标"}, nil
			}, Handler: func(context.Context, tool.Caller, json.RawMessage) (json.RawMessage, error) { return raw, nil }}); err != nil {
				t.Fatal(err)
			}
			f.calls = []c6Call{{"c6_large_result", map[string]any{}}}
			p := f.plan(t, "admin", id)
			if reconcile {
				if err := f.store.DecidePlan(context.Background(), f.users["admin"], id, runtime.PlanDecision{PlanID: p.ID, Digest: p.Digest, AcceptedItemIDs: []string{p.Items[0].ID}}); err != nil {
					t.Fatal(err)
				}
				if err := f.store.ReleaseConversation(context.Background(), f.users["admin"], id); err != nil {
					t.Fatal(err)
				}
				f.store.WithOperationLookup(func(context.Context, *sql.Tx, string, string) (runtime.RecordedOperation, error) {
					return runtime.RecordedOperation{Output: raw}, nil
				})
				// 使用真实操作 ID，验证被截断结果无法携带 operation_id 时仍保留查询返回的标识。
				seed := c6Invoke(t, f, id, c6Correct(f.releases[0], 1, "合成记录"))
				var op struct {
					ID string `json:"operation_id"`
				}
				if err := json.Unmarshal(seed.Output, &op); err != nil {
					t.Fatal(err)
				}
				f.store.WithOperationLookup(func(context.Context, *sql.Tx, string, string) (runtime.RecordedOperation, error) {
					return runtime.RecordedOperation{ID: op.ID, Output: raw}, nil
				})
				assertDone(t, f.request("admin", "POST", c6Path(id)+"/messages", map[string]string{"text": "仅对账"}), runtime.EndCompleted)
				var actual string
				if err := f.db.QueryRow(`SELECT operation_id::text FROM steward_plan_items WHERE id=$1`, p.Items[0].ID).Scan(&actual); err != nil || actual != op.ID {
					t.Fatalf("截断后丢操作标识：%s %v", actual, err)
				}
			} else {
				w := f.decision("admin", id, p, []string{p.Items[0].ID}, nil)
				assertDone(t, w, runtime.EndCompleted)
				if !strings.Contains(w.Body.String(), `"truncated":true`) {
					t.Fatal("事件丢失截断标记")
				}
			}
			var truncated, source bool
			if err := f.db.QueryRow(`SELECT t.truncated,(m.content::jsonb->'source'->>'truncated')::boolean FROM steward_tool_calls t JOIN steward_plan_items i ON i.tool_call_id=t.id JOIN steward_messages m ON m.conversation_id=$2::uuid AND m.role='tool' WHERE i.id=$1`, p.Items[0].ID, id).Scan(&truncated, &source); err != nil || !truncated || !source {
				t.Fatalf("截断元数据：call=%v source=%v err=%v", truncated, source, err)
			}
		})
	}
}

func TestPostgreSQLStewardHTTPC6CancelAfterPendingCall(t *testing.T) {
	f, id := newC6API(t, "admin", "assistant")
	if err := f.registry.Register(tool.Definition{Name: "c6_cancel", Scope: tool.ScopeRead, MinRole: tool.RoleAdmin, Input: json.RawMessage(`{"type":"object"}`), Output: json.RawMessage(`{}`), Handler: func(context.Context, tool.Caller, json.RawMessage) (json.RawMessage, error) {
		response := f.request("admin", "POST", c6Path(id)+"/cancel", nil)
		if response.Code != 200 || !strings.Contains(response.Body.String(), `"cancelled":true`) {
			return nil, fmt.Errorf("取消失败：%s", response.Body)
		}
		return json.RawMessage(`{"cancel_requested":true}`), nil
	}}); err != nil {
		t.Fatal(err)
	}
	f.calls = []c6Call{c6Correct(f.releases[0], 1, "等待确认"), {"c6_cancel", map[string]any{}}, c6Correct(f.releases[1], 1, "不应执行")}
	w := f.request("admin", "POST", c6Path(id)+"/messages", map[string]string{"text": "等待调用之后取消"})
	assertDone(t, w, runtime.EndCancelled)
	if strings.Contains(w.Body.String(), "database_unavailable") || strings.Contains(w.Body.String(), "event: plan") || f.hits.Load() != 1 {
		t.Fatalf("取消变成存储错误或生成方案：%s", w.Body)
	}
	f.unchanged(t)
	d, err := f.store.GetConversation(context.Background(), f.users["admin"], id)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Plans) != 0 || len(d.Messages) != 5 || d.Conversation.Status != "idle" {
		t.Fatalf("取消历史不完整：%+v", d)
	}
	for _, i := range []int{0, 2} {
		call := d.Messages[1].ToolCalls[i]
		if call.Status != "failed" || call.ErrorCode == nil || *call.ErrorCode != "cancelled" || !strings.Contains(string(call.Output), "cancelled") {
			t.Fatalf("取消调用未收尾：%+v", call)
		}
	}
	// 每个模型调用都在历史中有且只有一条结果，不依赖完成消息的保存顺序。
	for _, call := range []string{"c6-1-0", "c6-1-1", "c6-1-2"} {
		count := 0
		for _, message := range d.Messages {
			if message.Role == "tool" && strings.Contains(string(message.Content), call) {
				count++
			}
		}
		if count != 1 {
			t.Errorf("调用 %s 的结果数=%d", call, count)
		}
	}
}
