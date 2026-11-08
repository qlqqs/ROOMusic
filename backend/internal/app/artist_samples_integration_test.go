package app

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/maintenance"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

const artistLargeSamplesDirectoryEnv = "ROOMUSIC_ARTIST_LARGE_SAMPLES_DIR"
const artistRequiredLargeSample = "artists/queue-truncated"

// 更新：ROOMUSIC_UPDATE_API_SAMPLES=1 go test ./internal/app -run '^TestPostgreSQLArtistAPIResponseSamples$' -count=1。
// 所有 golden 都来自真实 handler；本套件只拥有艺人增量，不清理旧 DTO 样本。
func TestPostgreSQLArtistAPIResponseSamples(t *testing.T) {
	f := newArtistSamples(t)
	recordArtistPreparingSamples(f)
	recordArtistSearchReadyPublication(f)
	recordArtistReadSamples(f)
	recordArtistGovernanceSamples(f)
	recordArtistCandidateSamples(t, f.samples)
	recordArtistQueueLimit(t, f.samples)
	recordArtistCorrectionSearchSamples(t, f.samples)
	for _, name := range strings.Fields(artistRequiredSamples) {
		if _, ok := f.samples.samples[name]; !ok {
			t.Fatalf("缺少艺人场景样本：%s", name)
		}
	}
	for name := range f.samples.samples {
		if !isArtistAPISample(name) {
			t.Fatalf("艺人样本越界：%s", name)
		}
	}
	f.samples.finish()
}

// 同时供默认、搜索、个人套件排除独立艺人增量；比较和清理必须使用同一边界。
func isArtistAPISample(name string) bool {
	name = strings.TrimSuffix(name, ".json")
	for _, prefix := range []string{"artists/", "governance/artist-", "operations/artist", "favorites/artist", "search/artist-correction-"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	switch name {
	case "catalog/stats", "catalog/stats-artists-preparing", "catalog/status-search-ready", "search/suggestions-artists", "search/artists", "search/artists-alias-match", "search/albums-alias-match", "search/tracks-alias-match", "search/same-name-split-results", "search/artists-preparing", "catalog/status-artists-preparing", "catalog/status-all-ready":
		return true
	}
	return false
}

type artistSamples struct {
	t                             *testing.T
	app                           *integrationTestApplication
	samples                       *apiSampleRecorder
	admin, member                 integrationTestUser
	primary, alias, same, missing string
	releases                      []searchReleaseFixture
	trackCount                    int
}

func newArtistSamples(t *testing.T) *artistSamples {
	t.Helper()
	app := newIntegrationTestApplication(t)
	// 仅固定样本计数起点，不改变投影版本、代次或状态；发布仍由真实维护事务递增。
	mustExec(t, app, `UPDATE catalog_projection_state SET catalog_epoch=1 WHERE capability='search'`)
	f := &artistSamples{t: t, app: app, admin: app.createUser(t, "admin"), member: app.createUser(t, "user")}
	f.samples = newAPISampleRecorder(t, app)
	f.samples.scope = "artist-increment"
	f.samples.forbid(f.admin.token)
	f.samples.forbid(f.member.token)
	f.samples.forbid(app.dataDirectory)
	mustExec(t, app, `UPDATE users SET username=CASE role WHEN 'admin' THEN 'artist-curator' ELSE 'artist-listener' END`)
	// 固定合成身份使候选左右顺序、随机队列与多目标操作样本可复现；署名、投影、主名仍由真实维护入口生成。
	names := []string{"邓丽君", "Teresa Teng", "同名艺人", "失联艺人", "作曲者", "指挥者", "演奏者", "制作人", "Alpha Singer", "Beta Singer", "Gamma Singer"}
	for i, name := range names {
		f.identity(i+1, name)
	}
	f.primary = f.id(1)
	f.alias = f.id(2)
	f.same = f.id(3)
	f.missing = f.id(4)
	root := searchRoot(t, app)
	for i, spec := range []struct {
		title, artist string
		states        []string
	}{
		{"海边夜曲", "邓丽君", []string{"present", "present", "missing"}},
		{"英文现场", "Teresa Teng", []string{"present"}},
		{"同名甲专辑", "同名艺人", []string{"present"}},
		{"同名乙专辑", "同名艺人", []string{"present"}},
		{"暂不可用专辑", "失联艺人", []string{"missing"}},
	} {
		r := insertSearchRelease(t, app, root, groupedRelease{title: spec.title, artist: spec.artist, tracks: spec.states})
		mustExec(t, app, `UPDATE releases SET album_artist=artist,created_at='2026-01-01T00:00:00Z'::timestamptz+($2::int*interval '1 day') WHERE id=$1::uuid`, r.releaseID, i)
		for j, oldID := range r.trackIDs {
			f.trackCount++
			id := fmt.Sprintf("e0000000-0000-4000-8000-%012d", f.trackCount)
			mustExec(t, app, `UPDATE tracks SET id=$2::uuid,title=$3,artist=$4,duration_seconds=120,source_kind='flac_vorbis_comment',codec='flac',sample_rate=44100,bit_depth=16,channels=2 WHERE id=$1::uuid`, oldID, id, fmt.Sprintf("曲目%d-%d", i+1, j+1), spec.artist)
			r.trackIDs[j] = id
		}
		f.releases = append(f.releases, r)
	}
	for i, role := range []string{"composer", "conductor", "performer", "producer"} {
		mustExec(t, app, `INSERT INTO track_credits(track_id,role,name,position) VALUES($1::uuid,$2,$3,1)`, f.releases[0].trackIDs[0], role, names[4+i])
	}
	return f
}
func (f *artistSamples) id(n int) string { return fmt.Sprintf("b0000000-0000-4000-8000-%012d", n) }
func (f *artistSamples) identity(n int, name string) {
	key := textnorm.Text(name)
	hash := sha256.Sum256([]byte(key))
	mustExec(f.t, f.app, `INSERT INTO artists(id,initial_name) VALUES($1::uuid,$2)`, f.id(n), name)
	mustExec(f.t, f.app, `INSERT INTO artist_name_keys(id,normalization_version,normalized_key,key_hash,initial_artist_id) VALUES($1::uuid,$2,$3,$4,$5::uuid)`, fmt.Sprintf("c0000000-0000-4000-8000-%012d", n), textnorm.Version, key, hash[:], f.id(n))
}
func (f *artistSamples) publish() {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if err := maintenance.Run(ctx, f.app.database, slog.New(slog.NewTextHandler(io.Discard, nil)), maintenance.RunOptions{RefreshArtistResolutions: personal.RefreshArtistResolution}); err != nil {
		f.t.Fatal(err)
	}
}
func (f *artistSamples) rebuild() {
	mustExec(f.t, f.app, `UPDATE catalog_projection_state SET target_generation=target_generation+1,build_id=gen_random_uuid(),status='preparing',manifest_sealed=FALSE,catalog_epoch=catalog_epoch+1`)
	f.publish()
}
func (f *artistSamples) call(method, path string, body any, user *integrationTestUser, headers ...map[string]string) *httptest.ResponseRecorder {
	f.t.Helper()
	h := map[string]string{"X-Request-ID": sampleRequestID, "Idempotency-Key": uuid.New()}
	for _, extra := range headers {
		for k, v := range extra {
			h[k] = v
		}
	}
	return f.app.request(f.t, method, path, body, user, h)
}
func (f *artistSamples) record(name, method, path string, body any, status int) map[string]any {
	f.t.Helper()
	r := f.call(method, path, body, &f.admin)
	f.samples.record(name, r, status)
	return decodeJSONMap(f.t, r)
}
func (f *artistSamples) get(name, path string) map[string]any {
	return f.record(name, "GET", path, nil, 200)
}
func (f *artistSamples) error(name, method, path string, body any, status int, code string) {
	f.t.Helper()
	r := f.call(method, path, body, &f.admin)
	requireAPIError(f.t, r, status, code)
	f.samples.record(name, r, status)
}
func artistSampleItems(t *testing.T, body map[string]any, count int) []any {
	t.Helper()
	items, ok := body["items"].([]any)
	if !ok || len(items) != count {
		t.Fatalf("items期望%d，实际%v", count, body)
	}
	return items
}
func (f *artistSamples) revision(id string) float64 {
	r := f.call("GET", "/api/v1/artists/"+id, nil, &f.admin)
	if r.Code != 200 {
		f.t.Fatalf("读取艺人版本：%d %s", r.Code, r.Body.String())
	}
	return decodeJSONMap(f.t, r)["revision"].(float64)
}
func (f *artistSamples) key(id string) string {
	var key string
	if err := f.app.database.QueryRow(`SELECT id::text FROM artist_name_keys WHERE initial_artist_id=$1::uuid ORDER BY id LIMIT 1`, id).Scan(&key); err != nil {
		f.t.Fatal(err)
	}
	return key
}

func recordArtistPreparingSamples(f *artistSamples) {
	f.t.Helper()
	base := "/api/v1/artists/" + f.primary
	for _, tc := range []struct{ name, path string }{{"list", "/api/v1/artists"}, {"detail", base}, {"queue", base + "/queue"}} {
		f.error("artists/"+tc.name+"-preparing", "GET", tc.path, nil, 503, "artist_catalog_preparing")
	}
	f.error("search/artists-preparing", "GET", searchURL("artists", "邓丽君"), nil, 503, "artist_catalog_preparing")
	f.get("catalog/stats-artists-preparing", "/api/v1/catalog/stats")
	state := f.get("catalog/status-artists-preparing", "/api/v1/catalog/status")
	if state["artists_state"] != "preparing" {
		f.t.Fatal("整理中返回ready")
	}
	f.error("favorites/artists-preparing", "GET", favoritesURL+"/artists", nil, 503, "artist_catalog_preparing")
	for _, tc := range []struct {
		name, method, path string
		body               any
	}{
		{"merge", "POST", base + "/merge", map[string]any{"expected_revision": 1, "source_artist_id": f.alias, "source_expected_revision": 1}},
		{"rename", "PATCH", base + "/name", map[string]any{"expected_revision": 1, "mode": "set", "name": "整理中"}},
		{"split", "POST", base + "/split", map[string]any{"expected_revision": 1, "mode": "aliases", "name_key_ids": []string{f.key(f.primary)}, "basis": strings.Repeat("0", 64), "target": map[string]string{"kind": "new"}}},
	} {
		f.error("governance/artist-"+tc.name+"-not-ready", tc.method, tc.path, tc.body, 409, "artist_catalog_not_ready")
	}
}

// 在真实发布事务的窄回调上暂停；HTTP 必须读到已提交的搜索能力和未发布的艺人能力。
func recordArtistSearchReadyPublication(f *artistSamples) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	entered, release, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	go func() {
		defer close(exited)
		result <- maintenance.Run(ctx, f.app.database, slog.New(slog.NewTextHandler(io.Discard, nil)), maintenance.RunOptions{
			RefreshArtistResolutions: func(ctx context.Context, tx *sql.Tx, mapping map[string]string) error {
				if err := personal.RefreshArtistResolution(ctx, tx, mapping); err != nil {
					return err
				}
				close(entered)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			},
		})
	}()
	defer func() { cancel(); <-exited }()
	select {
	case <-entered:
	case err := <-result:
		f.t.Fatalf("未进入艺人发布回调：%v", err)
	case <-ctx.Done():
		f.t.Fatal(ctx.Err())
	}
	state := f.get("catalog/status-search-ready", "/api/v1/catalog/status")
	if state["search_mode"] != "normalized" || state["artists_state"] != "preparing" {
		f.t.Fatalf("搜索已发布、艺人待发布状态不符：%v", state)
	}
	close(release)
	if err := <-result; err != nil {
		f.t.Fatal(err)
	}
}

// 修正通过真实 PATCH 提交，紧随其后的搜索不调用维护/重扫；覆盖回退与已发布两条路径。
func recordArtistCorrectionSearchSamples(t *testing.T, samples *apiSampleRecorder) {
	t.Helper()
	app := newIntegrationTestApplication(t)
	// 仅固定样本计数起点，不改变投影版本、代次或状态；发布仍由真实维护事务递增。
	mustExec(t, app, `UPDATE catalog_projection_state SET catalog_epoch=1 WHERE capability='search'`)
	f := &artistSamples{t: t, app: app, samples: samples, admin: app.createUser(t, "admin")}
	samples.forbid(f.admin.token)
	samples.forbid(app.allowedRoot)
	samples.forbid(app.dataDirectory)
	fixture := insertSearchRelease(t, app, searchRoot(t, app), groupedRelease{title: "修正前目录词", artist: "原始录音者", tracks: []string{"present", "present", "missing"}})
	mustExec(t, app, `UPDATE tracks SET title='独立曲目' WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1::uuid)`, fixture.releaseID)
	oldTitle := "修正前目录词"
	for index, mode := range []string{"legacy", "normalized"} {
		if mode == "normalized" {
			f.publish()
		}
		newTitle := "提交即命中新词" + mode
		prefix := "search/artist-correction-" + mode
		f.record(prefix, "PATCH", "/api/v1/releases/"+fixture.releaseID+"/corrections", correctionBody(index+1, map[string]any{
			"title": setChange(newTitle), "album_artist": setChange("修正署名" + mode),
		}), 200)
		for _, kind := range []string{"albums", "tracks"} {
			response := f.call("GET", searchURL(kind, newTitle), nil, &f.admin)
			samples.record(prefix+"-"+kind+"-new", response, 200)
			if kind == "albums" {
				var page searchPage[catalog.SearchAlbumItem]
				if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
					t.Fatal(err)
				}
				if page.CatalogState.SearchMode != mode || len(page.Items) != 1 || page.Pagination.Total != 1 || page.Items[0].Group.ID != fixture.groupID || page.Items[0].MatchedReleaseID != fixture.releaseID || page.Items[0].Match.Original != newTitle || page.Items[0].Match.Field != "release_title" || page.Items[0].Match.Kind != "exact" {
					t.Fatalf("提交后专辑搜索未即时刷新：%+v", page)
				}
			} else {
				var page searchPage[catalog.SearchTrackItem]
				if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
					t.Fatal(err)
				}
				if page.CatalogState.SearchMode != mode || len(page.Items) != 2 || page.Pagination.Total != 2 {
					t.Fatalf("提交后曲目搜索数量/模式不符：%+v", page)
				}
				seen := map[string]bool{}
				for _, item := range page.Items {
					if seen[item.TrackID] || (item.TrackID != fixture.trackIDs[0] && item.TrackID != fixture.trackIDs[1]) || item.Release.ID != fixture.releaseID || item.Match.Original != newTitle || item.Match.Field != "release_title" || item.Match.Kind != "exact" {
						t.Fatalf("提交后曲目命中身份/原文不符：%+v", item)
					}
					seen[item.TrackID] = true
				}
			}
			old := f.get(prefix+"-"+kind+"-old", searchURL(kind, oldTitle))
			artistSampleItems(t, old, 0)
			if old["pagination"].(map[string]any)["total"] != float64(0) {
				t.Fatal("修正旧词仍有匹配总数")
			}
		}
		if mode == "normalized" {
			body := f.get(prefix+"-artists", searchURL("artists", "修正署名"+mode))
			item := artistSampleItems(t, body, 1)[0].(map[string]any)
			artist := item["artist"].(map[string]any)
			if !uuid.Valid(artist["id"].(string)) || artist["name"] != "修正署名"+mode || artist["track_count"] != float64(2) || artist["album_count"] != float64(1) || !reflect.DeepEqual(artist["roles"], []any{"release_artist", "album_artist"}) {
				t.Fatalf("提交后艺人搜索未即时刷新有效署名：%v", body)
			}
		}
		oldTitle = newTitle
	}
}

func recordArtistReadSamples(f *artistSamples) {
	f.t.Helper()
	base := "/api/v1/artists/" + f.primary
	state := f.get("catalog/status-all-ready", "/api/v1/catalog/status")
	if state["artists_state"] != "ready" || state["search_mode"] != "normalized" {
		f.t.Fatal("公开目录状态未完整发布")
	}
	f.get("catalog/stats", "/api/v1/catalog/stats")
	for _, tc := range []struct {
		name, path string
		count      int
	}{
		{"list-name", "/api/v1/artists?sort=name", 7},
		{"list-album-count", "/api/v1/artists?sort=album_count", 7},
		{"list-role", "/api/v1/artists?role=composer", 1},
		{"list-initial", "/api/v1/artists?initial=T", 2},
		{"list-empty", "/api/v1/artists?q=不存在的姓名", 0},
		{"list-out-of-range", "/api/v1/artists?page=20", 0},
		{"names", base + "/names", 1}, {"names-out-of-range", base + "/names?page=2", 0},
		{"albums", base + "/albums", 1}, {"role-tracks", base + "/tracks?role=track_artist", 3},
		{"role-tracks-missing", "/api/v1/artists/" + f.missing + "/tracks?role=track_artist", 1},
	} {
		body := f.get("artists/"+tc.name, tc.path)
		items := artistSampleItems(f.t, body, tc.count)
		if tc.name == "role-tracks-missing" && items[0].(map[string]any)["present"] != false {
			f.t.Fatal("missing曲目未保留")
		}
	}
	detail := f.get("artists/detail", base)
	if detail["artist"].(map[string]any)["name"] != "邓丽君" {
		f.t.Fatal("艺人详情错误")
	}
	missing := f.get("artists/detail-unavailable", "/api/v1/artists/"+f.missing)
	if missing["artist"].(map[string]any)["available"] != false {
		f.t.Fatal("不可用艺人被隐藏或当作可用")
	}
	for _, tc := range []struct {
		name, path string
		total      int
	}{
		{"queue", base + "/queue", 2}, {"queue-random", base + "/queue?order=random&seed=01000000-0000-4000-8000-000000000001", 2}, {"queue-empty", "/api/v1/artists/" + f.missing + "/queue", 0},
	} {
		body := f.get("artists/"+tc.name, tc.path)
		artistSampleItems(f.t, body, tc.total)
		if body["returned"] != float64(tc.total) || body["truncated"] != false || body["missing_total"] != float64(1) {
			f.t.Fatal("队列计数错误")
		}
		if tc.name == "queue-random" {
			want := append([]string(nil), f.releases[0].trackIDs[:2]...)
			artistSampleShuffle(want, "01000000-0000-4000-8000-000000000001")
			for i, item := range body["items"].([]any) {
				if item.(map[string]any)["track_id"] != want[i] {
					f.t.Fatal("随机队列未按固定种子排序")
				}
			}
		}
	}
	artistSampleItems(f.t, f.get("search/artists", searchURL("artists", "邓丽君")), 1)
	suggestion := f.get("search/suggestions-artists", "/api/v1/search/suggestions?q="+url.QueryEscape("邓丽君"))
	suggested := artistSampleItems(f.t, suggestion["artists"].(map[string]any), 1)
	if suggested[0].(map[string]any)["artist"].(map[string]any)["id"] != f.primary {
		f.t.Fatal("建议没有目标艺人")
	}
	if !reflect.DeepEqual(suggestion["available_types"], []any{"artists", "albums", "tracks"}) {
		f.t.Fatal("建议未开放三栏")
	}
	recordArtistReadErrors(f)
}

func recordArtistReadErrors(f *artistSamples) {
	base := "/api/v1/artists/" + f.primary
	for _, tc := range []struct {
		name, path, code string
		status           int
	}{
		{"invalid-query", "/api/v1/artists?q=" + url.QueryEscape(strings.Repeat("爱", 67)), "invalid_query", 400},
		{"invalid-id", "/api/v1/artists/not-an-id", "invalid_id", 400},
		{"not-found", "/api/v1/artists/" + uuid.New(), "not_found", 404},
		{"invalid-role", "/api/v1/artists?role=unknown", "invalid_artist_role", 400},
		{"invalid-initial", "/api/v1/artists?initial=unknown", "invalid_artist_initial", 400},
		{"invalid-sort", "/api/v1/artists?sort=unknown", "invalid_artist_sort", 400},
		{"invalid-seed", base + "/queue?order=random&seed=bad", "invalid_seed", 400},
		{"invalid-order", base + "/queue?order=bad", "invalid_queue_order", 400},
		{"invalid-pagination", "/api/v1/artists?page_size=101", "invalid_pagination", 400},
		{"invalid-limit", "/api/v1/search/suggestions?q=x&limit=9", "invalid_limit", 400},
		{"invalid-type", "/api/v1/search?type=unknown&q=x", "invalid_search_type", 400},
	} {
		f.error("artists/errors/"+tc.name, "GET", tc.path, nil, tc.status, tc.code)
	}
	r := f.call("GET", "/api/v1/artists", nil, nil)
	requireAPIError(f.t, r, 401, "unauthorized")
	f.samples.record("artists/errors/unauthorized", r, 401)
	rename := map[string]any{"expected_revision": 1, "mode": "set", "name": "新名字"}
	r = f.call("PATCH", base+"/name", rename, &f.member)
	requireAPIError(f.t, r, 403, "forbidden")
	f.samples.record("artists/errors/forbidden", r, 403)
	for _, tc := range []struct{ name, origin string }{{"origin-required", ""}, {"origin-forbidden", "http://evil.example"}} {
		r = f.call("PATCH", base+"/name", rename, &f.admin, map[string]string{"Origin": tc.origin})
		code := "origin_forbidden"
		if tc.origin == "" {
			code = "origin_required"
		}
		requireAPIError(f.t, r, 403, code)
		f.samples.record("artists/errors/"+tc.name, r, 403)
	}
}

func recordArtistGovernanceSamples(f *artistSamples) {
	t := f.t
	base := "/api/v1/artists/" + f.primary
	artistSampleItems(t, f.get("favorites/artists-empty", favoritesURL+"/artists"), 0)
	f.record("favorites/artist-added", "PUT", favoritesURL+"/artists/"+f.primary, nil, 200)
	if r := f.call("PUT", favoritesURL+"/artists/"+f.alias, nil, &f.admin); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	artistSampleItems(t, f.get("favorites/artists", favoritesURL+"/artists"), 2)
	lookup := f.record("favorites/artist-lookup", "POST", favoritesURL+"/lookup", map[string]any{"artist_ids": []string{f.primary, f.alias}, "track_ids": []string{f.releases[0].trackIDs[0]}}, 200)
	statuses := lookup["artists"].([]any)
	if len(statuses) != 2 {
		t.Fatal("批量收藏未返回艺人")
	}
	for _, item := range statuses {
		status := item.(map[string]any)
		if status["favorited"] != true || status["artist_id"] != status["resolved_artist_id"] {
			t.Fatal("批量收藏的艺人解析或状态错误")
		}
	}
	merge := map[string]any{"expected_revision": f.revision(f.primary), "source_artist_id": f.alias, "source_expected_revision": f.revision(f.alias)}
	key := uuid.New()
	r := f.call("POST", base+"/merge", merge, &f.admin, map[string]string{"Idempotency-Key": key})
	f.samples.record("governance/artist-merge", r, 200)
	merged := decodeJSONMap(t, r)
	if merged["source"].(map[string]any)["redirect_artist_id"] != f.primary {
		t.Fatal("合并未重定向来源艺人")
	}
	replay := f.call("POST", base+"/merge", merge, &f.admin, map[string]string{"Idempotency-Key": key})
	var originalBody, replayBody any
	if err := json.Unmarshal(r.Body.Bytes(), &originalBody); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(replay.Body.Bytes(), &replayBody); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(originalBody, replayBody) {
		t.Fatalf("合并幂等重放改变响应：%s / %s", r.Body.String(), replay.Body.String())
	}
	f.samples.record("governance/artist-merge-replayed", replay, 200)
	changed := map[string]any{"expected_revision": 2, "source_artist_id": f.same, "source_expected_revision": 1}
	r = f.call("POST", base+"/merge", changed, &f.admin, map[string]string{"Idempotency-Key": key})
	requireAPIError(t, r, 409, "idempotency_conflict")
	f.samples.record("artists/errors/idempotency-conflict", r, 409)
	body := f.get("artists/detail-redirect", "/api/v1/artists/"+f.alias)
	if body["redirect_artist_id"] != f.primary {
		t.Fatal("旧艺人ID没有重定向提示")
	}
	dedup := artistSampleItems(t, f.get("favorites/artists-merged-deduplicated", favoritesURL+"/artists"), 1)
	if dedup[0].(map[string]any)["artist"].(map[string]any)["id"] != f.primary {
		t.Fatal("合并后收藏未去重到目标")
	}
	for _, tc := range []struct {
		name, kind string
		count      int
	}{{"artists-alias-match", "artists", 1}, {"albums-alias-match", "albums", 2}, {"tracks-alias-match", "tracks", 3}} {
		body = f.get("search/"+tc.name, searchURL(tc.kind, "Teresa Teng"))
		items := artistSampleItems(t, body, tc.count)
		if tc.kind == "artists" && items[0].(map[string]any)["match"].(map[string]any)["field"] != "artist_alias" {
			t.Fatalf("别名匹配没有说明：%v", items)
		}
	}
	f.error("artists/errors/same-artist", "POST", base+"/merge", map[string]any{"expected_revision": 2, "source_artist_id": f.primary, "source_expected_revision": 2}, 409, "same_artist")
	f.error("artists/errors/artist-redirected", "PATCH", "/api/v1/artists/"+f.alias+"/name", map[string]any{"expected_revision": 2, "mode": "set", "name": "错误目标"}, 409, "artist_redirected")
	f.error("artists/errors/revision-conflict", "PATCH", base+"/name", map[string]any{"expected_revision": 1, "mode": "set", "name": "过期名称"}, 409, "revision_conflict")
	f.error("artists/errors/invalid-artist-name", "PATCH", base+"/name", map[string]any{"expected_revision": 2, "mode": "set", "name": " "}, 400, "invalid_artist_name")
	f.error("artists/errors/no-changes", "PATCH", base+"/name", map[string]any{"expected_revision": 2, "mode": "auto"}, 400, "no_changes")
	renamed := f.record("governance/artist-name-set", "PATCH", base+"/name", map[string]any{"expected_revision": 2, "mode": "set", "name": "邓丽君精选"}, 200)
	if renamed["display_name"] != "邓丽君精选" || renamed["name_source"] != "manual" {
		t.Fatal("改名没有设置人工主名")
	}
	automatic := f.record("governance/artist-name-auto", "PATCH", base+"/name", map[string]any{"expected_revision": 3, "mode": "auto"}, 200)
	if automatic["name_source"] != "auto" {
		t.Fatal("未恢复自动名称")
	}
	scope := map[string]any{"mode": "aliases", "name_key_ids": []string{f.key(f.alias)}}
	preview := f.record("governance/artist-split-preview-aliases", "POST", base+"/split-preview", scope, 200)
	if preview["scope"].(map[string]any)["track_count"].(float64) != 1 {
		t.Fatal("别名拆分范围不真实")
	}
	split := map[string]any{"expected_revision": f.revision(f.primary), "mode": "aliases", "name_key_ids": scope["name_key_ids"], "basis": preview["basis"], "target": map[string]any{"kind": "restore", "artist_id": f.alias, "expected_revision": f.revision(f.alias)}}
	bad := map[string]any{}
	for k, v := range split {
		bad[k] = v
	}
	bad["basis"] = strings.Repeat("0", 64)
	f.error("artists/errors/artist-scope-changed", "POST", base+"/split", bad, 409, "artist_scope_changed")
	restored := f.record("governance/artist-split-restored-id", "POST", base+"/split", split, 200)
	if restored["target"].(map[string]any)["id"] != f.alias {
		t.Fatal("拆分没有恢复旧ID")
	}
	artistSampleItems(t, f.get("favorites/artists-restored", favoritesURL+"/artists"), 2)
	// 按专辑拆分同名人；预览必须只包含当前选择的发行。
	sameBase := "/api/v1/artists/" + f.same
	albumScope := map[string]any{"mode": "albums", "name_key_ids": []string{f.key(f.same)}, "group_ids": []string{f.releases[2].groupID}}
	albumPreview := f.record("governance/artist-split-preview-albums", "POST", sameBase+"/split-preview", albumScope, 200)
	if albumPreview["scope"].(map[string]any)["release_count"] != float64(1) {
		t.Fatal("专辑预览不是一张发行")
	}
	newSplit := map[string]any{"expected_revision": f.revision(f.same), "mode": "albums", "name_key_ids": albumScope["name_key_ids"], "group_ids": albumScope["group_ids"], "basis": albumPreview["basis"], "target": map[string]any{"kind": "new", "display_name": "同名艺人甲"}}
	created := f.record("governance/artist-split-new", "POST", sameBase+"/split", newSplit, 200)
	newID := created["target"].(map[string]any)["id"].(string)
	if newID == f.same || newID == "" {
		t.Fatal("拆分没有新身份")
	}
	artistSampleItems(t, f.get("search/same-name-split-results", searchURL("artists", "同名艺人")), 2)
	if r = f.call("PUT", favoritesURL+"/artists/"+f.missing, nil, &f.admin); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	unavailable := f.get("favorites/artist-unavailable", favoritesURL+"/artists")
	items := artistSampleItems(t, unavailable, 3)
	found := false
	for _, item := range items {
		m := item.(map[string]any)
		if m["artist"].(map[string]any)["id"] == f.missing {
			found = true
			if m["available"] != false {
				t.Fatal("缺失收藏未标不可用")
			}
		}
	}
	if !found {
		t.Fatal("不可用收藏被隐藏")
	}
	f.record("favorites/artist-removed", "DELETE", favoritesURL+"/artists/"+f.primary, nil, 200)
	if len(decodeJSONMap(t, f.call("GET", favoritesURL+"/artists", nil, &f.admin))["items"].([]any)) != 2 {
		t.Fatal("取消收藏没有生效")
	}
	ops := artistSampleItems(t, f.get("operations/artists", "/api/v1/operations"), 5)
	for _, item := range ops {
		m := item.(map[string]any)
		if m["artist"] == nil || m["revertible"] != false {
			t.Fatal("艺人操作缺少目标或错误允许撤销")
		}
	}
	artistSampleItems(t, f.get("operations/artist-source-filter", "/api/v1/operations?artist_id="+f.alias), 2)
	// 加入独立发行操作后取并集，不能用单一艺人结果假充联合筛选。
	correction := map[string]any{"expected_revision": 1, "changes": map[string]any{"title": map[string]any{"mode": "set", "value": "专辑人工标题"}}}
	correctionResponse := f.call("PATCH", "/api/v1/releases/"+f.releases[3].releaseID+"/corrections", correction, &f.admin)
	if correctionResponse.Code != 200 {
		t.Fatalf("准备联合操作筛选：%d %s", correctionResponse.Code, correctionResponse.Body.String())
	}
	union := f.get("operations/artist-union-filter", "/api/v1/operations?artist_id="+f.alias+"&release_id="+f.releases[3].releaseID)
	artistSampleItems(t, union, 3)
	f.error("artists/errors/not-revertible", "POST", "/api/v1/operations/"+merged["operation_id"].(string)+"/revert", map[string]any{}, 409, "not_revertible")
	mustExec(t, f.app, `UPDATE operations SET after_state='{}'::jsonb WHERE id=$1::uuid`, merged["operation_id"])
	damaged := artistSampleItems(t, f.get("operations/artist-damaged-state", "/api/v1/operations?artist_id="+f.alias), 2)
	foundDamaged := false
	for _, item := range damaged {
		row := item.(map[string]any)
		if row["id"] == merged["operation_id"] {
			foundDamaged = true
			if row["changes"] != nil || row["revertible"] != false {
				t.Fatal("损坏操作状态未安全降级")
			}
		}
	}
	if !foundDamaged {
		t.Fatal("损坏操作被静默丢失")
	}
	mustExec(t, f.app, `ALTER TABLE artist_profiles RENAME TO artist_profiles_unavailable`)
	f.error("artists/errors/database-unavailable", "GET", "/api/v1/artists", nil, 503, "database_unavailable")
	mustExec(t, f.app, `ALTER TABLE artist_profiles_unavailable RENAME TO artist_profiles`)
}

func recordArtistCandidateSamples(t *testing.T, s *apiSampleRecorder) {
	t.Helper()
	app := newIntegrationTestApplication(t)
	f := &artistSamples{t: t, app: app, samples: s, admin: app.createUser(t, "admin")}
	s.forbid(f.admin.token)
	s.forbid(app.allowedRoot)
	s.forbid(app.dataDirectory)
	// 合成姓名避开演唱与乐器词表，保持括号内容为人名别名的前提。
	for i, name := range []string{"Alpha Example (Beta Example)", "Beta Example", "Alpha Example (Gamma Example)", "Gamma Example"} {
		f.identity(i+20, name)
	}
	directory := filepath.Join(app.allowedRoot, "candidate-album")
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	write := func(second string) {
		for i, name := range []string{"Alpha Example (" + second + ")", second} {
			raw := parsertest.FLACWithAudio(t, [16]byte{9, 1, byte(i + 3)}, []byte{9, 1, byte(i + 3), 4},
				fmt.Sprintf("TITLE=合成候选曲%d", i+1), "ALBUM=候选专辑", "ALBUMARTIST=Alpha Example ("+second+")",
				"ARTIST="+name, fmt.Sprintf("TRACKNUMBER=%d", i+1))
			if err := os.WriteFile(filepath.Join(directory, fmt.Sprintf("%02d.flac", i+1)), raw, 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	write("Beta Example")
	root := f.call("POST", "/api/v1/library-roots", map[string]any{"path": app.allowedRoot}, &f.admin)
	if root.Code != 201 {
		t.Fatalf("登记候选目录：%d %s", root.Code, root.Body.String())
	}
	scan := func() {
		r := f.call("POST", "/api/v1/scans", nil, &f.admin)
		if r.Code != 202 {
			t.Fatal(r.Body.String())
		}
		id := decodeJSONMap(t, r)["id"].(string)
		if status := waitForScanToFinish(t, app, id); status != "succeeded" {
			t.Fatal(status)
		}
	}
	scan()
	f.publish()
	candidates := f.get("governance/artist-candidates", "/api/v1/artist-candidates")
	items := artistSampleItems(t, candidates, 1)
	candidate := items[0].(map[string]any)
	left := candidate["left"].(map[string]any)["id"].(string)
	right := candidate["right"].(map[string]any)["id"].(string)
	if candidate["evidence_count"].(float64) != 1 {
		t.Fatal("候选应恰有一条真实括号别名证据")
	}
	reasons := candidate["reasons"].([]any)
	if len(reasons) != 1 {
		t.Fatalf("候选括号别名理由数量：%v", reasons)
	}
	reason := reasons[0].(map[string]any)
	if reason["kind"] != "bracket_alias" || reason["bracket_side"] != "left" || reason["alias_part"] != "inner" {
		t.Fatalf("候选括号别名理由：%v", reason)
	}
	ignore := func(name string, value bool) {
		body := map[string]any{"expected_revision": f.revision(left), "other_artist_id": right, "other_expected_revision": f.revision(right), "key_pairs": candidate["key_pairs"], "ignored": value}
		result := f.record(name, "POST", "/api/v1/artists/"+left+"/candidate-ignore", body, 200)
		if result["ignored"] != value {
			t.Fatal("忽略状态未改变")
		}
	}
	ignore("governance/artist-ignore", true)
	ignored := artistSampleItems(t, f.get("governance/artist-candidates-ignored", "/api/v1/artist-candidates?ignored=true"), 1)[0].(map[string]any)
	if !reflect.DeepEqual(ignored["key_pairs"], candidate["key_pairs"]) ||
		ignored["left"].(map[string]any)["id"] != left || ignored["right"].(map[string]any)["id"] != right {
		t.Fatal("已忽略候选的身份或名字对发生变化")
	}
	artistSampleItems(t, f.get("governance/artist-candidates-empty", "/api/v1/artist-candidates"), 0)
	scan()
	artistSampleItems(t, f.get("governance/artist-candidates-after-rescan", "/api/v1/artist-candidates"), 0)
	ignore("governance/artist-unignore", false)
	if r := f.call("GET", "/api/v1/artist-candidates", nil, &f.admin); len(decodeJSONMap(t, r)["items"].([]any)) != 1 {
		t.Fatal("取消忽略没有恢复候选")
	}
	artistSampleItems(t, f.get("governance/artist-candidates-ignored-empty", "/api/v1/artist-candidates?ignored=true"), 0)
	f.error("artists/errors/invalid-candidate-filter", "GET", "/api/v1/artist-candidates?ignored=yes", nil, 400, "invalid_filter")
	for _, query := range []string{"ignored=true&ignored=false", "ignored=true&ignored=true"} {
		r := f.call("GET", "/api/v1/artist-candidates?"+query, nil, &f.admin)
		requireAPIError(t, r, 400, "invalid_filter")
	}
	// 恢复后重新读取和完整扫描都必须保留同一名字对；不改变后续样本的治理版本。
	scan()
	for _, query := range []string{"", "?ignored=false"} {
		response := f.call("GET", "/api/v1/artist-candidates"+query, nil, &f.admin)
		if response.Code != 200 {
			t.Fatalf("恢复后读取候选：%d %s", response.Code, response.Body.String())
		}
		restored := artistSampleItems(t, decodeJSONMap(t, response), 1)[0].(map[string]any)
		if !reflect.DeepEqual(restored["key_pairs"], candidate["key_pairs"]) ||
			restored["left"].(map[string]any)["id"] != left || restored["right"].(map[string]any)["id"] != right {
			t.Fatal("恢复后真实重扫丢失候选身份或名字对")
		}
	}
	r := f.call("GET", "/api/v1/artist-candidates?ignored=true", nil, &f.admin)
	if r.Code != 200 {
		t.Fatalf("恢复后读取已忽略：%d %s", r.Code, r.Body.String())
	}
	artistSampleItems(t, decodeJSONMap(t, r), 0)
	// 相同旧键对再次忽略，新原始写法对必须仍能提示。
	body := map[string]any{"expected_revision": f.revision(left), "other_artist_id": right, "other_expected_revision": f.revision(right), "key_pairs": candidate["key_pairs"], "ignored": true}
	if r := f.call("POST", "/api/v1/artists/"+left+"/candidate-ignore", body, &f.admin); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	write("Gamma Example")
	scan()
	fresh := artistSampleItems(t, f.get("governance/artist-candidates-new-key-pair", "/api/v1/artist-candidates"), 1)
	if !strings.Contains(fmt.Sprint(fresh), "Gamma Example") {
		t.Fatal("新键对仍被旧忽略吞掉")
	}
}

func recordArtistQueueLimit(t *testing.T, s *apiSampleRecorder) {
	t.Helper()
	f := newArtistSamples(t)
	s.forbid(f.admin.token)
	s.forbid(f.app.allowedRoot)
	// 只增加合成目录事实，署名由真实后台构建，完整队列走 HTTP；不提交一万项巨型 golden。
	mustExec(t, f.app, `INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,duration_seconds)
 SELECT ('f0000000-0000-4000-8000-'||lpad(n::text,12,'0'))::uuid,t.medium_id,n+10,'边界曲目'||n,'邓丽君',1,t.source_root_id,'queue-limit-'||n||'.flac','present',NOW(),'flac_vorbis_comment',120
 FROM tracks t CROSS JOIN generate_series(1,9999)n WHERE t.id=$1::uuid`, f.releases[0].trackIDs[0])
	f.publish()
	response := f.call("GET", "/api/v1/artists/"+f.primary+"/queue", nil, &f.admin)
	if response.Code != 200 {
		t.Fatalf("超限队列：%d %s", response.Code, response.Body.String())
	}
	var queue catalog.ArtistQueue
	if err := json.Unmarshal(response.Body.Bytes(), &queue); err != nil {
		t.Fatal(err)
	}
	if queue.Total != 10001 || queue.Returned != 10000 || len(queue.Items) != 10000 || queue.Limit != 10000 || !queue.Truncated || queue.MissingTotal != 1 {
		t.Fatalf("queue-truncated计数：total=%d returned=%d missing=%d", queue.Total, queue.Returned, queue.MissingTotal)
	}
	seen := map[string]bool{}
	for _, track := range queue.Items {
		if seen[track.TrackID] || !track.Present {
			t.Fatal("超限队列重复或包含missing")
		}
		seen[track.TrackID] = true
	}
	recordArtistLargeSample(t, f.samples, response)
	t.Logf("artists/queue-truncated：真实 HTTP %d 字节，10001→10000，完整断言通过；仓外落盘由 %s 控制", response.Body.Len(), artistLargeSamplesDirectoryEnv)
	seed := "01000000-0000-4000-8000-000000000001"
	response = f.call("GET", "/api/v1/artists/"+f.primary+"/queue?order=random&seed="+seed, nil, &f.admin)
	if response.Code != 200 {
		t.Fatalf("超限随机队列：%d", response.Code)
	}
	if err := json.Unmarshal(response.Body.Bytes(), &queue); err != nil {
		t.Fatal(err)
	}
	allIDs := append([]string(nil), f.releases[0].trackIDs[:2]...)
	for i := 1; i <= 9999; i++ {
		allIDs = append(allIDs, fmt.Sprintf("f0000000-0000-4000-8000-%012d", i))
	}
	artistSampleShuffle(allIDs, seed)
	if len(queue.Items) != 10000 || !queue.Truncated {
		t.Fatal("随机队列未执行相同容量限制")
	}
	for i, track := range queue.Items {
		if track.TrackID != allIDs[i] {
			t.Fatal("随机队列先截断了候选或排序不稳定")
		}
	}
	// 专辑拆分超过 1,000 个发行，使用小型真实错误样本，不需要十万条轨道。
	mustExec(t, f.app, `INSERT INTO releases(id,group_id,title,artist,album_artist,source_root_id,candidate_kind,candidate_anchor)
 SELECT gen_random_uuid(),r.group_id,'范围占位'||n,'邓丽君','邓丽君',r.source_root_id,'ordinary_directory','scope-'||n FROM releases r CROSS JOIN generate_series(1,1001)n WHERE r.id=$1::uuid`, f.releases[0].releaseID)
	preview := f.call("POST", "/api/v1/artists/"+f.primary+"/split-preview", map[string]any{"mode": "albums", "name_key_ids": []string{f.key(f.primary)}, "group_ids": []string{f.releases[0].groupID}}, &f.admin)
	requireAPIError(t, preview, 409, "artist_scope_too_large")
	s.record("artists/errors/artist-scope-too-large", preview, 409)
}

// 独立入口只更新仓外巨型样本，不调用仓内 recorder.finish，不重写仓内小样本。
func TestPostgreSQLArtistQueueTruncatedResponseSample(t *testing.T) {
	samples := &apiSampleRecorder{t: t, samples: map[string][]byte{}}
	recordArtistQueueLimit(t, samples)
}

func recordArtistLargeSample(t *testing.T, source *apiSampleRecorder, response *httptest.ResponseRecorder) {
	t.Helper()
	directory := os.Getenv(artistLargeSamplesDirectoryEnv)
	if directory == "" {
		return
	}
	// 复用凭据/路径检查与 UUID、时间等规范化，不裁剪 items，也不改写业务字段。
	recorder := &apiSampleRecorder{t: t, samples: map[string][]byte{}, forbidden: source.forbidden}
	recorder.record(artistRequiredLargeSample, response, 200)
	content := recorder.samples[artistRequiredLargeSample]
	path := filepath.Join(directory, filepath.FromSlash(artistRequiredLargeSample)+".json")
	if os.Getenv(updateAPISamplesEnv) == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("已写完整仓外样本：%s（%d字节）", path, len(content))
		return
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取完整仓外样本 %s：%v；首次生成须设置 %s=1", path, err, updateAPISamplesEnv)
	}
	if !bytes.Equal(expected, content) {
		t.Fatalf("完整仓外样本 %s 与真实响应不一致：%s", path, firstDifference(expected, content))
	}
	t.Logf("已核对完整仓外样本：%s（%d字节）", path, len(content))
}

func TestArtistSampleRecorderOwnership(t *testing.T) {
	for _, name := range []string{"artists/detail.json", "artists/errors/invalid-id.json", "governance/artist-merge.json", "operations/artists.json", "favorites/artists.json", "favorites/artist-lookup.json", "search/artists.json", "search/albums-alias-match.json", "catalog/status-all-ready.json", "catalog/status-search-ready.json", "search/artist-correction-normalized-albums-new.json"} {
		t.Run(name, func(t *testing.T) {
			if !(&apiSampleRecorder{scope: "artist-increment"}).ownsSample(name) {
				t.Fatal("艺人样本未被独立套件管理")
			}
			if (&apiSampleRecorder{}).ownsSample(name) || (&apiSampleRecorder{scope: "scan-feedback"}).ownsSample(name) {
				t.Fatal("其他套件会清理艺人样本")
			}
		})
	}
	for _, name := range []string{"favorites/tracks.json", "search/tracks.json", "operations/list.json", "releases/get.json", "scan-feedback/report.json"} {
		if (&apiSampleRecorder{scope: "artist-increment"}).ownsSample(name) {
			t.Fatalf("艺人套件越界管理 %s", name)
		}
	}
}

// 仓内小样本清单；另有 artistRequiredLargeSample 指定的完整巨型样本，启用仓外目录时必须生成或比较。
const artistRequiredSamples = `
catalog/status-search-ready
search/artist-correction-legacy search/artist-correction-normalized
search/artist-correction-legacy-albums-new search/artist-correction-legacy-tracks-new
search/artist-correction-legacy-albums-old search/artist-correction-legacy-tracks-old
search/artist-correction-normalized-albums-new search/artist-correction-normalized-tracks-new
search/artist-correction-normalized-albums-old search/artist-correction-normalized-tracks-old
search/artist-correction-normalized-artists
artists/list-preparing artists/detail-preparing artists/queue-preparing
artists/list-name artists/list-album-count artists/list-role artists/list-initial artists/list-empty artists/list-out-of-range
artists/detail artists/detail-unavailable artists/detail-redirect artists/names artists/names-out-of-range
artists/albums artists/role-tracks artists/role-tracks-missing artists/queue artists/queue-empty artists/queue-random
governance/artist-merge governance/artist-merge-replayed governance/artist-split-preview-aliases governance/artist-split-preview-albums
governance/artist-split-new governance/artist-split-restored-id governance/artist-name-set governance/artist-name-auto
governance/artist-candidates governance/artist-candidates-empty governance/artist-ignore governance/artist-unignore
governance/artist-candidates-after-rescan governance/artist-candidates-new-key-pair
governance/artist-candidates-ignored governance/artist-candidates-ignored-empty artists/errors/invalid-candidate-filter
governance/artist-merge-not-ready governance/artist-split-not-ready governance/artist-rename-not-ready
operations/artists operations/artist-source-filter operations/artist-union-filter operations/artist-damaged-state
favorites/artists favorites/artists-empty favorites/artist-added favorites/artist-removed favorites/artist-unavailable
favorites/artists-merged-deduplicated favorites/artists-restored favorites/artists-preparing favorites/artist-lookup
search/suggestions-artists search/artists search/artists-alias-match search/albums-alias-match search/tracks-alias-match
search/same-name-split-results search/artists-preparing catalog/status-artists-preparing catalog/status-all-ready
artists/errors/unauthorized artists/errors/forbidden artists/errors/origin-required artists/errors/origin-forbidden
artists/errors/invalid-query artists/errors/invalid-id artists/errors/not-found artists/errors/invalid-role artists/errors/invalid-initial
artists/errors/invalid-sort artists/errors/invalid-seed artists/errors/invalid-order artists/errors/invalid-pagination
artists/errors/invalid-limit artists/errors/invalid-type artists/errors/same-artist artists/errors/artist-redirected
artists/errors/revision-conflict artists/errors/idempotency-conflict artists/errors/invalid-artist-name artists/errors/no-changes
artists/errors/artist-scope-changed artists/errors/artist-scope-too-large artists/errors/not-revertible artists/errors/database-unavailable
`

// 与公开队列合同一致：在全部候选上以 seed/ID 的 MD5 顺序排序，再截断。
func artistSampleShuffle(ids []string, seed string) {
	sort.Slice(ids, func(i, j int) bool {
		left, right := fmt.Sprintf("%x", md5.Sum([]byte(seed+":"+ids[i]))), fmt.Sprintf("%x", md5.Sum([]byte(seed+":"+ids[j])))
		if left == right {
			return ids[i] < ids[j]
		}
		return left < right
	})
}
