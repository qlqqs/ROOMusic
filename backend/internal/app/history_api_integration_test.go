package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/listening"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

const historyTracksURL = playsPath + "/tracks"
const historyQueueURL = playsPath + "/queue"

func historyTracksOf(t *testing.T, app *integrationTestApplication, user *integrationTestUser, query string) listening.HistoryTracks {
	t.Helper()
	response := app.request(t, http.MethodGet, historyTracksURL+query, nil, user, nil)
	if response.Code != 200 {
		t.Fatalf("history: HTTP %d: %s", response.Code, response.Body.String())
	}
	var got listening.HistoryTracks
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	requireNoPathLeak(t, app, response)
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("history must not be cached")
	}
	return got
}

func historyQueueOf(t *testing.T, app *integrationTestApplication, user *integrationTestUser) listening.HistoryQueue {
	t.Helper()
	response := app.request(t, http.MethodGet, historyQueueURL, nil, user, nil)
	if response.Code != 200 {
		t.Fatalf("queue: HTTP %d: %s", response.Code, response.Body.String())
	}
	var got listening.HistoryQueue
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	requireNoPathLeak(t, app, response)
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("queue must not be cached")
	}
	return got
}

func TestPostgreSQLHistoryHTTPIsolationAndDelete(t *testing.T) {
	app := newIntegrationTestApplication(t)
	library := createPlaybackLibrary(t, app)
	users := []integrationTestUser{app.createUser(t, "user"), app.createUser(t, "user"), app.createUser(t, "admin")}
	var tracks []string
	for i := range users {
		id := library.addTrack(t, app, playbackTrackSpec{relative: fmt.Sprintf("%d.flac", i), content: flacBytes(4096)})
		tracks = append(tracks, id)
		requirePlayRecorded(t, postPlay(t, app, &users[i], uuid.New(), id, 120))
		requirePlayRecorded(t, postPlay(t, app, &users[i], uuid.New(), id, 120))
	}
	// 用户参数不能改变权限，管理员同样只能看到本人；不能删除另外两人的曲目。
	for i := range users {
		got := historyTracksOf(t, app, &users[i], "?user_id="+users[(i+1)%3].id)
		queue := historyQueueOf(t, app, &users[i])
		if got.Pagination.Total != 1 || len(got.Items) != 1 || got.Items[0].TrackID != tracks[i] || got.Items[0].PlayCount != 2 || queue.Total != 1 || len(queue.Items) != 1 || queue.Items[0].TrackID != tracks[i] {
			t.Fatalf("user %d sees %+v %+v", i, got, queue)
		}
		for j, id := range tracks {
			if j == i {
				continue
			}
			response := app.request(t, http.MethodDelete, historyTracksURL+"/"+id, nil, &users[i], nil)
			if response.Code != 200 || strings.TrimSpace(response.Body.String()) != `{"deleted":0}` {
				t.Fatalf("cross-user delete: %s", response.Body.String())
			}
		}
	}
	// 同一曲目也属于另一人；本人其他曲目和另一人的同曲记录都不能被删。
	requirePlayRecorded(t, postPlay(t, app, &users[1], uuid.New(), tracks[0], 120))
	requirePlayRecorded(t, postPlay(t, app, &users[0], uuid.New(), tracks[1], 120))
	for _, want := range []int{2, 0} {
		response := app.request(t, http.MethodDelete, historyTracksURL+"/"+strings.ToUpper(tracks[0]), nil, &users[0], nil)
		if response.Code != 200 || strings.TrimSpace(response.Body.String()) != fmt.Sprintf(`{"deleted":%d}`, want) {
			t.Fatalf("delete: %d %s", response.Code, response.Body.String())
		}
	}
	if got := historyTracksOf(t, app, &users[0], ""); got.Pagination.Total != 1 || got.Items[0].TrackID != tracks[1] {
		t.Fatalf("remaining history: %+v", got)
	}
	if got := recentReleasesOf(t, app, &users[0]); len(got.Items) != 1 || got.Items[0].LastTrack.ID != tracks[1] {
		t.Fatalf("recent after delete: %+v", got)
	}
	if got := historyQueueOf(t, app, &users[1]); got.Total != 2 {
		t.Fatalf("other history changed: %+v", got)
	}
	if got := historyQueueOf(t, app, &users[2]); got.Total != 1 || got.Items[0].PlayCount != 2 {
		t.Fatalf("admin history changed: %+v", got)
	}
	response := app.request(t, http.MethodDelete, historyTracksURL+"/"+uuid.New(), nil, &users[0], nil)
	if response.Code != 200 || strings.TrimSpace(response.Body.String()) != `{"deleted":0}` {
		t.Fatalf("unknown track must be a no-op: %s", response.Body.String())
	}
}

func TestPostgreSQLHistoryHTTPValidationAndFailures(t *testing.T) {
	app := newIntegrationTestApplication(t)
	member := app.createUser(t, "user")
	deleteURL := historyTracksURL + "/" + uuid.New()
	routes := []struct{ method, path string }{{http.MethodGet, historyTracksURL}, {http.MethodGet, historyQueueURL}, {http.MethodDelete, deleteURL}}
	for _, route := range routes {
		requireAPIError(t, app.request(t, route.method, route.path, nil, nil, nil), 401, "unauthorized")
	}
	for origin, code := range map[string]string{"": "origin_required", "http://untrusted.example": "origin_forbidden"} {
		for _, user := range []*integrationTestUser{nil, &member} {
			requireAPIError(t, app.request(t, http.MethodDelete, historyTracksURL+"/invalid", nil, user, map[string]string{"Origin": origin}), 403, code)
		}
	}
	requireAPIError(t, app.request(t, http.MethodGet, historyTracksURL+"?page=0", nil, nil, nil), 401, "unauthorized")
	requireAPIError(t, app.request(t, http.MethodDelete, historyTracksURL+"/invalid", nil, nil, nil), 401, "unauthorized")
	for _, query := range []string{"?page=0", "?page=-1", "?page=abc", "?page=1.5", "?page_size=0", "?page_size=101", "?page_size=-1", "?page_size=x", "?page=9223372036854775807&page_size=100", "?page=999999999999999999999"} {
		requireAPIError(t, app.request(t, http.MethodGet, historyTracksURL+query, nil, &member, nil), 400, "invalid_pagination")
	}
	for _, id := range []string{"invalid", "1234", "00000000-0000-4000-8000-00000000000z"} {
		requireAPIError(t, app.request(t, http.MethodDelete, historyTracksURL+"/"+id, nil, &member, nil), 400, "invalid_id")
	}
	for _, query := range []string{"", "?page=&page_size=", "?page=1&page=0&page_size=50&page_size=101"} {
		got := historyTracksOf(t, app, &member, query)
		if got.Pagination.Page != 1 || got.Pagination.PageSize != 50 || got.Pagination.Total != 0 || got.Items == nil || len(got.Items) != 0 || got.HasHistory {
			t.Fatalf("defaults/empty: %+v", got)
		}
	}
	if got := historyTracksOf(t, app, &member, "?page_size=100"); got.Pagination.PageSize != 100 {
		t.Fatalf("100 rejected: %+v", got)
	}
	response := app.request(t, http.MethodGet, historyQueueURL+"?page=0&page_size=9999&sort=invalid", nil, &member, map[string]string{"Origin": "http://untrusted.example"})
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"limit":1000`) {
		t.Fatalf("queue query must be ignored: %d %s", response.Code, response.Body.String())
	}
	for _, route := range routes {
		requireAPIError(t, app.request(t, http.MethodPatch, route.path, nil, &member, nil), 405, "method_not_allowed")
	}
	for _, state := range []string{"disabled", "revoked", "expired"} {
		switch state {
		case "disabled":
			mustExec(t, app, `UPDATE users SET disabled_at=NOW() WHERE id=$1::uuid`, member.id)
		case "revoked":
			mustExec(t, app, `UPDATE users SET disabled_at=NULL WHERE id=$1::uuid`, member.id)
			mustExec(t, app, `UPDATE sessions SET revoked_at=NOW() WHERE user_id=$1::uuid`, member.id)
		case "expired":
			mustExec(t, app, `UPDATE sessions SET revoked_at=NULL,expires_at=NOW()-interval '1 hour' WHERE user_id=$1::uuid`, member.id)
		}
		for _, route := range routes {
			requireAPIError(t, app.request(t, route.method, route.path, nil, &member, nil), 401, "unauthorized")
		}
	}
	mustExec(t, app, `UPDATE sessions SET expires_at=NOW()+interval '1 hour' WHERE user_id=$1::uuid`, member.id)
	// 鉴权可用而历史表暂时不可用：三个路由统一 503，不把错误当空结果。
	mustExec(t, app, `ALTER TABLE play_history RENAME TO history_unavailable`)
	for _, route := range routes {
		requireAPIError(t, app.request(t, route.method, route.path, nil, &member, nil), 503, "database_unavailable")
	}
	mustExec(t, app, `ALTER TABLE history_unavailable RENAME TO play_history`)
	// 鉴权自身查库失败仍遵守既有 401 合同。
	mustExec(t, app, `ALTER TABLE sessions RENAME TO sessions_unavailable`)
	for _, route := range routes {
		requireAPIError(t, app.request(t, route.method, route.path, nil, &member, nil), 401, "unauthorized")
	}
}

func TestPostgreSQLHistoryHTTPProjectionFailure(t *testing.T) {
	app := newIntegrationTestApplication(t)
	library := createPlaybackLibrary(t, app)
	id := library.addTrack(t, app, playbackTrackSpec{relative: "a.flac", content: flacBytes(4096)})
	member := app.createUser(t, "user")
	requirePlayRecorded(t, postPlay(t, app, &member, uuid.New(), id, 120))
	mustExec(t, app, `INSERT INTO release_artworks(release_id,content_hash,mime_type,width,height,storage_key,source_type)
 SELECT release_id,repeat('a',64),'image/webp',640,480,'../private.webp','embedded' FROM media WHERE id=(SELECT medium_id FROM tracks WHERE id=$1::uuid)`, id)
	for _, path := range []string{historyTracksURL, historyQueueURL} {
		response := app.request(t, http.MethodGet, path, nil, &member, nil)
		requireAPIError(t, response, 503, "database_unavailable")
		if strings.Contains(response.Body.String(), "private") {
			t.Fatalf("unsafe detail leaked: %s", response.Body.String())
		}
	}
	// 删除不查询 catalog，坏摘要不能阻止本人删除记录。
	if response := app.request(t, http.MethodDelete, historyTracksURL+"/"+id, nil, &member, nil); response.Code != 200 {
		t.Fatalf("delete queried catalog: %s", response.Body.String())
	}
}
