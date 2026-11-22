package app

import (
	"bytes"
	"image"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/artwork"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func externalFixturePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func writeExternalFixture(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func removeExternalFixture(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

func externalScanWriter(t *testing.T, env *claimRescanEnvironment) *curation.Writer {
	t.Helper()
	w := curation.NewWriter(env.application.database, env.application.dataDirectory, nil)
	if _, err := w.SetSettings(t.Context(), env.admin.id, true); err != nil {
		t.Fatal(err)
	}
	return w
}

func requireExternalArtworkHTTP(t *testing.T, app *integrationTestApplication, user *integrationTestUser, stored artwork.Stored, data []byte) {
	t.Helper()
	path := "/api/v1/artworks/" + stored.Key
	r := app.request(t, "GET", path, nil, user, nil)
	if r.Code != 200 || !bytes.Equal(r.Body.Bytes(), data) || r.Header().Get("Content-Type") != stored.MIMEType {
		t.Fatalf("图片 %s 返回 %d，MIME=%q，字节数=%d", stored.Key, r.Code, r.Header().Get("Content-Type"), r.Body.Len())
	}
	requireArtworkCacheHeaders(t, r, stored.Key)
	r = app.request(t, "GET", path, nil, user, map[string]string{"If-None-Match": `"` + stored.Key + `"`})
	if r.Code != 304 || r.Body.Len() != 0 {
		t.Fatalf("图片 ETag 重验证返回 %d，字节数=%d", r.Code, r.Body.Len())
	}
	requireArtworkCacheHeaders(t, r, stored.Key)
}

func TestPostgreSQLExternalArtworkPriority(t *testing.T) {
	env := newClaimRescanEnvironment(t, "B2FixtureAlbum")
	app := env.application
	release, _ := env.releaseIDs(t, "B2FixtureAlbum")
	track, title := externalTrack(t, app, release)
	var group string
	if err := app.database.QueryRow("SELECT group_id::text FROM releases WHERE id=$1", release).Scan(&group); err != nil {
		t.Fatal(err)
	}
	req := externalArtworkRequest(release)
	external, _ := externalArtworkFile(t, app, req.Data)
	applyExternalArtwork(t, externalScanWriter(t, env), req)

	read := func(t *testing.T, method, path string, body any, keys ...string) any {
		t.Helper()
		r := app.request(t, method, path, body, &env.admin, nil)
		if r.Code != 200 {
			t.Fatalf("%s %s：%d %s", method, path, r.Code, r.Body)
		}
		var value any = decodeJSONMap(t, r)
		for _, key := range keys {
			if key == "[]" {
				items, ok := value.([]any)
				if !ok || len(items) != 1 {
					t.Fatalf("%s 的单项列表=%v", path, value)
				}
				value = items[0]
				continue
			}
			obj, ok := value.(map[string]any)
			if !ok || obj[key] == nil {
				t.Fatalf("%s 缺少 %s，当前节点=%v", path, key, value)
			}
			value = obj[key]
		}
		return value
	}
	check := func(t *testing.T, want artwork.Stored, data []byte) {
		t.Helper()
		for _, summary := range []struct {
			method, path string
			body         any
			keys         []string
		}{
			{"GET", "/api/v1/releases/" + release, nil, nil},
			{"GET", "/api/v1/releases?search=B2FixtureAlbum", nil, []string{"items", "[]"}},
			{"GET", "/api/v1/release-groups/" + group, nil, []string{"representative"}},
			{"GET", "/api/v1/release-groups", nil, []string{"items", "[]", "representative"}},
			{"POST", "/api/v1/tracks/summaries", map[string]any{"track_ids": []string{track}}, []string{"items", "[]", "release"}},
			{"GET", "/api/v1/search?type=tracks&q=" + url.QueryEscape(title), nil, []string{"items", "[]", "release"}},
			{"GET", "/api/v1/search?type=albums&q=B2FixtureAlbum", nil, []string{"items", "[]", "group", "representative"}},
		} {
			got := read(t, summary.method, summary.path, summary.body, summary.keys...).(map[string]any)
			expected := map[string]any{"resource_id": want.Key, "mime": want.MIMEType, "width": float64(want.Width), "height": float64(want.Height)}
			if got["id"] != release || !reflect.DeepEqual(got["artwork"], expected) {
				t.Errorf("%s 发行=%v，封面=%v，期望 %s %v", summary.path, got["id"], got["artwork"], release, expected)
			}
		}
		requireExternalArtworkHTTP(t, app, &env.admin, want, data)
	}
	t.Run("仅外部封面", func(t *testing.T) { check(t, external, req.Data) })
	localData := externalFixturePNG(t, 3, 2)
	local, _ := externalArtworkFile(t, app, localData)
	sidecar := filepath.Join(env.library, "B2FixtureAlbum", "cover.png")
	writeExternalFixture(t, sidecar, localData)
	runScan(t, app, &env.admin)
	t.Run("扫描加入本地封面", func(t *testing.T) { check(t, local, localData) })
	removeExternalFixture(t, sidecar)
	runScan(t, app, &env.admin)
	t.Run("全量扫描删除本地封面", func(t *testing.T) { check(t, external, req.Data) })
	requireAPIError(t, app.request(t, "GET", "/api/v1/artworks/"+local.Key, nil, &env.admin, nil), 404, "not_found")
}

func requireExternalLyricsHTTP(t *testing.T, app *integrationTestApplication, user *integrationTestUser, track, source, kind string, lines []map[string]any) {
	t.Helper()
	r := app.request(t, "GET", "/api/v1/tracks/"+track+"/lyrics", nil, user, nil)
	if r.Code != 200 {
		t.Fatalf("歌词返回 %d %s", r.Code, r.Body)
	}
	var lyrics any
	if source != "" {
		lyrics = map[string]any{"source": source, "kind": kind, "lines": lines}
	}
	requireExternalJSON(t, r.Body.Bytes(), map[string]any{"track_id": track, "lyrics": lyrics})
}

func TestPostgreSQLExternalLyricsPriority(t *testing.T) {
	env := newClaimRescanEnvironment(t, "a")
	app := env.application
	release, _ := env.releaseIDs(t, "a")
	track, _ := externalTrack(t, app, release)
	req := externalLyricsRequest(track)
	id := applyExternalLyrics(t, externalScanWriter(t, env), req)
	readOnline := func() {
		t.Helper()
		requireExternalLyricsHTTP(t, app, &env.admin, track, "online", "synced", []map[string]any{
			{"start_ms": 1000, "text": "第一行"}, {"start_ms": 2000, "text": "第二行"},
		})
		if n := countRows(t, app, `SELECT count(*) FROM track_external_lyrics WHERE track_id=$1 AND operation_id=$2 AND lyrics=$3`,
			track, id, externalNormalizedLyrics); n != 1 {
			t.Fatalf("重扫后外部歌词匹配行数=%d，期望 1", n)
		}
	}
	readOnline()
	sidecar := filepath.Join(env.library, "a", "01.lrc")
	writeExternalFixture(t, sidecar, []byte("[00:02.50]扫描到的歌词"))
	runScan(t, app, &env.admin)
	requireExternalLyricsHTTP(t, app, &env.admin, track, "external", "synced", []map[string]any{{"start_ms": 2500, "text": "扫描到的歌词"}})
	writeExternalFixture(t, sidecar, []byte("[ti:只有元标签]"))
	runScan(t, app, &env.admin)
	if n := countRows(t, app, `SELECT count(*) FROM tracks WHERE id=$1 AND lyrics='[ti:只有元标签]'`, track); n != 1 {
		t.Fatalf("扫描后的本地元标签行数=%d，期望 1", n)
	}
	readOnline()
	removeExternalFixture(t, sidecar)
	runScan(t, app, &env.admin)
	readOnline()
}

func TestPostgreSQLExternalArtworkRescan(t *testing.T) {
	env := newClaimRescanEnvironment(t, "a", "b")
	app := env.application
	w := externalScanWriter(t, env)
	a, _ := env.releaseIDs(t, "a")
	track, _ := externalTrack(t, app, a)
	req := externalArtworkRequest(a)
	// 用 PNG 让扫描器也识别同一份字节。
	req.Data = externalFixturePNG(t, 2, 4)
	stored, path := externalArtworkFile(t, app, req.Data)
	artOp := applyExternalArtwork(t, w, req)
	lyricsOp := applyExternalLyrics(t, w, externalLyricsRequest(track))
	check := func() {
		t.Helper()
		externalArtworkState(t, app, a, artOp, stored)
		requireExternalFile(t, path, req.Data)
		if n := countRows(t, app, `SELECT count(*) FROM track_external_lyrics WHERE track_id=$1 AND operation_id=$2 AND lyrics=$3`,
			track, lyricsOp, externalNormalizedLyrics); n != 1 {
			t.Fatalf("曲目 %s 的外部歌词匹配行数=%d", track, n)
		}
	}
	sidecar := filepath.Join(env.library, "b", "cover.png")
	b, _ := env.releaseIDs(t, "b")
	assertLocal := func(want string) {
		t.Helper()
		var got string
		if err := app.database.QueryRow(`SELECT COALESCE((SELECT storage_key FROM release_artworks WHERE release_id=$1),'')`, b).Scan(&got); err != nil || got != want {
			t.Fatalf("扫描的本地封面=%q，错误=%v，期望 %q", got, err, want)
		}
	}
	writeExternalFixture(t, sidecar, req.Data)
	runScan(t, app, &env.admin)
	assertLocal(stored.Key)
	check()
	different := externalFixturePNG(t, 3, 5)
	replacement, _ := externalArtworkFile(t, app, different)
	writeExternalFixture(t, sidecar, different)
	runScan(t, app, &env.admin)
	assertLocal(replacement.Key)
	check()
	writeExternalFixture(t, sidecar, req.Data)
	runScan(t, app, &env.admin)
	assertLocal(stored.Key)
	removeExternalFixture(t, sidecar)
	runScan(t, app, &env.admin)
	assertLocal("")
	check()

	if err := os.Rename(filepath.Join(env.library, "a"), filepath.Join(env.library, "moved")); err != nil {
		t.Fatal(err)
	}
	runScan(t, app, &env.admin)
	if visible, _ := env.releaseIDs(t, "moved"); visible != a {
		t.Fatalf("移动后发行=%q，期望 %q", visible, a)
	}
	check()

	env.correct(t, a, "移动后保留的人工标题")
	for i := 1; i <= 2; i++ {
		env.write(t, "moved", i, byte(90+i), "New")
	}
	runScan(t, app, &env.admin)
	newID, oldID := env.releaseIDs(t, "moved")
	if newID == "" || oldID != a || newID == a {
		t.Fatalf("待认领发行=(%q,%q)，期望旧发行 %s 和新发行", newID, oldID, a)
	}
	preview := app.request(t, "GET", claimPreviewPath(newID, a), nil, &env.admin, nil)
	if preview.Code != 200 {
		t.Fatalf("认领预览=%d %s", preview.Code, preview.Body)
	}
	p := decodeJSONMap(t, preview)
	body := claimBody(int64(p["revision"].(float64)), int64(p["source_revision"].(float64)), a, p["token"].(string))
	claimed := app.request(t, "POST", claimPath(newID), body, &env.admin, idempotencyHeader(uuid.New()))
	if claimed.Code != 200 {
		t.Fatalf("认领=%d %s", claimed.Code, claimed.Body)
	}
	check()
	externalArtworkState(t, app, newID, "", artwork.Stored{})
}
