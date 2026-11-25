package app

import (
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
)

func (f *schedulerFixture) update(t *testing.T, method, path string, body any) {
	t.Helper()
	resp := f.application.request(t, method, path, body, &f.admin, nil)
	if resp.Code != 200 {
		t.Fatalf("%s %s HTTP %d %s", method, path, resp.Code, resp.Body)
	}
}

func schedulerEnd(t *testing.T, sub *runtime.Subscription) runtime.EndReason {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-sub.Events:
			if !ok {
				t.Fatal("运行订阅关闭前未收到结束原因")
			}
			if event.End != nil {
				return event.End.Reason
			}
		case <-timer.C:
			t.Fatal("未收到运行结束事件")
		}
	}
}

func TestPostgreSQLCurationModelStopConditions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason runtime.EndReason
	}{
		{"disabled", runtime.EndCurationStopped}, {"paused", runtime.EndCurationStopped},
		{"enabler_disabled", runtime.EndCurationStopped}, {"enabler_demoted", runtime.EndCurationStopped},
		{"enabler_changed", runtime.EndCurationStopped}, {"endpoint_cleared", runtime.EndCancelled},
		{"key_changed", runtime.EndCancelled}, {"limit", runtime.EndCompleted},
		{"scan", runtime.EndCurationStopped}, {"musicbrainz_disabled", runtime.EndCurationStopped},
		{"logout", runtime.EndCompleted}, {"paused_on_final_step", runtime.EndCompleted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSchedulerFixture(t)
			f.configure(t, &f.admin)
			other := f.application.createUser(t, "admin")
			if tc.name == "enabler_changed" {
				f.configure(t, &other)
			}
			item := f.pending(t, f.a)
			f.enable(t)
			first := f.next(t, false)
			conversation := f.item(t, item.ID).ConversationID
			sub := f.application.app.steward.Subscribe(conversation, 0)
			if sub == nil {
				t.Fatal("后台运行没有事件订阅")
			}
			defer sub.Close()
			switch tc.name {
			case "disabled":
				f.update(t, "PUT", curationBase+"/settings", map[string]bool{"enabled": false})
			case "paused", "paused_on_final_step":
				f.pause(t)
			case "enabler_disabled":
				mustExec(t, f.application, `UPDATE users SET disabled_at=now() WHERE id=$1`, f.admin.id)
			case "enabler_demoted":
				mustExec(t, f.application, `UPDATE users SET role='user' WHERE id=$1`, f.admin.id)
			case "enabler_changed":
				_, err := f.writer.SetSettings(t.Context(), other.id, true)
				requireCurationOK(t, err)
			case "endpoint_cleared":
				f.update(t, "PUT", stewardBase+"/settings", map[string]string{"endpoint": ""})
			case "key_changed":
				f.update(t, "PUT", stewardBase+"/settings", map[string]string{"key": "synthetic-test-key"})
			case "limit":
				f.update(t, "PUT", curationBase+"/settings", map[string]int{"daily_token_limit": 20})
			case "scan":
				mustExec(t, f.application, `INSERT INTO scan_runs(id,status,started_at) VALUES($1,'running',now())`, uuid.New())
			case "musicbrainz_disabled":
				f.update(t, "PUT", curationBase+"/sources/musicbrainz", map[string]bool{"enabled": false})
			case "logout":
				f.update(t, "POST", "/api/v1/auth/logout", nil)
			}
			switch {
			case tc.reason == runtime.EndCancelled:
				// 端点与密钥变化应在模型服务仍挂起时立即结束运行。
			case tc.name == "paused_on_final_step":
				first.complete(t)
			default:
				first.tool(t, "read_pending_items", map[string]any{})
				if tc.name == "logout" || tc.name == "limit" {
					next := f.next(t, false)
					next.tool(t, "shelve_pending_item", map[string]string{"item_id": item.ID, "reason": "资料不足"})
					f.next(t, false).complete(t)
				}
			}
			reason := schedulerEnd(t, sub)
			if reason != tc.reason {
				t.Errorf("当前一步后的结束原因为 %s，期望 %s", reason, tc.reason)
			}
			switch tc.name {
			case "enabler_changed", "key_changed":
				// 新身份或新密钥可领取新批次，旧会话必须先释放。
				resumed := f.next(t, false)
				claimed := f.item(t, item.ID)
				if claimed.ConversationID == conversation || claimed.Attempts != 0 {
					t.Fatalf("重新领取 %+v", claimed)
				}
				if tc.name == "enabler_changed" {
					b3RequireCount(t, f.application, 1, `SELECT count(*) FROM steward_conversations WHERE id=$1 AND user_id=$2`, claimed.ConversationID, other.id)
				}
				f.pause(t)
				resumed.complete(t)
			case "logout", "limit":
				if got := f.item(t, item.ID); got.Status != "shelved" {
					t.Fatalf("%s 后本项的工具未生效：%+v", tc.name, got)
				}
			default:
				schedulerWait(t, "释放领取", func() bool { return f.item(t, item.ID).Status == "waiting_model" })
				if got := f.item(t, item.ID); got.Attempts != 0 || got.ClaimedAt != nil || got.ConversationID != "" {
					t.Errorf("停止后领取信息应清空，未处理次数应为 0，实际 %+v", got)
				}
				select {
				case <-f.requests:
					t.Fatal("条件不满足时再次请求了模型")
				case <-time.After(100 * time.Millisecond):
				}
				b3RequireCount(t, f.application, 1, `SELECT count(*) FROM steward_conversations`)
			}
		})
	}
}
