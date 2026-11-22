package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/artwork"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func requireExternalRevert(t *testing.T, raw json.RawMessage, op, release string, revision int) string {
	t.Helper()
	var response struct {
		ID string `json:"operation_id"`
	}
	if err := json.Unmarshal(raw, &response); err != nil || !uuid.Valid(response.ID) {
		t.Fatalf("撤销响应=%s，错误=%v", raw, err)
	}
	requireExternalJSON(t, raw, map[string]any{"operation_id": response.ID, "reverts_operation_id": op,
		"target_id": release, "target_type": "release", "revision": revision})
	return response.ID
}

func revertExternal(t *testing.T, f *curationFixture, op, release string, revision int) string {
	t.Helper()
	response := f.revert(t, &f.admin, op, uuid.New())
	if response.Code != 200 {
		t.Fatalf("撤销返回 %d %s", response.Code, response.Body)
	}
	id := requireExternalRevert(t, response.Body.Bytes(), op, release, revision)
	_, got, _ := f.state(t, release)
	if got != int64(revision) {
		t.Fatalf("撤销后的人工版本=%d，期望 %d", got, revision)
	}
	return id
}

func externalMarkers(t *testing.T, f *curationFixture, targetType, target, subject, digest, source, extID, revert string, value any) map[string]string {
	t.Helper()
	identity := fmt.Sprintf("%x", sha256.Sum256([]byte(source+"\x00"+extID)))
	rows, err := f.application.database.Query(`SELECT id::text,subject,value_hash,value,operation_id::text,created_by::text
		FROM curation_suppressions WHERE target_type=$1 AND target_id=$2`, targetType, target)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := map[string]string{subject: digest, subject + "_source": identity}
	ids := make(map[string]string, len(want))
	for rows.Next() {
		var id, field, hash, op, actor string
		var raw json.RawMessage
		if err := rows.Scan(&id, &field, &hash, &raw, &op, &actor); err != nil {
			t.Fatal(err)
		}
		if hash != want[field] || op != revert || actor != f.admin.id {
			t.Fatalf("标记=(%s,%s,%s,%s)，期望哈希=%s，操作=%s，操作者=%s", field, hash, op, actor, want[field], revert, f.admin.id)
		}
		delete(want, field)
		requireExternalJSON(t, raw, value)
		ids[field] = id
	}
	if err := rows.Err(); err != nil || len(want) != 0 {
		t.Fatalf("缺少标记=%v，错误=%v", want, err)
	}
	return ids
}

func clearCurationSuppression(t *testing.T, f *curationFixture, id string) {
	t.Helper()
	r := f.application.request(t, "DELETE", curationBase+"/suppressions/"+id, nil, &f.admin, nil)
	if r.Code != 204 || r.Body.Len() != 0 {
		t.Fatalf("清除标记 %s 返回 %d %s", id, r.Code, r.Body)
	}
}

func TestPostgreSQLExternalArtworkRevert(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	app := f.application
	req := externalArtworkRequest(f.a)
	stored, path := externalArtworkFile(t, app, req.Data)
	id := applyExternalArtwork(t, f.writer, req)
	_, revision := f.correct(t, f.a, 1, map[string]any{"title": setChange("人工修改标题")})
	requireAPIError(t, f.revert(t, &f.ordinary, id, uuid.New()), 403, "forbidden")
	externalArtworkState(t, app, f.a, id, stored)
	reverted := revertExternal(t, f, id, f.a, revision)
	externalArtworkState(t, app, f.a, "", artwork.Stored{})
	requireExternalFile(t, path, nil)
	requireAPIError(t, app.request(t, "GET", "/api/v1/artworks/"+stored.Key, nil, &f.admin, nil), 404, "not_found")
	marks := externalMarkers(t, f, "release", f.a, "artwork", stored.Hash, req.Source.Provider, req.ExternalID, reverted,
		map[string]any{"source": "coverartarchive", "external_id": req.ExternalID, "mime_type": "image/gif", "width": 1, "height": 1})

	before := countRows(t, app, "SELECT count(*) FROM operations")
	byContent, byIdentity := req, req
	byContent.Key, byContent.ExternalID = uuid.New(), "different-external-id"
	byIdentity.Key, byIdentity.Data = uuid.New(), append(bytes.Clone(req.Data), 4)
	for _, request := range []curation.ArtworkRequest{byContent, byIdentity} {
		r, err := f.writer.ApplyArtwork(t.Context(), request)
		requireExternalError(t, r, err, 409, "nothing_to_apply", "suppressed")
		_, skippedPath := externalArtworkFile(t, app, request.Data)
		requireExternalFile(t, skippedPath, nil)
		externalArtworkState(t, app, f.a, "", artwork.Stored{})
	}
	if n := countRows(t, app, "SELECT count(*) FROM operations"); n != before {
		t.Fatalf("不再提出命中后的操作数=%d，期望 %d", n, before)
	}
	clearCurationSuppression(t, f, marks["artwork"])
	req.Key = uuid.New()
	next := applyExternalArtwork(t, f.writer, req)
	externalArtworkState(t, app, f.a, next, stored)
	requireExternalFile(t, path, req.Data)
}

func TestPostgreSQLExternalArtworkRevertSharedFile(t *testing.T) {
	for _, reference := range []string{"本地", "外部"} {
		t.Run(reference, func(t *testing.T) {
			f := newCurationFixture(t)
			f.enable(t)
			app := f.application
			req := externalArtworkRequest(f.a)
			stored, path := externalArtworkFile(t, app, req.Data)
			id := applyExternalArtwork(t, f.writer, req)
			var otherOwner string
			store := artwork.NewStore(app.dataDirectory)
			if reference == "本地" {
				if err := store.Save(t.Context(), app.database, f.b, req.Data, "embedded"); err != nil {
					t.Fatal(err)
				}
			} else {
				req.ReleaseID, req.Key = f.b, uuid.New()
				otherOwner = applyExternalArtwork(t, f.writer, req)
			}
			revertExternal(t, f, id, f.a, 1)
			externalArtworkState(t, app, f.a, "", artwork.Stored{})
			requireExternalFile(t, path, req.Data)
			requireExternalArtworkHTTP(t, app, &f.admin, stored, req.Data)
			if otherOwner == "" {
				if err := store.Remove(t.Context(), app.database, f.b); err != nil {
					t.Fatal(err)
				}
			} else {
				externalArtworkState(t, app, f.b, otherOwner, stored)
				revertExternal(t, f, otherOwner, f.b, 1)
			}
			requireExternalFile(t, path, nil)
		})
	}
}

func TestPostgreSQLExternalLyricsRevert(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	app := f.application
	track, _ := externalTrack(t, app, f.a)
	req := externalLyricsRequest(track)
	id := applyExternalLyrics(t, f.writer, req)
	_, revision := f.correct(t, f.a, 1, map[string]any{"title": setChange("人工修改发行")})
	requireAPIError(t, f.revert(t, &f.ordinary, id, uuid.New()), 403, "forbidden")
	// 曲目改挂后，撤销仍按原操作的发行与曲目定位。
	mustExec(t, app, `INSERT INTO media(id,release_id,position) VALUES($1,$2,2)`, uuid.New(), f.b)
	mustExec(t, app, `UPDATE tracks SET medium_id=(SELECT id FROM media WHERE release_id=$2 AND position=2) WHERE id=$1`, track, f.b)
	reverted := revertExternal(t, f, id, f.a, revision)
	if n := countRows(t, app, "SELECT count(*) FROM track_external_lyrics WHERE track_id=$1", track); n != 0 {
		t.Fatalf("撤销后歌词行数=%d，期望 0", n)
	}
	requireExternalLyricsHTTP(t, app, &f.admin, track, "", "", nil)
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(externalNormalizedLyrics)))
	marks := externalMarkers(t, f, "track", track, "lyrics", hash, req.Source.Provider, req.ExternalID, reverted,
		map[string]any{"source": "lrclib", "external_id": "42", "kind": "synced", "lines": 2})

	before := countRows(t, app, "SELECT count(*) FROM operations")
	byContent, byIdentity := req, req
	byContent.Key, byContent.ExternalID = uuid.New(), "43"
	byIdentity.Key, byIdentity.Text = uuid.New(), "同一个编号的不同歌词"
	for _, request := range []curation.LyricsRequest{byContent, byIdentity} {
		r, err := f.writer.ApplyLyrics(t.Context(), request)
		requireExternalError(t, r, err, 409, "nothing_to_apply", "suppressed")
	}
	if n := countRows(t, app, "SELECT count(*) FROM operations"); n != before {
		t.Fatalf("不再提出命中后的操作数=%d，期望 %d", n, before)
	}
	requireExternalLyricsHTTP(t, app, &f.admin, track, "", "", nil)
	clearCurationSuppression(t, f, marks["lyrics"])
	req.Key, req.BatchID = uuid.New(), f.batch(t, "program")
	next := applyExternalLyrics(t, f.writer, req)
	externalRecord(t, f, next, f.b, "track_external_lyrics", req.BatchID, req.Source)
	requireExternalLyricsHTTP(t, app, &f.admin, track, "online", "synced", []map[string]any{
		{"start_ms": 1000, "text": "第一行"}, {"start_ms": 2000, "text": "第二行"},
	})
}

func TestPostgreSQLExternalScopeRevert(t *testing.T) {
	for _, kind := range []string{"batch", "provider"} {
		t.Run(kind, func(t *testing.T) {
			f := newCurationFixture(t)
			f.enable(t)
			app := f.application
			track, _ := externalTrack(t, app, f.a)
			reqArt, reqLyrics := externalArtworkRequest(f.a), externalLyricsRequest(track)
			batch := f.batch(t, "program")
			reqArt.BatchID, reqLyrics.BatchID = batch, batch
			artID := applyExternalArtwork(t, f.writer, reqArt)
			lyricsID := applyExternalLyrics(t, f.writer, reqLyrics)
			_, path := externalArtworkFile(t, app, reqArt.Data)
			control := externalArtworkRequest(f.b)
			control.Source.Provider = "unrelated"
			control.Data = append(control.Data, 17)
			controlID := applyExternalArtwork(t, f.writer, control)
			controlStored, controlPath := externalArtworkFile(t, app, control.Data)
			if err := f.writer.CloseBatch(t.Context(), batch); err != nil {
				t.Fatal(err)
			}
			check := func(scope curation.Scope, want int) {
				t.Helper()
				got := f.scope(t, scope, "", uuid.New(), "external-scope")
				if got.Reverted != want || len(got.Skipped) != 0 || got.NextCursor != nil {
					t.Fatalf("按 %v 撤销=%+v，期望撤销 %d 条", scope, got, want)
				}
			}
			if kind == "batch" {
				check(curation.Scope{Kind: "batch", ID: batch}, 2)
			} else {
				check(curation.Scope{Kind: "provider", Provider: "coverartarchive"}, 1)
				if n := countRows(t, app, "SELECT count(*) FROM track_external_lyrics WHERE operation_id=$1", lyricsID); n != 1 {
					t.Fatalf("按封面来源撤销后的歌词行数=%d，期望 1", n)
				}
				check(curation.Scope{Kind: "provider", Provider: "lrclib"}, 1)
			}
			externalArtworkState(t, app, f.a, "", artwork.Stored{})
			requireExternalFile(t, path, nil)
			requireExternalLyricsHTTP(t, app, &f.admin, track, "", "", nil)
			if n := countRows(t, app, "SELECT count(*) FROM operations WHERE reverts_operation_id IN ($1,$2)", artID, lyricsID); n != 2 {
				t.Fatalf("范围撤销的记录数=%d，期望 2", n)
			}
			_, revision, _ := f.state(t, f.a)
			if revision != 1 {
				t.Fatalf("范围撤销后的人工版本=%d，期望 1", revision)
			}
			externalArtworkState(t, app, f.b, controlID, controlStored)
			requireExternalFile(t, controlPath, control.Data)
		})
	}
}
