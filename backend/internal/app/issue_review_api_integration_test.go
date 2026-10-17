package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func issueReviewPath(releaseID string) string {
	return "/api/v1/releases/" + releaseID + "/issue-review"
}

func reviewBody(revision int64, issue, basis string) map[string]any {
	return map[string]any{"expected_revision": revision, "issue": issue, "action": "review", "basis": basis}
}

func unreviewBody(revision int64, issue string) map[string]any {
	return map[string]any{"expected_revision": revision, "issue": issue, "action": "unreview"}
}

// issueEnvironment 在 groupEnvironment 的基础上给 A 加上可以核对的问题：同一目录的归组冲突与一首缺失曲目。
type issueEnvironment struct {
	*groupEnvironment
	store *catalog.Store
}

func newIssueEnvironment(t *testing.T) *issueEnvironment {
	t.Helper()
	environment := &issueEnvironment{groupEnvironment: newGroupEnvironment(t)}
	environment.store = catalog.NewStore(environment.application.database)
	mustExec(t, environment.application, `INSERT INTO release_grouping_evidence(release_id,candidate_kind,rule_id,source_refs,reason) VALUES($1::uuid,'same_dir_split','organizer_v2','["a/01.flac","a/02.flac"]','same_directory_conflict')`, environment.a)
	mustExec(t, environment.application, `INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind)
		SELECT $2::uuid, media.id, 9, 'Missing', 'Artist A', 1, $3::uuid, 'a/09.flac', 'missing', NOW(), 'physical' FROM media WHERE media.release_id=$1::uuid`, environment.a, uuid.New(), environment.rootID)
	return environment
}

func (environment *issueEnvironment) token(t *testing.T, releaseID, issue string) string {
	t.Helper()
	token, err := environment.store.LoadIssueBasisToken(context.Background(), releaseID, issue)
	if err != nil {
		t.Fatalf("load the %s basis token: %v", issue, err)
	}
	return token
}

func (environment *issueEnvironment) postReview(t *testing.T, user *integrationTestUser, releaseID, key string, body any, extraHeaders ...map[string]string) *httptest.ResponseRecorder {
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
	return environment.application.request(t, http.MethodPost, issueReviewPath(releaseID), body, user, headers)
}

func (environment *issueEnvironment) attention(t *testing.T, releaseID string) int64 {
	t.Helper()
	var detail struct {
		AttentionCount int64 `json:"attention_count"`
	}
	getJSON(t, environment.application, &environment.ordinary, "/api/v1/releases/"+releaseID, &detail)
	return detail.AttentionCount
}

func TestPostgreSQLIssueReviewAPIRejectsUnauthorizedAndInvalidRequestsWithoutSideEffects(t *testing.T) {
	environment := newIssueEnvironment(t)
	valid := reviewBody(1, "grouping", environment.token(t, environment.a, "grouping"))

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
		{name: "body that is not an object", user: &environment.admin, release: environment.a, key: uuid.New(), body: "review", status: 400, code: "invalid_json"},
		{name: "expected revision zero", user: &environment.admin, release: environment.a, key: uuid.New(), body: reviewBody(0, "grouping", "x"), status: 400, code: "invalid_input"},
		{name: "a field that cannot be reviewed", user: &environment.admin, release: environment.a, key: uuid.New(), body: reviewBody(1, "title", "x"), status: 400, code: "invalid_input"},
		{name: "missing issue", user: &environment.admin, release: environment.a, key: uuid.New(), body: map[string]any{"expected_revision": 1, "action": "review", "basis": "x"}, status: 400, code: "invalid_input"},
		{name: "unknown action", user: &environment.admin, release: environment.a, key: uuid.New(), body: map[string]any{"expected_revision": 1, "issue": "grouping", "action": "ignore"}, status: 400, code: "invalid_input"},
		{name: "review without a basis", user: &environment.admin, release: environment.a, key: uuid.New(), body: map[string]any{"expected_revision": 1, "issue": "grouping", "action": "review"}, status: 400, code: "invalid_input"},
		{name: "review with an empty basis", user: &environment.admin, release: environment.a, key: uuid.New(), body: reviewBody(1, "grouping", ""), status: 400, code: "invalid_input"},
		{name: "unreview with a basis", user: &environment.admin, release: environment.a, key: uuid.New(), body: map[string]any{"expected_revision": 1, "issue": "grouping", "action": "unreview", "basis": "x"}, status: 400, code: "invalid_input"},
		{name: "release without visible tracks", user: &environment.admin, release: environment.hidden, key: uuid.New(), body: valid, status: 404, code: "not_found"},
		{name: "unknown release", user: &environment.admin, release: uuid.New(), key: uuid.New(), body: valid, status: 404, code: "not_found"},
		{name: "stale revision", user: &environment.admin, release: environment.a, key: uuid.New(), body: reviewBody(9, "grouping", environment.token(t, environment.a, "grouping")), status: 409, code: "revision_conflict"},
		{name: "a basis the page did not see", user: &environment.admin, release: environment.a, key: uuid.New(), body: reviewBody(1, "grouping", "0000"), status: 409, code: "issue_changed"},
		{name: "an issue the release does not have", user: &environment.admin, release: environment.b, key: uuid.New(), body: reviewBody(1, "grouping", "0000"), status: 409, code: "issue_changed"},
		{name: "nothing to unreview", user: &environment.admin, release: environment.a, key: uuid.New(), body: unreviewBody(1, "grouping"), status: 400, code: "no_changes"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			requireAPIError(t, environment.postReview(t, testCase.user, testCase.release, testCase.key, testCase.body, testCase.headers), testCase.status, testCase.code)
		})
	}

	// 所有被拒绝的请求都没有任何副作用：版本号不变、没有核对、没有操作记录。
	_, revision, operations := environment.state(t, environment.a)
	reviews := countRows(t, environment.application, `SELECT COUNT(*) FROM release_issue_reviews`)
	if revision != 1 || operations != 0 || reviews != 0 {
		t.Fatalf("rejected requests left revision %d, %d operations, %d reviews; want 1, 0, 0", revision, operations, reviews)
	}
}

// 核对、取消核对与它们的撤销经路由与鉴权：“需要关注”随之变化；同键同义重放返回同一份响应，同键异义 409；
// 操作记录列表与按发行筛选里有这类记录，改动摘要正确。
func TestPostgreSQLIssueReviewAPIReviewUnreviewRevertAndRecords(t *testing.T) {
	environment := newIssueEnvironment(t)
	application := environment.application
	// A 有 1 个拿不准的字段、1 个归组冲突与 1 首缺失曲目。
	if got := environment.attention(t, environment.a); got != 3 {
		t.Fatalf("attention before any review = %d, want 3", got)
	}

	key := uuid.New()
	body := reviewBody(1, "grouping", environment.token(t, environment.a, "grouping"))
	reviewed := environment.postReview(t, &environment.admin, environment.a, key, body)
	if reviewed.Code != http.StatusOK {
		t.Fatalf("review: HTTP %d: %s", reviewed.Code, reviewed.Body.String())
	}
	result := decodeJSONMap(t, reviewed)
	if result["release_id"] != environment.a || result["issue"] != "grouping" || result["reviewed"] != true || result["revision"] != float64(2) || result["operation_id"] == "" {
		t.Fatalf("review result = %v", result)
	}
	reviewOperation := result["operation_id"].(string)
	if got := environment.attention(t, environment.a); got != 2 {
		t.Fatalf("attention after reviewing the grouping conflict = %d, want 2", got)
	}
	// 同键同义重放：同一份响应，不再执行；同键异义：409。
	if replay := environment.postReview(t, &environment.admin, environment.a, key, body); replay.Code != http.StatusOK || decodeJSONMap(t, replay)["operation_id"] != reviewOperation {
		t.Fatalf("replay: HTTP %d: %s", replay.Code, replay.Body.String())
	}
	requireAPIError(t, environment.postReview(t, &environment.admin, environment.a, key, unreviewBody(2, "grouping")), 409, "idempotency_conflict")
	if _, revision, operations := environment.state(t, environment.a); revision != 2 || operations != 1 {
		t.Fatalf("after the replay and the conflicting key: revision %d, %d operations; want 2 and 1", revision, operations)
	}

	missing := environment.postReview(t, &environment.admin, environment.a, uuid.New(), reviewBody(2, "missing_tracks", environment.token(t, environment.a, "missing_tracks")))
	if missing.Code != http.StatusOK {
		t.Fatalf("review the missing tracks: HTTP %d: %s", missing.Code, missing.Body.String())
	}
	unreviewed := environment.postReview(t, &environment.admin, environment.a, uuid.New(), unreviewBody(3, "grouping"))
	if unreviewed.Code != http.StatusOK || decodeJSONMap(t, unreviewed)["reviewed"] != false {
		t.Fatalf("unreview: HTTP %d: %s", unreviewed.Code, unreviewed.Body.String())
	}
	if got := environment.attention(t, environment.a); got != 2 {
		t.Fatalf("attention after unreviewing the grouping conflict = %d, want 2 (the field and the grouping conflict)", got)
	}
	// 撤销取消核对：恢复原来的核对；之后又有修改时更早的记录不能撤销。
	unreviewOperation := decodeJSONMap(t, unreviewed)["operation_id"].(string)
	requireAPIError(t, environment.application.request(t, http.MethodPost, revertPath(reviewOperation), nil, &environment.admin, idempotencyHeader(uuid.New())), 409, "revert_conflict")
	revert := environment.application.request(t, http.MethodPost, revertPath(unreviewOperation), nil, &environment.admin, idempotencyHeader(uuid.New()))
	if revert.Code != http.StatusOK {
		t.Fatalf("revert the unreview: HTTP %d: %s", revert.Code, revert.Body.String())
	}
	if response := decodeJSONMap(t, revert); response["target_type"] != "release" || response["target_id"] != environment.a || response["revision"] != float64(5) {
		t.Fatalf("revert response = %v", response)
	}
	if got := environment.attention(t, environment.a); got != 1 {
		t.Fatalf("attention after reverting the unreview = %d, want 1 (only the field)", got)
	}
	var operationID string
	if err := application.database.QueryRow(`SELECT operation_id::text FROM release_issue_reviews WHERE release_id=$1::uuid AND issue='grouping'`, environment.a).Scan(&operationID); err != nil || operationID != reviewOperation {
		t.Fatalf("the restored grouping review points at %q (%v), want the original review %s", operationID, err, reviewOperation)
	}

	// 记录列表：按发行筛选，最新的在前；改动摘要写明问题与前后是否有核对。最新的是撤销记录（不能再撤销），其余都已不是最近一次修改。
	var listing struct {
		Items []struct {
			ID      string `json:"id"`
			Type    string `json:"type"`
			Changes struct {
				Kind  string `json:"kind"`
				Issue string `json:"issue"`
				From  struct {
					Reviewed   bool `json:"reviewed"`
					TrackCount *int `json:"track_count"`
				} `json:"from"`
				To struct {
					Reviewed   bool `json:"reviewed"`
					TrackCount *int `json:"track_count"`
				} `json:"to"`
			} `json:"changes"`
			Revertible bool `json:"revertible"`
		} `json:"items"`
	}
	getJSON(t, application, &environment.admin, "/api/v1/operations?release_id="+environment.a, &listing)
	if len(listing.Items) != 4 {
		t.Fatalf("records for A = %d, want 4", len(listing.Items))
	}
	latest, missingRecord := listing.Items[0], listing.Items[2]
	if latest.Type != "revert" || latest.Changes.Kind != "release_issue_review" || latest.Changes.Issue != "grouping" || latest.Changes.From.Reviewed || !latest.Changes.To.Reviewed || latest.Revertible {
		t.Fatalf("the revert record = %+v", latest)
	}
	if missingRecord.Type != "release_issue_review" || missingRecord.Changes.Issue != "missing_tracks" || missingRecord.Changes.To.TrackCount == nil || *missingRecord.Changes.To.TrackCount != 1 || missingRecord.Revertible {
		t.Fatalf("the missing-tracks record = %+v", missingRecord)
	}
	// 普通成员看不到记录。
	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/operations?release_id="+environment.a, nil, &environment.ordinary, nil), 403, "forbidden")
}

// 同一发行上的并发核对：同一个版本号只有一个成功，其余 409，都不是 500。
func TestPostgreSQLIssueReviewAPIConcurrentRequests(t *testing.T) {
	environment := newIssueEnvironment(t)
	grouping, missing := environment.token(t, environment.a, "grouping"), environment.token(t, environment.a, "missing_tracks")
	var waitGroup sync.WaitGroup
	responses := make([]*httptest.ResponseRecorder, 8)
	for index := range responses {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			body := reviewBody(1, "grouping", grouping)
			if index%2 == 1 {
				body = reviewBody(1, "missing_tracks", missing)
			}
			responses[index] = environment.postReview(t, &environment.admin, environment.a, uuid.New(), body)
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
			t.Fatalf("concurrent review: HTTP %d: %s", response.Code, response.Body.String())
		}
	}
	if _, revision, operations := environment.state(t, environment.a); succeeded != 1 || revision != 2 || operations != 1 {
		t.Fatalf("%d concurrent reviews succeeded (revision %d, %d operations); want exactly one", succeeded, revision, operations)
	}
}

// 合成素材的真实重扫：同一目录两种专辑标签造出归组冲突、MEDIA 标签不一致、删除文件造出缺失曲目；核对之后原样重扫保持，
// 依据变了重新计入（来源文件变了、介质候选变了、曲目回来后再次缺失）；扫描不改核对行与操作记录。
func TestPostgreSQLIssueReviewsSurviveARealRescan(t *testing.T) {
	application := newIntegrationTestApplication(t)
	admin := application.createUser(t, "admin")
	library := createLibraryDirectory(t, application, "classical")
	// 冲突目录：两种专辑标签各两首，没有过半数，扫描按标签拆成两个发行。
	conflict := filepath.Join(library, "conflict")
	writeSyntheticTrack(t, filepath.Join(conflict, "01.flac"), 1, "TITLE=One", "ARTIST=Pianist", "ALBUM=Album One", "TRACKNUMBER=1")
	writeSyntheticTrack(t, filepath.Join(conflict, "02.flac"), 2, "TITLE=Two", "ARTIST=Pianist", "ALBUM=Album One", "TRACKNUMBER=2")
	writeSyntheticTrack(t, filepath.Join(conflict, "03.flac"), 8, "TITLE=Three", "ARTIST=Pianist", "ALBUM=Album Two", "TRACKNUMBER=3")
	writeSyntheticTrack(t, filepath.Join(conflict, "04.flac"), 9, "TITLE=Four", "ARTIST=Pianist", "ALBUM=Album Two", "TRACKNUMBER=4")
	media := filepath.Join(library, "media")
	writeSyntheticTrack(t, filepath.Join(media, "01.flac"), 3, "TITLE=First", "ARTIST=Cellist", "ALBUM=Suites", "MEDIA=CD", "TRACKNUMBER=1")
	writeSyntheticTrack(t, filepath.Join(media, "02.flac"), 4, "TITLE=Second", "ARTIST=Cellist", "ALBUM=Suites", "MEDIA=Vinyl", "TRACKNUMBER=2")
	writeSyntheticTrack(t, filepath.Join(media, "03.flac"), 5, "TITLE=Third", "ARTIST=Cellist", "ALBUM=Suites", "MEDIA=CD", "TRACKNUMBER=3")
	if response := application.request(t, http.MethodPost, "/api/v1/library-roots", map[string]string{"path": library}, &admin, idempotencyHeader("create-classical")); response.Code != http.StatusCreated {
		t.Fatalf("register the library: HTTP %d: %s", response.Code, response.Body.String())
	}
	runScan(t, application, &admin)
	// 删除一个文件，完整扫描把它标为缺失。
	if err := os.Remove(filepath.Join(media, "03.flac")); err != nil {
		t.Fatalf("remove a track: %v", err)
	}
	runScan(t, application, &admin)

	store := catalog.NewStore(application.database)
	// idOf 返回这个标题唯一的可见发行（重扫可能留下同名但不可见的旧发行）。
	idOf := func(title string) string {
		t.Helper()
		var releaseID string
		if err := application.database.QueryRow(`SELECT id::text FROM releases WHERE title=$1 AND EXISTS (
			SELECT 1 FROM media JOIN tracks ON tracks.medium_id=media.id WHERE media.release_id=releases.id AND tracks.source_status='present')`, title).Scan(&releaseID); err != nil {
			t.Fatalf("read the visible release %q: %v", title, err)
		}
		return releaseID
	}
	albumOne, suites := idOf("Album One"), idOf("Suites")
	revisionOf := func(releaseID string) int64 {
		t.Helper()
		return int64(decodeJSONMap(t, application.request(t, http.MethodGet, "/api/v1/releases/"+releaseID, nil, &admin, nil))["revision"].(float64))
	}
	attentionOf := func(releaseID string) int64 {
		t.Helper()
		return int64(decodeJSONMap(t, application.request(t, http.MethodGet, "/api/v1/releases/"+releaseID, nil, &admin, nil))["attention_count"].(float64))
	}
	review := func(releaseID, issue string) {
		t.Helper()
		var detail catalog.ReleaseIssues
		getJSON(t, application, &admin, "/api/v1/releases/"+releaseID+"/issues", &detail)
		token := ""
		switch issue {
		case "grouping":
			token = detail.Grouping.Basis
		case "missing_tracks":
			token = detail.MissingTracks.Basis
		default:
			for _, field := range detail.Fields {
				if field.Field == issue && field.Basis != nil {
					token = *field.Basis
				}
			}
		}
		if stored, err := store.LoadIssueBasisToken(context.Background(), releaseID, issue); err != nil || token == "" || token != stored {
			t.Fatalf("the %s issue of %s: token from the detail %q, stored %q (%v)", issue, releaseID, token, stored, err)
		}
		if response := application.request(t, http.MethodPost, issueReviewPath(releaseID), reviewBody(revisionOf(releaseID), issue, token), &admin, idempotencyHeader(uuid.New())); response.Code != http.StatusOK {
			t.Fatalf("review %s: HTTP %d: %s", issue, response.Code, response.Body.String())
		}
	}
	// Album One：归组冲突（两种专辑标签各一首）。Suites：介质类型拿不准、一首缺失曲目。
	beforeOne, beforeSuites := attentionOf(albumOne), attentionOf(suites)
	review(albumOne, "grouping")
	review(suites, "media_type")
	review(suites, "missing_tracks")
	if attentionOf(albumOne) != beforeOne-1 || attentionOf(suites) != beforeSuites-2 {
		t.Fatalf("attention after the reviews: %d (was %d), %d (was %d)", attentionOf(albumOne), beforeOne, attentionOf(suites), beforeSuites)
	}
	reviewsBefore := countRows(t, application, `SELECT COUNT(*) FROM release_issue_reviews`)
	operationsBefore := countRows(t, application, `SELECT COUNT(*) FROM operations`)

	// 原样重扫：核对保持，扫描不改核对行与操作记录。
	runScan(t, application, &admin)
	if attentionOf(albumOne) != beforeOne-1 || attentionOf(suites) != beforeSuites-2 {
		t.Fatalf("after an unchanged rescan: %d, %d; want %d, %d", attentionOf(albumOne), attentionOf(suites), beforeOne-1, beforeSuites-2)
	}
	if countRows(t, application, `SELECT COUNT(*) FROM release_issue_reviews`) != reviewsBefore || countRows(t, application, `SELECT COUNT(*) FROM operations`) != operationsBefore {
		t.Fatal("an unchanged rescan changed the reviews or the operation records")
	}

	// 改动之后重扫：冲突目录两种专辑标签各多了一首（仍然没有过半数，冲突还在；已有曲目仍占多数，发行延续），
	// 但 Album One 的来源文件变了；介质候选从 Vinyl 变成 SACD；缺失的曲目回来后再次缺失。
	writeSyntheticTrack(t, filepath.Join(conflict, "05.flac"), 6, "TITLE=Five", "ARTIST=Pianist", "ALBUM=Album One", "TRACKNUMBER=5")
	writeSyntheticTrack(t, filepath.Join(conflict, "06.flac"), 7, "TITLE=Six", "ARTIST=Pianist", "ALBUM=Album Two", "TRACKNUMBER=6")
	writeSyntheticTrack(t, filepath.Join(media, "02.flac"), 4, "TITLE=Second", "ARTIST=Cellist", "ALBUM=Suites", "MEDIA=SACD", "TRACKNUMBER=2")
	writeSyntheticTrack(t, filepath.Join(media, "03.flac"), 5, "TITLE=Third", "ARTIST=Cellist", "ALBUM=Suites", "MEDIA=Vinyl", "TRACKNUMBER=3")
	runScan(t, application, &admin)
	if err := os.Remove(filepath.Join(media, "03.flac")); err != nil {
		t.Fatalf("remove the track again: %v", err)
	}
	runScan(t, application, &admin)
	if got := idOf("Album One"); got != albumOne {
		t.Fatalf("Album One became a new release %s; the test needs the same release", got)
	}
	if got := idOf("Suites"); got != suites {
		t.Fatalf("Suites became a new release %s; the test needs the same release", got)
	}
	if attentionOf(albumOne) != beforeOne || attentionOf(suites) != beforeSuites {
		t.Fatalf("after the basis changed: %d, %d; want everything counted again: %d, %d", attentionOf(albumOne), attentionOf(suites), beforeOne, beforeSuites)
	}
	// 详情说明“核对之后情况变了”：三类问题都回到待处理，核对行还在。
	var stale catalog.ReleaseIssues
	getJSON(t, application, &admin, "/api/v1/releases/"+suites+"/issues", &stale)
	for _, field := range stale.Fields {
		if field.Field == "media_type" && (field.Status != "open" || field.Review == nil) {
			t.Fatalf("the media type after its candidates changed = %+v", field)
		}
	}
	if stale.MissingTracks == nil || stale.MissingTracks.Status != "open" || stale.MissingTracks.Review == nil {
		t.Fatalf("the missing tracks after the track went missing again = %+v", stale.MissingTracks)
	}
	getJSON(t, application, &admin, "/api/v1/releases/"+albumOne+"/issues", &stale)
	if stale.Grouping == nil || stale.Grouping.Status != "open" || stale.Grouping.Review == nil {
		t.Fatalf("the grouping conflict after its files changed = %+v", stale.Grouping)
	}
	if countRows(t, application, `SELECT COUNT(*) FROM release_issue_reviews`) != reviewsBefore || countRows(t, application, `SELECT COUNT(*) FROM operations`) != operationsBefore {
		t.Fatal("a rescan deleted the stale reviews or changed the operation records")
	}
}

// 读取接口：只有管理员；筛选与分页的 400；版本的问题带依据令牌、核对人的用户名；与核对接口用同一个令牌。
func TestPostgreSQLIssueReadAPI(t *testing.T) {
	environment := newIssueEnvironment(t)
	application := environment.application
	for _, testCase := range []struct {
		name   string
		user   *integrationTestUser
		path   string
		status int
		code   string
	}{
		{"list without a session", nil, "/api/v1/release-issues", 401, "unauthorized"},
		{"list as an ordinary member", &environment.ordinary, "/api/v1/release-issues", 403, "forbidden"},
		{"detail without a session", nil, "/api/v1/releases/" + environment.a + "/issues", 401, "unauthorized"},
		{"detail as an ordinary member", &environment.ordinary, "/api/v1/releases/" + environment.a + "/issues", 403, "forbidden"},
		{"unknown status", &environment.admin, "/api/v1/release-issues?status=ignored", 400, "invalid_filter"},
		{"empty status", &environment.admin, "/api/v1/release-issues?status=", 400, "invalid_filter"},
		{"two statuses", &environment.admin, "/api/v1/release-issues?status=open&status=reviewed", 400, "invalid_filter"},
		{"unknown category", &environment.admin, "/api/v1/release-issues?category=artwork", 400, "invalid_filter"},
		{"page zero", &environment.admin, "/api/v1/release-issues?page=0", 400, "invalid_pagination"},
		{"page size over the limit", &environment.admin, "/api/v1/release-issues?page_size=101", 400, "invalid_pagination"},
		{"malformed release id", &environment.admin, "/api/v1/releases/not-a-uuid/issues", 400, "invalid_id"},
		{"release without visible tracks", &environment.admin, "/api/v1/releases/" + environment.hidden + "/issues", 404, "not_found"},
		{"unknown release", &environment.admin, "/api/v1/releases/" + uuid.New() + "/issues", 404, "not_found"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			requireAPIError(t, application.request(t, http.MethodGet, testCase.path, nil, testCase.user, nil), testCase.status, testCase.code)
		})
	}

	// 列表：A（1 个字段、1 个归组、1 首缺失）与 B（2 个字段），各类之和等于 attention_count。
	var listing struct {
		Items []struct {
			Release struct {
				ID             string `json:"id"`
				AttentionCount int64  `json:"attention_count"`
			} `json:"release"`
			Open catalog.IssueCounts `json:"open"`
		} `json:"items"`
		Summary    catalog.IssueSummary `json:"summary"`
		Pagination struct {
			Total int64 `json:"total"`
		} `json:"pagination"`
	}
	getJSON(t, application, &environment.admin, "/api/v1/release-issues", &listing)
	if len(listing.Items) != 2 || listing.Items[0].Release.ID != environment.a || listing.Items[1].Release.ID != environment.b || listing.Pagination.Total != 2 {
		t.Fatalf("issue list = %+v", listing)
	}
	for _, item := range listing.Items {
		if item.Open.Fields+item.Open.Grouping+item.Open.MissingTracks != item.Release.AttentionCount {
			t.Fatalf("%s: open %+v does not sum to attention_count %d", item.Release.ID, item.Open, item.Release.AttentionCount)
		}
	}
	if listing.Summary.Open != (catalog.IssueSummaryCounts{All: 2, Fields: 2, Grouping: 1, MissingTracks: 1}) {
		t.Fatalf("summary = %+v", listing.Summary)
	}

	// 详情的令牌就是核对要带回的令牌；核对之后带上核对人的用户名。
	var detail catalog.ReleaseIssues
	getJSON(t, application, &environment.admin, "/api/v1/releases/"+environment.a+"/issues", &detail)
	if detail.Grouping == nil || detail.Grouping.Basis != environment.token(t, environment.a, "grouping") || detail.MissingTracks == nil || detail.MissingTracks.Count != 1 {
		t.Fatalf("detail = %+v", detail)
	}
	if response := environment.postReview(t, &environment.admin, environment.a, uuid.New(), reviewBody(detail.Revision, "grouping", detail.Grouping.Basis)); response.Code != http.StatusOK {
		t.Fatalf("review with the token from the detail: HTTP %d: %s", response.Code, response.Body.String())
	}
	getJSON(t, application, &environment.admin, "/api/v1/releases/"+environment.a+"/issues", &detail)
	var username string
	if err := application.database.QueryRow(`SELECT username FROM users WHERE id=$1::uuid`, environment.admin.id).Scan(&username); err != nil {
		t.Fatalf("read the admin's username: %v", err)
	}
	if detail.Grouping.Status != "reviewed" || detail.Grouping.Review == nil || detail.Grouping.Review.Reviewer.ID != environment.admin.id || detail.Grouping.Review.Reviewer.Username != username {
		t.Fatalf("grouping after the review = %+v (review %+v)", detail.Grouping, detail.Grouping.Review)
	}
}
