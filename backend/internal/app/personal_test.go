package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

const personalBase = "/api/v1/me"
const playlistsURL = personalBase + "/playlists"
const favoritesURL = personalBase + "/favorites"

type personalFixture struct {
	t              *testing.T
	app            *integrationTestApplication
	user           integrationTestUser
	tracks, groups []string
}

func newPersonalFixture(t *testing.T, count int) *personalFixture {
	t.Helper()
	app := newIntegrationTestApplication(t)
	f := &personalFixture{t: t, app: app, user: app.createUser(t, "user")}
	library := createPlaybackLibrary(t, app)
	for i := 0; i < count; i++ {
		id := library.addTrack(t, app, playbackTrackSpec{relative: fmt.Sprintf("personal-%d.flac", i), content: flacBytes(4096), bitDepth: 24, sampleRate: 96000, channels: 2})
		mustExec(t, app, `UPDATE tracks SET title=$2,duration_seconds=120 WHERE id=$1::uuid`, id, fmt.Sprintf("合成曲目 %d", i+1))
		var group string
		if err := app.database.QueryRow(`SELECT r.group_id::text FROM tracks t JOIN media m ON m.id=t.medium_id JOIN releases r ON r.id=m.release_id WHERE t.id=$1::uuid`, id).Scan(&group); err != nil {
			t.Fatal(err)
		}
		f.tracks = append(f.tracks, id)
		f.groups = append(f.groups, group)
	}
	return f
}
func (f *personalFixture) call(method, path string, body any) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.app.request(f.t, method, path, body, &f.user, map[string]string{"Idempotency-Key": uuid.New(), "X-Request-ID": sampleRequestID})
}
func personalOK(t *testing.T, response *httptest.ResponseRecorder, status int) *httptest.ResponseRecorder {
	t.Helper()
	if response.Code != status {
		t.Fatalf("期望 HTTP %d，得到 %d: %s", status, response.Code, response.Body)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("个人数据不能缓存")
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatal("个人响应必须为 JSON")
	}
	return response
}
func personalBody(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}
func personalState(t *testing.T, response *httptest.ResponseRecorder) personal.PlaylistState {
	t.Helper()
	var body struct {
		Playlist personal.PlaylistState `json:"playlist"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Playlist
}
func (f *personalFixture) create(name string, ids []string) personal.PlaylistState {
	f.t.Helper()
	return personalState(f.t, personalOK(f.t, f.call("POST", playlistsURL, map[string]any{"name": name, "track_ids": ids}), 201))
}
func (f *personalFixture) items(id string) []map[string]any {
	f.t.Helper()
	body := personalBody(f.t, personalOK(f.t, f.call("GET", playlistsURL+"/"+id+"/items", nil), 200))
	entries := body["items"].([]any)
	items := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		items = append(items, entry.(map[string]any))
	}
	return items
}
func (f *personalFixture) state(id string) personal.PlaylistState {
	f.t.Helper()
	return personalState(f.t, personalOK(f.t, f.call("GET", playlistsURL+"/"+id, nil), 200))
}
func (f *personalFixture) remove(id, item string) *httptest.ResponseRecorder {
	f.t.Helper()
	state := f.state(id)
	return personalOK(f.t, f.call("DELETE", playlistsURL+"/"+id+"/items/"+item, map[string]any{"expected_revision": state.Revision}), 200)
}
func personalToken(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	return personalBody(t, response)["undo"].(map[string]any)["token"].(string)
}
func personalRaw(app *integrationTestApplication, method, path, body string, user *integrationTestUser, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Host = "roomusic.test"
	request.Header.Set("Origin", app.origin)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Request-ID", sampleRequestID)
	request.Header.Set("Idempotency-Key", uuid.New())
	if user != nil {
		request.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: user.token})
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	app.handler.ServeHTTP(response, request)
	return response
}
