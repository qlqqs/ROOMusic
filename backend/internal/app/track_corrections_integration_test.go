package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

const (
	scanTrackTitle   = "原始序曲"
	scanTrackArtist  = "原始演奏者"
	fixedTrackTitle  = "修订终曲"
	fixedTrackArtist = "新版演奏者"
)

type trackCorrectionFixture struct {
	app           *integrationTestApplication
	admin, member integrationTestUser
	searchReleaseFixture
}

func newTrackCorrectionFixture(t *testing.T, statuses ...string) trackCorrectionFixture {
	t.Helper()
	app := newIntegrationTestApplication(t)
	f := trackCorrectionFixture{app: app, admin: app.createUser(t, "admin"), member: app.createUser(t, "user")}
	f.searchReleaseFixture = insertSearchRelease(t, app, searchRoot(t, app), groupedRelease{title: "合成发行", artist: "专辑艺人", tracks: statuses})
	mustExec(t, app, `UPDATE tracks SET title=$2,artist=$3,duration_seconds=200,codec='flac',sample_rate=44100,channels=2 WHERE id=$1::uuid`, f.trackIDs[0], scanTrackTitle, scanTrackArtist)
	return f
}

func trackCorrectionPath(id string) string { return "/api/v1/tracks/" + id + "/corrections" }

func (f trackCorrectionFixture) trackState(t *testing.T, id string) governance.TrackCorrectionState {
	t.Helper()
	var state governance.TrackCorrectionState
	getJSON(t, f.app, &f.admin, trackCorrectionPath(id), &state)
	return state
}

func (f trackCorrectionFixture) patchTrack(t *testing.T, id string, revision int, changes map[string]any) governance.TrackCorrectionResult {
	t.Helper()
	response := f.app.request(t, http.MethodPatch, trackCorrectionPath(id), correctionBody(revision, changes), &f.admin, idempotencyHeader(uuid.New()))
	return requireTrackCorrectionResult(t, response)
}

func requireTrackCorrectionResult(t *testing.T, response *httptest.ResponseRecorder) governance.TrackCorrectionResult {
	t.Helper()
	var result governance.TrackCorrectionResult
	if response.Code != http.StatusOK {
		t.Fatalf("修正曲目返回 HTTP %d：%s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || !uuid.Valid(result.OperationID) {
		t.Fatalf("曲目修正回执无效：%s，%v", response.Body.String(), err)
	}
	return result
}

func requireTrackCorrectionFields(t *testing.T, state governance.TrackCorrectionState, revision int64, scanned, effective [2]string, corrected [2]bool) {
	t.Helper()
	if state.Revision != revision || len(state.Fields) != 2 {
		t.Fatalf("曲目修正的版本或字段不匹配：%+v，期望版本 %d 与两个字段", state, revision)
	}
	for i, name := range []string{"title", "artist"} {
		field := state.Fields[i]
		if field.Field != name || field.ScanValue != scanned[i] || field.EffectiveValue != effective[i] || (field.Correction != nil) != corrected[i] {
			t.Errorf("字段 %s 实际为 %+v，期望扫描值 %q、有效值 %q、人工修正 %v", name, field, scanned[i], effective[i], corrected[i])
		}
		if corrected[i] && field.Correction != nil && (field.Correction.Mode != "set" || field.Correction.Value != effective[i]) {
			t.Errorf("字段 %s 的人工修正为 %+v，期望 set %q", name, field.Correction, effective[i])
		}
	}
}

func (f trackCorrectionFixture) revertTrack(t *testing.T, operationID string) *httptest.ResponseRecorder {
	t.Helper()
	return f.app.request(t, http.MethodPost, revertPath(operationID), nil, &f.admin, idempotencyHeader(uuid.New()))
}

func TestPostgreSQLTrackCorrectionAPI(t *testing.T) {
	f := newTrackCorrectionFixture(t, "present", "present", "missing")
	track, sibling, missing := f.trackIDs[0], f.trackIDs[1], f.trackIDs[2]
	unknown := uuid.New()
	valid := correctionBody(1, map[string]any{"title": setChange(fixedTrackTitle), "artist": setChange(fixedTrackArtist)})
	badChange := correctionBody(1, map[string]any{"title": nullChange})
	for _, tc := range []struct {
		name, id, code string
		user           *integrationTestUser
		status         int
	}{
		{"未登录先于 ID", "bad-id", "unauthorized", nil, 401},
		{"角色先于 ID", "bad-id", "forbidden", &f.member, 403},
		{"非法 ID", "bad-id", "invalid_id", &f.admin, 400},
		{"未知曲目", unknown, "not_found", &f.admin, 404},
		{"缺失曲目", missing, "not_found", &f.admin, 404},
	} {
		t.Run("GET/"+tc.name, func(t *testing.T) {
			requireAPIError(t, f.app.request(t, http.MethodGet, trackCorrectionPath(tc.id), nil, tc.user, nil), tc.status, tc.code)
		})
	}
	key := uuid.New()
	foreignOrigin, missingOrigin := "http://elsewhere.test", ""
	for _, tc := range []struct {
		name, id, key, code string
		user                *integrationTestUser
		body                any
		origin              *string
		status              int
	}{
		{"跨源先于登录", "bad-id", "", "origin_forbidden", nil, nil, &foreignOrigin, 403},
		{"缺少来源先于登录", "bad-id", "", "origin_required", nil, nil, &missingOrigin, 403},
		{"未登录先于 ID", "bad-id", "", "unauthorized", nil, nil, nil, 401},
		{"角色先于 ID", "bad-id", "", "forbidden", &f.member, nil, nil, 403},
		{"非法 ID 先于键", "bad-id", "", "invalid_id", &f.admin, badChange, nil, 400},
		{"缺少键先于字段", track, "", "invalid_input", &f.admin, badChange, nil, 400},
		{"非法键", track, "bad-key", "invalid_input", &f.admin, valid, nil, 400},
		{"非法版本先于字段", track, key, "invalid_input", &f.admin, correctionBody(0, map[string]any{"title": nullChange}), nil, 400},
		{"字段校验先于查询", unknown, key, "invalid_correction", &f.admin, badChange, nil, 400},
		{"艺人不能为空", track, key, "invalid_correction", &f.admin, correctionBody(1, map[string]any{"artist": setChange(" ")}), nil, 400},
		{"未知字段", track, key, "invalid_correction", &f.admin, correctionBody(1, map[string]any{"track_number": setChange("2")}), nil, 400},
		{"空修改", track, key, "no_changes", &f.admin, correctionBody(1, map[string]any{}), nil, 400},
		{"无修正可清除", track, key, "no_changes", &f.admin, correctionBody(1, map[string]any{"artist": clearChange}), nil, 400},
		{"未知曲目", unknown, key, "not_found", &f.admin, valid, nil, 404},
		{"缺失曲目", missing, key, "not_found", &f.admin, valid, nil, 404},
	} {
		t.Run("PATCH/"+tc.name, func(t *testing.T) {
			headers := idempotencyHeader(tc.key)
			if tc.origin != nil {
				headers["Origin"] = *tc.origin
			}
			requireAPIError(t, f.app.request(t, http.MethodPatch, trackCorrectionPath(tc.id), tc.body, tc.user, headers), tc.status, tc.code)
		})
	}
	if count := countRows(t, f.app, `SELECT count(*) FROM operations`); count != 0 {
		t.Fatalf("被拒绝的请求留下了 %d 条操作", count)
	}
	scanned := [2]string{scanTrackTitle, scanTrackArtist}
	fixed := [2]string{fixedTrackTitle, fixedTrackArtist}
	requireTrackCorrectionFields(t, f.trackState(t, track), 1, scanned, scanned, [2]bool{})
	patch := func(id, key string, body any) *httptest.ResponseRecorder {
		return f.app.request(t, http.MethodPatch, trackCorrectionPath(id), body, &f.admin, idempotencyHeader(key))
	}
	correctRelease := f.app.request(t, http.MethodPatch, "/api/v1/releases/"+f.releaseID+"/corrections", correctionBody(1, map[string]any{"title": setChange("修正后的发行")}), &f.admin, idempotencyHeader(uuid.New()))
	if correctRelease.Code != 200 {
		t.Fatalf("发行修正返回 HTTP %d：%s", correctRelease.Code, correctRelease.Body.String())
	}
	releaseOperation := decodeJSONMap(t, correctRelease)["operation_id"].(string)
	siblingEdit := f.patchTrack(t, sibling, 1, map[string]any{"title": setChange("第二首修正")})
	first := patch(track, key, valid)
	result := requireTrackCorrectionResult(t, first)
	if result.TrackID != track || result.ReleaseID != f.releaseID {
		t.Fatalf("回执的曲目或发行错误：%+v", result)
	}
	requireTrackCorrectionFields(t, result.TrackCorrectionState, 2, scanned, fixed, [2]bool{true, true})
	requireTrackCorrectionFields(t, f.trackState(t, track), 2, scanned, fixed, [2]bool{true, true})
	if replay := patch(track, strings.ToUpper(key), valid); replay.Code != 200 || !jsonResponsesEqual(t, first, replay) {
		t.Fatalf("同键重放返回 HTTP %d：%s", replay.Code, replay.Body.String())
	}
	requireAPIError(t, patch(sibling, key, valid), 409, "idempotency_conflict")
	requireAPIError(t, patch(track, uuid.New(), valid), 409, "revision_conflict")
	requireAPIError(t, patch(track, uuid.New(), correctionBody(2, map[string]any{"title": setChange(fixedTrackTitle)})), 400, "no_changes")
	var release governance.CorrectionState
	getJSON(t, f.app, &f.admin, "/api/v1/releases/"+f.releaseID+"/corrections", &release)
	if siblingState := f.trackState(t, sibling); siblingState.Revision != 2 || release.Revision != 2 {
		t.Fatalf("另一首曲目版本为 %d、发行版本为 %d，均期望 2", siblingState.Revision, release.Revision)
	}
	for _, id := range []string{releaseOperation, siblingEdit.OperationID} {
		if response := f.revertTrack(t, id); response.Code != 200 || decodeJSONMap(t, response)["revision"] != float64(3) {
			t.Fatalf("撤销独立操作 %s 返回 HTTP %d：%s", id, response.Code, response.Body.String())
		}
	}
	cleared := f.patchTrack(t, track, 2, map[string]any{"title": clearChange, "artist": clearChange})
	requireTrackCorrectionFields(t, cleared.TrackCorrectionState, 3, scanned, scanned, [2]bool{})
}

func TestPostgreSQLTrackCorrectionRevert(t *testing.T) {
	f := newTrackCorrectionFixture(t, "present")
	track := f.trackIDs[0]
	initial := f.patchTrack(t, track, 1, map[string]any{"title": setChange(fixedTrackTitle)})
	second := f.patchTrack(t, track, 2, map[string]any{"title": clearChange, "artist": setChange(fixedTrackArtist)})
	requireAPIError(t, f.revertTrack(t, initial.OperationID), 409, "revert_conflict")
	ctx := context.Background()
	record, err := operations.Load(ctx, f.app.database, second.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	previewer := governance.TrackCorrectionOperations().(operations.RevertPreviewer)
	preview, err := previewer.PreviewRevert(ctx, f.app.database, record)
	want := []operations.PreviewChange{
		{Field: "title", Before: scanTrackTitle, After: fixedTrackTitle},
		{Field: "artist", Before: fixedTrackArtist, After: scanTrackArtist},
	}
	if err != nil || !reflect.DeepEqual(preview, want) {
		t.Fatalf("撤销预览为 %+v，期望 %+v，错误 %v", preview, want, err)
	}
	scanned := [2]string{scanTrackTitle, scanTrackArtist}
	requireTrackCorrectionFields(t, f.trackState(t, track), 3, scanned, [2]string{scanTrackTitle, fixedTrackArtist}, [2]bool{false, true})
	response := f.revertTrack(t, second.OperationID)
	if response.Code != 200 || decodeJSONMap(t, response)["revision"] != float64(4) || decodeJSONMap(t, response)["reverts_operation_id"] != second.OperationID {
		t.Fatalf("撤销曲目修正返回 HTTP %d：%s", response.Code, response.Body.String())
	}
	requireTrackCorrectionFields(t, f.trackState(t, track), 4, scanned, [2]string{fixedTrackTitle, scanTrackArtist}, [2]bool{true, false})
}

func TestPostgreSQLTrackCorrectionRescan(t *testing.T) {
	app := newIntegrationTestApplication(t)
	f := trackCorrectionFixture{app: app, admin: app.createUser(t, "admin")}
	library := createLibraryDirectory(t, app, "track-correction")
	file := filepath.Join(library, "album", "01.flac")
	writeTrack := func(title, artist string) {
		writeSyntheticTrack(t, file, 1, "TITLE="+title, "ARTIST="+artist, "ALBUM=合成发行", "ALBUMARTIST=专辑艺人", "TRACKNUMBER=1")
	}
	writeTrack(scanTrackTitle, scanTrackArtist)
	if response := app.request(t, http.MethodPost, "/api/v1/library-roots", map[string]string{"path": library}, &f.admin, idempotencyHeader(uuid.New())); response.Code != 201 {
		t.Fatalf("登记扫描目录返回 HTTP %d：%s", response.Code, response.Body.String())
	}
	runScan(t, app, &f.admin)
	var track string
	if err := app.database.QueryRow(`SELECT id::text FROM tracks WHERE relative_path='album/01.flac' AND source_status='present'`).Scan(&track); err != nil {
		t.Fatal(err)
	}
	fixed := [2]string{fixedTrackTitle, fixedTrackArtist}
	f.patchTrack(t, track, 1, map[string]any{"title": setChange(fixed[0]), "artist": setChange(fixed[1])})
	rescanned := [2]string{"重扫标题", "重扫艺人"}
	writeTrack(rescanned[0], rescanned[1])
	runScan(t, app, &f.admin)
	requireTrackCorrectionFields(t, f.trackState(t, track), 2, rescanned, fixed, [2]bool{true, true})
	cleared := f.patchTrack(t, track, 2, map[string]any{"title": clearChange, "artist": clearChange})
	requireTrackCorrectionFields(t, cleared.TrackCorrectionState, 3, rescanned, rescanned, [2]bool{})
}

func TestPostgreSQLTrackCorrectionSearch(t *testing.T) {
	for _, mode := range []string{"legacy", "normalized"} {
		t.Run(mode, func(t *testing.T) {
			f := newTrackCorrectionFixture(t, "present")
			track := f.trackIDs[0]
			if mode == "normalized" {
				publishSearch(t, f.app)
			}
			for _, query := range []string{scanTrackTitle, scanTrackArtist} {
				page := requireSearchTracks(t, f.app, &f.member, query, track)
				if page.CatalogState.SearchMode != mode {
					t.Fatalf("搜索模式为 %q，期望 %q", page.CatalogState.SearchMode, mode)
				}
			}
			f.patchTrack(t, track, 1, map[string]any{"title": setChange(fixedTrackTitle), "artist": setChange(fixedTrackArtist)})
			for _, query := range []string{fixedTrackTitle, fixedTrackArtist} {
				item := requireSearchTracks(t, f.app, &f.member, query, track).Items[0]
				if item.Title != fixedTrackTitle || item.Artist != fixedTrackArtist {
					t.Errorf("搜索 %q 返回 %+v", query, item)
				}
			}
			for _, query := range []string{scanTrackTitle, scanTrackArtist} {
				requireSearchTracks(t, f.app, &f.member, query)
			}
		})
	}
}

func TestPostgreSQLTrackCorrectionArtistFacts(t *testing.T) {
	f := newTrackCorrectionFixture(t, "present")
	track := f.trackIDs[0]
	publishSearch(t, f.app)
	store := catalog.NewStore(f.app.database)
	assertCredits := func(layer, name, artistID string) {
		t.Helper()
		var count int
		var gotLayer, gotName, gotArtistID string
		if err := f.app.database.QueryRow(`SELECT count(*),COALESCE(min(c.layer),''),COALESCE(min(n.spelling),''),COALESCE(min(e.artist_id::text),'')
			FROM artist_effective_credits e JOIN artist_credits c ON c.id=e.credit_id JOIN artist_names n ON n.id=e.name_id
			WHERE e.track_id=$1::uuid AND e.role='track_artist'`, track).Scan(&count, &gotLayer, &gotName, &gotArtistID); err != nil {
			t.Fatalf("读取有效曲目署名：%v（数量 %d）", err, count)
		}
		if count != 1 || gotLayer != layer || gotName != name || gotArtistID != artistID {
			t.Fatalf("有效曲目署名为 %d 条 %q/%q/%q，期望 1 条 %q/%q/%q", count, gotLayer, gotName, gotArtistID, layer, name, artistID)
		}
		summaries, err := store.LoadTrackSummaries(context.Background(), []string{track})
		if err != nil {
			t.Fatal(err)
		}
		requireSearchArtistRefs(t, summaries[track].ArtistRefs, artistID, name)
		var detail catalog.ReleaseDetail
		getJSON(t, f.app, &f.member, "/api/v1/releases/"+f.releaseID, &detail)
		requireSearchArtistRefs(t, detail.Media[0].Tracks[0].ArtistRefs, artistID, name)
	}
	assertArtistPage := func(artistID, name string, count int) {
		t.Helper()
		path := "/api/v1/artists/" + artistID
		var profile catalog.ArtistDetail
		getJSON(t, f.app, &f.member, path, &profile)
		if profile.Artist.ID != artistID || profile.Artist.Name != name || profile.Artist.TrackCount != int64(count) {
			t.Errorf("艺人 %q 的概要为 %+v，期望曲目数 %d", name, profile.Artist, count)
		}
		var page catalog.ArtistPage[catalog.TrackSummary]
		getJSON(t, f.app, &f.member, path+"/tracks?role=track_artist", &page)
		if len(page.Items) != count || (count == 1 && (page.Items[0].TrackID != track || page.Items[0].Artist != name)) {
			t.Fatalf("艺人 %q 的曲目页为 %+v", name, page)
		}
	}
	scannedID := effectiveTrackArtistID(t, f.app, track, scanTrackArtist)
	assertCredits("scan", scanTrackArtist, scannedID)
	assertArtistPage(scannedID, scanTrackArtist, 1)
	f.patchTrack(t, track, 1, map[string]any{"artist": setChange(scanTrackArtist)})
	assertCredits("correction", scanTrackArtist, scannedID)
	f.patchTrack(t, track, 2, map[string]any{"artist": clearChange})
	changed := f.patchTrack(t, track, 3, map[string]any{"artist": setChange(fixedTrackArtist)})
	fixedID := effectiveTrackArtistID(t, f.app, track, fixedTrackArtist)
	if fixedID == scannedID {
		t.Fatal("不同署名解析成同一个艺人")
	}
	assertCredits("correction", fixedTrackArtist, fixedID)
	assertArtistPage(fixedID, fixedTrackArtist, 1)
	assertArtistPage(scannedID, scanTrackArtist, 0)
	if response := f.revertTrack(t, changed.OperationID); response.Code != 200 {
		t.Fatalf("撤销艺人修正返回 HTTP %d：%s", response.Code, response.Body.String())
	}
	assertCredits("scan", scanTrackArtist, scannedID)
	assertArtistPage(scannedID, scanTrackArtist, 1)
	assertArtistPage(fixedID, fixedTrackArtist, 0)
}

func effectiveTrackArtistID(t *testing.T, app *integrationTestApplication, track, name string) string {
	t.Helper()
	var id string
	if err := app.database.QueryRow(`SELECT e.artist_id::text FROM artist_effective_credits e JOIN artist_names n ON n.id=e.name_id
		WHERE e.track_id=$1::uuid AND e.role='track_artist' AND n.spelling=$2`, track, name).Scan(&id); err != nil {
		t.Fatalf("读取曲目艺人 %q：%v", name, err)
	}
	return id
}
