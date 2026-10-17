package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// 认领的接口（阶段 M4 第三项第 2 步）：权限与参数的检查顺序、同键重放与异义、并发请求、拒绝时不留副作用。
// 旧版本是 A（先用接口做出修正，再让它的曲目都消失），新版本是 B。

func claimPath(releaseID string) string { return "/api/v1/releases/" + releaseID + "/claim" }

func claimPreviewPath(releaseID, sourceID string) string {
	return "/api/v1/releases/" + releaseID + "/claim-preview?source=" + sourceID
}

func claimBody(revision, sourceRevision int64, sourceID, preview string) map[string]any {
	return map[string]any{"expected_revision": revision, "source_release_id": sourceID, "source_revision": sourceRevision, "preview": preview}
}

// claimEnvironment 是 groupEnvironment 加上：A 有一个标题修正并已不可见（旧版本），B 是新版本。
type claimEnvironment struct {
	*groupEnvironment
	// visible 是另一个可见版本：拿它当旧版本，应当得到 not_claimable。
	visible string
}

func newClaimEnvironment(t *testing.T) *claimEnvironment {
	t.Helper()
	environment := &claimEnvironment{groupEnvironment: newGroupEnvironment(t)}
	environment.visible, _ = insertGroupedRelease(t, environment.application, environment.rootID, groupedRelease{title: "Album C", artist: "Artist C", year: 2010, tracks: []string{"present"}})
	response := environment.application.request(t, http.MethodPatch, "/api/v1/releases/"+environment.a+"/corrections",
		correctionBody(1, map[string]any{"title": setChange("我改过的 A")}), &environment.admin, idempotencyHeader(uuid.New()))
	if response.Code != http.StatusOK {
		t.Fatalf("correct A: HTTP %d: %s", response.Code, response.Body.String())
	}
	mustExec(t, environment.application, `UPDATE tracks SET source_status='missing' WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1::uuid)`, environment.a)
	return environment
}

func (environment *claimEnvironment) preview(t *testing.T, user *integrationTestUser, releaseID, sourceID string) *httptest.ResponseRecorder {
	t.Helper()
	return environment.application.request(t, http.MethodGet, claimPreviewPath(releaseID, sourceID), nil, user, nil)
}

func (environment *claimEnvironment) claim(t *testing.T, user *integrationTestUser, releaseID, key string, body any, extraHeaders ...map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	headers := map[string]string{}
	if key != "" {
		headers["Idempotency-Key"] = key
	}
	for _, extra := range extraHeaders {
		for name, value := range extra {
			headers[name] = value
		}
	}
	return environment.application.request(t, http.MethodPost, claimPath(releaseID), body, user, headers)
}

// previewToken 读取预览，返回新版本、旧版本的版本号与令牌。
func (environment *claimEnvironment) previewToken(t *testing.T) (revision, sourceRevision int64, token string) {
	t.Helper()
	response := environment.preview(t, &environment.admin, environment.b, environment.a)
	if response.Code != http.StatusOK {
		t.Fatalf("preview: HTTP %d: %s", response.Code, response.Body.String())
	}
	body := decodeJSONMap(t, response)
	return int64(body["revision"].(float64)), int64(body["source_revision"].(float64)), body["token"].(string)
}

type claimState struct {
	targetRevision, sourceRevision int64
	targetGroup, sourceGroup       string
	corrections, operations        int
}

func (environment *claimEnvironment) state(t *testing.T) claimState {
	t.Helper()
	var state claimState
	database := environment.application.database
	if err := database.QueryRow(`SELECT group_id::text, edit_revision FROM releases WHERE id=$1::uuid`, environment.b).Scan(&state.targetGroup, &state.targetRevision); err != nil {
		t.Fatalf("read the new release: %v", err)
	}
	if err := database.QueryRow(`SELECT group_id::text, edit_revision FROM releases WHERE id=$1::uuid`, environment.a).Scan(&state.sourceGroup, &state.sourceRevision); err != nil {
		t.Fatalf("read the old release: %v", err)
	}
	state.corrections = countRows(t, environment.application, `SELECT COUNT(*) FROM release_corrections`)
	state.operations = countRows(t, environment.application, `SELECT COUNT(*) FROM operations`)
	return state
}

func TestPostgreSQLClaimAPIRejectsUnauthorizedAndInvalidRequestsWithoutSideEffects(t *testing.T) {
	environment := newClaimEnvironment(t)
	revision, sourceRevision, token := environment.previewToken(t)
	valid := claimBody(revision, sourceRevision, environment.a, token)
	before := environment.state(t)

	for _, testCase := range []struct {
		name    string
		user    *integrationTestUser
		release string
		key     string
		body    any
		headers map[string]string
		status  int
		code    string
	}{
		{name: "no session", release: environment.b, key: uuid.New(), body: valid, status: 401, code: "unauthorized"},
		{name: "ordinary member", user: &environment.ordinary, release: environment.b, key: uuid.New(), body: valid, status: 403, code: "forbidden"},
		{name: "cross-origin administrator", user: &environment.admin, release: environment.b, key: uuid.New(), body: valid, headers: map[string]string{"Origin": "http://evil.example"}, status: 403, code: "origin_forbidden"},
		{name: "cross-origin without a session", release: environment.b, key: uuid.New(), body: valid, headers: map[string]string{"Origin": "http://evil.example"}, status: 403, code: "origin_forbidden"},
		{name: "missing origin", user: &environment.admin, release: environment.b, key: uuid.New(), body: valid, headers: map[string]string{"Origin": ""}, status: 403, code: "origin_required"},
		{name: "malformed release id", user: &environment.admin, release: "not-a-uuid", key: uuid.New(), body: valid, status: 400, code: "invalid_id"},
		{name: "missing idempotency key", user: &environment.admin, release: environment.b, body: valid, status: 400, code: "invalid_input"},
		{name: "idempotency key that is not a UUID", user: &environment.admin, release: environment.b, key: "not-a-uuid", body: valid, status: 400, code: "invalid_input"},
		{name: "body that is not an object", user: &environment.admin, release: environment.b, key: uuid.New(), body: "claim", status: 400, code: "invalid_json"},
		{name: "expected revision zero", user: &environment.admin, release: environment.b, key: uuid.New(), body: claimBody(0, sourceRevision, environment.a, token), status: 400, code: "invalid_input"},
		{name: "source revision zero", user: &environment.admin, release: environment.b, key: uuid.New(), body: claimBody(revision, 0, environment.a, token), status: 400, code: "invalid_input"},
		{name: "source id that is not a UUID", user: &environment.admin, release: environment.b, key: uuid.New(), body: claimBody(revision, sourceRevision, "x", token), status: 400, code: "invalid_input"},
		{name: "the same release as source and target", user: &environment.admin, release: environment.b, key: uuid.New(), body: claimBody(revision, sourceRevision, environment.b, token), status: 400, code: "invalid_input"},
		{name: "missing preview", user: &environment.admin, release: environment.b, key: uuid.New(), body: claimBody(revision, sourceRevision, environment.a, ""), status: 400, code: "invalid_input"},
		{name: "unknown target", user: &environment.admin, release: uuid.New(), key: uuid.New(), body: valid, status: 404, code: "not_found"},
		{name: "target without visible tracks", user: &environment.admin, release: environment.hidden, key: uuid.New(), body: claimBody(1, sourceRevision, environment.a, token), status: 404, code: "not_found"},
		{name: "unknown source", user: &environment.admin, release: environment.b, key: uuid.New(), body: claimBody(revision, sourceRevision, uuid.New(), token), status: 404, code: "not_found"},
		{name: "a visible release as the source", user: &environment.admin, release: environment.b, key: uuid.New(), body: claimBody(revision, 1, environment.visible, token), status: 409, code: "not_claimable"},
		{name: "a hidden release without manual state as the source", user: &environment.admin, release: environment.b, key: uuid.New(), body: claimBody(revision, 1, environment.hidden, token), status: 409, code: "not_claimable"},
		{name: "stale revision of the new release", user: &environment.admin, release: environment.b, key: uuid.New(), body: claimBody(revision+5, sourceRevision, environment.a, token), status: 409, code: "revision_conflict"},
		{name: "stale revision of the old release", user: &environment.admin, release: environment.b, key: uuid.New(), body: claimBody(revision, sourceRevision+5, environment.a, token), status: 409, code: "revision_conflict"},
		{name: "a preview the page did not see", user: &environment.admin, release: environment.b, key: uuid.New(), body: claimBody(revision, sourceRevision, environment.a, "0000"), status: 409, code: "claim_changed"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			requireAPIError(t, environment.claim(t, testCase.user, testCase.release, testCase.key, testCase.body, testCase.headers), testCase.status, testCase.code)
		})
	}
	if after := environment.state(t); after != before {
		t.Fatalf("a rejected claim changed the state: %+v -> %+v", before, after)
	}
}

func TestPostgreSQLClaimPreviewAPI(t *testing.T) {
	environment := newClaimEnvironment(t)
	for _, testCase := range []struct {
		name   string
		user   *integrationTestUser
		path   string
		status int
		code   string
	}{
		{"without a session", nil, claimPreviewPath(environment.b, environment.a), 401, "unauthorized"},
		{"as an ordinary member", &environment.ordinary, claimPreviewPath(environment.b, environment.a), 403, "forbidden"},
		{"malformed release id", &environment.admin, claimPreviewPath("not-a-uuid", environment.a), 400, "invalid_id"},
		{"missing source", &environment.admin, "/api/v1/releases/" + environment.b + "/claim-preview", 400, "invalid_id"},
		{"source that is not a UUID", &environment.admin, claimPreviewPath(environment.b, "x"), 400, "invalid_id"},
		{"two sources", &environment.admin, claimPreviewPath(environment.b, environment.a) + "&source=" + environment.a, 400, "invalid_id"},
		{"the same release as source and target", &environment.admin, claimPreviewPath(environment.b, environment.b), 400, "invalid_input"},
		{"unknown target", &environment.admin, claimPreviewPath(uuid.New(), environment.a), 404, "not_found"},
		{"target without visible tracks", &environment.admin, claimPreviewPath(environment.hidden, environment.a), 404, "not_found"},
		{"unknown source", &environment.admin, claimPreviewPath(environment.b, uuid.New()), 404, "not_found"},
		{"a visible release as the source", &environment.admin, claimPreviewPath(environment.b, environment.visible), 409, "not_claimable"},
		{"a hidden release without manual state as the source", &environment.admin, claimPreviewPath(environment.b, environment.hidden), 409, "not_claimable"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			requireAPIError(t, environment.application.request(t, http.MethodGet, testCase.path, nil, testCase.user, nil), testCase.status, testCase.code)
		})
	}

	response := environment.preview(t, &environment.admin, environment.b, environment.a)
	if response.Code != http.StatusOK {
		t.Fatalf("preview: HTTP %d: %s", response.Code, response.Body.String())
	}
	body := decodeJSONMap(t, response)
	release, source := body["release"].(map[string]any), body["source"].(map[string]any)
	if release["id"] != environment.b || release["visible"] != true || source["id"] != environment.a || source["visible"] != false {
		t.Fatalf("preview releases = %v / %v", release, source)
	}
	items := body["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["kind"] != "correction" || items[0].(map[string]any)["field"] != "title" || items[0].(map[string]any)["action"] != "move" {
		t.Fatalf("preview items = %v", items)
	}
	if token, _ := body["token"].(string); len(token) != 64 {
		t.Fatalf("preview token = %q", body["token"])
	}
}

func TestPostgreSQLClaimAPIClaimsAndReplays(t *testing.T) {
	environment := newClaimEnvironment(t)
	revision, sourceRevision, token := environment.previewToken(t)
	key := uuid.New()
	body := claimBody(revision, sourceRevision, environment.a, token)

	first := environment.claim(t, &environment.admin, environment.b, key, body)
	if first.Code != http.StatusOK {
		t.Fatalf("claim: HTTP %d: %s", first.Code, first.Body.String())
	}
	result := decodeJSONMap(t, first)
	if result["release_id"] != environment.b || result["source_release_id"] != environment.a || result["group_id"] != environment.groupB || result["previous_group_id"] != environment.groupB ||
		int64(result["revision"].(float64)) != revision+1 || int64(result["source_revision"].(float64)) != sourceRevision+1 || result["operation_id"] == nil {
		t.Fatalf("claim result = %v", result)
	}
	moved := result["moved"].(map[string]any)
	if corrections := moved["corrections"].([]any); len(corrections) != 1 || corrections[0] != "title" {
		t.Fatalf("moved = %v", moved)
	}
	// B 现在显示迁过来的标题。
	detail := decodeJSONMap(t, environment.application.request(t, http.MethodGet, "/api/v1/releases/"+environment.b, nil, &environment.ordinary, nil))
	if detail["title"] != "我改过的 A" {
		t.Fatalf("the new release shows %v", detail["title"])
	}
	after := environment.state(t)

	// 同键同义重放：同一份响应，不再执行。
	replay := environment.claim(t, &environment.admin, environment.b, key, body)
	if replay.Code != http.StatusOK || strings.TrimSpace(replay.Body.String()) == "" {
		t.Fatalf("replay: HTTP %d: %s", replay.Code, replay.Body.String())
	}
	replayed := decodeJSONMap(t, replay)
	if replayed["operation_id"] != result["operation_id"] || environment.state(t) != after {
		t.Fatalf("the replay executed again or returned another operation: %v", replayed)
	}
	// 同键异义：409，不执行。
	requireAPIError(t, environment.claim(t, &environment.admin, environment.b, key, claimBody(revision, sourceRevision, environment.a, "ffff")), 409, "idempotency_conflict")
	if environment.state(t) != after {
		t.Fatal("a conflicting replay changed the state")
	}
	// 认领之后旧版本不再待认领：再用新键认领被拒绝。
	requireAPIError(t, environment.claim(t, &environment.admin, environment.b, uuid.New(), body), 409, "not_claimable")
}

// 同时提交同一个认领：恰好一个成功，其余是 409，操作记录只有一条。
func TestPostgreSQLClaimAPIConcurrentRequests(t *testing.T) {
	environment := newClaimEnvironment(t)
	revision, sourceRevision, token := environment.previewToken(t)
	body := claimBody(revision, sourceRevision, environment.a, token)
	before := environment.state(t)

	var waitGroup sync.WaitGroup
	responses := make([]*httptest.ResponseRecorder, 8)
	for index := range responses {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			responses[index] = environment.claim(t, &environment.admin, environment.b, uuid.New(), body)
		}(index)
	}
	waitGroup.Wait()
	succeeded := 0
	for _, response := range responses {
		switch response.Code {
		case http.StatusOK:
			succeeded++
		case http.StatusConflict:
		default:
			t.Fatalf("concurrent claim: HTTP %d: %s", response.Code, response.Body.String())
		}
	}
	after := environment.state(t)
	if succeeded != 1 || after.operations != before.operations+1 || after.targetRevision != before.targetRevision+1 {
		t.Fatalf("%d concurrent claims succeeded (%+v -> %+v); want exactly one", succeeded, before, after)
	}
}
