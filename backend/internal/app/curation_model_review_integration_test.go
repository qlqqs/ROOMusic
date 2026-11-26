package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
)

func (q schedulerRequest) lastToolResult(t *testing.T) string {
	t.Helper()
	for i := len(q.Messages) - 1; i >= 0; i-- {
		if q.Messages[i].Role == "tool" {
			var text string
			requireCurationOK(t, json.Unmarshal(q.Messages[i].Content, &text))
			return text
		}
	}
	t.Fatal("主模型未收到工具结果")
	return ""
}

func schedulerCorrection(release string, revision int, field string, change any) map[string]any {
	return map[string]any{"release_id": release, "expected_revision": revision, "changes": map[string]any{field: change},
		"basis": map[string]string{"source": "external", "note": "合成发行资料"}}
}

func TestPostgreSQLCurationModelResolveMatchProgramChain(t *testing.T) {
	f := newSchedulerFixture(t)
	target, group := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{title: "合成正式专辑", artist: "合成正式艺人", tracks: []string{"present", "present"}})
	f.enable(t)
	curationCApplyLinks(t, f.curationFixture, curationCLinkRequest(f.b, "", curationCLink("release_group", f.groupB, f.remote.Group)))
	item := f.pending(t, target)
	f.configure(t, &f.admin)
	request := f.next(t, false)
	var content string
	requireCurationOK(t, json.Unmarshal(request.Messages[len(request.Messages)-1].Content, &content))
	items, found, err := metricsPendingItems(content)
	requireCurationOK(t, err)
	if !found || len(items) != 1 || items[0].ID != item.ID || len(items[0].Candidates) != 1 || items[0].Candidates[0].ID != f.remote.ID {
		t.Fatalf("首条消息缺少候选：%s", content)
	}
	request.selectCandidate(t, item.ID, f.remote.ID)
	f.next(t, true).review(t, "approved")
	schedulerWait(t, "审查通过后写入对应", func() bool {
		p := f.item(t, item.ID)
		return p.Status == "resolved" && p.Resolution != nil && *p.Resolution == "model_matched"
	})
	batch := programString(t, f.application, `SELECT id FROM curation_batches WHERE tier='model'`)
	schedulerWait(t, "关闭模型批次", func() bool {
		return countRows(t, f.application, `SELECT count(*) FROM curation_batches WHERE id=$1 AND closed_at IS NOT NULL`, batch) == 1
	})
	for _, kind := range []string{"external_link", "release_auto_correction", "release_group_change"} {
		b3RequireCount(t, f.application, 1, `SELECT count(*) FROM operations WHERE batch_id=$1 AND operation_type=$2 AND actor_id=$3 AND origin->>'provider_kind'='model' AND origin->>'provider'='test-main'`, batch, kind, f.admin.id)
	}
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM releases WHERE id=$1 AND group_id=$2`, target, f.groupB)
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM release_effective WHERE id=$1 AND year=2024`, target)
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM external_links WHERE source='musicbrainz' AND object_id=$1 AND external_id=$2`, target, f.remote.ID)
	for _, recording := range f.remote.Recordings {
		b3RequireCount(t, f.application, 1, `SELECT count(*) FROM external_links WHERE source='musicbrainz' AND object_type='track' AND external_id=$1`, recording)
	}
	linkOp := programString(t, f.application, `SELECT id FROM operations WHERE batch_id=$1 AND operation_type='external_link'`, batch)
	f.undo(t, linkOp)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM external_links WHERE operation_id=$1`, linkOp)
	result := f.scope(t, curation.Scope{Kind: "batch", ID: batch}, "", uuid.New(), uuid.New())
	if result.Reverted != 2 || len(result.Skipped) != 0 {
		t.Fatalf("撤销剩余两条操作 %+v", result)
	}
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM releases WHERE id=$1 AND group_id=$2`, target, group)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM release_auto_values WHERE release_id=$1`, target)
}

func TestPostgreSQL整理审查未采用即结项(t *testing.T) {
	for _, tc := range []struct{ name, review, reason string }{
		{"拒绝", "rejected", "review_rejected"}, {"拿不准", "needs_human_confirmation", "review_uncertain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSchedulerFixture(t)
			f.configure(t, &f.admin)
			release, _, item := f.matchFixture(t)
			f.enable(t)
			f.next(t, false).selectCandidate(t, item.ID, f.remote.ID)
			review := f.next(t, true)
			conversation := f.item(t, item.ID).ConversationID
			review.review(t, tc.review)
			schedulerWait(t, "建议保存并结项", func() bool {
				p := f.progress(t)
				return p.State == "idle" && p.Items.WaitingModel == 0 && p.Items.InProgress == 0
			})
			detail, err := runtime.NewStore(f.application.database).GetConversation(t.Context(), f.admin.id, conversation)
			requireCurationOK(t, err)
			if len(detail.Plans) != 1 || len(detail.Plans[0].Items) != 1 || detail.Plans[0].Items[0].Decision != "rejected_by_review" ||
				!strings.Contains(string(detail.Plans[0].Items[0].Result), "rejected_by_review") {
				t.Fatalf("方案取舍 %+v", detail.Plans)
			}
			exceptions, total, err := f.writer.ListExceptions(t.Context(), "open", 1, 10)
			requireCurationOK(t, err)
			if total != 1 {
				t.Fatalf("建议 %+v", exceptions)
			}
			ex := exceptions[0]
			finished := f.item(t, item.ID)
			if ex.PendingItemID != item.ID || ex.ReasonCode != tc.reason || !ex.CanAccept || ex.Review.Decision != tc.review ||
				len(ex.Reasons) != 1 || ex.Reasons[0] != "合成审查理由" || finished.Status != "resolved" ||
				finished.Resolution == nil || *finished.Resolution != "suggestion_recorded" || f.progress(t).Items.WaitingHuman != 0 {
				t.Fatalf("建议 %+v，结项 %+v", ex, finished)
			}
			b3RequireCount(t, f.application, 0, `SELECT count(*) FROM operations WHERE target_id=$1`, release)
		})
	}
}

func TestPostgreSQLCurationModelReviewUnavailableBackoff(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	_, _, item := f.matchFixture(t)
	f.enable(t)
	f.next(t, false).selectCandidate(t, item.ID, f.remote.ID)
	conversation := f.item(t, item.ID).ConversationID
	delays := []time.Duration{5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 24 * time.Hour}
	for i, delay := range delays {
		// 审查必须提交结构化决定，纯文本应走不可用分支。
		f.next(t, true).complete(t)
		want := f.clock.options().Now().Add(delay)
		schedulerWait(t, "记录下一次尝试时间", func() bool {
			got := f.item(t, item.ID)
			return got.NextAttemptAt != nil && got.NextAttemptAt.Equal(want)
		})
		got := f.item(t, item.ID)
		b3RequireCount(t, f.application, 0, `SELECT count(*) FROM operations WHERE target_id=$1`, item.TargetID)
		if i < 3 {
			if got.Status != "in_progress" || got.ConversationID != conversation || int(got.ReviewRetries) != i+1 {
				t.Fatalf("第 %d 次退避 %+v", i+1, got)
			}
		} else if got.Status != "waiting_model" || got.ConversationID != "" || got.Attempts != 0 {
			t.Fatalf("重审用尽 %+v", got)
		}
		f.clock.now.Add(int64(delay - time.Second))
		select {
		case q := <-f.requests:
			t.Fatalf("到期前已请求模型 %q", q.Model)
		case <-time.After(100 * time.Millisecond):
		}
		if i == 0 {
			f.update(t, "PUT", curationBase+"/settings", map[string]int{"daily_token_limit": 1})
		}
		f.clock.now.Add(int64(time.Second))
		if i == 0 {
			select {
			case <-f.requests:
				t.Fatal("日额用尽时启动了排队重审")
			case <-time.After(100 * time.Millisecond):
			}
			got := f.item(t, item.ID)
			if got.ConversationID != conversation || got.ReviewRetries != 1 {
				t.Fatalf("日额用尽时应保留重审方案：%+v", got)
			}
			f.update(t, "PUT", curationBase+"/settings", map[string]any{"daily_token_limit": nil})
		}
	}
	resumed := f.next(t, false)
	if got := f.item(t, item.ID); got.ConversationID == conversation || got.Status != "in_progress" {
		t.Fatalf("24 小时后重新领取 %+v", got)
	}
	f.pause(t)
	resumed.complete(t)
}

func TestPostgreSQLCurationModelReviewProviderCooldown(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	_, _, item := f.matchFixture(t)
	f.enable(t)
	f.next(t, false).selectCandidate(t, item.ID, f.remote.ID)
	review := f.next(t, true)
	conversation := f.item(t, item.ID).ConversationID
	review.fail <- schedulerHTTPFailure{status: 503, retryAfter: "90"}
	schedulerWait(t, "审查模型冷却时释放领取", func() bool { return f.item(t, item.ID).Status == "waiting_model" })
	got := f.item(t, item.ID)
	if got.Attempts != 0 || got.ReviewRetries != 0 || got.NextAttemptAt != nil {
		t.Fatalf("审查接口 503 后的待定项 %+v", got)
	}
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM steward_plans WHERE conversation_id=$1 AND status='superseded'`, conversation)
	f.clock.now.Add(int64(90 * time.Second))
	resumed := f.next(t, false)
	if f.item(t, item.ID).ConversationID == conversation {
		t.Fatal("冷却到期后应重新领取")
	}
	f.pause(t)
	resumed.complete(t)
}
