package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
	"github.com/qlqq/roomusic/backend/internal/listening"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

const (
	playsPath         = "/api/v1/me/plays"
	recentReleasesURL = "/api/v1/me/plays/recent-releases"
)

func idempotencyKeyHeader(key string) map[string]string {
	return map[string]string{"Idempotency-Key": key}
}

// postPlay 带着幂等键发一次记录请求；seconds 与 trackID 直接放进 JSON，所以测试可以传入任何类型。
func postPlay(t *testing.T, application *integrationTestApplication, user *integrationTestUser, key string, trackID, seconds any) *httptest.ResponseRecorder {
	t.Helper()
	return application.request(t, http.MethodPost, playsPath, map[string]any{"track_id": trackID, "played_seconds": seconds}, user, idempotencyKeyHeader(key))
}

func requirePlayRecorded(t *testing.T, response *httptest.ResponseRecorder) listening.Recorded {
	t.Helper()
	if response.Code != http.StatusCreated {
		t.Fatalf("record a play: HTTP %d: %s", response.Code, response.Body.String())
	}
	var recorded listening.Recorded
	if err := json.Unmarshal(response.Body.Bytes(), &recorded); err != nil {
		t.Fatalf("decode the recorded play: %v: %s", err, response.Body.String())
	}
	return recorded
}

func recentReleasesOf(t *testing.T, application *integrationTestApplication, user *integrationTestUser) listening.RecentReleases {
	t.Helper()
	response := application.request(t, http.MethodGet, recentReleasesURL, nil, user, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("recent releases: HTTP %d: %s", response.Code, response.Body.String())
	}
	var recent listening.RecentReleases
	if err := json.Unmarshal(response.Body.Bytes(), &recent); err != nil {
		t.Fatalf("decode recent releases: %v: %s", err, response.Body.String())
	}
	return recent
}

func recentTitles(recent listening.RecentReleases) []string {
	titles := make([]string, 0, len(recent.Items))
	for _, item := range recent.Items {
		titles = append(titles, item.Release.Title)
	}
	return titles
}

func requireTitles(t *testing.T, recent listening.RecentReleases, want ...string) {
	t.Helper()
	got := recentTitles(recent)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("recent releases = %v, want %v", got, want)
	}
}

func TestPostgreSQLListeningRequestsAreCheckedInOrder(t *testing.T) {
	application := newIntegrationTestApplication(t)
	library := createPlaybackLibrary(t, application)
	trackID := library.addTrack(t, application, playbackTrackSpec{relative: "a.flac", content: flacBytes(4096)})
	missingID := library.addTrack(t, application, playbackTrackSpec{relative: "gone.flac", content: flacBytes(4096)})
	mustExec(t, application, `UPDATE tracks SET duration_seconds=200 WHERE id=$1::uuid`, trackID) // 门槛 100 秒，服务端接受 ≥ 99 秒
	mustExec(t, application, `UPDATE tracks SET source_status='missing' WHERE id=$1::uuid`, missingID)
	member := application.createUser(t, "user")
	rows := func() int { return countRows(t, application, `SELECT COUNT(*) FROM play_history`) }

	// 写请求先看来源：跨源或缺少 Origin 即使没有会话也是 403，什么都不会写。
	for name, origin := range map[string]string{"cross-origin": "http://untrusted.example", "missing Origin": ""} {
		for _, user := range []*integrationTestUser{nil, &member} {
			headers := idempotencyKeyHeader(uuid.New())
			headers["Origin"] = origin
			response := application.request(t, http.MethodPost, playsPath, map[string]any{"track_id": trackID, "played_seconds": 120}, user, headers)
			if response.Code != http.StatusForbidden {
				t.Fatalf("%s POST (session %v): HTTP %d, want 403: %s", name, user != nil, response.Code, response.Body.String())
			}
			response = application.request(t, http.MethodDelete, playsPath, nil, user, map[string]string{"Origin": origin})
			if response.Code != http.StatusForbidden {
				t.Fatalf("%s DELETE (session %v): HTTP %d, want 403: %s", name, user != nil, response.Code, response.Body.String())
			}
		}
	}
	if rows() != 0 {
		t.Fatalf("a rejected request wrote %d rows", rows())
	}

	// 同源但没有会话：三个路由都是 401，响应里没有任何数据。
	requireAPIError(t, postPlay(t, application, nil, uuid.New(), trackID, 120), http.StatusUnauthorized, "unauthorized")
	requireAPIError(t, application.request(t, http.MethodGet, recentReleasesURL, nil, nil, nil), http.StatusUnauthorized, "unauthorized")
	requireAPIError(t, application.request(t, http.MethodDelete, playsPath, nil, nil, nil), http.StatusUnauthorized, "unauthorized")

	// 登录后的检查：幂等键 → JSON → 字段 → 曲目 → 门槛。
	requireAPIError(t, application.request(t, http.MethodPost, playsPath, map[string]any{"track_id": trackID, "played_seconds": 120}, &member, nil), http.StatusBadRequest, "invalid_input")
	requireAPIError(t, postPlay(t, application, &member, "not-a-uuid", trackID, 120), http.StatusBadRequest, "invalid_input")
	rawPost := func(raw string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, playsPath, strings.NewReader(raw))
		request.Host = "roomusic.test"
		request.Header.Set("Origin", application.origin)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", uuid.New())
		request.AddCookie(&http.Cookie{Name: "roomusic_session", Value: member.token})
		response := httptest.NewRecorder()
		application.handler.ServeHTTP(response, request)
		return response
	}
	for name, raw := range map[string]string{
		"broken JSON":                  `{"track_id":`,
		"empty body":                   ``,
		"seconds is a string":          `{"track_id":"` + trackID + `","played_seconds":"120"}`,
		"seconds out of float64 range": `{"track_id":"` + trackID + `","played_seconds":1e999}`,
		"track is a number":            `{"track_id":7,"played_seconds":120}`,
	} {
		t.Run(name, func(t *testing.T) {
			response := rawPost(raw)
			requireAPIError(t, response, http.StatusBadRequest, "invalid_json")
			// 一个响应只有一个 JSON 文档（DecodeJSON 失败后不再写第二个信封）。
			decoder := json.NewDecoder(bytes.NewReader(response.Body.Bytes()))
			var envelope map[string]any
			if err := decoder.Decode(&envelope); err != nil || decoder.More() {
				t.Fatalf("the response is not a single JSON document: %s", response.Body.String())
			}
		})
	}
	for name, raw := range map[string]string{
		"no fields":       `{}`,
		"no seconds":      `{"track_id":"` + trackID + `"}`,
		"no track":        `{"played_seconds":60}`,
		"null track":      `{"track_id":null,"played_seconds":60}`,
		"null seconds":    `{"track_id":"` + trackID + `","played_seconds":null}`,
		"track not UUID":  `{"track_id":"nope","played_seconds":60}`,
		"zero seconds":    `{"track_id":"` + trackID + `","played_seconds":0}`,
		"negative":        `{"track_id":"` + trackID + `","played_seconds":-5}`,
		"more than a day": `{"track_id":"` + trackID + `","played_seconds":86401}`,
	} {
		t.Run(name, func(t *testing.T) { requireAPIError(t, rawPost(raw), http.StatusBadRequest, "invalid_input") })
	}
	requireAPIError(t, postPlay(t, application, &member, uuid.New(), uuid.New(), 120), http.StatusNotFound, "not_found")
	requireAPIError(t, postPlay(t, application, &member, uuid.New(), missingID, 120), http.StatusNotFound, "not_found")
	requireAPIError(t, postPlay(t, application, &member, uuid.New(), trackID, 98), http.StatusBadRequest, "play_too_short")
	if rows() != 0 {
		t.Fatalf("rejected plays wrote %d rows", rows())
	}

	// 达到门槛减去 1 秒容差：201，响应只有 id、track_id、played_at。
	key := uuid.New()
	response := postPlay(t, application, &member, key, trackID, 99)
	recorded := requirePlayRecorded(t, response)
	if response.Header().Get("Cache-Control") != "no-store" || !strings.HasPrefix(response.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("record response headers: Cache-Control %q, Content-Type %q", response.Header().Get("Cache-Control"), response.Header().Get("Content-Type"))
	}
	var fields map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &fields); err != nil || len(fields) != 3 || fields["track_id"] != trackID || recorded.ID == "" {
		t.Fatalf("record response = %s, want exactly id, track_id and played_at", response.Body.String())
	}
	if _, err := time.Parse(time.RFC3339Nano, fields["played_at"].(string)); err != nil || !strings.HasSuffix(fields["played_at"].(string), "Z") {
		t.Fatalf("played_at %v is not a UTC RFC 3339 time: %v", fields["played_at"], err)
	}
	requireNoPathLeak(t, application, response)

	// 同键同请求：同样的 201 与同一响应，不写新行；同键不同请求：409。
	replay := postPlay(t, application, &member, key, trackID, 99)
	if replay.Code != http.StatusCreated || replay.Body.String() != response.Body.String() {
		t.Fatalf("replay: HTTP %d: %s, want the first response %s", replay.Code, replay.Body.String(), response.Body.String())
	}
	requireAPIError(t, postPlay(t, application, &member, key, trackID, 150), http.StatusConflict, "idempotency_conflict")
	requireAPIError(t, postPlay(t, application, &member, key, library.addTrack(t, application, playbackTrackSpec{relative: "b.flac", content: flacBytes(4096)}), 99), http.StatusConflict, "idempotency_conflict")
	if rows() != 1 {
		t.Fatalf("rows after replays and conflicts = %d, want 1", rows())
	}

	// 会话撤销后，三个路由立即是 401。
	mustExec(t, application, `UPDATE sessions SET revoked_at=NOW() WHERE user_id=$1::uuid`, member.id)
	requireAPIError(t, postPlay(t, application, &member, uuid.New(), trackID, 120), http.StatusUnauthorized, "unauthorized")
	requireAPIError(t, application.request(t, http.MethodGet, recentReleasesURL, nil, &member, nil), http.StatusUnauthorized, "unauthorized")
	requireAPIError(t, application.request(t, http.MethodDelete, playsPath, nil, &member, nil), http.StatusUnauthorized, "unauthorized")
	if rows() != 1 {
		t.Fatalf("rows after the session was revoked = %d, want 1", rows())
	}
}

func TestPostgreSQLListeningHistoryIsPrivateEvenFromAdministrators(t *testing.T) {
	application := newIntegrationTestApplication(t)
	library := createPlaybackLibrary(t, application)
	addTrack := func(relative, title string) string {
		trackID := library.addTrack(t, application, playbackTrackSpec{relative: relative, content: flacBytes(4096)})
		mustExec(t, application, `UPDATE releases SET title=$1 WHERE id=(SELECT media.release_id FROM tracks JOIN media ON media.id=tracks.medium_id WHERE tracks.id=$2::uuid)`, title, trackID)
		return trackID
	}
	memberTrack, otherTrack, adminTrack := addTrack("member.flac", "成员听的专辑"), addTrack("other.flac", "另一位成员听的专辑"), addTrack("admin.flac", "管理员听的专辑")
	admin, member, other := application.createUser(t, "admin"), application.createUser(t, "user"), application.createUser(t, "user")

	// 同一个幂等键在不同用户之间互不干扰：各写各的。
	sharedKey := uuid.New()
	requirePlayRecorded(t, postPlay(t, application, &member, sharedKey, memberTrack, 60))
	requirePlayRecorded(t, postPlay(t, application, &other, sharedKey, otherTrack, 60))
	requirePlayRecorded(t, postPlay(t, application, &admin, sharedKey, adminTrack, 60))
	if rows := countRows(t, application, `SELECT COUNT(*) FROM play_history`); rows != 3 {
		t.Fatalf("rows = %d, want 3", rows)
	}

	// 每个人（含管理员）只看到自己的记录。
	requireTitles(t, recentReleasesOf(t, application, &member), "成员听的专辑")
	requireTitles(t, recentReleasesOf(t, application, &other), "另一位成员听的专辑")
	requireTitles(t, recentReleasesOf(t, application, &admin), "管理员听的专辑")

	// 管理员清除只影响自己；成员清除也一样；再清除得到 0。
	cleared := application.request(t, http.MethodDelete, playsPath, nil, &admin, nil)
	if cleared.Code != http.StatusOK || strings.TrimSpace(cleared.Body.String()) != `{"deleted":1}` {
		t.Fatalf("admin clear: HTTP %d: %s", cleared.Code, cleared.Body.String())
	}
	if recent := recentReleasesOf(t, application, &admin); recent.HasHistory || len(recent.Items) != 0 {
		t.Fatalf("admin history after clearing = %+v", recent)
	}
	requireTitles(t, recentReleasesOf(t, application, &member), "成员听的专辑")
	requireTitles(t, recentReleasesOf(t, application, &other), "另一位成员听的专辑")
	if response := application.request(t, http.MethodDelete, playsPath, nil, &admin, nil); strings.TrimSpace(response.Body.String()) != `{"deleted":0}` {
		t.Fatalf("second clear: %s, want zero deleted", response.Body.String())
	}
	if response := application.request(t, http.MethodDelete, playsPath, nil, &member, nil); strings.TrimSpace(response.Body.String()) != `{"deleted":1}` {
		t.Fatalf("member clear: %s, want one deleted", response.Body.String())
	}
	requireTitles(t, recentReleasesOf(t, application, &other), "另一位成员听的专辑")

	// 路径与请求体里没有用户 ID：带上别人的 user_id 不会改变作用对象（未知字段被忽略，记录属于会话用户）。
	response := application.request(t, http.MethodPost, playsPath, map[string]any{"track_id": memberTrack, "played_seconds": 60, "user_id": other.id}, &member, idempotencyKeyHeader(uuid.New()))
	requirePlayRecorded(t, response)
	if rows := countRows(t, application, `SELECT COUNT(*) FROM play_history WHERE user_id=$1::uuid`, member.id); rows != 1 {
		t.Fatalf("rows for the member = %d, want 1", rows)
	}
	if rows := countRows(t, application, `SELECT COUNT(*) FROM play_history WHERE user_id=$1::uuid AND track_id=$2::uuid`, other.id, memberTrack); rows != 0 {
		t.Fatalf("a request body user_id wrote %d rows for another user", rows)
	}
}

func TestPostgreSQLListeningResponseShapesAndDisabledMembers(t *testing.T) {
	application := newIntegrationTestApplication(t)
	library := createPlaybackLibrary(t, application)
	trackID := library.addTrack(t, application, playbackTrackSpec{relative: "a.flac", content: flacBytes(4096)})
	member := application.createUser(t, "user")

	// 没有任何记录：空数组（不是 null）与 has_history=false。
	empty := application.request(t, http.MethodGet, recentReleasesURL, nil, &member, nil)
	if empty.Code != http.StatusOK || strings.TrimSpace(empty.Body.String()) != `{"items":[],"has_history":false}` {
		t.Fatalf("empty history: HTTP %d: %s", empty.Code, empty.Body.String())
	}
	if empty.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("recent releases Cache-Control = %q, want no-store", empty.Header().Get("Cache-Control"))
	}

	requirePlayRecorded(t, postPlay(t, application, &member, uuid.New(), trackID, 30))
	response := application.request(t, http.MethodGet, recentReleasesURL, nil, &member, nil)
	var raw struct {
		Items []map[string]any `json:"items"`
		Has   bool             `json:"has_history"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil || !raw.Has || len(raw.Items) != 1 {
		t.Fatalf("recent releases = %s (error %v)", response.Body.String(), err)
	}
	item := raw.Items[0]
	if len(item) != 4 || item["group_id"] == nil || item["release"] == nil || item["last_played_at"] == nil || item["last_track"] == nil {
		t.Fatalf("recent item fields = %v, want exactly group_id, release, last_played_at and last_track", item)
	}
	if release := item["release"].(map[string]any); release["artwork"] != nil || release["title"] != "专辑" || release["track_count"] != float64(1) {
		t.Fatalf("recent item release = %v", release)
	}
	if lastTrack := item["last_track"].(map[string]any); len(lastTrack) != 2 || lastTrack["id"] != trackID || lastTrack["title"] != "曲目" {
		t.Fatalf("recent item last_track = %v", lastTrack)
	}
	requireNoPathLeak(t, application, response)

	// 成员被停用后无法访问，但记录保留；重新启用后本人又能看到。
	mustExec(t, application, `UPDATE users SET disabled_at=NOW() WHERE id=$1::uuid`, member.id)
	requireAPIError(t, application.request(t, http.MethodGet, recentReleasesURL, nil, &member, nil), http.StatusUnauthorized, "unauthorized")
	requireAPIError(t, postPlay(t, application, &member, uuid.New(), trackID, 30), http.StatusUnauthorized, "unauthorized")
	if rows := countRows(t, application, `SELECT COUNT(*) FROM play_history WHERE user_id=$1::uuid`, member.id); rows != 1 {
		t.Fatalf("rows of a disabled member = %d, want 1 (kept)", rows)
	}
	mustExec(t, application, `UPDATE users SET disabled_at=NULL WHERE id=$1::uuid`, member.id)
	requireTitles(t, recentReleasesOf(t, application, &member), "专辑")
}

// scanTheLibrary 经管理员接口启动一次扫描并等它完整成功。
func scanTheLibrary(t *testing.T, application *integrationTestApplication, admin *integrationTestUser) {
	t.Helper()
	started := application.request(t, http.MethodPost, "/api/v1/scans", map[string]any{}, admin, nil)
	if started.Code != http.StatusAccepted {
		t.Fatalf("start a scan: HTTP %d: %s", started.Code, started.Body.String())
	}
	if status := waitForScanToFinish(t, application, decodeJSONMap(t, started)["id"].(string)); status != "succeeded" {
		t.Fatalf("scan finished %q, want succeeded", status)
	}
}

// 合成 FLAC（240 秒）经真实扫描入库后，播放记录跟着曲目身份走：目录改名、跨目录移动、文件消失与回来，
// 记录都指向同一首曲目，“最近播放”显示它当前所在的专辑，缺失的曲目不显示但记录仍在。
func TestPostgreSQLListeningFollowsTracksThroughRescans(t *testing.T) {
	application := newIntegrationTestApplication(t)
	admin, member := application.createUser(t, "admin"), application.createUser(t, "user")
	root := createLibraryDirectory(t, application, "listening-library")
	if created := application.request(t, http.MethodPost, "/api/v1/library-roots", map[string]string{"path": root}, &admin, idempotencyKeyHeader("listening-root")); created.Code != http.StatusCreated {
		t.Fatalf("register the library root: HTTP %d: %s", created.Code, created.Body.String())
	}
	write := func(relative string, seed byte, album, title string, number int) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create %s: %v", filepath.Dir(path), err)
		}
		content := parsertest.FLACWithAudio(t, [16]byte{seed}, bytes.Repeat([]byte{seed}, 64),
			"TITLE="+title, "ARTIST=Sample Artist", "ALBUM="+album, "TRACKNUMBER="+string(rune('0'+number)))
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatalf("write %s: %v", relative, err)
		}
	}
	move := func(from, to string) {
		t.Helper()
		target := filepath.Join(root, filepath.FromSlash(to))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatalf("create %s: %v", filepath.Dir(target), err)
		}
		if err := os.Rename(filepath.Join(root, filepath.FromSlash(from)), target); err != nil {
			t.Fatalf("move %s: %v", from, err)
		}
	}
	// Alpha 有三首，目录里还有一首属于 Gamma 的曲目（杂音，随多数留在 Alpha 的发行里）；Gamma 两首；Beta 一首。
	for number := 1; number <= 3; number++ {
		write("Alpha/0"+string(rune('0'+number))+".flac", byte(number), "Alpha", "Alpha "+string(rune('0'+number)), number)
	}
	write("Alpha/stray.flac", 20, "Gamma", "Gamma Stray", 3)
	write("Gamma/01.flac", 31, "Gamma", "Gamma 1", 1)
	write("Gamma/02.flac", 32, "Gamma", "Gamma 2", 2)
	write("Beta/01.flac", 40, "Beta", "Beta 1", 1)
	scanTheLibrary(t, application, &admin)

	trackAt := func(relative string) string {
		t.Helper()
		var id string
		if err := application.database.QueryRow(`SELECT id::text FROM tracks WHERE relative_path=$1`, relative).Scan(&id); err != nil {
			t.Fatalf("read the track at %q: %v", relative, err)
		}
		return id
	}
	releaseOf := func(title string) (releaseID, groupID string) {
		t.Helper()
		if err := application.database.QueryRow(`SELECT id::text,group_id::text FROM releases WHERE title=$1`, title).Scan(&releaseID, &groupID); err != nil {
			t.Fatalf("read the release %q: %v", title, err)
		}
		return releaseID, groupID
	}
	alpha1, stray, gamma1, beta1 := trackAt("Alpha/01.flac"), trackAt("Alpha/stray.flac"), trackAt("Gamma/01.flac"), trackAt("Beta/01.flac")
	alphaRelease, alphaGroup := releaseOf("Alpha")
	gammaRelease, gammaGroup := releaseOf("Gamma")
	betaRelease, _ := releaseOf("Beta")

	// 扫描得到的时长（240 秒）决定门槛：T = 120，服务端接受 ≥ 119 秒。
	var duration float64
	if err := application.database.QueryRow(`SELECT duration_seconds FROM tracks WHERE id=$1::uuid`, alpha1).Scan(&duration); err != nil || duration != 240 {
		t.Fatalf("scanned duration = %v (error %v), want 240", duration, err)
	}
	requireAPIError(t, postPlay(t, application, &member, uuid.New(), alpha1, 100), http.StatusBadRequest, "play_too_short")
	baseTime := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	for index, track := range []string{beta1, gamma1, alpha1, stray} { // 由旧到新
		requirePlayRecorded(t, postPlay(t, application, &member, uuid.New(), track, 119))
		mustExec(t, application, `UPDATE play_history SET played_at=$3 WHERE user_id=$1::uuid AND track_id=$2::uuid`, member.id, track, baseTime.Add(time.Duration(index)*time.Hour))
	}
	itemOf := func(recent listening.RecentReleases, title string) listening.RecentRelease {
		t.Helper()
		for _, item := range recent.Items {
			if item.Release.Title == title {
				return item
			}
		}
		t.Fatalf("recent releases %v have no %q", recentTitles(recent), title)
		return listening.RecentRelease{}
	}

	// 扫描之后：杂音曲目在 Alpha 的发行里，所以 Alpha 排最前，代表它最近播放的曲目。
	recent := recentReleasesOf(t, application, &member)
	requireTitles(t, recent, "Alpha", "Gamma", "Beta")
	if item := itemOf(recent, "Alpha"); item.Release.ID != alphaRelease || item.GroupID != alphaGroup || item.LastTrack.ID != stray {
		t.Fatalf("Alpha item = %+v, want release %s and the stray track %s", item, alphaRelease, stray)
	}

	// 杂音曲目移到 Gamma 的目录：曲目 ID 不变，记录跟着它显示在 Gamma 的发行里。
	move("Alpha/stray.flac", "Gamma/03.flac")
	scanTheLibrary(t, application, &admin)
	if trackAt("Gamma/03.flac") != stray {
		t.Fatalf("the moved track got a new ID")
	}
	recent = recentReleasesOf(t, application, &member)
	requireTitles(t, recent, "Gamma", "Alpha", "Beta")
	if item := itemOf(recent, "Gamma"); item.Release.ID != gammaRelease || item.GroupID != gammaGroup || item.LastTrack.ID != stray {
		t.Fatalf("Gamma item after the move = %+v, want release %s and the moved track %s", item, gammaRelease, stray)
	}
	if item := itemOf(recent, "Alpha"); item.LastTrack.ID != alpha1 {
		t.Fatalf("Alpha item after the move = %+v, want last track %s", item, alpha1)
	}

	// 目录改名：曲目与发行 ID 都不变，最近播放仍显示同一批专辑。
	move("Alpha", "Alpha Renamed")
	scanTheLibrary(t, application, &admin)
	if trackAt("Alpha Renamed/01.flac") != alpha1 {
		t.Fatalf("the track in the renamed directory got a new ID")
	}
	recent = recentReleasesOf(t, application, &member)
	requireTitles(t, recent, "Gamma", "Alpha", "Beta")
	if itemOf(recent, "Alpha").Release.ID != alphaRelease || itemOf(recent, "Beta").Release.ID != betaRelease {
		t.Fatalf("release IDs changed after the rename: %+v", recent.Items)
	}

	// 文件消失（完整扫描把它标为缺失）：Beta 不再显示，但记录仍在库里，仍有历史。
	if err := os.Remove(filepath.Join(root, "Beta", "01.flac")); err != nil {
		t.Fatalf("remove the Beta file: %v", err)
	}
	scanTheLibrary(t, application, &admin)
	recent = recentReleasesOf(t, application, &member)
	requireTitles(t, recent, "Gamma", "Alpha")
	if !recent.HasHistory || countRows(t, application, `SELECT COUNT(*) FROM play_history WHERE user_id=$1::uuid`, member.id) != 4 {
		t.Fatalf("history after a file went missing: %+v", recent)
	}
	requireAPIError(t, postPlay(t, application, &member, uuid.New(), beta1, 119), http.StatusNotFound, "not_found")

	// 文件回来：沿用原曲目 ID，记录照常出现。
	write("Beta/01.flac", 40, "Beta", "Beta 1", 1)
	scanTheLibrary(t, application, &admin)
	if trackAt("Beta/01.flac") != beta1 {
		t.Fatalf("the returned file got a new track ID")
	}
	requireTitles(t, recentReleasesOf(t, application, &member), "Gamma", "Alpha", "Beta")
}
