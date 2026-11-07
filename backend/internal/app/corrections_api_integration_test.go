package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func setChange(value any) map[string]any { return map[string]any{"mode": "set", "value": value} }

var (
	nullChange  = map[string]any{"mode": "null"}
	clearChange = map[string]any{"mode": "clear"}
)

func correctionBody(revision int, changes map[string]any) map[string]any {
	return map[string]any{"expected_revision": revision, "changes": changes}
}

func idempotencyHeader(key string) map[string]string {
	return map[string]string{"Idempotency-Key": key}
}

// correctionsEnvironment 是一个管理员、一个普通成员、一张可见发行（有流派与厂牌）与一张没有可见曲目的发行。
type correctionsEnvironment struct {
	application *integrationTestApplication
	admin       integrationTestUser
	ordinary    integrationTestUser
	rootID      string
	releaseID   string
	groupID     string
	hiddenID    string
}

func newCorrectionsEnvironment(t *testing.T) *correctionsEnvironment {
	t.Helper()
	application := newIntegrationTestApplication(t)
	environment := &correctionsEnvironment{application: application, admin: application.createUser(t, "admin"), ordinary: application.createUser(t, "user"), rootID: uuid.New()}
	mustExec(t, application, `INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,$2,'active',1)`, environment.rootID, application.allowedRoot)
	environment.releaseID, environment.groupID = insertGroupedRelease(t, application, environment.rootID, groupedRelease{title: "原始标题", artist: "原始艺人", year: 1998, tracks: []string{"present"}})
	environment.hiddenID, _ = insertGroupedRelease(t, application, environment.rootID, groupedRelease{title: "没有可见曲目", artist: "艺人", tracks: []string{"missing"}})
	mustExec(t, application, `UPDATE releases SET genres=ARRAY['古典']::text[], label='原始厂牌' WHERE id=$1::uuid`, environment.releaseID)
	return environment
}

func (environment *correctionsEnvironment) patch(t *testing.T, user *integrationTestUser, releaseID, key string, body any, extraHeaders ...map[string]string) *httptest.ResponseRecorder {
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
	return environment.application.request(t, http.MethodPatch, "/api/v1/releases/"+releaseID+"/corrections", body, user, headers)
}

func (environment *correctionsEnvironment) counts(t *testing.T) (operations, corrections int, revision int64) {
	t.Helper()
	application := environment.application
	operations = countRows(t, application, `SELECT COUNT(*) FROM operations`)
	corrections = countRows(t, application, `SELECT COUNT(*) FROM release_corrections`)
	if err := application.database.QueryRow(`SELECT edit_revision FROM releases WHERE id=$1::uuid`, environment.releaseID).Scan(&revision); err != nil {
		t.Fatalf("read revision: %v", err)
	}
	return operations, corrections, revision
}

func TestPostgreSQLCorrectionsAPIRejectsUnauthorizedAndInvalidRequestsWithoutSideEffects(t *testing.T) {
	environment := newCorrectionsEnvironment(t)
	application := environment.application
	valid := correctionBody(1, map[string]any{"genre": setChange([]string{"爵士"})})

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
		{name: "no session", release: environment.releaseID, key: uuid.New(), body: valid, status: 401, code: "unauthorized"},
		{name: "ordinary member", user: &environment.ordinary, release: environment.releaseID, key: uuid.New(), body: valid, status: 403, code: "forbidden"},
		{name: "cross-origin administrator", user: &environment.admin, release: environment.releaseID, key: uuid.New(), body: valid, headers: map[string]string{"Origin": "http://evil.example"}, status: 403, code: "origin_forbidden"},
		{name: "cross-origin without a session", release: environment.releaseID, key: uuid.New(), body: valid, headers: map[string]string{"Origin": "http://evil.example"}, status: 403, code: "origin_forbidden"},
		{name: "missing origin", user: &environment.admin, release: environment.releaseID, key: uuid.New(), body: valid, headers: map[string]string{"Origin": ""}, status: 403, code: "origin_required"},
		{name: "malformed release id", user: &environment.admin, release: "not-a-uuid", key: uuid.New(), body: valid, status: 400, code: "invalid_id"},
		{name: "missing idempotency key", user: &environment.admin, release: environment.releaseID, body: valid, status: 400, code: "invalid_input"},
		{name: "idempotency key that is not a UUID", user: &environment.admin, release: environment.releaseID, key: "not-a-uuid", body: valid, status: 400, code: "invalid_input"},
		{name: "body that is not an object", user: &environment.admin, release: environment.releaseID, key: uuid.New(), body: "not an object", status: 400, code: "invalid_json"},
		{name: "expected revision zero", user: &environment.admin, release: environment.releaseID, key: uuid.New(), body: correctionBody(0, map[string]any{"genre": setChange([]string{"爵士"})}), status: 400, code: "invalid_input"},
		{name: "unknown field", user: &environment.admin, release: environment.releaseID, key: uuid.New(), body: correctionBody(1, map[string]any{"source_type": setChange("CD")}), status: 400, code: "invalid_correction"},
		{name: "title set to empty", user: &environment.admin, release: environment.releaseID, key: uuid.New(), body: correctionBody(1, map[string]any{"title": nullChange}), status: 400, code: "invalid_correction"},
		{name: "album artist set to empty", user: &environment.admin, release: environment.releaseID, key: uuid.New(), body: correctionBody(1, map[string]any{"album_artist": nullChange}), status: 400, code: "invalid_correction"},
		{name: "blank text", user: &environment.admin, release: environment.releaseID, key: uuid.New(), body: correctionBody(1, map[string]any{"genre": setChange([]string{"   "})}), status: 400, code: "invalid_correction"},
		{name: "year zero", user: &environment.admin, release: environment.releaseID, key: uuid.New(), body: correctionBody(1, map[string]any{"year": setChange(0)}), status: 400, code: "invalid_correction"},
		{name: "year as text", user: &environment.admin, release: environment.releaseID, key: uuid.New(), body: correctionBody(1, map[string]any{"year": setChange("1990")}), status: 400, code: "invalid_correction"},
		{name: "one invalid change among valid ones", user: &environment.admin, release: environment.releaseID, key: uuid.New(), body: correctionBody(1, map[string]any{"genre": setChange([]string{"爵士"}), "year": setChange(10000)}), status: 400, code: "invalid_correction"},
		{name: "no changes at all", user: &environment.admin, release: environment.releaseID, key: uuid.New(), body: correctionBody(1, map[string]any{}), status: 400, code: "no_changes"},
		{name: "clear without a correction", user: &environment.admin, release: environment.releaseID, key: uuid.New(), body: correctionBody(1, map[string]any{"genre": clearChange}), status: 400, code: "no_changes"},
		{name: "release without visible tracks", user: &environment.admin, release: environment.hiddenID, key: uuid.New(), body: valid, status: 404, code: "not_found"},
		{name: "unknown release", user: &environment.admin, release: uuid.New(), key: uuid.New(), body: valid, status: 404, code: "not_found"},
		{name: "stale revision", user: &environment.admin, release: environment.releaseID, key: uuid.New(), body: correctionBody(7, map[string]any{"genre": setChange([]string{"爵士"})}), status: 409, code: "revision_conflict"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			response := environment.patch(t, testCase.user, testCase.release, testCase.key, testCase.body, testCase.headers)
			requireAPIError(t, response, testCase.status, testCase.code)
		})
	}

	// GET：未登录 401、普通成员 403、标识无效 400、不存在或没有可见曲目 404。
	get := func(user *integrationTestUser, releaseID string) *httptest.ResponseRecorder {
		return application.request(t, http.MethodGet, "/api/v1/releases/"+releaseID+"/corrections", nil, user, nil)
	}
	requireAPIError(t, get(nil, environment.releaseID), 401, "unauthorized")
	requireAPIError(t, get(&environment.ordinary, environment.releaseID), 403, "forbidden")
	requireAPIError(t, get(&environment.admin, "not-a-uuid"), 400, "invalid_id")
	requireAPIError(t, get(&environment.admin, environment.hiddenID), 404, "not_found")
	requireAPIError(t, get(&environment.admin, uuid.New()), 404, "not_found")

	// 所有被拒绝的请求都没有任何副作用。
	if operations, corrections, revision := environment.counts(t); operations != 0 || corrections != 0 || revision != 1 {
		t.Fatalf("rejected requests left operations %d, corrections %d, revision %d; want 0, 0, 1", operations, corrections, revision)
	}
}

func TestPostgreSQLCorrectionsAPIStateAndSuccess(t *testing.T) {
	environment := newCorrectionsEnvironment(t)
	application := environment.application
	mustExec(t, application, `INSERT INTO release_field_decisions(release_id,field_key,selected_value,selected_source,confidence,action,rule_id) VALUES($1::uuid,'title',to_jsonb('原始标题'::text),'tag','low','uncertain_apply','majority_v1')`, environment.releaseID)

	var state struct {
		ReleaseID string `json:"release_id"`
		Revision  int64  `json:"revision"`
		Fields    []struct {
			Field          string         `json:"field"`
			ScanValue      any            `json:"scan_value"`
			Correction     map[string]any `json:"correction"`
			EffectiveValue any            `json:"effective_value"`
			Uncertain      bool           `json:"uncertain"`
		} `json:"fields"`
		OperationID string `json:"operation_id"`
	}
	getJSON(t, application, &environment.admin, "/api/v1/releases/"+environment.releaseID+"/corrections", &state)
	names := make([]string, 0, len(state.Fields))
	for _, field := range state.Fields {
		names = append(names, field.Field)
	}
	if state.ReleaseID != environment.releaseID || state.Revision != 1 || !reflect.DeepEqual(names, []string{"title", "album_artist", "year", "genre", "label", "catalog_number", "edition", "barcode"}) {
		t.Fatalf("GET corrections = release %s revision %d fields %v", state.ReleaseID, state.Revision, names)
	}
	if title := state.Fields[0]; title.ScanValue != "原始标题" || title.EffectiveValue != "原始标题" || title.Correction != nil || !title.Uncertain {
		t.Errorf("title = %+v; want the scanned value, no correction and the uncertain flag", title)
	}
	if albumArtist := state.Fields[1]; albumArtist.ScanValue != "原始艺人" || albumArtist.Uncertain {
		t.Errorf("album artist = %+v; want the display artist as the scanned value", albumArtist)
	}

	// 设值、设空：成功响应是新的状态加 operation_id。
	key := uuid.New()
	response := environment.patch(t, &environment.admin, environment.releaseID, key, correctionBody(1, map[string]any{
		"title": setChange("  修正后的标题 "), "year": setChange(1975), "genre": nullChange,
	}))
	if response.Code != http.StatusOK {
		t.Fatalf("correct release: HTTP %d: %s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatalf("decode correction result: %v", err)
	}
	if state.Revision != 2 || state.OperationID == "" || state.Fields[0].EffectiveValue != "修正后的标题" || state.Fields[0].Correction["mode"] != "set" ||
		state.Fields[2].EffectiveValue != float64(1975) || state.Fields[3].EffectiveValue != nil || state.Fields[3].Correction["mode"] != "null" || !reflect.DeepEqual(state.Fields[3].ScanValue, []any{"古典"}) {
		t.Fatalf("correction result = %s", response.Body.String())
	}
	var recordedActor, recordedType, recordedTarget string
	var recordedExpected, recordedResult int64
	if err := application.database.QueryRow(`SELECT actor_id::text, operation_type, target_id::text, expected_revision, result_revision FROM operations WHERE id=$1::uuid`, state.OperationID).
		Scan(&recordedActor, &recordedType, &recordedTarget, &recordedExpected, &recordedResult); err != nil {
		t.Fatalf("read operation record: %v", err)
	}
	if recordedActor != environment.admin.id || recordedType != "release_correction" || recordedTarget != environment.releaseID || recordedExpected != 1 || recordedResult != 2 {
		t.Fatalf("operation record = %s %s %s %d→%d", recordedActor, recordedType, recordedTarget, recordedExpected, recordedResult)
	}

	// 详情（所有登录成员可读）：有效值、版本号与有修正的字段名，不含修正值来源与操作者。
	for name, user := range map[string]*integrationTestUser{"admin": &environment.admin, "ordinary member": &environment.ordinary} {
		detail := application.request(t, http.MethodGet, "/api/v1/releases/"+environment.releaseID, nil, user, nil)
		if detail.Code != http.StatusOK {
			t.Fatalf("%s detail: HTTP %d: %s", name, detail.Code, detail.Body.String())
		}
		decoded := decodeJSONMap(t, detail)
		if decoded["title"] != "修正后的标题" || decoded["year"] != float64(1975) || !reflect.DeepEqual(decoded["genres"], []any{}) || decoded["label"] != "原始厂牌" || decoded["revision"] != float64(2) ||
			!reflect.DeepEqual(decoded["corrected_fields"], []any{"title", "year", "genre"}) {
			t.Errorf("%s detail = title %v year %v genres %v label %v revision %v corrected %v", name, decoded["title"], decoded["year"], decoded["genres"], decoded["label"], decoded["revision"], decoded["corrected_fields"])
		}
		for key := range decoded {
			if strings.Contains(key, "operation") || strings.Contains(key, "actor") {
				t.Errorf("%s detail exposes %q", name, key)
			}
		}
	}
	// 没有修正的发行：空数组而不是 null。
	if other := decodeJSONMap(t, application.request(t, http.MethodGet, "/api/v1/releases/"+environment.releaseID, nil, &environment.ordinary, nil)); other["corrected_fields"] == nil {
		t.Error("corrected_fields must be an array")
	}
	var list releaseListResponse
	getJSON(t, application, &environment.ordinary, "/api/v1/releases?q="+"修正后", &list)
	if list.Pagination.Total != 1 || list.Items[0].Title != "修正后的标题" {
		t.Fatalf("search for the corrected title = %+v", list)
	}
	// 修正过的字段不再计入需要关注（title 的不确定被覆盖）。
	getJSON(t, application, &environment.ordinary, "/api/v1/releases?attention=required", &list)
	if list.Pagination.Total != 0 {
		t.Fatalf("a release whose only uncertain field was corrected is still listed for attention: %+v", list)
	}
}

func TestPostgreSQLCorrectionsAPIIdempotencyAndConflicts(t *testing.T) {
	environment := newCorrectionsEnvironment(t)
	application := environment.application
	otherReleaseID, _ := insertGroupedRelease(t, application, environment.rootID, groupedRelease{title: "另一张", artist: "艺人", tracks: []string{"present"}})
	body := correctionBody(1, map[string]any{"label": setChange("新厂牌")})
	key := uuid.New()

	first := environment.patch(t, &environment.admin, environment.releaseID, key, body)
	if first.Code != http.StatusOK {
		t.Fatalf("first request: HTTP %d: %s", first.Code, first.Body.String())
	}
	// 同键同义：回放同一份响应，不再执行。
	replay := environment.patch(t, &environment.admin, environment.releaseID, strings.ToUpper(key), body)
	if replay.Code != http.StatusOK || !jsonResponsesEqual(t, replay, first) {
		t.Fatalf("replay: HTTP %d: %s; want the recorded response %s", replay.Code, replay.Body.String(), first.Body.String())
	}
	if operations, _, revision := environment.counts(t); operations != 1 || revision != 2 {
		t.Fatalf("after a replay: operations %d, revision %d; want 1, 2", operations, revision)
	}
	// 同键异义：不同内容、不同目标、不同预期版本都是冲突。
	for name, conflicting := range map[string]struct {
		release string
		body    any
	}{
		"different value":    {environment.releaseID, correctionBody(1, map[string]any{"label": setChange("别的厂牌")})},
		"different field":    {environment.releaseID, correctionBody(1, map[string]any{"edition": setChange("再版")})},
		"different semantic": {environment.releaseID, correctionBody(1, map[string]any{"label": nullChange})},
		"different revision": {environment.releaseID, correctionBody(2, map[string]any{"label": setChange("新厂牌")})},
		"different release":  {otherReleaseID, body},
	} {
		if response := environment.patch(t, &environment.admin, conflicting.release, key, conflicting.body); response.Code != http.StatusConflict ||
			decodeJSONMap(t, response)["error"].(map[string]any)["code"] != "idempotency_conflict" {
			t.Errorf("%s: HTTP %d: %s; want 409 idempotency_conflict", name, response.Code, response.Body.String())
		}
	}
	if operations, corrections, revision := environment.counts(t); operations != 1 || corrections != 1 || revision != 2 {
		t.Fatalf("after conflicting reuse: operations %d, corrections %d, revision %d; want 1, 1, 2", operations, corrections, revision)
	}

	// 同样的请求换一个键：预期版本已过期，409 revision_conflict，不是 no_changes。
	requireAPIError(t, environment.patch(t, &environment.admin, environment.releaseID, uuid.New(), body), 409, "revision_conflict")
	// 键的作用域是操作者：另一个管理员可以用同一个键做自己的操作。
	secondAdmin := application.createUser(t, "admin")
	second := environment.patch(t, &secondAdmin, environment.releaseID, key, correctionBody(2, map[string]any{"edition": setChange("再版")}))
	if second.Code != http.StatusOK {
		t.Fatalf("another administrator reusing the key: HTTP %d: %s", second.Code, second.Body.String())
	}
	if operations, _, revision := environment.counts(t); operations != 2 || revision != 3 {
		t.Fatalf("after the second administrator: operations %d, revision %d; want 2, 3", operations, revision)
	}

	// 记录写入失败：整体回滚（修正、版本号与记录都不变），响应不含内部细节。
	installRejectingInsertTrigger(t, application, "operations", "TRUE")
	failed := environment.patch(t, &environment.admin, environment.releaseID, uuid.New(), correctionBody(3, map[string]any{"barcode": setChange("4988000000000")}))
	requireAPIError(t, failed, 500, "operation_failed")
	if strings.Contains(failed.Body.String(), "test rejection") || strings.Contains(failed.Body.String(), "operations") {
		t.Fatalf("the failure response leaks internals: %s", failed.Body.String())
	}
	if operations, corrections, revision := environment.counts(t); operations != 2 || corrections != 2 || revision != 3 {
		t.Fatalf("after a failed record write: operations %d, corrections %d, revision %d; want 2, 2, 3", operations, corrections, revision)
	}
	mustExec(t, application, `DROP TRIGGER reject_operations_insert ON operations`)
	if retry := environment.patch(t, &environment.admin, environment.releaseID, uuid.New(), correctionBody(3, map[string]any{"barcode": setChange("4988000000000")})); retry.Code != http.StatusOK {
		t.Fatalf("retry after the trigger was removed: HTTP %d: %s", retry.Code, retry.Body.String())
	}
}

// 同一个键的并发请求只执行一次；同一个预期版本、不同键的并发请求只有一个成功，其余是版本冲突。
func TestPostgreSQLCorrectionsAPIConcurrentRequests(t *testing.T) {
	environment := newCorrectionsEnvironment(t)
	application := environment.application

	runOverlapping := func(send func(index int) *httptest.ResponseRecorder) []*httptest.ResponseRecorder {
		t.Helper()
		holder, err := application.database.Begin()
		if err != nil {
			t.Fatalf("begin lock holder: %v", err)
		}
		t.Cleanup(func() { _ = holder.Rollback() })
		// 第一个请求在读取修正表时被挡住，其余的在键锁或发行行锁上排队，所以请求一定真正重叠。
		if _, err := holder.Exec(`LOCK TABLE release_corrections IN ACCESS EXCLUSIVE MODE`); err != nil {
			t.Fatalf("lock release_corrections: %v", err)
		}
		responses := make(chan *httptest.ResponseRecorder, overlappingRootRequests)
		for index := 0; index < overlappingRootRequests; index++ {
			go func() { responses <- send(index) }()
		}
		waitForLockWaiters(t, application.database, overlappingRootRequests)
		if err := holder.Rollback(); err != nil {
			t.Fatalf("release lock holder: %v", err)
		}
		results := make([]*httptest.ResponseRecorder, 0, overlappingRootRequests)
		timeout := time.After(15 * time.Second)
		for len(results) < overlappingRootRequests {
			select {
			case response := <-responses:
				results = append(results, response)
			case <-timeout:
				t.Fatalf("only %d of %d overlapping requests finished", len(results), overlappingRootRequests)
			}
		}
		return results
	}

	// 同键同义。
	key := uuid.New()
	same := runOverlapping(func(int) *httptest.ResponseRecorder {
		return environment.patch(t, &environment.admin, environment.releaseID, key, correctionBody(1, map[string]any{"label": setChange("并发厂牌")}))
	})
	operationIDs := map[string]bool{}
	for _, response := range same {
		if response.Code != http.StatusOK {
			t.Fatalf("an overlapping request with the same key: HTTP %d: %s", response.Code, response.Body.String())
		}
		operationIDs[decodeJSONMap(t, response)["operation_id"].(string)] = true
	}
	if operations, corrections, revision := environment.counts(t); len(operationIDs) != 1 || operations != 1 || corrections != 1 || revision != 2 {
		t.Fatalf("same-key overlap: %d distinct operations in responses, operations %d, corrections %d, revision %d; want exactly one execution", len(operationIDs), operations, corrections, revision)
	}

	// 同一个预期版本、不同的键与内容。
	different := runOverlapping(func(index int) *httptest.ResponseRecorder {
		return environment.patch(t, &environment.admin, environment.releaseID, uuid.New(), correctionBody(2, map[string]any{"edition": setChange(fmt.Sprintf("版本 %d", index))}))
	})
	succeeded, conflicted := 0, 0
	winner := ""
	for _, response := range different {
		switch response.Code {
		case http.StatusOK:
			succeeded++
			state := decodeJSONMap(t, response)
			for _, field := range state["fields"].([]any) {
				if item := field.(map[string]any); item["field"] == "edition" {
					winner = item["effective_value"].(string)
				}
			}
		case http.StatusConflict:
			conflicted++
			requireAPIError(t, response, 409, "revision_conflict")
		default:
			t.Fatalf("unexpected response: HTTP %d: %s", response.Code, response.Body.String())
		}
	}
	var stored string
	if err := application.database.QueryRow(`SELECT text_value FROM release_corrections WHERE release_id=$1::uuid AND field='edition'`, environment.releaseID).Scan(&stored); err != nil {
		t.Fatalf("read the stored edition: %v", err)
	}
	if operations, _, revision := environment.counts(t); succeeded != 1 || conflicted != overlappingRootRequests-1 || operations != 2 || revision != 3 || stored != winner {
		t.Fatalf("different-key overlap: %d succeeded, %d conflicted, operations %d, revision %d, stored %q, winner %q; want one winner and conflicts for the rest", succeeded, conflicted, operations, revision, stored, winner)
	}
}

// 合成素材的真实扫描：修正在各处显示同一个有效值，重扫之后仍然生效，扫描值与证据照常更新，清除修正后显示最新的扫描值。
func TestPostgreSQLCorrectionsSurviveARealRescanAndShowEverywhere(t *testing.T) {
	application := newIntegrationTestApplication(t)
	admin := application.createUser(t, "admin")
	library := createLibraryDirectory(t, application, "classical")
	zuluFile := filepath.Join(library, "zulu", "01.flac")
	writeSyntheticTrack(t, zuluFile, 1, "TITLE=Zulu Prelude", "ARTIST=Zulu Pianist", "ALBUM=Zulu Album", "ALBUMARTIST=Zulu Pianist", "DATE=2000", "GENRE=Classical", "LABEL=Zulu Label", "TRACKNUMBER=1")
	writeSyntheticTrack(t, filepath.Join(library, "beta", "01.flac"), 2, "TITLE=Beta Prelude", "ARTIST=Beta Pianist", "ALBUM=Beta Album", "ALBUMARTIST=Beta Pianist", "DATE=2001", "TRACKNUMBER=1")
	if response := application.request(t, http.MethodPost, "/api/v1/library-roots", map[string]string{"path": library}, &admin, idempotencyHeader("create-classical")); response.Code != http.StatusCreated {
		t.Fatalf("register the library: HTTP %d: %s", response.Code, response.Body.String())
	}
	scan := func() { runScan(t, application, &admin) }
	scan()

	releaseIDOf := func(title string) (releaseID, groupID string) {
		t.Helper()
		if err := application.database.QueryRow(`SELECT id::text, group_id::text FROM releases WHERE title=$1`, title).Scan(&releaseID, &groupID); err != nil {
			t.Fatalf("read release %q: %v", title, err)
		}
		return releaseID, groupID
	}
	zuluID, zuluGroup := releaseIDOf("Zulu Album")
	var zuluTrack string
	if err := application.database.QueryRow(`SELECT tracks.id::text FROM tracks JOIN media ON media.id=tracks.medium_id WHERE media.release_id=$1::uuid`, zuluID).Scan(&zuluTrack); err != nil {
		t.Fatalf("read the track: %v", err)
	}
	detailOf := func() map[string]any {
		t.Helper()
		response := application.request(t, http.MethodGet, "/api/v1/releases/"+zuluID, nil, &admin, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("release detail: HTTP %d: %s", response.Code, response.Body.String())
		}
		return decodeJSONMap(t, response)
	}
	revision := int(detailOf()["revision"].(float64))
	patch := func(changes map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		response := application.request(t, http.MethodPatch, "/api/v1/releases/"+zuluID+"/corrections", correctionBody(revision, changes), &admin, idempotencyHeader(uuid.New()))
		if response.Code != http.StatusOK {
			t.Fatalf("correct release: HTTP %d: %s", response.Code, response.Body.String())
		}
		revision = int(decodeJSONMap(t, response)["revision"].(float64))
		return response
	}
	patch(map[string]any{"title": setChange("Alpha Album (corrected)"), "album_artist": setChange("Corrected Pianist"), "year": nullChange, "genre": nullChange})

	// 各处读取显示同一个有效值：列表（排序）、搜索、按组读取、详情与署名、最近播放。
	var list releaseListResponse
	getJSON(t, application, &admin, "/api/v1/releases?page_size=10", &list)
	if list.Pagination.Total != 2 || list.Items[0].ID != zuluID || list.Items[0].Title != "Alpha Album (corrected)" || list.Items[0].Artist != "Corrected Pianist" || list.Items[0].Year != nil || list.Items[1].Title != "Beta Album" {
		t.Fatalf("release list = %+v; want the corrected release first (it sorts before Beta Album now)", list)
	}
	for search, wantTotal := range map[string]int64{"Zulu Album": 0, "Alpha Album": 1, "Corrected Pianist": 1, "Zulu Pianist": 0} {
		getJSON(t, application, &admin, "/api/v1/releases?q="+strings.ReplaceAll(search, " ", "%20"), &list)
		if list.Pagination.Total != wantTotal {
			t.Errorf("search %q: total %d, want %d", search, list.Pagination.Total, wantTotal)
		}
	}
	var groups releaseGroupListResponse
	getJSON(t, application, &admin, "/api/v1/release-groups?page_size=10", &groups)
	if groups.Pagination.Total != 2 || groups.Items[0].Representative.Title != "Alpha Album (corrected)" || groups.Items[0].ID != zuluGroup {
		t.Fatalf("release groups = %+v", groups)
	}
	detail := detailOf()
	if detail["title"] != "Alpha Album (corrected)" || detail["year"] != nil || !reflect.DeepEqual(detail["genres"], []any{}) || detail["label"] != "Zulu Label" ||
		!reflect.DeepEqual(detail["corrected_fields"], []any{"title", "album_artist", "year", "genre"}) {
		t.Fatalf("detail = title %v year %v genres %v label %v corrected %v", detail["title"], detail["year"], detail["genres"], detail["label"], detail["corrected_fields"])
	}
	if credits, _ := detail["credits"].([]any); len(credits) != 1 || credits[0].(map[string]any)["name"] != "Corrected Pianist" {
		t.Fatalf("credits = %v; want the corrected album artist", detail["credits"])
	}
	requirePlayRecorded(t, postPlay(t, application, &admin, uuid.New(), zuluTrack, 150.0))
	if recent := recentReleasesOf(t, application, &admin); len(recent.Items) != 1 || recent.Items[0].Release.Title != "Alpha Album (corrected)" {
		t.Fatalf("recent plays = %+v; want the corrected title", recent)
	}
	revisionBeforeRescan := revision

	// 重扫：磁盘上的标签变了（音频内容不变，所以曲目与发行延续）。扫描值与证据更新，修正不变，没有修正的字段读到新的扫描值，
	// 人工修改版本号不被扫描改动。
	writeSyntheticTrack(t, zuluFile, 1, "TITLE=Zulu Prelude", "ARTIST=Zulu Pianist", "ALBUM=Zulu Album Remastered", "ALBUMARTIST=Zulu Pianist Revised", "DATE=2010", "GENRE=Baroque", "LABEL=New Zulu Label", "TRACKNUMBER=1")
	scan()
	if rescannedID, _ := releaseIDOf("Zulu Album Remastered"); rescannedID != zuluID {
		t.Fatalf("the rescanned release has ID %s, want the same release %s", rescannedID, zuluID)
	}
	detail = detailOf()
	if detail["title"] != "Alpha Album (corrected)" || detail["album_artist"] != "Corrected Pianist" || detail["year"] != nil || !reflect.DeepEqual(detail["genres"], []any{}) ||
		detail["label"] != "New Zulu Label" || detail["revision"] != float64(revisionBeforeRescan) {
		t.Fatalf("after the rescan: title %v, album artist %v, year %v, genres %v, label %v, revision %v; want the corrections intact, the label rescanned and the revision untouched",
			detail["title"], detail["album_artist"], detail["year"], detail["genres"], detail["label"], detail["revision"])
	}
	var evidence struct {
		Fields []struct {
			Field string  `json:"field"`
			Value *string `json:"value"`
		} `json:"fields"`
	}
	getJSON(t, application, &admin, "/api/v1/releases/"+zuluID+"/evidence", &evidence)
	scannedTitle := ""
	for _, field := range evidence.Fields {
		if field.Field == "title" && field.Value != nil {
			scannedTitle = *field.Value
		}
	}
	if scannedTitle != "Zulu Album Remastered" {
		t.Fatalf("the evidence reports the scanned title %q, want it updated by the rescan", scannedTitle)
	}
	var state struct {
		Fields []struct {
			Field          string         `json:"field"`
			ScanValue      any            `json:"scan_value"`
			Correction     map[string]any `json:"correction"`
			EffectiveValue any            `json:"effective_value"`
		} `json:"fields"`
	}
	getJSON(t, application, &admin, "/api/v1/releases/"+zuluID+"/corrections", &state)
	seen := map[string]bool{}
	for _, field := range state.Fields {
		seen[field.Field] = true
		switch field.Field {
		case "title":
			if field.ScanValue != "Zulu Album Remastered" || field.EffectiveValue != "Alpha Album (corrected)" || field.Correction == nil {
				t.Errorf("title after the rescan = %+v", field)
			}
		case "year":
			if field.ScanValue != float64(2010) || field.EffectiveValue != nil || field.Correction["mode"] != "null" {
				t.Errorf("year after the rescan = %+v", field)
			}
		case "label":
			if field.ScanValue != "New Zulu Label" || field.Correction != nil {
				t.Errorf("label after the rescan = %+v", field)
			}
		}
	}
	if !seen["title"] || !seen["year"] || !seen["label"] {
		t.Fatalf("corrections state is missing fields: %v", seen)
	}

	// 清除修正：显示最新的扫描值（重扫之后的值）。
	patch(map[string]any{"title": clearChange, "album_artist": clearChange, "year": clearChange, "genre": clearChange})
	detail = detailOf()
	if detail["title"] != "Zulu Album Remastered" || detail["album_artist"] != "Zulu Pianist Revised" || detail["year"] != float64(2010) || !reflect.DeepEqual(detail["genres"], []any{"Baroque"}) ||
		!reflect.DeepEqual(detail["corrected_fields"], []any{}) {
		t.Fatalf("after clearing: title %v, album artist %v, year %v, genres %v, corrected %v; want the latest scanned values", detail["title"], detail["album_artist"], detail["year"], detail["genres"], detail["corrected_fields"])
	}
}
