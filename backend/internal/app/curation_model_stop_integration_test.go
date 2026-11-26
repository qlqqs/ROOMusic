package app

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
)

func TestPostgreSQLCurationModelConcurrentProviderCooldown(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failure  schedulerHTTPFailure
		cooldown time.Duration
	}{
		{"429_retry_after", schedulerHTTPFailure{status: http.StatusTooManyRequests, retryAfter: "120"}, 2 * time.Minute},
		{"503_backoff", schedulerHTTPFailure{status: http.StatusServiceUnavailable}, 10 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSchedulerFixture(t)
			f.configure(t, &f.admin)
			failed := f.pending(t, f.a)
			f.update(t, http.MethodPut, curationBase+"/settings", map[string]any{"enabled": true, "model_concurrency": 2})
			first := f.next(t, false)
			firstConversation := f.item(t, failed.ID).ConversationID
			active := f.pending(t, f.b)
			second := f.next(t, false)
			thirdRelease, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{
				title: "排队中的专辑", artist: "第三位艺人", tracks: []string{"present"},
			})
			waiting := f.pending(t, thirdRelease)
			first.fail <- tc.failure
			schedulerWait(t, "失败项释放领取", func() bool { return f.item(t, failed.ID).Status == "waiting_model" })
			if got := f.item(t, failed.ID); got.Attempts != 0 {
				t.Fatalf("模型服务故障计入失败次数：%+v", got)
			}

			second.selectCandidate(t, active.ID, "")
			schedulerWait(t, "在途发行正常升级第二档", func() bool { return f.item(t, active.ID).Route == "lookup" })
			f.clock.now.Add(int64(tc.cooldown - time.Second))
			select {
			case <-f.requests:
				t.Fatal("并发项完成后提前领取了新项")
			case <-time.After(100 * time.Millisecond):
			}
			if got := f.item(t, waiting.ID); got.Status != "waiting_model" || got.ConversationID != "" {
				t.Fatalf("冷却期间第三项被领取：%+v", got)
			}
			b3RequireCount(t, f.application, 2, `SELECT count(*) FROM steward_conversations WHERE kind='curation'`)

			f.clock.now.Add(int64(time.Second))
			resumed := f.next(t, false)
			got := f.item(t, failed.ID)
			if got.Status != "in_progress" || got.Attempts != 0 || got.ConversationID == firstConversation {
				t.Fatalf("服务恢复后失败项应按原尝试次数重新领取：%+v", got)
			}
			f.pause(t)
			resumed.complete(t)
		})
	}
}

func TestPostgreSQLCurationModelConcurrentProviderBackoff(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	const concurrency, readyItems = 3, 4
	for i := range readyItems {
		title := fmt.Sprintf("核对专辑 %d", i+1)
		release, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{
			title: title, artist: fmt.Sprintf("核对艺人 %d", i+1), tracks: []string{"present"},
		})
		queueModelVerification(t, f, "verify_name", curation.VerificationCandidate{
			ReleaseID: release, Field: "title", Before: title, After: "候选标题",
		})
	}
	f.update(t, http.MethodPut, curationBase+"/settings", map[string]any{"enabled": true, "model_concurrency": concurrency})
	var requests [concurrency]schedulerRequest
	for i := range requests {
		requests[i] = f.next(t, false)
	}
	if p := f.progress(t); p.Concurrency != concurrency || p.Running != concurrency || p.Items.WaitingModel != readyItems-concurrency {
		t.Fatalf("预期三个核对请求在途且一个核对项排队，实际进度 %+v", p)
	}

	start := f.clock.now.Load()
	failure := schedulerHTTPFailure{status: http.StatusServiceUnavailable}
	isCooling := func(p curation.ModelProgress) bool {
		return p.State == "unavailable" && p.UnavailableReason != nil && *p.UnavailableReason == "provider_error"
	}
	checkCooldown := func(elapsed time.Duration, want bool) {
		t.Helper()
		f.clock.now.Store(start + int64(elapsed))
		if p := f.progress(t); isCooling(p) != want {
			t.Fatalf("距首次失败 %s 时，预期冷却=%t，实际进度 %+v", elapsed, want, p)
		}
	}

	// 等每次失败释放领取后才推进时钟，由测试控制三次失败的先后顺序。
	for i, request := range requests {
		f.clock.now.Store(start + int64(time.Duration(i)*time.Second))
		request.fail <- failure
		schedulerWait(t, fmt.Sprintf("第 %d 个失败项释放且仍在冷却", i+1), func() bool {
			p := f.progress(t)
			return isCooling(p) && p.Running == concurrency-i-1 && p.Items.WaitingModel == int64(readyItems-concurrency+i+1)
		})
	}

	// 通过进度接口的状态变化约束冷却截止时刻。
	checkCooldown(10*time.Minute-time.Second, true)
	select {
	case <-f.requests:
		t.Fatal("冷却期间提前领取了核对项")
	case <-time.After(100 * time.Millisecond):
	}
	const lastFailure = (concurrency - 1) * time.Second
	checkCooldown(lastFailure+10*time.Minute, false)

	// 越过冷却截止时刻并确认调度器重新领取，挂起其他请求直到第四次失败。
	const nextFailure = lastFailure + 10*time.Minute + time.Second
	f.clock.now.Store(start + int64(nextFailure))
	for i := range requests {
		requests[i] = f.next(t, false)
	}
	requests[0].fail <- failure
	schedulerWait(t, "冷却到期后的失败项释放并重新进入冷却", func() bool {
		p := f.progress(t)
		return isCooling(p) && p.Running == concurrency-1 && p.Items.WaitingModel == readyItems-concurrency+1
	})
	checkCooldown(nextFailure+20*time.Minute-time.Second, true)
	checkCooldown(nextFailure+20*time.Minute, false)
	requests[0] = f.next(t, false)
	f.pause(t)
	for _, request := range requests {
		request.complete(t)
	}
	schedulerWait(t, "暂停后所有核对请求结束", func() bool { return f.progress(t).Running == 0 })
}

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
		{"logout", runtime.EndCompleted}, {"paused_on_final_step", runtime.EndCurationStopped},
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
			default:
				first.selectCandidate(t, item.ID, "")
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
				if got := f.item(t, item.ID); got.Status != "waiting_route" || got.Route != "lookup" {
					t.Fatalf("%s 后本项未能升级：%+v", tc.name, got)
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
