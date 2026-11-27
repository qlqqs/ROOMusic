package app

import (
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

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
	for _, name := range []string{"correct_release", "resolve_release_match"} {
		t.Run(name, func(t *testing.T) {
			f := newSchedulerFixture(t)
			release, _, pending := f.matchFixture(t)
			conversation := modelConversation(t, f.curationFixture)
			item := modelClaim(t, f.curationFixture, conversation, pending)
			var input any = schedulerCorrection(release, 1, "edition", setChange("合成版本"))
			if name == "resolve_release_match" {
				input = f.matchProposal(item.ID)
			}
			assertHistoricalExceptionLocked(t, f, item, name, input)
			b3RequireCount(t, f.application, 0, `SELECT count(*) FROM release_corrections WHERE release_id=$1`, release)
			b3RequireCount(t, f.application, 0, `SELECT count(*) FROM external_links WHERE object_id=$1`, release)
		})
	}
}

func assertHistoricalExceptionLocked(t *testing.T, f *schedulerFixture, item curation.PendingItem, name string, input any) {
	t.Helper()
	raw, err := json.Marshal(input)
	requireCurationOK(t, err)
	id, err := f.writer.CreateException(t.Context(), curation.ModelException{
		PendingItemID: item.ID, PlanItemID: uuid.New(), ConversationID: &item.ConversationID, Tool: name, Provider: "test-main",
		Input: raw, Preview: []byte(`{"changes":[]}`), Review: curation.ExceptionReview{ID: uuid.New(), Model: "test-review", Source: "model", Decision: "needs_human_confirmation", Reasons: []string{"旧版审查"}},
	})
	requireCurationOK(t, err)
	for _, action := range []string{"accept", "decline"} {
		requireAPIError(t, f.decide(t, &f.admin, id, action), 404, "not_found")
	}
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_model_exceptions WHERE id=$1 AND status='open' AND decided_by IS NULL`, id)
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
		{"GET", "/settings", nil}, {"PUT", "/settings", map[string]string{"model": "x"}}, {"GET", "/progress", nil}, {"GET", "/conversations", nil}, {"POST", "/exceptions/" + uuid.New() + "/accept", nil}, {"POST", "/exceptions/" + uuid.New() + "/decline", nil},
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
	id := add()
	for _, query := range []string{"", "?page_size=1", "?status=open"} {
		requireAPIError(t, call("GET", "/exceptions"+query, nil), 404, "not_found")
	}
	for _, action := range []string{"accept", "decline"} {
		requireAPIError(t, f.decide(t, &f.admin, "bad", action), 400, "invalid_id")
		requireAPIError(t, f.decide(t, &f.admin, uuid.New(), action), 404, "not_found")
		requireAPIError(t, f.decide(t, &f.admin, id, action), 404, "not_found")
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
			requireAPIError(t, f.decide(t, &f.admin, id, "accept"), 404, "not_found")
			b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_model_exceptions WHERE id=$1 AND status='open' AND decided_by IS NULL`, id)
		})
	}
}

func TestPostgreSQLCurationModelRulesSettingsAndProgress(t *testing.T) {
	f := newSchedulerFixture(t)
	call := func(method, path string, body any) map[string]any {
		t.Helper()
		response := f.application.request(t, method, curationBase+path, body, &f.admin, nil)
		if response.Code != 200 {
			t.Fatalf("%s %s HTTP %d：%s", method, path, response.Code, response.Body)
		}
		return decodeJSONMap(t, response)
	}
	check := func(body map[string]any, want map[string]any) {
		t.Helper()
		for key, value := range want {
			if actual, exists := body[key]; !exists || actual != value {
				t.Errorf("%s=%v（存在=%t），期望 %v", key, actual, exists, value)
			}
		}
	}
	retry := curationBase + "/model/retry"
	requireAPIError(t, f.application.request(t, "POST", retry, nil, &f.ordinary, nil), 403, "forbidden")
	requireAPIError(t, f.application.request(t, "POST", retry, nil, &f.admin, map[string]string{"Origin": "https://synthetic.invalid"}), 403, "origin_forbidden")

	defaults := call("GET", "/settings", nil)
	check(defaults, map[string]any{"model_concurrency": float64(4), "daily_token_limit": float64(10000000), "max_request_tokens": float64(128000)})
	if _, legacy := defaults["web_daily_token_limit"]; legacy {
		t.Error("设置仍包含旧联网子额度")
	}
	for _, patch := range []map[string]any{
		{"web_daily_token_limit": 1},
		{"max_request_tokens": nil}, {"max_request_tokens": "128000"}, {"max_request_tokens": 1.5},
		{"max_request_tokens": 0}, {"max_request_tokens": 1000000000001},
		{"model_concurrency": nil}, {"model_concurrency": "16"}, {"model_concurrency": 0}, {"model_concurrency": 17},
		{"daily_token_limit": false}, {"daily_token_limit": 1.5}, {"daily_token_limit": 0},
	} {
		body := f.application.request(t, "PUT", curationBase+"/settings", patch, &f.admin, nil)
		if body.Code != 400 {
			t.Errorf("非法设置 %v 应返回 400，实际 HTTP %d：%s", patch, body.Code, body.Body)
		}
	}
	check(call("PUT", "/settings", map[string]any{"model_concurrency": 1, "max_request_tokens": 1, "daily_token_limit": nil}),
		map[string]any{"model_concurrency": float64(1), "max_request_tokens": float64(1), "daily_token_limit": nil})
	check(call("PUT", "/settings", map[string]any{"model_concurrency": 16, "max_request_tokens": 1000000000000}),
		map[string]any{"model_concurrency": float64(16), "max_request_tokens": float64(1000000000000), "daily_token_limit": nil})

	f.enable(t)
	response := call("GET", "/progress", nil)
	progress, ok := response["model"].(map[string]any)
	if !ok {
		t.Fatalf("未配置时仍应返回完整的 model 对象：%v", response)
	}
	check(progress, map[string]any{
		"configured": false, "state": "unavailable", "unavailable_reason": "not_configured",
		"pause_kind": nil, "reason_detail": nil, "retry_at": nil, "can_retry": false,
	})
	hint, ok := progress["context_hint"].(string)
	if !ok || !strings.Contains(hint, "实际加载的上下文长度") {
		t.Errorf("缺少本机上下文提示：%v", progress["context_hint"])
	}
	for _, key := range []string{"web_today", "web_daily_token_limit", "web_limit_reached", "web_retry_at"} {
		if _, legacy := progress[key]; legacy {
			t.Errorf("进度仍含旧联网额度字段 %s", key)
		}
	}
	check(call("POST", "/model/retry", nil), map[string]any{"unavailable_reason": "not_configured", "can_retry": false})
	requireCurationRuleRequestCount(t, f, 0)
}
