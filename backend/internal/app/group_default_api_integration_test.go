package app

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func defaultBody(revision int64, releaseID any) map[string]any {
	return map[string]any{"expected_revision": revision, "release_id": releaseID}
}

func defaultPath(groupID string) string { return "/api/v1/release-groups/" + groupID + "/default" }

// defaultEnvironment 在 groupEnvironment 的基础上把 B 归入 A 的专辑：专辑 A 有两个可见版本（A 1998、B 2005），
// 另有一张独立的专辑 C（2010）和一个没有可见曲目的发行；管理员、普通成员各一个会话。
type defaultEnvironment struct {
	*groupEnvironment
	c, groupC string
}

func newDefaultEnvironment(t *testing.T) *defaultEnvironment {
	t.Helper()
	environment := &defaultEnvironment{groupEnvironment: newGroupEnvironment(t)}
	environment.c, environment.groupC = insertGroupedRelease(t, environment.application, environment.rootID, groupedRelease{title: "Album C", artist: "Artist C", year: 2010, tracks: []string{"present"}})
	if response := environment.post(t, &environment.admin, environment.b, uuid.New(), joinBody(1, environment.groupA)); response.Code != http.StatusOK {
		t.Fatalf("join B into A's album: HTTP %d: %s", response.Code, response.Body.String())
	}
	return environment
}

func (environment *defaultEnvironment) postDefault(t *testing.T, user *integrationTestUser, groupID, key string, body any, extraHeaders ...map[string]string) *httptest.ResponseRecorder {
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
	return environment.application.request(t, http.MethodPost, defaultPath(groupID), body, user, headers)
}

func (environment *defaultEnvironment) revert(t *testing.T, user *integrationTestUser, operationID, key string) *httptest.ResponseRecorder {
	t.Helper()
	return environment.application.request(t, http.MethodPost, revertPath(operationID), nil, user, map[string]string{"Idempotency-Key": key})
}

func (environment *defaultEnvironment) detail(t *testing.T, groupID string) catalog.ReleaseGroupDetail {
	t.Helper()
	var detail catalog.ReleaseGroupDetail
	getJSON(t, environment.application, &environment.ordinary, "/api/v1/release-groups/"+groupID, &detail)
	return detail
}

func (environment *defaultEnvironment) groupState(t *testing.T, groupID string) (revision int64, stored string, operations int) {
	t.Helper()
	var storedDefault *string
	if err := environment.application.database.QueryRow(`SELECT edit_revision, default_release_id::text FROM release_groups WHERE id=$1::uuid`, groupID).Scan(&revision, &storedDefault); err != nil {
		t.Fatalf("read the group state: %v", err)
	}
	if storedDefault != nil {
		stored = *storedDefault
	}
	return revision, stored, countRows(t, environment.application, `SELECT COUNT(*) FROM operations`)
}

func TestPostgreSQLGroupDefaultAPIRejectsUnauthorizedAndInvalidRequestsWithoutSideEffects(t *testing.T) {
	environment := newDefaultEnvironment(t)
	valid := defaultBody(1, environment.b)

	for _, testCase := range []struct {
		name    string
		user    *integrationTestUser
		group   string
		key     string
		body    any
		headers map[string]string
		status  int
		code    string
	}{
		{name: "no session", group: environment.groupA, key: uuid.New(), body: valid, status: 401, code: "unauthorized"},
		{name: "ordinary member", user: &environment.ordinary, group: environment.groupA, key: uuid.New(), body: valid, status: 403, code: "forbidden"},
		{name: "cross-origin administrator", user: &environment.admin, group: environment.groupA, key: uuid.New(), body: valid, headers: map[string]string{"Origin": "http://evil.example"}, status: 403, code: "origin_forbidden"},
		{name: "cross-origin without a session", group: environment.groupA, key: uuid.New(), body: valid, headers: map[string]string{"Origin": "http://evil.example"}, status: 403, code: "origin_forbidden"},
		{name: "missing origin", user: &environment.admin, group: environment.groupA, key: uuid.New(), body: valid, headers: map[string]string{"Origin": ""}, status: 403, code: "origin_required"},
		{name: "malformed album id", user: &environment.admin, group: "not-a-uuid", key: uuid.New(), body: valid, status: 400, code: "invalid_id"},
		{name: "missing idempotency key", user: &environment.admin, group: environment.groupA, body: valid, status: 400, code: "invalid_input"},
		{name: "idempotency key that is not a UUID", user: &environment.admin, group: environment.groupA, key: "not-a-uuid", body: valid, status: 400, code: "invalid_input"},
		{name: "body that is not an object", user: &environment.admin, group: environment.groupA, key: uuid.New(), body: "default", status: 400, code: "invalid_json"},
		{name: "expected revision zero", user: &environment.admin, group: environment.groupA, key: uuid.New(), body: defaultBody(0, environment.b), status: 400, code: "invalid_input"},
		{name: "expected revision missing", user: &environment.admin, group: environment.groupA, key: uuid.New(), body: map[string]any{"release_id": environment.b}, status: 400, code: "invalid_input"},
		{name: "release id missing", user: &environment.admin, group: environment.groupA, key: uuid.New(), body: map[string]any{"expected_revision": 1}, status: 400, code: "invalid_input"},
		{name: "release id that is not a UUID", user: &environment.admin, group: environment.groupA, key: uuid.New(), body: defaultBody(1, "not-a-uuid"), status: 400, code: "invalid_input"},
		{name: "release id that is a number", user: &environment.admin, group: environment.groupA, key: uuid.New(), body: defaultBody(1, 5), status: 400, code: "invalid_input"},
		{name: "album that does not exist", user: &environment.admin, group: uuid.New(), key: uuid.New(), body: valid, status: 404, code: "not_found"},
		{name: "album without visible versions", user: &environment.admin, group: environment.groupHiddenOnly, key: uuid.New(), body: defaultBody(1, nil), status: 404, code: "not_found"},
		{name: "a version of another album", user: &environment.admin, group: environment.groupA, key: uuid.New(), body: defaultBody(1, environment.c), status: 409, code: "not_in_group"},
		{name: "a version without visible tracks", user: &environment.admin, group: environment.groupA, key: uuid.New(), body: defaultBody(1, environment.hidden), status: 409, code: "not_in_group"},
		{name: "a release that does not exist", user: &environment.admin, group: environment.groupA, key: uuid.New(), body: defaultBody(1, uuid.New()), status: 409, code: "not_in_group"},
		{name: "stale revision", user: &environment.admin, group: environment.groupA, key: uuid.New(), body: defaultBody(9, environment.b), status: 409, code: "revision_conflict"},
		{name: "nothing to clear", user: &environment.admin, group: environment.groupA, key: uuid.New(), body: defaultBody(1, nil), status: 400, code: "no_changes"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			requireAPIError(t, environment.postDefault(t, testCase.user, testCase.group, testCase.key, testCase.body, testCase.headers), testCase.status, testCase.code)
		})
	}

	// 所有被拒绝的请求都没有任何副作用：版本号不变、没有设定、没有新的操作记录（只有 setup 里的那次归入）。
	revision, stored, operations := environment.groupState(t, environment.groupA)
	if revision != 1 || stored != "" || operations != 1 {
		t.Fatalf("rejected requests left revision %d, default %q, %d operations; want 1, none, 1", revision, stored, operations)
	}
}

type operationListResponse struct {
	Items []struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Release *struct {
			ID string `json:"id"`
		} `json:"release"`
		Album *struct {
			ID      string `json:"id"`
			Title   string `json:"title"`
			Visible bool   `json:"visible"`
		} `json:"album"`
		Changes struct {
			Kind string `json:"kind"`
			From *struct {
				GroupTitle string `json:"group_title"`
				Default    *struct {
					ReleaseID string `json:"release_id"`
					Title     string `json:"title"`
				} `json:"default"`
			} `json:"from"`
			To *struct {
				GroupTitle string `json:"group_title"`
				Default    *struct {
					ReleaseID string `json:"release_id"`
					Title     string `json:"title"`
				} `json:"default"`
			} `json:"to"`
		} `json:"changes"`
		Reverts    *string `json:"reverts"`
		RevertedBy *string `json:"reverted_by"`
		Revertible bool    `json:"revertible"`
	} `json:"items"`
	Pagination struct {
		Total int64 `json:"total"`
	} `json:"pagination"`
}

func TestPostgreSQLGroupDefaultAPISetClearRevertAndReads(t *testing.T) {
	environment := newDefaultEnvironment(t)
	application := environment.application

	// 没有设定：代表版本是年份最早的 A，没有生效中的默认版本；普通成员也能读到。
	before := environment.detail(t, environment.groupA)
	if before.Revision != 1 || before.DefaultReleaseID != nil || before.Representative.ID != environment.a || before.ReleaseCount != 2 {
		t.Fatalf("album before a default = %+v", before)
	}

	// 设为默认版本：响应、组详情、专辑卡片与列表排序都跟着变。
	key := uuid.New()
	set := environment.postDefault(t, &environment.admin, environment.groupA, key, defaultBody(1, environment.b))
	if set.Code != http.StatusOK {
		t.Fatalf("set the default: HTTP %d: %s", set.Code, set.Body.String())
	}
	result := decodeJSONMap(t, set)
	if result["group_id"] != environment.groupA || result["default_release_id"] != environment.b || result["revision"] != float64(2) || result["operation_id"] == "" || len(result) != 4 {
		t.Fatalf("set result = %v", result)
	}
	after := environment.detail(t, environment.groupA)
	if after.Revision != 2 || after.DefaultReleaseID == nil || *after.DefaultReleaseID != environment.b || after.Representative.ID != environment.b ||
		len(after.Releases) != 2 || after.Releases[0].ID != environment.b || after.Releases[1].ID != environment.a {
		t.Fatalf("album after the default = %+v", after)
	}
	var groups releaseGroupListResponse
	getJSON(t, application, &environment.ordinary, "/api/v1/release-groups?page_size=10", &groups)
	if groups.Pagination.Total != 2 || groups.Items[0].ID != environment.groupA || groups.Items[0].Representative.ID != environment.b || groups.Items[0].Representative.Title != "Album B" {
		t.Fatalf("album list after the default = %+v", groups)
	}

	// 同键同义：重放同一份响应，不再执行；同键异义：冲突。
	replay := environment.postDefault(t, &environment.admin, environment.groupA, key, defaultBody(1, environment.b))
	if replay.Code != http.StatusOK || !jsonResponsesEqual(t, replay, set) {
		t.Fatalf("replay: HTTP %d: %s; want %s", replay.Code, replay.Body.String(), set.Body.String())
	}
	requireAPIError(t, environment.postDefault(t, &environment.admin, environment.groupA, key, defaultBody(1, environment.a)), 409, "idempotency_conflict")
	if revision, stored, operations := environment.groupState(t, environment.groupA); revision != 2 || stored != environment.b || operations != 2 {
		t.Fatalf("after the replay: revision %d, default %q, %d operations; want 2, B, 2", revision, stored, operations)
	}
	// 已设定的值再设一次是 no_changes。
	requireAPIError(t, environment.postDefault(t, &environment.admin, environment.groupA, uuid.New(), defaultBody(2, environment.b)), 400, "no_changes")

	// 操作记录：专辑目标的记录给 album（当前标题与可见性）、release 为 null；按专辑、按版本与并集筛选。
	var byGroup operationListResponse
	getJSON(t, application, &environment.admin, "/api/v1/operations?group_id="+environment.groupA, &byGroup)
	if byGroup.Pagination.Total != 1 || len(byGroup.Items) != 1 {
		t.Fatalf("operations of the album = %+v; want only the default-version record", byGroup)
	}
	item := byGroup.Items[0]
	if item.Type != "release_group_default" || item.Release != nil || item.Album == nil || item.Album.ID != environment.groupA || item.Album.Title != "Album B" || !item.Album.Visible ||
		item.Changes.Kind != "release_group_default" || item.Changes.From == nil || item.Changes.From.Default != nil || item.Changes.From.GroupTitle != "Album A" ||
		item.Changes.To == nil || item.Changes.To.Default == nil || item.Changes.To.Default.ReleaseID != environment.b || item.Changes.To.Default.Title != "Album B" ||
		!item.Revertible || item.Reverts != nil || item.RevertedBy != nil {
		t.Fatalf("default-version item = %+v", item)
	}
	var byRelease, union operationListResponse
	getJSON(t, application, &environment.admin, "/api/v1/operations?release_id="+environment.b, &byRelease)
	getJSON(t, application, &environment.admin, "/api/v1/operations?release_id="+environment.b+"&group_id="+environment.groupA, &union)
	if byRelease.Pagination.Total != 1 || byRelease.Items[0].Type != "release_group_change" || byRelease.Items[0].Album != nil || byRelease.Items[0].Release == nil {
		t.Fatalf("operations of the version = %+v; want only the earlier group change", byRelease)
	}
	if union.Pagination.Total != 2 || len(union.Items) != 2 || union.Items[0].Type != "release_group_default" || union.Items[1].Type != "release_group_change" {
		t.Fatalf("operations of the version and the album = %+v; want both, newest first", union)
	}

	// 撤销：响应的目标是专辑，回到自动选择，撤销记录也出现在专辑的记录里且不能再撤销。
	reverted := environment.revert(t, &environment.admin, item.ID, uuid.New())
	if reverted.Code != http.StatusOK {
		t.Fatalf("revert: HTTP %d: %s", reverted.Code, reverted.Body.String())
	}
	revertResult := decodeJSONMap(t, reverted)
	if revertResult["reverts_operation_id"] != item.ID || revertResult["target_type"] != "release_group" || revertResult["target_id"] != environment.groupA ||
		revertResult["revision"] != float64(3) || revertResult["operation_id"] == "" || revertResult["release_id"] != nil {
		t.Fatalf("revert result = %v", revertResult)
	}
	if reverted := environment.detail(t, environment.groupA); reverted.Revision != 3 || reverted.DefaultReleaseID != nil || reverted.Representative.ID != environment.a {
		t.Fatalf("album after the revert = %+v", reverted)
	}
	getJSON(t, application, &environment.admin, "/api/v1/operations?group_id="+environment.groupA, &byGroup)
	if byGroup.Pagination.Total != 2 || byGroup.Items[0].Type != "revert" || byGroup.Items[0].Reverts == nil || *byGroup.Items[0].Reverts != item.ID ||
		byGroup.Items[0].Changes.Kind != "release_group_default" || byGroup.Items[0].Revertible || byGroup.Items[1].RevertedBy == nil || byGroup.Items[1].Revertible {
		t.Fatalf("records after the revert = %+v", byGroup)
	}
	requireAPIError(t, environment.revert(t, &environment.admin, item.ID, uuid.New()), 409, "already_reverted")
	requireAPIError(t, environment.revert(t, &environment.admin, byGroup.Items[0].ID, uuid.New()), 409, "not_revertible")

	// 设定、再清除；之后又有调整时，更早的那次不能撤销；普通成员不能撤销。
	setAgain := decodeJSONMap(t, environment.postDefault(t, &environment.admin, environment.groupA, uuid.New(), defaultBody(3, environment.b)))
	cleared := environment.postDefault(t, &environment.admin, environment.groupA, uuid.New(), defaultBody(4, nil))
	if cleared.Code != http.StatusOK {
		t.Fatalf("clear the default: HTTP %d: %s", cleared.Code, cleared.Body.String())
	}
	if result := decodeJSONMap(t, cleared); result["default_release_id"] != nil || result["revision"] != float64(5) {
		t.Fatalf("clear result = %v", result)
	}
	if detail := environment.detail(t, environment.groupA); detail.DefaultReleaseID != nil || detail.Representative.ID != environment.a || detail.Revision != 5 {
		t.Fatalf("album after clearing = %+v", detail)
	}
	requireAPIError(t, environment.revert(t, &environment.admin, setAgain["operation_id"].(string), uuid.New()), 409, "revert_conflict")
	requireAPIError(t, environment.revert(t, &environment.ordinary, setAgain["operation_id"].(string), uuid.New()), 403, "forbidden")
}

func TestPostgreSQLGroupDefaultAPIConcurrentRequests(t *testing.T) {
	environment := newDefaultEnvironment(t)
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

	// 同一个键的并发请求只执行一次，其余重放同一份响应。
	key := uuid.New()
	same := overlap(overlappingRootRequests, func(int) *httptest.ResponseRecorder {
		return environment.postDefault(t, &environment.admin, environment.groupA, key, defaultBody(1, environment.b))
	})
	operationIDs := map[string]bool{}
	for _, response := range same {
		if response.Code != http.StatusOK {
			t.Fatalf("an overlapping request with the same key: HTTP %d: %s", response.Code, response.Body.String())
		}
		operationIDs[decodeJSONMap(t, response)["operation_id"].(string)] = true
	}
	if revision, stored, operations := environment.groupState(t, environment.groupA); len(operationIDs) != 1 || revision != 2 || stored != environment.b || operations != 2 {
		t.Fatalf("same-key overlap: %d distinct operations, revision %d, default %q, %d operations; want exactly one execution", len(operationIDs), revision, stored, operations)
	}

	// 不同的键、同一个预期版本：恰好一个成功，另一个得到版本冲突；不会出现 500（死锁）。
	crossed := overlap(2, func(index int) *httptest.ResponseRecorder {
		if index == 0 {
			return environment.postDefault(t, &environment.admin, environment.groupA, uuid.New(), defaultBody(2, environment.a))
		}
		return environment.postDefault(t, &environment.admin, environment.groupA, uuid.New(), defaultBody(2, nil))
	})
	succeeded, conflicts := 0, 0
	for _, response := range crossed {
		switch response.Code {
		case http.StatusOK:
			succeeded++
		case http.StatusConflict:
			requireAPIError(t, response, 409, "revision_conflict")
			conflicts++
		default:
			t.Fatalf("crossed default-version requests: HTTP %d: %s (a deadlock would be 500)", response.Code, response.Body.String())
		}
	}
	if succeeded != 1 || conflicts != 1 {
		t.Fatalf("crossed default-version requests: %d succeeded, %d conflicted; want one of each", succeeded, conflicts)
	}
}

// 合成素材的真实重扫：设定默认版本之后重扫（标签变了、新增一张专辑），默认版本、专辑的版本号与操作记录都保持。
func TestPostgreSQLDefaultVersionSurvivesARealRescan(t *testing.T) {
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
	beta, _ := idOf("Beta Album")
	revisionOf := func(releaseID string) int {
		t.Helper()
		return int(decodeJSONMap(t, application.request(t, http.MethodGet, "/api/v1/releases/"+releaseID, nil, &admin, nil))["revision"].(float64))
	}
	if joined := application.request(t, http.MethodPost, "/api/v1/releases/"+beta+"/group", joinBody(revisionOf(beta), alphaGroup), &admin, idempotencyHeader(uuid.New())); joined.Code != http.StatusOK {
		t.Fatalf("join: HTTP %d: %s", joined.Code, joined.Body.String())
	}
	var detail catalog.ReleaseGroupDetail
	getJSON(t, application, &admin, "/api/v1/release-groups/"+alphaGroup, &detail)
	if detail.Releases[0].ID != alpha || detail.DefaultReleaseID != nil {
		t.Fatalf("the joined album before a default = %+v; want Alpha first", detail)
	}
	set := application.request(t, http.MethodPost, defaultPath(alphaGroup), defaultBody(detail.Revision, beta), &admin, idempotencyHeader(uuid.New()))
	if set.Code != http.StatusOK {
		t.Fatalf("set the default: HTTP %d: %s", set.Code, set.Body.String())
	}
	operationsBefore := countRows(t, application, `SELECT COUNT(*) FROM operations`)

	// 重扫：标签变了（音频不变，发行延续），并新增一张专辑。
	writeSyntheticTrack(t, betaFile, 2, "TITLE=Beta Prelude", "ARTIST=Beta Pianist", "ALBUM=Beta Album Remastered", "ALBUMARTIST=Beta Pianist", "DATE=2006", "TRACKNUMBER=1")
	writeSyntheticTrack(t, filepath.Join(library, "gamma", "01.flac"), 3, "TITLE=Gamma Prelude", "ARTIST=Gamma Pianist", "ALBUM=Gamma Album", "ALBUMARTIST=Gamma Pianist", "DATE=2010", "TRACKNUMBER=1")
	runScan(t, application, &admin)

	if renamed, groupID := idOf("Beta Album Remastered"); renamed != beta || groupID != alphaGroup {
		t.Fatalf("after the rescan the version is %s in album %s; want %s in %s", renamed, groupID, beta, alphaGroup)
	}
	var rescanned catalog.ReleaseGroupDetail
	getJSON(t, application, &admin, "/api/v1/release-groups/"+alphaGroup, &rescanned)
	if rescanned.Revision != detail.Revision+1 || rescanned.DefaultReleaseID == nil || *rescanned.DefaultReleaseID != beta ||
		rescanned.Representative.ID != beta || rescanned.Releases[0].ID != beta || rescanned.Releases[1].ID != alpha {
		t.Fatalf("album after the rescan = %+v; want the default kept (Beta first) at revision %d", rescanned, detail.Revision+1)
	}
	if operations := countRows(t, application, `SELECT COUNT(*) FROM operations`); operations != operationsBefore {
		t.Fatalf("the rescan changed the operation records: %d → %d", operationsBefore, operations)
	}
	var groups releaseGroupListResponse
	getJSON(t, application, &admin, "/api/v1/release-groups?page_size=10", &groups)
	if groups.Pagination.Total != 2 {
		t.Fatalf("albums after the rescan = %d, want the joined album and the new one", groups.Pagination.Total)
	}
	for _, item := range groups.Items {
		if item.ID == alphaGroup && (item.Representative.ID != beta || item.Representative.Title != "Beta Album Remastered" || item.ReleaseCount != 2) {
			t.Fatalf("the joined album's card after the rescan = %+v; want the default version Beta as the card", item)
		}
	}
}
