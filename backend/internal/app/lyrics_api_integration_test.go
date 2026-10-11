package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func lyricsPath(trackID string) string { return "/api/v1/tracks/" + trackID + "/lyrics" }

func decodeTrackLyrics(t *testing.T, body string) catalog.TrackLyrics {
	t.Helper()
	var decoded catalog.TrackLyrics
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode lyrics response: %v: %s", err, body)
	}
	return decoded
}

func TestPostgreSQLTrackLyricsRequireLoginAndCheckInputInOrder(t *testing.T) {
	application := newIntegrationTestApplication(t)
	library := createPlaybackLibrary(t, application)
	trackID := library.addTrack(t, application, playbackTrackSpec{relative: "a.flac", content: flacBytes(4096)})
	mustExec(t, application, `UPDATE tracks SET lyrics='[00:01.00]secret words',lyrics_source='vorbis_lyrics' WHERE id=$1::uuid`, trackID)
	user := application.createUser(t, "user")

	// 先鉴权：未登录时，存在的、不存在的、格式错误的标识都是 401，响应里没有歌词。
	for _, path := range []string{lyricsPath(trackID), lyricsPath(uuid.New()), lyricsPath("not-a-uuid")} {
		response := application.request(t, http.MethodGet, path, nil, nil, nil)
		requireAPIError(t, response, http.StatusUnauthorized, "unauthorized")
		if strings.Contains(response.Body.String(), "secret") {
			t.Fatalf("an anonymous response reveals the lyrics: %s", response.Body.String())
		}
	}
	requireAPIError(t, application.request(t, http.MethodGet, lyricsPath("not-a-uuid"), nil, &user, nil), http.StatusBadRequest, "invalid_id")
	requireAPIError(t, application.request(t, http.MethodGet, lyricsPath(uuid.New()), nil, &user, nil), http.StatusNotFound, "not_found")

	// 会话撤销后同样是 401。
	mustExec(t, application, `UPDATE sessions SET revoked_at=NOW() WHERE user_id=$1::uuid`, user.id)
	requireAPIError(t, application.request(t, http.MethodGet, lyricsPath(trackID), nil, &user, nil), http.StatusUnauthorized, "unauthorized")
}

func TestPostgreSQLTrackLyricsAreServedParsedToAnyMember(t *testing.T) {
	application := newIntegrationTestApplication(t)
	library := createPlaybackLibrary(t, application)
	addTrack := func(relative string) string {
		return library.addTrack(t, application, playbackTrackSpec{relative: relative, content: flacBytes(4096)})
	}
	setLyrics := func(trackID, raw string) {
		mustExec(t, application, `UPDATE tracks SET lyrics=$1,lyrics_source='vorbis_lyrics' WHERE id=$2::uuid`, raw, trackID)
	}
	synced, plain, none, onlyMeta, missing := addTrack("synced.flac"), addTrack("plain.flac"), addTrack("none.flac"), addTrack("meta.flac"), addTrack("missing.flac")
	setLyrics(synced, "[ti:Song]\n[offset:+200]\n[00:10.00][00:30.00]chorus\n[00:05.00]intro\n[00:05.00]对照")
	setLyrics(plain, "first line\n\nsecond paragraph")
	setLyrics(onlyMeta, "[ti:Song]\n[ar:Artist]")
	setLyrics(missing, "[00:01.00]gone")
	mustExec(t, application, `UPDATE tracks SET source_status='missing' WHERE id=$1::uuid`, missing)
	// 普通成员（不是管理员）也可以读取。
	member := application.createUser(t, "user")
	get := func(trackID string) *httptest.ResponseRecorder {
		t.Helper()
		return application.request(t, http.MethodGet, lyricsPath(trackID), nil, &member, nil)
	}

	response := get(synced)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || !strings.HasPrefix(response.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("synced lyrics: HTTP %d, Cache-Control %q, Content-Type %q: %s", response.Code, response.Header().Get("Cache-Control"), response.Header().Get("Content-Type"), response.Body.String())
	}
	requireNoPathLeak(t, application, response)
	decoded := decodeTrackLyrics(t, response.Body.String())
	if decoded.TrackID != synced || decoded.Lyrics == nil || decoded.Lyrics.Kind != catalog.LyricsKindSynced || decoded.Lyrics.Source != catalog.LyricsSourceEmbedded {
		t.Fatalf("synced lyrics = %+v", decoded)
	}
	type line struct {
		start int64
		text  string
	}
	var got []line
	for _, item := range decoded.Lyrics.Lines {
		if item.StartMS == nil {
			t.Fatalf("a synced line has no start time: %s", response.Body.String())
		}
		got = append(got, line{*item.StartMS, item.Text})
	}
	// offset +200 让每一行提前 200 毫秒；一行多个时间戳展开，同一时间的两行保持原顺序。
	want := []line{{4800, "intro"}, {4800, "对照"}, {9800, "chorus"}, {29800, "chorus"}}
	if len(got) != len(want) {
		t.Fatalf("synced lines = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("synced lines = %v, want %v", got, want)
		}
	}

	response = get(plain)
	decoded = decodeTrackLyrics(t, response.Body.String())
	if response.Code != http.StatusOK || decoded.Lyrics == nil || decoded.Lyrics.Kind != catalog.LyricsKindPlain || len(decoded.Lyrics.Lines) != 3 {
		t.Fatalf("plain lyrics: HTTP %d: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"start_ms":null`) || strings.Contains(response.Body.String(), `"start_ms":0`) {
		t.Fatalf("plain lines must carry a null start time: %s", response.Body.String())
	}

	// 没有歌词、只有元标签的歌词都是 200 加 null，不是 404。
	for name, trackID := range map[string]string{"no lyrics": none, "only meta tags": onlyMeta} {
		response = get(trackID)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"lyrics":null`) || decodeTrackLyrics(t, response.Body.String()).TrackID != trackID {
			t.Fatalf("%s: HTTP %d: %s", name, response.Code, response.Body.String())
		}
	}

	// 缺失的曲目对歌词不可见；目录停用不影响已入库曲目的歌词。
	requireAPIError(t, get(missing), http.StatusNotFound, "not_found")
	mustExec(t, application, `UPDATE library_roots SET status='disabled' WHERE id=$1::uuid`, library.rootID)
	if response = get(synced); response.Code != http.StatusOK {
		t.Fatalf("lyrics after the root was disabled: HTTP %d: %s", response.Code, response.Body.String())
	}
}
