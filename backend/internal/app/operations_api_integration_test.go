package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func revertPath(operationID string) string { return "/api/v1/operations/" + operationID + "/revert" }

// operationsEnvironment 在两张可见专辑（A、B）上准备好操作记录的场景：管理员、普通成员与它们的会话。
type operationsEnvironment struct {
	*groupEnvironment
}

func newOperationsEnvironment(t *testing.T) *operationsEnvironment {
	t.Helper()
	return &operationsEnvironment{newGroupEnvironment(t)}
}

// correct 修正 A 的字段并返回这次操作的 ID 与新的版本号。
func (environment *operationsEnvironment) correct(t *testing.T, releaseID string, revision int, changes map[string]any) (operationID string, newRevision int) {
	t.Helper()
	response := environment.application.request(t, http.MethodPatch, "/api/v1/releases/"+releaseID+"/corrections", correctionBody(revision, changes), &environment.admin, idempotencyHeader(uuid.New()))
	if response.Code != http.StatusOK {
		t.Fatalf("correct release: HTTP %d: %s", response.Code, response.Body.String())
	}
	result := decodeJSONMap(t, response)
	return result["operation_id"].(string), int(result["revision"].(float64))
}

func (environment *operationsEnvironment) join(t *testing.T, releaseID string, revision int, groupID string) (operationID string, newRevision int) {
	t.Helper()
	response := environment.post(t, &environment.admin, releaseID, uuid.New(), joinBody(revision, groupID))
	if response.Code != http.StatusOK {
		t.Fatalf("join: HTTP %d: %s", response.Code, response.Body.String())
	}
	result := decodeJSONMap(t, response)
	return result["operation_id"].(string), int(result["revision"].(float64))
}

func (environment *operationsEnvironment) revert(t *testing.T, user *integrationTestUser, operationID, key string, extraHeaders ...map[string]string) *httptest.ResponseRecorder {
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
	return environment.application.request(t, http.MethodPost, revertPath(operationID), nil, user, headers)
}

type operationsListResponse struct {
	Items []struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		CreatedAt string `json:"created_at"`
		Actor     struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"actor"`
		Release *struct {
			ID      string `json:"id"`
			Title   string `json:"title"`
			GroupID string `json:"group_id"`
			Visible bool   `json:"visible"`
		} `json:"release"`
		Changes struct {
			Kind string `json:"kind"`
			From *struct {
				GroupID string `json:"group_id"`
				Title   string `json:"group_title"`
			} `json:"from"`
			To *struct {
				GroupID string `json:"group_id"`
				Title   string `json:"group_title"`
			} `json:"to"`
			Fields []struct {
				Field string `json:"field"`
			} `json:"fields"`
		} `json:"changes"`
		Reverts    *string `json:"reverts"`
		RevertedBy *string `json:"reverted_by"`
		Revertible bool    `json:"revertible"`
	} `json:"items"`
	Pagination struct {
		Page     int   `json:"page"`
		PageSize int   `json:"page_size"`
		Total    int64 `json:"total"`
	} `json:"pagination"`
}

func (environment *operationsEnvironment) list(t *testing.T, query string) (operationsListResponse, string) {
	t.Helper()
	response := environment.application.request(t, http.MethodGet, "/api/v1/operations"+query, nil, &environment.admin, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("list operations: HTTP %d: %s", response.Code, response.Body.String())
	}
	var decoded operationsListResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode the operations list: %v", err)
	}
	return decoded, response.Body.String()
}

func TestPostgreSQLOperationsAPIListsRecordsForAdministratorsOnly(t *testing.T) {
	environment := newOperationsEnvironment(t)
	application := environment.application

	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/operations", nil, nil, nil), 401, "unauthorized")
	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/operations", nil, &environment.ordinary, nil), 403, "forbidden")
	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/operations?page=0", nil, &environment.admin, nil), 400, "invalid_pagination")
	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/operations?page_size=101", nil, &environment.admin, nil), 400, "invalid_pagination")
	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/operations?release_id=not-a-uuid", nil, &environment.admin, nil), 400, "invalid_id")

	// 空列表。
	if empty, _ := environment.list(t, ""); empty.Pagination.Total != 0 || empty.Items == nil || len(empty.Items) != 0 {
		t.Fatalf("empty list = %+v", empty)
	}

	correction, revision := environment.correct(t, environment.a, 1, map[string]any{"title": setChange("Album A (corrected)"), "year": nullChange})
	joinOperation, _ := environment.join(t, environment.b, 1, environment.groupA)
	genreOperation, _ := environment.correct(t, environment.a, revision, map[string]any{"genre": setChange("古典")})

	listed, raw := environment.list(t, "")
	if listed.Pagination.Total != 3 || listed.Pagination.Page != 1 || listed.Pagination.PageSize != 50 || len(listed.Items) != 3 {
		t.Fatalf("list = %+v", listed.Pagination)
	}
	// 新的在前；谁、何时、哪张专辑（当前的状态）、改了什么、能不能撤销。
	ids := []string{listed.Items[0].ID, listed.Items[1].ID, listed.Items[2].ID}
	if !reflect.DeepEqual(ids, []string{genreOperation, joinOperation, correction}) {
		t.Fatalf("order = %v, want newest first %v", ids, []string{genreOperation, joinOperation, correction})
	}
	latestA, join, first := listed.Items[0], listed.Items[1], listed.Items[2]
	if latestA.Type != "release_correction" || latestA.Actor.ID != environment.admin.id || latestA.Actor.Username == "" || latestA.Release == nil ||
		latestA.Release.ID != environment.a || latestA.Release.Title != "Album A (corrected)" || latestA.Release.GroupID != environment.groupA || !latestA.Release.Visible ||
		latestA.Changes.Kind != "release_correction" || len(latestA.Changes.Fields) != 1 || latestA.Changes.Fields[0].Field != "genre" || !latestA.Revertible ||
		latestA.Reverts != nil || latestA.RevertedBy != nil || latestA.CreatedAt == "" {
		t.Fatalf("latest correction item = %+v", latestA)
	}
	if join.Type != "release_group_change" || join.Release == nil || join.Release.ID != environment.b || join.Release.GroupID != environment.groupA ||
		join.Changes.Kind != "release_group_change" || join.Changes.From == nil || join.Changes.From.GroupID != environment.groupB || join.Changes.From.Title != "Album B" ||
		join.Changes.To == nil || join.Changes.To.GroupID != environment.groupA || join.Changes.To.Title != "Album A (corrected)" || !join.Revertible {
		t.Fatalf("group change item = %+v", join)
	}
	// 同一张专辑之后又被修改过：第一条修正不能再撤销。
	if first.Revertible || len(first.Changes.Fields) != 2 || first.Changes.Fields[0].Field != "title" || first.Changes.Fields[1].Field != "year" {
		t.Fatalf("superseded correction item = %+v", first)
	}
	// 记录里没有请求体、前后状态原文、指纹、幂等键或服务器路径。
	for _, forbidden := range []string{"before_state", "after_state", "fingerprint", "idempotency", "request_id", application.allowedRoot, "password"} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("the operations list exposes %q: %s", forbidden, raw)
		}
	}

	// 按发行筛选与分页。
	onlyB, _ := environment.list(t, "?release_id="+strings.ToUpper(environment.b))
	if onlyB.Pagination.Total != 1 || len(onlyB.Items) != 1 || onlyB.Items[0].ID != joinOperation {
		t.Fatalf("filter by release = %+v", onlyB)
	}
	pageTwo, _ := environment.list(t, "?page=2&page_size=2")
	if pageTwo.Pagination.Total != 3 || pageTwo.Pagination.Page != 2 || len(pageTwo.Items) != 1 || pageTwo.Items[0].ID != correction {
		t.Fatalf("page 2 = %+v", pageTwo)
	}
	if none, _ := environment.list(t, "?release_id="+uuid.New()); none.Pagination.Total != 0 || none.Items == nil {
		t.Fatalf("filter by an unknown release = %+v", none)
	}

	// 撤销之后：原记录有 reverted_by，撤销记录有 reverts，两者都不能再撤销，目标显示的是撤销后的状态。
	reverted := environment.revert(t, &environment.admin, joinOperation, uuid.New())
	if reverted.Code != http.StatusOK {
		t.Fatalf("revert: HTTP %d: %s", reverted.Code, reverted.Body.String())
	}
	revertOperation := decodeJSONMap(t, reverted)["operation_id"].(string)
	after, _ := environment.list(t, "")
	if after.Pagination.Total != 4 || after.Items[0].ID != revertOperation || after.Items[0].Type != "revert" || after.Items[0].Reverts == nil || *after.Items[0].Reverts != joinOperation ||
		after.Items[0].Revertible || after.Items[0].Changes.Kind != "release_group_change" || after.Items[0].Release == nil || after.Items[0].Release.GroupID != environment.groupB {
		t.Fatalf("revert record item = %+v", after.Items[0])
	}
	if reorderedJoin := after.Items[2]; reorderedJoin.ID != joinOperation || reorderedJoin.RevertedBy == nil || *reorderedJoin.RevertedBy != revertOperation || reorderedJoin.Revertible {
		t.Fatalf("reverted item = %+v", reorderedJoin)
	}
	types := []string{}
	for _, item := range after.Items {
		types = append(types, item.Type)
	}
	sort.Strings(types)
	if !reflect.DeepEqual(types, []string{"release_correction", "release_correction", "release_group_change", "revert"}) {
		t.Fatalf("record types = %v", types)
	}
}

func TestPostgreSQLRevertAPIRejectsUnauthorizedAndInvalidRequestsWithoutSideEffects(t *testing.T) {
	environment := newOperationsEnvironment(t)
	application := environment.application

	// A：第一条修正之后又被修改过（revert_conflict）。B：归组之后被撤销（already_reverted，以及撤销记录 not_revertible）。
	// C：修正之后曲目都缺失了，不再可见（404）。
	superseded, revision := environment.correct(t, environment.a, 1, map[string]any{"genre": setChange("古典")})
	environment.correct(t, environment.a, revision, map[string]any{"genre": setChange("爵士")})
	joinOperation, _ := environment.join(t, environment.b, 1, environment.groupA)
	reverted := environment.revert(t, &environment.admin, joinOperation, uuid.New())
	if reverted.Code != http.StatusOK {
		t.Fatalf("revert the join: HTTP %d: %s", reverted.Code, reverted.Body.String())
	}
	revertRecord := decodeJSONMap(t, reverted)["operation_id"].(string)
	third, _ := insertGroupedRelease(t, application, environment.rootID, groupedRelease{title: "Album C", artist: "Artist C", year: 2010, tracks: []string{"present"}})
	hiddenOperation, _ := environment.correct(t, third, 1, map[string]any{"label": setChange("厂牌")})
	mustExec(t, application, `UPDATE tracks SET source_status='missing' WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1::uuid)`, third)
	records := countRows(t, application, `SELECT COUNT(*) FROM operations`)

	for _, testCase := range []struct {
		name      string
		user      *integrationTestUser
		operation string
		key       string
		headers   map[string]string
		status    int
		code      string
	}{
		{name: "no session", operation: superseded, key: uuid.New(), status: 401, code: "unauthorized"},
		{name: "ordinary member", user: &environment.ordinary, operation: superseded, key: uuid.New(), status: 403, code: "forbidden"},
		{name: "cross-origin administrator", user: &environment.admin, operation: superseded, key: uuid.New(), headers: map[string]string{"Origin": "http://evil.example"}, status: 403, code: "origin_forbidden"},
		{name: "cross-origin without a session", operation: superseded, key: uuid.New(), headers: map[string]string{"Origin": "http://evil.example"}, status: 403, code: "origin_forbidden"},
		{name: "missing origin", user: &environment.admin, operation: superseded, key: uuid.New(), headers: map[string]string{"Origin": ""}, status: 403, code: "origin_required"},
		{name: "malformed operation id", user: &environment.admin, operation: "not-a-uuid", key: uuid.New(), status: 400, code: "invalid_id"},
		{name: "missing idempotency key", user: &environment.admin, operation: superseded, status: 400, code: "invalid_input"},
		{name: "idempotency key that is not a UUID", user: &environment.admin, operation: superseded, key: "not-a-uuid", status: 400, code: "invalid_input"},
		{name: "unknown operation", user: &environment.admin, operation: uuid.New(), key: uuid.New(), status: 404, code: "not_found"},
		{name: "operation already reverted", user: &environment.admin, operation: joinOperation, key: uuid.New(), status: 409, code: "already_reverted"},
		{name: "revert of a revert", user: &environment.admin, operation: revertRecord, key: uuid.New(), status: 409, code: "not_revertible"},
		{name: "release changed afterwards", user: &environment.admin, operation: superseded, key: uuid.New(), status: 409, code: "revert_conflict"},
		{name: "release no longer visible", user: &environment.admin, operation: hiddenOperation, key: uuid.New(), status: 404, code: "not_found"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			requireAPIError(t, environment.revert(t, testCase.user, testCase.operation, testCase.key, testCase.headers), testCase.status, testCase.code)
		})
	}
	if got := countRows(t, application, `SELECT COUNT(*) FROM operations`); got != records {
		t.Fatalf("rejected reverts wrote records: %d → %d", records, got)
	}
}

func TestPostgreSQLRevertAPIUndoesCorrectionsAndGroupChangesAndReplays(t *testing.T) {
	environment := newOperationsEnvironment(t)
	application := environment.application
	titleOf := func(releaseID string) (string, any) {
		t.Helper()
		detail := decodeJSONMap(t, application.request(t, http.MethodGet, "/api/v1/releases/"+releaseID, nil, &environment.ordinary, nil))
		return detail["title"].(string), detail["year"]
	}

	// 字段修正：撤销之后显示回扫描值，修正标记消失。
	correction, revision := environment.correct(t, environment.a, 1, map[string]any{"title": setChange("Album A (corrected)"), "year": nullChange})
	if title, year := titleOf(environment.a); title != "Album A (corrected)" || year != nil {
		t.Fatalf("after the correction: %q, %v", title, year)
	}
	key := uuid.New()
	reverted := environment.revert(t, &environment.admin, correction, key)
	if reverted.Code != http.StatusOK {
		t.Fatalf("revert: HTTP %d: %s", reverted.Code, reverted.Body.String())
	}
	result := decodeJSONMap(t, reverted)
	if result["reverts_operation_id"] != correction || result["release_id"] != environment.a || result["revision"] != float64(revision+1) || result["operation_id"] == "" {
		t.Fatalf("revert result = %v", result)
	}
	if title, year := titleOf(environment.a); title != "Album A" || year != float64(1998) {
		t.Fatalf("after the revert: %q, %v; want the scanned values", title, year)
	}
	detail := decodeJSONMap(t, application.request(t, http.MethodGet, "/api/v1/releases/"+environment.a, nil, &environment.ordinary, nil))
	if !reflect.DeepEqual(detail["corrected_fields"], []any{}) || detail["revision"] != float64(revision+1) {
		t.Fatalf("detail after the revert: corrected %v, revision %v", detail["corrected_fields"], detail["revision"])
	}

	// 同键同义：重放同一份响应，不再撤销；同键异义：冲突；不同的键再撤销同一条：already_reverted。
	replay := environment.revert(t, &environment.admin, correction, strings.ToUpper(key))
	if replay.Code != http.StatusOK || !jsonResponsesEqual(t, replay, reverted) {
		t.Fatalf("replay: HTTP %d: %s; want %s", replay.Code, replay.Body.String(), reverted.Body.String())
	}
	other, _ := environment.correct(t, environment.b, 1, map[string]any{"genre": setChange("古典")})
	requireAPIError(t, environment.revert(t, &environment.admin, other, key), 409, "idempotency_conflict")
	requireAPIError(t, environment.revert(t, &environment.admin, correction, uuid.New()), 409, "already_reverted")
	if got := countRows(t, application, `SELECT COUNT(*) FROM operations WHERE operation_type='revert'`); got != 1 {
		t.Fatalf("revert records = %d, want exactly 1", got)
	}

	// 人工归组：撤销之后回到原组，原地址恢复。
	joinOperation, _ := environment.join(t, environment.a, revision+1, environment.groupB)
	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/release-groups/"+environment.groupA, nil, &environment.ordinary, nil), 404, "not_found")
	if response := environment.revert(t, &environment.admin, joinOperation, uuid.New()); response.Code != http.StatusOK {
		t.Fatalf("revert the join: HTTP %d: %s", response.Code, response.Body.String())
	}
	var detailOfA struct {
		ReleaseCount int `json:"release_count"`
	}
	getJSON(t, application, &environment.ordinary, "/api/v1/release-groups/"+environment.groupA, &detailOfA)
	var detailOfB struct {
		ReleaseCount int `json:"release_count"`
	}
	getJSON(t, application, &environment.ordinary, "/api/v1/release-groups/"+environment.groupB, &detailOfB)
	if detailOfA.ReleaseCount != 1 || detailOfB.ReleaseCount != 1 {
		t.Fatalf("after reverting the join: group A has %d versions, group B has %d; want 1 and 1", detailOfA.ReleaseCount, detailOfB.ReleaseCount)
	}

	// 撤销之后用旧的版本号写入被拒绝（撤销也递增了版本号）。
	requireAPIError(t, application.request(t, http.MethodPatch, "/api/v1/releases/"+environment.a+"/corrections", correctionBody(revision+1, map[string]any{"genre": setChange("爵士")}), &environment.admin, idempotencyHeader(uuid.New())), 409, "revision_conflict")
}

// 对同一条记录的并发撤销：同键只执行一次；不同的键恰好一个成功，其余是 already_reverted。
func TestPostgreSQLRevertAPIConcurrentRequests(t *testing.T) {
	environment := newOperationsEnvironment(t)
	application := environment.application

	overlap := func(operationID string, requests int, send func(index int) *httptest.ResponseRecorder) []*httptest.ResponseRecorder {
		t.Helper()
		holder, err := application.database.Begin()
		if err != nil {
			t.Fatalf("begin lock holder: %v", err)
		}
		t.Cleanup(func() { _ = holder.Rollback() })
		// 锁住原记录：所有撤销请求都卡在“锁住原记录”（或等同一个幂等键的锁）上，释放之后才依次进入，所以一定真正重叠。
		if _, err := holder.Exec(`SELECT id FROM operations WHERE id=$1::uuid FOR UPDATE`, operationID); err != nil {
			t.Fatalf("lock the original record: %v", err)
		}
		responses := make(chan *httptest.ResponseRecorder, requests)
		for index := 0; index < requests; index++ {
			go func() { responses <- send(index) }()
		}
		waitForLockWaiters(t, application.database, requests)
		if err := holder.Rollback(); err != nil {
			t.Fatalf("release lock holder: %v", err)
		}
		results := make([]*httptest.ResponseRecorder, 0, requests)
		timeout := time.After(15 * time.Second)
		for len(results) < requests {
			select {
			case response := <-responses:
				results = append(results, response)
			case <-timeout:
				t.Fatalf("only %d of %d overlapping requests finished", len(results), requests)
			}
		}
		return results
	}

	first, _ := environment.correct(t, environment.a, 1, map[string]any{"genre": setChange("古典")})
	key := uuid.New()
	same := overlap(first, overlappingRootRequests, func(int) *httptest.ResponseRecorder {
		return environment.revert(t, &environment.admin, first, key)
	})
	operationIDs := map[string]bool{}
	for _, response := range same {
		if response.Code != http.StatusOK {
			t.Fatalf("an overlapping revert with the same key: HTTP %d: %s", response.Code, response.Body.String())
		}
		operationIDs[decodeJSONMap(t, response)["operation_id"].(string)] = true
	}
	if reverts := countRows(t, application, `SELECT COUNT(*) FROM operations WHERE operation_type='revert'`); len(operationIDs) != 1 || reverts != 1 {
		t.Fatalf("same-key overlap: %d distinct operations, %d revert records; want exactly one", len(operationIDs), reverts)
	}

	second, _ := environment.correct(t, environment.b, 1, map[string]any{"genre": setChange("爵士")})
	different := overlap(second, 4, func(int) *httptest.ResponseRecorder {
		return environment.revert(t, &environment.admin, second, uuid.New())
	})
	succeeded, already := 0, 0
	for _, response := range different {
		switch response.Code {
		case http.StatusOK:
			succeeded++
		case http.StatusConflict:
			requireAPIError(t, response, 409, "already_reverted")
			already++
		default:
			t.Fatalf("overlapping reverts with different keys: HTTP %d: %s", response.Code, response.Body.String())
		}
	}
	if succeeded != 1 || already != 3 {
		t.Fatalf("different-key overlap: %d succeeded, %d already reverted; want 1 and 3", succeeded, already)
	}
}
