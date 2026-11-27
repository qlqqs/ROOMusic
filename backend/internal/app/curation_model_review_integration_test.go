package app

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation/program"
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
	f, clock := newJudgeProgramFixture(t, nil)
	release, _, item := f.matchFixture(t)
	item = judgeFixtureItem(t, f, release, program.Candidate{ExternalID: f.remote.ID, Method: "tag_id"})
	f.configure(t, &f.admin)
	f.enable(t)
	request := f.next(t, false)
	requireCurationRuleItems(t, request, item.ID)
	request.selectCandidate(t, item.ID, f.remote.ID)
	modelWaitResolution(t, f, item.ID, "judge_submitted")
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_work_states WHERE target_id=$1 AND needs_work AND result->'judge'->>'item_id'=$2`, release, item.ID)
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_pending_items WHERE id=$1 AND evidence->'judge'->>'selected_id'=$2`, item.ID, f.remote.ID)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM external_links WHERE object_id=$1`, release)
	clock.advance(t, 0)
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1 AND external_id=$2`, release, f.remote.ID)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM steward_reviews`)
}

func TestPostgreSQL整理审查未采用即结项(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	release, _, item := f.matchFixture(t)
	f.enable(t)
	f.next(t, false).undetermined(t, item.ID, "无法核实候选")
	modelWaitResolution(t, f, item.ID, "undetermined")
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM operations WHERE target_id=$1`, release)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM curation_model_exceptions WHERE pending_item_id=$1`, item.ID)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM steward_reviews`)
}

func TestPostgreSQLCurationModelReviewUnavailableBackoff(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	f.update(t, "PUT", stewardBase+"/settings", map[string]string{"review_model": ""})
	_, _, item := f.matchFixture(t)
	item = modelRouteFixture(t, f, item, "")
	f.enable(t)
	f.next(t, false).selectCandidate(t, item.ID, f.remote.ID)
	modelWaitResolution(t, f, item.ID, "judge_submitted")
	got := f.item(t, item.ID)
	if got.NextAttemptAt != nil || got.ReviewRetries != 0 {
		t.Fatalf("识别提交后产生了重审退避 %+v", got)
	}
	requireCurationRuleRequestCount(t, f, 1)
}

func TestPostgreSQLCurationModelReviewProviderCooldown(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	_, _, item := f.matchFixture(t)
	f.enable(t)
	request := f.next(t, false)
	conversation := f.item(t, item.ID).ConversationID
	request.fail <- schedulerHTTPFailure{status: 503, retryAfter: "90"}
	schedulerWait(t, "主模型冷却时释放领取", func() bool { return f.item(t, item.ID).Status == "waiting_model" })
	got := f.item(t, item.ID)
	if got.Attempts != 0 || got.ReviewRetries != 0 || got.NextAttemptAt != nil {
		t.Fatalf("主模型 503 后的待定项 %+v", got)
	}
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM steward_plans WHERE conversation_id=$1`, conversation)
	f.clock.now.Add(int64(90 * time.Second))
	resumed := f.next(t, false)
	if f.item(t, item.ID).ConversationID == conversation {
		t.Fatal("冷却到期后应重新领取")
	}
	f.pause(t)
	resumed.complete(t)
}
