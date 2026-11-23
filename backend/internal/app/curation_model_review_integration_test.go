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
	candidates, err := json.Marshal([]map[string]string{{"external_id": f.remote.ID, "title": "合成正式专辑"}})
	requireCurationOK(t, err)
	mustExec(t, f.application, `UPDATE curation_pending_items SET candidates=$2 WHERE id=$1`, item.ID, candidates)
	f.configure(t, &f.admin)
	f.next(t, false).tool(t, "read_pending_items", map[string]any{})
	selectMatch := f.next(t, false)
	seen := selectMatch.lastToolResult(t)
	if !strings.Contains(seen, item.ID) || !strings.Contains(seen, f.remote.ID) {
		t.Fatalf("读取待定项返回 %s", seen)
	}
	selectMatch.tool(t, "resolve_release_match", map[string]any{"item_id": item.ID, "external_id": f.remote.ID, "basis": map[string]string{"note": "两首录音与候选一致"}})
	f.next(t, true).review(t, "approved")
	last := f.next(t, false)
	resolved := f.item(t, item.ID)
	if resolved.Status != "resolved" || resolved.Resolution == nil || *resolved.Resolution != "model_matched" {
		t.Fatalf("审查通过后待定项 %+v，工具结果 %s", resolved, last.lastToolResult(t))
	}
	last.complete(t)
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

func TestPostgreSQLCurationModelReviewPaths(t *testing.T) {
	for _, tc := range []struct{ review, decision, code string }{
		{"approved", "accepted", ""}, {"rejected", "rejected_by_review", "rejected_by_review"}, {"needs_human_confirmation", "escalated", "escalated_to_human"},
	} {
		t.Run(tc.review, func(t *testing.T) {
			f := newSchedulerFixture(t)
			f.configure(t, &f.admin)
			item := f.pending(t, f.a)
			f.enable(t)
			f.next(t, false).tool(t, "correct_release", schedulerCorrection(f.a, 1, "edition", setChange("合成版本")))
			review := f.next(t, true)
			conversation := f.item(t, item.ID).ConversationID
			review.review(t, tc.review)
			last := f.next(t, false)
			detail, err := runtime.NewStore(f.application.database).GetConversation(t.Context(), f.admin.id, conversation)
			requireCurationOK(t, err)
			if len(detail.Plans) != 1 || len(detail.Plans[0].Items) != 1 || detail.Plans[0].Items[0].Decision != tc.decision {
				t.Fatalf("方案取舍 %+v", detail.Plans)
			}
			exceptions, total, err := f.writer.ListExceptions(t.Context(), "open", 1, 10)
			requireCurationOK(t, err)
			if tc.review == "approved" {
				if total != 0 {
					t.Fatalf("批准后例外 %+v", exceptions)
				}
				b3RequireCount(t, f.application, 1, `SELECT count(*) FROM release_auto_values WHERE release_id=$1 AND field='edition' AND text_value='合成版本'`, f.a)
			} else {
				if total != 1 || len(exceptions) != 1 {
					t.Fatalf("例外 %+v", exceptions)
				}
				ex := exceptions[0]
				if ex.PendingItemID != item.ID || ex.Review.Decision != tc.review || len(ex.Review.Reasons) != 1 || ex.Review.Reasons[0] != "合成审查理由" || f.item(t, item.ID).Status != "waiting_human" || !strings.Contains(last.lastToolResult(t), tc.code) {
					t.Fatalf("例外 %+v，运行收到 %s", ex, last.lastToolResult(t))
				}
				b3RequireCount(t, f.application, 0, `SELECT count(*) FROM operations`)
			}
			f.pause(t)
			last.complete(t)
		})
	}
}

func TestPostgreSQLCurationModelReviewUnavailableBackoff(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	item := f.pending(t, f.a)
	f.enable(t)
	f.next(t, false).tool(t, "correct_release", schedulerCorrection(f.a, 1, "edition", setChange("合成版本")))
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
		f.clock.now.Add(int64(time.Second))
	}
	resumed := f.next(t, false)
	if got := f.item(t, item.ID); got.ConversationID == conversation || got.Status != "in_progress" {
		t.Fatalf("24 小时后重新领取 %+v", got)
	}
	f.pause(t)
	resumed.complete(t)
}
