package app

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func joinBody(revision int, groupID string) map[string]any {
	return map[string]any{"expected_revision": revision, "action": "join", "group_id": groupID}
}

func detachBody(revision int) map[string]any {
	return map[string]any{"expected_revision": revision, "action": "detach"}
}

// groupEnvironment 是两张各自成组的可见专辑 A（1998）与 B（2005），一张没有可见曲目的发行与只有它的组，一个管理员与一个普通成员。
type groupEnvironment struct {
	application     *integrationTestApplication
	admin, ordinary integrationTestUser
	rootID          string
	a, b, hidden    string
	groupA, groupB  string
	groupHiddenOnly string
}

func newGroupEnvironment(t *testing.T) *groupEnvironment {
	t.Helper()
	application := newIntegrationTestApplication(t)
	environment := &groupEnvironment{application: application, admin: application.createUser(t, "admin"), ordinary: application.createUser(t, "user"), rootID: uuid.New()}
	mustExec(t, application, `INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,$2,'active',1)`, environment.rootID, application.allowedRoot)
	environment.a, environment.groupA = insertGroupedRelease(t, application, environment.rootID, groupedRelease{title: "Album A", artist: "Artist A", year: 1998, tracks: []string{"present"}, uncertain: 1})
	environment.b, environment.groupB = insertGroupedRelease(t, application, environment.rootID, groupedRelease{title: "Album B", artist: "Artist B", year: 2005, tracks: []string{"present"}, uncertain: 2})
	environment.hidden, environment.groupHiddenOnly = insertGroupedRelease(t, application, environment.rootID, groupedRelease{title: "Hidden", artist: "Artist H", tracks: []string{"missing"}})
	return environment
}

func (environment *groupEnvironment) post(t *testing.T, user *integrationTestUser, releaseID, key string, body any, extraHeaders ...map[string]string) *httptest.ResponseRecorder {
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
	return environment.application.request(t, http.MethodPost, "/api/v1/releases/"+releaseID+"/group", body, user, headers)
}

func (environment *groupEnvironment) state(t *testing.T, releaseID string) (groupID string, revision int64, operations int) {
	t.Helper()
	if err := environment.application.database.QueryRow(`SELECT group_id::text, edit_revision FROM releases WHERE id=$1::uuid`, releaseID).Scan(&groupID, &revision); err != nil {
		t.Fatalf("read release state: %v", err)
	}
	return groupID, revision, countRows(t, environment.application, `SELECT COUNT(*) FROM operations`)
}

func TestPostgreSQLGroupChangeAPIRejectsUnauthorizedAndInvalidRequestsWithoutSideEffects(t *testing.T) {
	environment := newGroupEnvironment(t)
	valid := joinBody(1, environment.groupB)

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
		{name: "no session", release: environment.a, key: uuid.New(), body: valid, status: 401, code: "unauthorized"},
		{name: "ordinary member", user: &environment.ordinary, release: environment.a, key: uuid.New(), body: valid, status: 403, code: "forbidden"},
		{name: "cross-origin administrator", user: &environment.admin, release: environment.a, key: uuid.New(), body: valid, headers: map[string]string{"Origin": "http://evil.example"}, status: 403, code: "origin_forbidden"},
		{name: "cross-origin without a session", release: environment.a, key: uuid.New(), body: valid, headers: map[string]string{"Origin": "http://evil.example"}, status: 403, code: "origin_forbidden"},
		{name: "missing origin", user: &environment.admin, release: environment.a, key: uuid.New(), body: valid, headers: map[string]string{"Origin": ""}, status: 403, code: "origin_required"},
		{name: "malformed release id", user: &environment.admin, release: "not-a-uuid", key: uuid.New(), body: valid, status: 400, code: "invalid_id"},
		{name: "missing idempotency key", user: &environment.admin, release: environment.a, body: valid, status: 400, code: "invalid_input"},
		{name: "idempotency key that is not a UUID", user: &environment.admin, release: environment.a, key: "not-a-uuid", body: valid, status: 400, code: "invalid_input"},
		{name: "body that is not an object", user: &environment.admin, release: environment.a, key: uuid.New(), body: "join", status: 400, code: "invalid_json"},
		{name: "expected revision zero", user: &environment.admin, release: environment.a, key: uuid.New(), body: joinBody(0, environment.groupB), status: 400, code: "invalid_input"},
		{name: "unknown action", user: &environment.admin, release: environment.a, key: uuid.New(), body: map[string]any{"expected_revision": 1, "action": "merge", "group_id": environment.groupB}, status: 400, code: "invalid_input"},
		{name: "missing action", user: &environment.admin, release: environment.a, key: uuid.New(), body: map[string]any{"expected_revision": 1}, status: 400, code: "invalid_input"},
		{name: "join without a target", user: &environment.admin, release: environment.a, key: uuid.New(), body: map[string]any{"expected_revision": 1, "action": "join"}, status: 400, code: "invalid_input"},
		{name: "join with a malformed target", user: &environment.admin, release: environment.a, key: uuid.New(), body: joinBody(1, "not-a-uuid"), status: 400, code: "invalid_input"},
		{name: "detach with a target", user: &environment.admin, release: environment.a, key: uuid.New(), body: map[string]any{"expected_revision": 1, "action": "detach", "group_id": environment.groupB}, status: 400, code: "invalid_input"},
		{name: "release without visible tracks", user: &environment.admin, release: environment.hidden, key: uuid.New(), body: valid, status: 404, code: "not_found"},
		{name: "unknown release", user: &environment.admin, release: uuid.New(), key: uuid.New(), body: valid, status: 404, code: "not_found"},
		{name: "target group that does not exist", user: &environment.admin, release: environment.a, key: uuid.New(), body: joinBody(1, uuid.New()), status: 404, code: "group_not_found"},
		{name: "target group without visible versions", user: &environment.admin, release: environment.a, key: uuid.New(), body: joinBody(1, environment.groupHiddenOnly), status: 404, code: "group_not_found"},
		{name: "join the group it is already in", user: &environment.admin, release: environment.a, key: uuid.New(), body: joinBody(1, environment.groupA), status: 409, code: "same_group"},
		{name: "detach an album that is already alone", user: &environment.admin, release: environment.a, key: uuid.New(), body: detachBody(1), status: 409, code: "already_alone"},
		{name: "stale revision", user: &environment.admin, release: environment.a, key: uuid.New(), body: joinBody(9, environment.groupB), status: 409, code: "revision_conflict"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			requireAPIError(t, environment.post(t, testCase.user, testCase.release, testCase.key, testCase.body, testCase.headers), testCase.status, testCase.code)
		})
	}

	// 所有被拒绝的请求都没有任何副作用：没有移动、版本号不变、没有操作记录、没有新增的组。
	groupID, revision, operations := environment.state(t, environment.a)
	groups := countRows(t, environment.application, `SELECT COUNT(*) FROM release_groups`)
	if groupID != environment.groupA || revision != 1 || operations != 0 || groups != 3 {
		t.Fatalf("rejected requests left group %s, revision %d, %d operations, %d groups; want group A, 1, 0, 3", groupID, revision, operations, groups)
	}
}

func TestPostgreSQLGroupChangeAPIJoinDetachAndReads(t *testing.T) {
	environment := newGroupEnvironment(t)
	application := environment.application

	// 归入：响应、目标组的成员与 attention 之和、原组消失。
	key := uuid.New()
	joined := environment.post(t, &environment.admin, environment.a, key, joinBody(1, environment.groupB))
	if joined.Code != http.StatusOK {
		t.Fatalf("join: HTTP %d: %s", joined.Code, joined.Body.String())
	}
	result := decodeJSONMap(t, joined)
	if result["release_id"] != environment.a || result["group_id"] != environment.groupB || result["previous_group_id"] != environment.groupA || result["revision"] != float64(2) || result["operation_id"] == "" {
		t.Fatalf("join result = %v", result)
	}
	var detail struct {
		ID             string `json:"id"`
		ReleaseCount   int    `json:"release_count"`
		AttentionCount int    `json:"attention_count"`
		Representative struct {
			ID string `json:"id"`
		} `json:"representative"`
		Releases []struct {
			ID string `json:"id"`
		} `json:"releases"`
	}
	getJSON(t, application, &environment.ordinary, "/api/v1/release-groups/"+environment.groupB, &detail)
	if detail.ReleaseCount != 2 || detail.AttentionCount != 3 || detail.Representative.ID != environment.a || len(detail.Releases) != 2 || detail.Releases[0].ID != environment.a || detail.Releases[1].ID != environment.b {
		t.Fatalf("target group = %+v; want A (1998) as the representative, both versions and attention 1+2", detail)
	}
	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/release-groups/"+environment.groupA, nil, &environment.ordinary, nil), 404, "not_found")
	var groups releaseGroupListResponse
	getJSON(t, application, &environment.ordinary, "/api/v1/release-groups", &groups)
	if groups.Pagination.Total != 1 || groups.Items[0].ID != environment.groupB {
		t.Fatalf("group list = %+v; want only the joined album", groups)
	}
	var releases releaseListResponse
	getJSON(t, application, &environment.ordinary, "/api/v1/releases", &releases)
	if releases.Pagination.Total != 2 {
		t.Fatalf("release list total = %d, want both versions", releases.Pagination.Total)
	}
	if groupID, revision, operations := environment.state(t, environment.a); groupID != environment.groupB || revision != 2 || operations != 1 {
		t.Fatalf("after the join: group %s, revision %d, %d operations", groupID, revision, operations)
	}

	// 同键同义：重放同一份响应，不再执行；同键异义：冲突。
	replay := environment.post(t, &environment.admin, environment.a, strings.ToUpper(key), joinBody(1, environment.groupB))
	if replay.Code != http.StatusOK || !jsonResponsesEqual(t, replay, joined) {
		t.Fatalf("replay: HTTP %d: %s; want the recorded response %s", replay.Code, replay.Body.String(), joined.Body.String())
	}
	requireAPIError(t, environment.post(t, &environment.admin, environment.a, key, detachBody(1)), 409, "idempotency_conflict")
	requireAPIError(t, environment.post(t, &environment.admin, environment.a, key, joinBody(1, environment.groupA)), 409, "idempotency_conflict")
	if _, revision, operations := environment.state(t, environment.a); revision != 2 || operations != 1 {
		t.Fatalf("after replays and conflicts: revision %d, %d operations; want 2, 1", revision, operations)
	}

	// 移出：得到一个新组；两张专辑各自可见；原组仍然存在但没有成员变化。
	detached := environment.post(t, &environment.admin, environment.a, uuid.New(), detachBody(2))
	if detached.Code != http.StatusOK {
		t.Fatalf("detach: HTTP %d: %s", detached.Code, detached.Body.String())
	}
	result = decodeJSONMap(t, detached)
	newGroup, _ := result["group_id"].(string)
	if newGroup == "" || newGroup == environment.groupA || newGroup == environment.groupB || result["previous_group_id"] != environment.groupB || result["revision"] != float64(3) {
		t.Fatalf("detach result = %v; want a brand new group", result)
	}
	getJSON(t, application, &environment.ordinary, "/api/v1/release-groups", &groups)
	if groups.Pagination.Total != 2 {
		t.Fatalf("group list after detaching = %+v; want two albums", groups)
	}
	getJSON(t, application, &environment.ordinary, "/api/v1/release-groups/"+newGroup, &detail)
	if detail.ReleaseCount != 1 || detail.Representative.ID != environment.a {
		t.Fatalf("new group = %+v", detail)
	}

	// 回到原来的组的做法：把 A 归入 B 的旧组 groupA 已经没有可见版本，所以不能当作目标；这是撤销（第 4 步）才能做的事。
	requireAPIError(t, environment.post(t, &environment.admin, environment.b, uuid.New(), joinBody(1, environment.groupA)), 404, "group_not_found")
}

// 同一个键的并发请求只执行一次；两个请求同时把各自的版本归入对方的组，不死锁、结果一致。
func TestPostgreSQLGroupChangeAPIConcurrentRequests(t *testing.T) {
	environment := newGroupEnvironment(t)
	application := environment.application

	overlap := func(requests int, send func(index int) *httptest.ResponseRecorder) []*httptest.ResponseRecorder {
		t.Helper()
		holder, err := application.database.Begin()
		if err != nil {
			t.Fatalf("begin lock holder: %v", err)
		}
		t.Cleanup(func() { _ = holder.Rollback() })
		// 请求卡在组行上：同键请求的第一个在取组行锁时被挡住，其余在键锁上排队，所以请求一定真正重叠。
		if _, err := holder.Exec(`LOCK TABLE release_groups IN ACCESS EXCLUSIVE MODE`); err != nil {
			t.Fatalf("lock release_groups: %v", err)
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

	key := uuid.New()
	same := overlap(overlappingRootRequests, func(int) *httptest.ResponseRecorder {
		return environment.post(t, &environment.admin, environment.a, key, joinBody(1, environment.groupB))
	})
	operationIDs := map[string]bool{}
	for _, response := range same {
		if response.Code != http.StatusOK {
			t.Fatalf("an overlapping request with the same key: HTTP %d: %s", response.Code, response.Body.String())
		}
		operationIDs[decodeJSONMap(t, response)["operation_id"].(string)] = true
	}
	if groupID, revision, operations := environment.state(t, environment.a); len(operationIDs) != 1 || groupID != environment.groupB || revision != 2 || operations != 1 {
		t.Fatalf("same-key overlap: %d distinct operations, group %s, revision %d, %d operations; want exactly one execution", len(operationIDs), groupID, revision, operations)
	}

	// 回到起点，再让两个版本同时归入对方的组：恰好一个成功，另一个发现目标组已经没有可见版本。
	mustExec(t, application, `UPDATE releases SET group_id=$2::uuid, edit_revision=1 WHERE id=$1::uuid`, environment.a, environment.groupA)
	mustExec(t, application, `DELETE FROM operations`)
	crossed := overlap(2, func(index int) *httptest.ResponseRecorder {
		if index == 0 {
			return environment.post(t, &environment.admin, environment.a, uuid.New(), joinBody(1, environment.groupB))
		}
		return environment.post(t, &environment.admin, environment.b, uuid.New(), joinBody(1, environment.groupA))
	})
	succeeded, notFound := 0, 0
	for _, response := range crossed {
		switch response.Code {
		case http.StatusOK:
			succeeded++
		case http.StatusNotFound:
			requireAPIError(t, response, 404, "group_not_found")
			notFound++
		default:
			t.Fatalf("crossed group changes: HTTP %d: %s (a deadlock would be 500)", response.Code, response.Body.String())
		}
	}
	var groups releaseGroupListResponse
	getJSON(t, application, &environment.ordinary, "/api/v1/release-groups", &groups)
	if succeeded != 1 || notFound != 1 || groups.Pagination.Total != 1 || groups.Items[0].ReleaseCount != 2 {
		t.Fatalf("crossed group changes: %d succeeded, %d found an empty target, groups %+v; want one of each and a single album with both versions", succeeded, notFound, groups)
	}
}

// 合成素材的真实重扫：人工归组之后重扫，已有发行的归组保持；新发现的专辑仍各自成组。
func TestPostgreSQLManualGroupingSurvivesARealRescan(t *testing.T) {
	application := newIntegrationTestApplication(t)
	admin := application.createUser(t, "admin")
	library := createLibraryDirectory(t, application, "classical")
	alphaFile, betaFile := filepath.Join(library, "alpha", "01.flac"), filepath.Join(library, "beta", "01.flac")
	writeSyntheticTrack(t, alphaFile, 1, "TITLE=Alpha Prelude", "ARTIST=Alpha Pianist", "ALBUM=Alpha Album", "ALBUMARTIST=Alpha Pianist", "DATE=2000", "TRACKNUMBER=1")
	writeSyntheticTrack(t, betaFile, 2, "TITLE=Beta Prelude", "ARTIST=Beta Pianist", "ALBUM=Beta Album", "ALBUMARTIST=Beta Pianist", "DATE=2005", "TRACKNUMBER=1")
	if response := application.request(t, http.MethodPost, "/api/v1/library-roots", map[string]string{"path": library}, &admin, idempotencyHeader("create-classical")); response.Code != http.StatusCreated {
		t.Fatalf("register the library: HTTP %d: %s", response.Code, response.Body.String())
	}
	runScan(t, application, &admin)

	idOf := func(title string) (releaseID, groupID string) {
		t.Helper()
		if err := application.database.QueryRow(`SELECT id::text, group_id::text FROM releases WHERE title=$1`, title).Scan(&releaseID, &groupID); err != nil {
			t.Fatalf("read release %q: %v", title, err)
		}
		return releaseID, groupID
	}
	alpha, alphaGroup := idOf("Alpha Album")
	beta, betaGroup := idOf("Beta Album")
	if alphaGroup == betaGroup {
		t.Fatal("the scan put two albums into one group")
	}
	revisionOf := func(releaseID string) float64 {
		t.Helper()
		return decodeJSONMap(t, application.request(t, http.MethodGet, "/api/v1/releases/"+releaseID, nil, &admin, nil))["revision"].(float64)
	}
	joined := application.request(t, http.MethodPost, "/api/v1/releases/"+beta+"/group", joinBody(int(revisionOf(beta)), alphaGroup), &admin, idempotencyHeader(uuid.New()))
	if joined.Code != http.StatusOK {
		t.Fatalf("join: HTTP %d: %s", joined.Code, joined.Body.String())
	}
	revisionAfterJoin := revisionOf(beta)

	// 重扫：标签变了（音频不变，发行延续），并新增一张专辑。
	writeSyntheticTrack(t, betaFile, 2, "TITLE=Beta Prelude", "ARTIST=Beta Pianist", "ALBUM=Beta Album Remastered", "ALBUMARTIST=Beta Pianist", "DATE=2006", "TRACKNUMBER=1")
	writeSyntheticTrack(t, filepath.Join(library, "gamma", "01.flac"), 3, "TITLE=Gamma Prelude", "ARTIST=Gamma Pianist", "ALBUM=Gamma Album", "ALBUMARTIST=Gamma Pianist", "DATE=2010", "TRACKNUMBER=1")
	runScan(t, application, &admin)

	if renamed, groupID := idOf("Beta Album Remastered"); renamed != beta || groupID != alphaGroup {
		t.Fatalf("after the rescan the joined release is %s in group %s; want %s in %s", renamed, groupID, beta, alphaGroup)
	}
	if revisionOf(beta) != revisionAfterJoin {
		t.Fatalf("the rescan changed the manual revision %v → %v", revisionAfterJoin, revisionOf(beta))
	}
	_, gammaGroup := idOf("Gamma Album")
	if gammaGroup == alphaGroup {
		t.Fatal("a newly discovered album was put into an existing group")
	}
	var groups releaseGroupListResponse
	getJSON(t, application, &admin, "/api/v1/release-groups?page_size=10", &groups)
	if groups.Pagination.Total != 2 {
		t.Fatalf("groups after the rescan = %d, want the joined album and the new one", groups.Pagination.Total)
	}
	var detail struct {
		ReleaseCount int `json:"release_count"`
		Releases     []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"releases"`
	}
	getJSON(t, application, &admin, "/api/v1/release-groups/"+alphaGroup, &detail)
	if detail.ReleaseCount != 2 || detail.Releases[0].ID != alpha || detail.Releases[1].Title != "Beta Album Remastered" {
		t.Fatalf("joined group after the rescan = %+v; want Alpha first and the rescanned Beta", detail)
	}
}
