package app

import (
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func (f *schedulerFixture) proposeException(t *testing.T, tool string, input any) curation.ModelException {
	t.Helper()
	q := f.next(t, false)
	if tool == "resolve_release_match" {
		var match struct {
			ItemID string `json:"item_id"`
		}
		raw, err := json.Marshal(input)
		requireCurationOK(t, err)
		requireCurationOK(t, json.Unmarshal(raw, &match))
		q.selectCandidate(t, match.ItemID, f.remote.ID)
	} else {
		q.tool(t, tool, input)
	}
	f.next(t, true).review(t, "needs_human_confirmation")
	schedulerWait(t, "审查不确定后保留建议", func() bool {
		_, total, err := f.writer.ListExceptions(t.Context(), "open", 1, 10)
		requireCurationOK(t, err)
		return total == 1
	})
	items, total, err := f.writer.ListExceptions(t.Context(), "open", 1, 10)
	requireCurationOK(t, err)
	if total != 1 || len(items) != 1 || items[0].Tool != tool {
		t.Fatalf("新建例外 %+v", items)
	}
	return items[0]
}

func (f *schedulerFixture) decide(t *testing.T, actor *integrationTestUser, id, action string) *httptest.ResponseRecorder {
	t.Helper()
	return f.application.request(t, "POST", curationBase+"/exceptions/"+id+"/"+action, nil, actor, nil)
}

func readExceptionDecision(t *testing.T, response *httptest.ResponseRecorder, want string, actor string) curation.ModelException {
	t.Helper()
	if response.Code != 200 {
		t.Fatalf("决定失败 HTTP %d %s", response.Code, response.Body)
	}
	var body struct {
		Exception curation.ModelException
		IDs       []string `json:"operation_ids"`
	}
	requireCurationOK(t, json.Unmarshal(response.Body.Bytes(), &body))
	e := body.Exception
	if e.Status != want || e.DecidedBy == nil || *e.DecidedBy != actor || e.DecidedAt == nil || (want == "accepted" && (len(body.IDs) == 0 || !slices.Equal(body.IDs, e.OperationIDs))) {
		t.Fatalf("例外决定 %s", response.Body)
	}
	return e
}

func (f *schedulerFixture) matchFixture(t *testing.T) (string, string, curation.PendingItem) {
	t.Helper()
	release, group := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{title: "合成正式专辑", artist: "合成正式艺人", tracks: []string{"present", "present"}})
	track := programString(t, f.application, `SELECT t.id FROM tracks t JOIN media m ON t.medium_id=m.id WHERE m.release_id=$1 AND t.position=1`, release)
	programTag(t, f.application, track, "mb_release", f.remote.ID)
	mustExec(t, f.application, `UPDATE tracks SET duration_seconds=CASE position WHEN 1 THEN 12 ELSE 16 END WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1)`, release)
	return release, group, f.pending(t, release)
}

func (f *schedulerFixture) matchProposal(itemID string) any {
	return map[string]any{"item_id": itemID, "external_id": f.remote.ID, "basis": map[string]string{"note": "两首录音与候选一致"}}
}

func TestPostgreSQLCurationExceptionAcceptDecline(t *testing.T) {
	for _, action := range []string{"accept", "stale", "decline"} {
		t.Run("field_"+action, func(t *testing.T) {
			f := newSchedulerFixture(t)
			f.configure(t, &f.admin)
			reviewer := f.application.createUser(t, "admin")
			f.enable(t)
			f.pause(t)
			conversation := modelConversation(t, f.curationFixture)
			item := modelClaim(t, f.curationFixture, conversation, f.pending(t, f.a))
			raw, err := json.Marshal(schedulerCorrection(f.a, 1, "edition", setChange("合成版本")))
			requireCurationOK(t, err)
			id, err := f.writer.CreateException(t.Context(), curation.ModelException{
				PendingItemID: item.ID, PlanItemID: uuid.New(), ConversationID: &conversation, Tool: "correct_release", Provider: "test-main",
				Input: raw, Preview: []byte(`{"changes":[{"field":"edition","before":null,"after":"合成版本"}]}`),
				Review: curation.ExceptionReview{ID: uuid.New(), Model: "test-review", Decision: "needs_human_confirmation", Source: "model", Reasons: []string{"合成审查理由"}},
			})
			requireCurationOK(t, err)
			ex, err := f.writer.Exception(t.Context(), id)
			requireCurationOK(t, err)
			switch action {
			case "accept":
				e := readExceptionDecision(t, f.decide(t, &reviewer, ex.ID, "accept"), "accepted", reviewer.id)
				b3RequireCount(t, f.application, 1, `SELECT count(*) FROM release_corrections WHERE release_id=$1 AND field='edition' AND text_value='合成版本'`, f.a)
				b3RequireCount(t, f.application, 1, `SELECT count(*) FROM operations WHERE id=$1 AND actor_id=$2 AND batch_id IS NULL AND origin->>'review_decision'='human' AND origin->>'review_id'=$3`, e.OperationIDs[0], reviewer.id, ex.Review.ID)
				requireAPIError(t, f.decide(t, &reviewer, ex.ID, "decline"), 409, "exception_decided")
			case "stale":
				f.correct(t, f.a, 1, map[string]any{"edition": setChange("新版前值")})
				requireAPIError(t, f.decide(t, &reviewer, ex.ID, "accept"), 409, "exception_stale")
				b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_model_exceptions WHERE id=$1 AND status='open' AND decided_by IS NULL`, ex.ID)
				b3RequireCount(t, f.application, 1, `SELECT count(*) FROM release_effective WHERE id=$1 AND edition='新版前值'`, f.a)
			case "decline":
				readExceptionDecision(t, f.decide(t, &reviewer, ex.ID, "decline"), "declined", reviewer.id)
				requireAPIError(t, f.decide(t, &reviewer, ex.ID, "accept"), 409, "exception_decided")
				b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_suppressions WHERE target_id=$1 AND subject='edition' AND created_by=$2`, f.a, reviewer.id)
				modelWaitResolution(t, f, item.ID, "suggestion_recorded")
				programWrite := autoRequest(f.a, "musicbrainz", "", map[string]any{"edition": "合成版本"})
				programWrite.OnlyEmpty = true
				_, err = f.writer.Apply(t.Context(), programWrite)
				curationCRequireProblem(t, err, 409, "nothing_to_apply")
				b3RequireCount(t, f.application, 0, `SELECT count(*) FROM release_auto_values WHERE release_id=$1`, f.a)
			}
		})
	}

	for _, action := range []string{"resume", "decline"} {
		t.Run("match_"+action, func(t *testing.T) {
			clock := newProgramClock()
			f := newSchedulerFixture(t, WithCurationProgram(true, clock.options()))
			clock.wait(t)
			if action == "decline" {
				mustExec(t, f.application, `UPDATE tracks SET source_status='missing'`)
			}
			f.enable(t)
			release, _, item := f.matchFixture(t)
			if action == "resume" {
				curationCApplyLinks(t, f.curationFixture, curationCLinkRequest(f.b, "", curationCLink("release_group", f.groupB, f.remote.Group)))
			}
			f.configure(t, &f.admin)
			reviewer := f.application.createUser(t, "admin")
			ex := f.proposeException(t, "resolve_release_match", f.matchProposal(item.ID))
			if action == "resume" {
				// 在建立对应提交之后、补全写入之前中断，保留真实的恢复快照。
				installRejectingInsertTrigger(t, f.application, "operations", "NEW.operation_type='release_auto_correction'")
				response := f.decide(t, &reviewer, ex.ID, "accept")
				if response.Code < 500 {
					t.Fatalf("未中断采纳 HTTP %d %s", response.Code, response.Body)
				}
				b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_model_exceptions WHERE id=$1 AND status='accepting' AND decided_by=$2 AND preview ? 'confirmation_snapshot'`, ex.ID, reviewer.id)
				first := programString(t, f.application, `SELECT operation_id FROM external_links WHERE object_type='release' AND object_id=$1`, release)
				requireAPIError(t, f.decide(t, &f.admin, ex.ID, "accept"), 409, "exception_decided")
				mustExec(t, f.application, `DROP TRIGGER reject_operations_insert ON operations`)
				result := readExceptionDecision(t, f.decide(t, &reviewer, ex.ID, "accept"), "accepted", reviewer.id)
				if len(result.OperationIDs) != 3 || result.OperationIDs[0] != first {
					t.Fatalf("续做得到操作 %v，原对应操作 %s", result.OperationIDs, first)
				}
				for _, id := range result.OperationIDs {
					b3RequireCount(t, f.application, 1, `SELECT count(*) FROM operations WHERE id=$1 AND actor_id=$2 AND batch_id IS NULL AND origin->>'provider_kind'='model' AND origin->>'review_id'=$3 AND origin->>'review_decision'='human'`, id, reviewer.id, ex.Review.ID)
				}
				got := f.item(t, item.ID)
				if got.Status != "resolved" || got.Resolution == nil || *got.Resolution != "suggestion_recorded" {
					t.Fatalf("采纳匹配后 %+v", got)
				}
				requireAPIError(t, f.decide(t, &reviewer, ex.ID, "accept"), 409, "exception_decided")
				return
			}
			readExceptionDecision(t, f.decide(t, &reviewer, ex.ID, "decline"), "declined", reviewer.id)
			got := f.item(t, item.ID)
			if got.Status != "resolved" || got.Resolution == nil || *got.Resolution != "suggestion_recorded" {
				t.Fatalf("拒绝匹配后 %+v", got)
			}
			b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_suppressions WHERE target_id=$1 AND subject='external_link' AND created_by=$2`, release, reviewer.id)
			// 让程序看到相同的 MusicBrainz 标签，并让模型再尝试同一对应。
			f.update(t, "PUT", stewardBase+"/settings", map[string]string{"endpoint": ""})
			clock.advance(t, time.Minute)
			b3RequireCount(t, f.application, 0, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1`, release)
			nextItem := f.pending(t, release)
			f.configure(t, &f.admin)
			f.next(t, false).selectCandidate(t, nextItem.ID, f.remote.ID)
			schedulerWait(t, "抑制后的候选已释放", func() bool { return f.item(t, nextItem.ID).Status != "in_progress" })
			b3RequireCount(t, f.application, 0, `SELECT count(*) FROM operations WHERE operation_type='external_link' AND target_id=$1`, release)
		})
	}
}

func TestPostgreSQLCurationModelAPI(t *testing.T) {
	f := newSchedulerFixture(t)
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		return f.application.request(t, method, curationBase+path, body, &f.admin, nil)
	}
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/settings", nil}, {"PUT", "/settings", map[string]string{"model": "x"}}, {"GET", "/progress", nil}, {"GET", "/exceptions", nil}, {"GET", "/conversations", nil}, {"POST", "/exceptions/" + uuid.New() + "/accept", nil}, {"POST", "/exceptions/" + uuid.New() + "/decline", nil},
	} {
		requireAPIError(t, f.application.request(t, tc.method, curationBase+tc.path, tc.body, &f.ordinary, nil), 403, "forbidden")
	}
	for _, patch := range []any{map[string]any{"model": nil}, map[string]any{"model": strings.Repeat("音", 67)}, map[string]any{"model": false}, map[string]any{"daily_token_limit": 0}, map[string]any{"daily_token_limit": 1000000000001}, map[string]any{"daily_token_limit": 1.5}, map[string]any{"daily_token_limit": "10"}} {
		requireAPIError(t, call("PUT", "/settings", patch), 400, "invalid_input")
	}
	checkSettings := func(patch any, model string, limit any) {
		t.Helper()
		response := call("PUT", "/settings", patch)
		if response.Code != 200 {
			t.Fatalf("设置 HTTP %d %s", response.Code, response.Body)
		}
		for _, r := range []*httptest.ResponseRecorder{response, call("GET", "/settings", nil)} {
			body := decodeJSONMap(t, r)
			got, present := body["daily_token_limit"]
			if !present || got != limit || body["model"] != model {
				t.Fatalf("模型设置 %s", r.Body)
			}
		}
	}
	checkSettings(map[string]any{"model": strings.Repeat("x", 200), "daily_token_limit": 1000000000000}, strings.Repeat("x", 200), float64(1000000000000))
	checkSettings(map[string]string{"model": ""}, "", float64(1000000000000))
	checkSettings(map[string]any{"daily_token_limit": nil}, "", nil)
	f.pending(t, f.a)
	p := f.progress(t)
	if p.State != "disabled" || p.Items.WaitingModel != 1 || p.Items.InProgress != 0 || p.Items.WaitingHuman != 0 || p.Items.Shelved != 0 || p.DailyTokenLimit != nil || p.Today.Date != "2026-01-02" {
		t.Fatalf("进度的模型段 %+v", p)
	}

	conversation := modelConversation(t, f.curationFixture)
	items, err := f.writer.ClaimPendingItems(t.Context(), conversation, f.clock.options().Now(), 1)
	requireCurationOK(t, err)
	if len(items) != 1 {
		t.Fatalf("缺少合成待定项")
	}
	add := func() string {
		in, err := json.Marshal(schedulerCorrection(f.a, 1, "edition", setChange("合成版本")))
		requireCurationOK(t, err)
		id, err := f.writer.CreateException(t.Context(), curation.ModelException{PendingItemID: items[0].ID, PlanItemID: uuid.New(), ConversationID: &conversation,
			Tool: "correct_release", Input: in, Preview: []byte(`{"changes":[]}`), Provider: "test-main", Review: curation.ExceptionReview{ID: uuid.New(), Decision: "rejected", Model: "test-review", Source: "model", Reasons: []string{"合成理由"}}})
		requireCurationOK(t, err)
		return id
	}
	first, second, third := add(), add(), add()
	readExceptionDecision(t, f.decide(t, &f.admin, second, "decline"), "declined", f.admin.id)
	for _, c := range []struct {
		query, id   string
		page, total int
	}{
		{"?page_size=1", third, 1, 2}, {"?status=open&page=2&page_size=1", first, 2, 2}, {"?status=all&page=2&page_size=1", second, 2, 3},
	} {
		var response struct {
			Items       []curation.ModelException
			Total, Page int
			Size        int `json:"page_size"`
		}
		getJSON(t, f.application, &f.admin, curationBase+"/exceptions"+c.query, &response)
		if response.Page != c.page || response.Size != 1 || response.Total != c.total || len(response.Items) != 1 || response.Items[0].ID != c.id {
			t.Fatalf("例外页 %s %+v", c.query, response)
		}
	}
	for _, query := range []string{"?status=resolved", "?status=", "?page=0", "?page_size=101"} {
		requireAPIError(t, call("GET", "/exceptions"+query, nil), 400, "invalid_input")
	}
	for _, action := range []string{"accept", "decline"} {
		requireAPIError(t, f.decide(t, &f.admin, "bad", action), 400, "invalid_id")
		requireAPIError(t, f.decide(t, &f.admin, uuid.New(), action), 404, "not_found")
		requireAPIError(t, f.decide(t, &f.admin, second, action), 409, "exception_decided")
	}
}

func TestPostgreSQLCurationModelAPIMatchErrors(t *testing.T) {
	for _, code := range []string{"curation_disabled", "source_disabled", "source_unavailable", "nothing_to_apply"} {
		t.Run(code, func(t *testing.T) {
			f := newSchedulerFixture(t)
			f.enable(t)
			release, group, item := f.matchFixture(t)
			external := f.remote.ID
			switch code {
			case "curation_disabled":
				f.update(t, "PUT", curationBase+"/settings", map[string]bool{"enabled": false})
			case "source_disabled":
				f.update(t, "PUT", curationBase+"/sources/musicbrainz", map[string]bool{"enabled": false})
			case "source_unavailable":
				external = unavailableMusicBrainzID
			case "nothing_to_apply":
				links := []curation.LinkIntent{curationCLink("release", release, external), curationCLink("release_group", group, f.remote.Group)}
				for i, recording := range f.remote.Recordings {
					track := programString(t, f.application, `SELECT t.id FROM tracks t JOIN media m ON t.medium_id=m.id WHERE m.release_id=$1 AND t.position=$2`, release, i+1)
					links = append(links, curationCLink("track", track, recording))
				}
				curationCApplyLinks(t, f.curationFixture, curationCLinkRequest(release, "", links...))
				item = f.pending(t, release)
			}
			conversation := modelConversation(t, f.curationFixture)
			modelClaim(t, f.curationFixture, conversation, item)
			in, err := json.Marshal(map[string]any{"item_id": item.ID, "external_id": external, "basis": map[string]string{"note": "合成依据"}})
			requireCurationOK(t, err)
			id, err := f.writer.CreateException(t.Context(), curation.ModelException{PendingItemID: item.ID, PlanItemID: uuid.New(), ConversationID: &conversation,
				Tool: "resolve_release_match", Input: in, Preview: []byte(`{"changes":[]}`), Provider: "test-main", Review: curation.ExceptionReview{ID: uuid.New(), Decision: "rejected", Model: "test-review", Source: "model", Reasons: []string{"需确认"}}})
			requireCurationOK(t, err)
			status := 409
			if code == "source_unavailable" {
				status = 503
			}
			requireAPIError(t, f.decide(t, &f.admin, id, "accept"), status, code)
			b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_model_exceptions WHERE id=$1 AND status='open' AND decided_by IS NULL`, id)
		})
	}
}
