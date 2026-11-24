package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/artwork"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

func artistMediaCommand(t *testing.T, app *integrationTestApplication, user *integrationTestUser, method, artist, path, key string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return app.request(t, method, "/api/v1/artists/"+artist+path, body, user, idempotencyHeader(key))
}

func artistImageUpload(t *testing.T, app *integrationTestApplication, user *integrationTestUser, artist, key string, revision int64, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	requireCurationOK(t, form.WriteField("expected_revision", fmt.Sprint(revision)))
	part, err := form.CreateFormFile("image", "portrait.png")
	requireCurationOK(t, err)
	_, err = part.Write(data)
	requireCurationOK(t, err)
	requireCurationOK(t, form.Close())

	req := httptest.NewRequest(http.MethodPut, app.origin+"/api/v1/artists/"+artist+"/image", &body)
	req.Header.Set("Origin", app.origin)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("Idempotency-Key", key)
	req.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: user.token})
	response := httptest.NewRecorder()
	app.handler.ServeHTTP(response, req)
	return response
}

func requireArtistMediaEdit(t *testing.T, response *httptest.ResponseRecorder, artist, layer string, revision int64) string {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("艺人编辑 HTTP %d：%s", response.Code, response.Body)
	}
	var receipt struct {
		OperationID string `json:"operation_id"`
		ArtistID    string `json:"artist_id"`
		Revision    int64  `json:"revision"`
		Layer       string `json:"layer"`
	}
	requireCurationOK(t, json.Unmarshal(response.Body.Bytes(), &receipt))
	if !uuid.Valid(receipt.OperationID) || receipt.ArtistID != artist || receipt.Layer != layer || receipt.Revision != revision {
		t.Fatalf("艺人编辑回执不符：%+v，期望艺人=%s 层=%q 版本=%d", receipt, artist, layer, revision)
	}
	return receipt.OperationID
}

func rejectArtistField(t *testing.T, f *artistMediaProgram, field string, revision int64) *httptest.ResponseRecorder {
	t.Helper()
	return artistMediaCommand(t, f.application, &f.admin, "POST", f.artist, "/media/reject", uuid.New(),
		map[string]any{"field": field, "expected_revision": revision})
}

func seedArtistPhoto(t *testing.T, f *artistMediaProgram, data []byte) string {
	t.Helper()
	applied, err := f.writer.ApplyArtistImage(t.Context(), curation.ArtistImageRequest{ArtistID: f.artist, MBID: artistMediaMBID, Key: uuid.New(),
		Image: sources.ArtistImage{ID: "synthetic-portrait", Data: data,
			Attribution: sources.Attribution{Source: "fanarttv", SourceURL: "https://fanart.tv/artist/synthetic"}}})
	return requireExternalApply(t, applied, err)
}

func TestPostgreSQLArtistManualLayerAndUndo(t *testing.T) {
	f := newArtistMediaProgram(t, mediaUpstream{})
	app := f.application
	linkArtistMedia(t, app, f.artist, artistMediaMBID)
	autoBytes := externalFixturePNG(t, 2, 2)
	seedArtistPhoto(t, f, autoBytes)
	_, err := f.writer.ApplyArtistBio(t.Context(), curation.ArtistBioRequest{ArtistID: f.artist, MBID: artistMediaMBID, Key: uuid.New(),
		Bio: sources.ArtistBio{ID: "zh:Fixture", Text: "自动简介", Lang: "zh",
			Attribution: sources.Attribution{Source: "wikipedia", SourceURL: "https://zh.wikipedia.org/wiki/Fixture"}}})
	requireCurationOK(t, err)
	auto := f.media(t)
	if auto.Image == nil || auto.Bio == nil {
		t.Fatalf("缺少自动层：%+v", auto)
	}

	manual := func(text, lang string, revision int64) string {
		t.Helper()
		response := artistMediaCommand(t, app, &f.admin, "PUT", f.artist, "/bio", uuid.New(),
			map[string]any{"expected_revision": revision, "text": text, "lang": lang})
		return requireArtistMediaEdit(t, response, f.artist, "", revision+1)
	}
	assertManualBio := func(text, lang string) {
		t.Helper()
		bio := f.media(t).Bio
		if bio == nil || bio.Source != "manual" || bio.Text != text || bio.Lang != lang || bio.SourceURL != nil || bio.License != nil || bio.LicenseURL != nil {
			t.Fatalf("人工简介应为 %q (%s)，得到 %+v", text, lang, bio)
		}
	}
	initialBio := strings.Repeat("潮", 4000)
	bioOp := manual("  "+initialBio+"\n", "zh", auto.Revision)
	assertManualBio(initialBio, "zh")
	replacementBio := manual("A synthetic biography.", "en", f.media(t).Revision)
	assertManualBio("A synthetic biography.", "en")
	requireAPIError(t, f.revert(t, &f.admin, bioOp, uuid.New()), 409, "revert_conflict")
	revision := f.media(t).Revision
	clearBio := requireArtistMediaEdit(t, rejectArtistField(t, f, "bio", revision), f.artist, "manual", revision+1)
	if got := f.media(t).Bio; !reflect.DeepEqual(got, auto.Bio) {
		t.Fatalf("清除人工简介后应显示原自动简介，得到 %+v", got)
	}
	f.undo(t, clearBio)
	assertManualBio("A synthetic biography.", "en")
	f.undo(t, replacementBio)
	assertManualBio(initialBio, "zh")
	f.undo(t, bioOp)
	if got := f.media(t).Bio; !reflect.DeepEqual(got, auto.Bio) {
		t.Fatalf("撤销首条人工简介后应显示原自动简介，得到 %+v", got)
	}

	oldBytes, newBytes := externalFixturePNG(t, 3, 4), externalFixturePNG(t, 5, 6)
	oldFile, oldPath := externalArtworkFile(t, app, oldBytes)
	newFile, newPath := externalArtworkFile(t, app, newBytes)
	upload := func(data []byte) string {
		t.Helper()
		revision := f.media(t).Revision
		return requireArtistMediaEdit(t, artistImageUpload(t, app, &f.admin, f.artist, uuid.New(), revision, data), f.artist, "", revision+1)
	}
	assertPhoto := func(want artwork.Stored) {
		t.Helper()
		image := f.media(t).Image
		if image == nil || image.Source != "manual" || image.Resource != want.Key ||
			image.SourceURL != nil || image.Author != nil || image.License != nil || image.LicenseURL != nil {
			t.Fatalf("人工图片应为 %+v，得到 %+v", want, image)
		}
		f.assertCount(t, 1, `SELECT count(*) FROM artist_manual_artworks
			WHERE artist_id=$1 AND storage_key=$2 AND mime=$3 AND width=$4 AND height=$5`, f.artist, want.Key, want.MIMEType, want.Width, want.Height)
	}
	assertRetained := func(stored artwork.Stored, operation string, data []byte) {
		t.Helper()
		f.assertCount(t, 1, "SELECT count(*) FROM artwork_retained WHERE storage_key=$1 AND operation_id=$2", stored.Key, operation)
		store := artwork.NewStore(app.dataDirectory)
		requireCurationOK(t, store.Discard(t.Context(), app.database, stored.Key))
		requireExternalArtworkHTTP(t, app, &f.admin, stored, data)
	}

	imageOp := upload(oldBytes)
	assertPhoto(oldFile)
	replaceImage := upload(newBytes)
	assertPhoto(newFile)
	assertRetained(oldFile, replaceImage, oldBytes)
	requireAPIError(t, f.revert(t, &f.admin, imageOp, uuid.New()), 409, "revert_conflict")
	revision = f.media(t).Revision
	clearImage := requireArtistMediaEdit(t, rejectArtistField(t, f, "image", revision), f.artist, "manual", revision+1)
	assertRetained(newFile, clearImage, newBytes)
	if got := f.media(t).Image; !reflect.DeepEqual(got, auto.Image) {
		t.Fatalf("清除人工图片后应显示原自动图片，得到 %+v", got)
	}
	f.undo(t, clearImage)
	assertPhoto(newFile)
	requireExternalFile(t, newPath, newBytes)
	f.undo(t, replaceImage)
	assertPhoto(oldFile)
	requireExternalFile(t, oldPath, oldBytes)
	f.undo(t, imageOp)
	if got := f.media(t).Image; !reflect.DeepEqual(got, auto.Image) {
		t.Fatalf("撤销首张人工图片后应显示原自动图片，得到 %+v", got)
	}
	f.assertCount(t, 0, "SELECT count(*) FROM curation_suppressions WHERE target_id=$1", f.artist)
}

func TestPostgreSQLArtistManualSharedArtwork(t *testing.T) {
	for _, first := range []string{"manual", "auto"} {
		t.Run(first, func(t *testing.T) {
			f := newArtistMediaProgram(t, mediaUpstream{})
			app := f.application
			linkArtistMedia(t, app, f.artist, artistMediaMBID)
			data := externalFixturePNG(t, 2, 3)
			stored, path := externalArtworkFile(t, app, data)
			auto := seedArtistPhoto(t, f, data)
			revision := f.media(t).Revision
			manual := requireArtistMediaEdit(t, artistImageUpload(t, app, &f.admin, f.artist, uuid.New(), revision, data), f.artist, "", revision+1)
			f.assertCount(t, 0, "SELECT count(*) FROM artwork_retained WHERE storage_key=$1", stored.Key)
			undo, remaining, source := manual, auto, "fanarttv"
			if first == "auto" {
				undo, remaining, source = auto, manual, "manual"
			}
			f.undo(t, undo)
			if image := f.media(t).Image; image == nil || image.Source != source || image.Resource != stored.Key {
				t.Fatalf("先撤 %s 后应保留 %s 的共享图片，得到 %+v", first, source, image)
			}
			requireExternalFile(t, path, data)
			requireExternalArtworkHTTP(t, app, &f.admin, stored, data)
			f.undo(t, remaining)
			if image := f.media(t).Image; image != nil {
				t.Fatalf("两层已撤销，仍返回图片 %+v", image)
			}
			requireExternalFile(t, path, nil)
			if _, err := artwork.NewStore(app.dataDirectory).Lookup(t.Context(), app.database, stored.Key); err == nil {
				t.Fatal("没有引用时 Lookup 仍返回该图片")
			}
		})
	}
}

func TestPostgreSQLArtistManualRejectAuto(t *testing.T) {
	f := newArtistMediaProgram(t, mediaUpstream{fanartStatus: 200, wikiZH: "合成导语"})
	linkArtistMedia(t, f.application, f.artist, artistMediaMBID)
	f.key(t, "fanarttv", "project", "synthetic-project")
	f.run(t)
	first := f.media(t)
	if first.Image == nil || first.Image.Source != "fanarttv" || first.Bio == nil || first.Bio.Source != "wikipedia" {
		t.Fatalf("首轮自动资料不符：%+v", first)
	}
	state := f.stateFor(t, "artist_media", f.artist)
	for _, tc := range []struct{ field, table, source, item string }{
		{"image", "artist_external_artworks", "fanarttv", "fanart-thumb"},
		{"bio", "artist_external_bios", "wikipedia", "https://zh.wikipedia.org/wiki/Fixture"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			app := f.application
			op := programString(t, app, "SELECT operation_id::text FROM "+tc.table+" WHERE artist_id=$1", f.artist)
			revision := f.media(t).Revision
			key := uuid.New()
			body := map[string]any{"field": tc.field, "expected_revision": revision}
			first := artistMediaCommand(t, app, &f.admin, "POST", f.artist, "/media/reject", key, body)
			revert := requireArtistMediaEdit(t, first, f.artist, "auto", revision+1)
			replay := artistMediaCommand(t, app, &f.admin, "POST", f.artist, "/media/reject", key, body)
			if got := requireArtistMediaEdit(t, replay, f.artist, "auto", revision+1); got != revert {
				t.Errorf("自动层撤销重放产生不同回执，首次=%s，重放=%s", revert, got)
			}
			f.assertCount(t, 1, "SELECT count(*) FROM operations WHERE id=$1 AND reverts_operation_id=$2", revert, op)
			curationCRequireMarker(t, f.curationFixture, "artist", f.artist, "artist_"+tc.field,
				tc.source+"\x00"+tc.item, revert, map[string]string{"source": tc.source, "external_id": tc.item})
		})
	}
	f.clock.advance(t, 6*time.Hour)
	if next := f.stateFor(t, "artist_media", f.artist); next.Generation <= state.Generation || next.Status != "no_match" {
		t.Fatalf("撤销后应重新检查并保留空缺，首轮=%+v 下一轮=%+v", state, next)
	}
	if detail := f.media(t); detail.Image != nil || detail.Bio != nil {
		t.Fatalf("已标记不对的资料被写回：%+v", detail)
	}
	requireAPIError(t, rejectArtistField(t, f, "image", f.media(t).Revision), 409, "nothing_to_apply")
}

func TestPostgreSQLArtistManualRejectConcurrentRevert(t *testing.T) {
	f := newArtistMediaProgram(t, mediaUpstream{})
	app := f.application
	linkArtistMedia(t, app, f.artist, artistMediaMBID)
	original := seedArtistPhoto(t, f, externalFixturePNG(t, 2, 3))
	revision := f.media(t).Revision
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	barrier, barrierPID := externalBarrier(t, ctx, app.database, "SELECT 1")
	defer barrier.Rollback()
	requireCurationOK(t, catalog.AcquireArtistWriteGate(ctx, barrier))

	start := func(path string, body any) <-chan *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		requireCurationOK(t, err)
		req := httptest.NewRequestWithContext(ctx, "POST", app.origin+path, bytes.NewReader(raw))
		req.Header.Set("Origin", app.origin)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", uuid.New())
		req.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: f.admin.token})
		result := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			response := httptest.NewRecorder()
			app.handler.ServeHTTP(response, req)
			result <- response
		}()
		return result
	}
	waitBlocked := func(blocker int, events ...string) int {
		t.Helper()
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			var pid int
			var event string
			err := app.database.QueryRowContext(ctx, `WITH waiting AS (
				SELECT pid, wait_event FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid))
				ORDER BY pid LIMIT 1
			) SELECT COALESCE((SELECT pid FROM waiting),0),COALESCE((SELECT wait_event FROM waiting),'')`, blocker).Scan(&pid, &event)
			requireCurationOK(t, err)
			if pid != 0 {
				if !slices.Contains(events, event) {
					t.Fatalf("请求等待 PostgreSQL 事务 %d 的锁事件为 %q，期望 %v", blocker, event, events)
				}
				return pid
			}
			select {
			case <-ctx.Done():
				t.Fatalf("未观察到等待 PostgreSQL 事务 %d 的请求：%v", blocker, ctx.Err())
			case <-ticker.C:
			}
		}
	}

	reverted := start(revertPath(original), nil)
	revertPID := waitBlocked(barrierPID, "advisory")
	rejected := start("/api/v1/artists/"+f.artist+"/media/reject", map[string]any{"field": "image", "expected_revision": revision})
	// 原操作被单条撤销锁住时，标记不对必须先等原操作，之后才申请目录锁。
	waitBlocked(revertPID, "transactionid", "tuple")
	requireCurationOK(t, barrier.Commit())
	if response := awaitExternalResult(t, ctx, reverted); response.Code != 200 {
		t.Fatalf("单条撤销 HTTP %d：%s", response.Code, response.Body)
	}
	requireAPIError(t, awaitExternalResult(t, ctx, rejected), 409, "revert_conflict")
	f.assertCount(t, 1, "SELECT count(*) FROM operations WHERE reverts_operation_id=$1", original)
	f.assertCount(t, 1, "SELECT count(*) FROM curation_suppressions WHERE target_id=$1 AND subject='artist_image'", f.artist)
}

func TestPostgreSQLArtistManualHTTPGuards(t *testing.T) {
	f := newArtistMediaProgram(t, mediaUpstream{})
	app := f.application
	base := "/api/v1/artists/" + f.artist
	revision := f.media(t).Revision
	bio := map[string]any{"expected_revision": revision, "text": "合成简介", "lang": "zh"}
	for _, route := range []struct{ method, path string }{
		{"PUT", "/image"}, {"PUT", "/bio"}, {"PUT", "/links"}, {"POST", "/media/reject"}, {"POST", "/link-preview"},
	} {
		t.Run(route.path, func(t *testing.T) {
			requireAPIError(t, app.request(t, route.method, base+route.path, nil, &f.ordinary, idempotencyHeader(uuid.New())), 403, "forbidden")
			requireAPIError(t, app.request(t, route.method, base+route.path, nil, &f.admin, map[string]string{"Origin": "https://other.example"}), 403, "origin_forbidden")
			if route.path != "/link-preview" {
				requireAPIError(t, app.request(t, route.method, base+route.path, nil, &f.admin, nil), 400, "invalid_input")
			}
		})
	}
	bioPut := func(key string, body any) *httptest.ResponseRecorder {
		t.Helper()
		return artistMediaCommand(t, app, &f.admin, "PUT", f.artist, "/bio", key, body)
	}
	requireAPIError(t, bioPut(uuid.New(), json.RawMessage(`{"text":"重复","text":"简介","lang":"zh","expected_revision":1}`)), 400, "invalid_json")
	requireAPIError(t, bioPut(uuid.New(), map[string]any{"expected_revision": revision, "text": strings.Repeat("a", 64<<10), "lang": "en"}), 400, "invalid_json")
	requireAPIError(t, bioPut(uuid.New(), map[string]any{"expected_revision": revision, "text": strings.Repeat("潮", 4001), "lang": "zh"}), 400, "invalid_input")
	requireAPIError(t, artistImageUpload(t, app, &f.admin, f.artist, uuid.New(), revision, make([]byte, (16<<20)+1)), 413, "invalid_input")
	requireAPIError(t, artistImageUpload(t, app, &f.admin, f.artist, uuid.New(), revision, externalArtworkRequest("").Data), 400, "invalid_input")
	if got := f.media(t); got.Revision != revision || got.Bio != nil || got.Image != nil {
		t.Fatalf("拒绝的写入改变了艺人详情：%+v", got)
	}

	key := uuid.New()
	first := bioPut(key, bio)
	requireArtistMediaEdit(t, first, f.artist, "", revision+1)
	replay := bioPut(key, bio)
	requireArtistMediaEdit(t, replay, f.artist, "", revision+1)
	if !reflect.DeepEqual(decodeJSONMap(t, first), decodeJSONMap(t, replay)) {
		t.Fatalf("同键重放的回执发生变化：%s / %s", first.Body, replay.Body)
	}
	requireAPIError(t, bioPut(uuid.New(), bio), 409, "revision_conflict")
	requireAPIError(t, rejectArtistField(t, f, "bio", revision), 409, "revision_conflict")
	requireAPIError(t, artistImageUpload(t, app, &f.admin, f.artist, uuid.New(), revision, externalFixturePNG(t, 2, 2)), 409, "revision_conflict")
	bio["text"] = "同一个幂等键的新内容"
	requireAPIError(t, bioPut(key, bio), 409, "idempotency_conflict")
	f.assertCount(t, 1, "SELECT count(*) FROM operations WHERE target_id=$1", f.artist)
}
