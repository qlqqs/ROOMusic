package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/artwork"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

const externalSyncedLyrics = "\uFEFF[00:01.00]第一行\r\n[00:02.00]第二行"
const externalNormalizedLyrics = "[00:01.00]第一行\n[00:02.00]第二行"

func externalArtworkRequest(release string) curation.ArtworkRequest {
	return curation.ArtworkRequest{
		ReleaseID: release, Data: []byte{'G', 'I', 'F', '8', '9', 'a', 1, 0, 1, 0},
		Source:     curation.Source{Kind: "source", Provider: "coverartarchive", Basis: "合成封面依据"},
		ExternalID: "fixture-release/42", Key: uuid.New(),
	}
}

func externalLyricsRequest(track string) curation.LyricsRequest {
	return curation.LyricsRequest{
		TrackID: track, Text: externalSyncedLyrics,
		Source:     curation.Source{Kind: "source", Provider: "lrclib", Basis: "合成歌词依据"},
		ExternalID: "42", Key: uuid.New(),
	}
}

func externalTrack(t *testing.T, app *integrationTestApplication, release string) (id, title string) {
	t.Helper()
	err := app.database.QueryRow(`SELECT t.id::text,t.title FROM tracks t JOIN media m ON m.id=t.medium_id
		WHERE m.release_id=$1 AND t.source_status='present' ORDER BY t.position,t.id LIMIT 1`, release).Scan(&id, &title)
	if err != nil {
		t.Fatal(err)
	}
	return id, title
}

func externalArtworkFile(t *testing.T, app *integrationTestApplication, data []byte) (artwork.Stored, string) {
	t.Helper()
	s, err := artwork.NewStore(app.dataDirectory).Describe(data)
	if err != nil {
		t.Fatal(err)
	}
	return s, filepath.Join(app.dataDirectory, "artwork", s.Key)
}

func requireExternalFile(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if want == nil && os.IsNotExist(err) {
		return
	}
	if err != nil || want == nil || !bytes.Equal(got, want) {
		t.Fatalf("封面文件 %s，字节数=%d，错误=%v，期望字节=%v", path, len(got), err, want)
	}
}

func requireExternalApply(t *testing.T, result curation.ExternalApplied, err error) string {
	t.Helper()
	if err != nil || !uuid.Valid(result.OperationID) || result.Skipped != "" {
		t.Fatalf("补充结果=%+v，错误=%v", result, err)
	}
	return result.OperationID
}

func applyExternalArtwork(t *testing.T, w *curation.Writer, req curation.ArtworkRequest) string {
	t.Helper()
	r, err := w.ApplyArtwork(t.Context(), req)
	return requireExternalApply(t, r, err)
}

func applyExternalLyrics(t *testing.T, w *curation.Writer, req curation.LyricsRequest) string {
	t.Helper()
	r, err := w.ApplyLyrics(t.Context(), req)
	return requireExternalApply(t, r, err)
}

func requireExternalError(t *testing.T, result curation.ExternalApplied, err error, status int, code, skipped string) {
	t.Helper()
	gotStatus, gotCode, _ := operations.Problem(err)
	if err == nil || gotStatus != status || gotCode != code || result.OperationID != "" || result.Skipped != skipped {
		t.Fatalf("补充结果=%+v，错误=%v（%d %s），期望 %d %s，跳过原因=%q", result, err, gotStatus, gotCode, status, code, skipped)
	}
}

func requireExternalJSON(t *testing.T, raw json.RawMessage, want any) {
	t.Helper()
	wantRaw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var gotValue, wantValue any
	if json.Unmarshal(raw, &gotValue) != nil || json.Unmarshal(wantRaw, &wantValue) != nil || !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("JSON=%s，期望 %s", raw, wantRaw)
	}
}

func externalRecord(t *testing.T, f *curationFixture, op, release, kind, batch string, source curation.Source) operations.Record {
	t.Helper()
	r, err := operations.Load(t.Context(), f.application.database, op)
	if err != nil || r.ActorID != f.admin.id || r.Type != kind || r.TargetType != "release" || r.TargetID != release ||
		r.BatchID == nil || *r.BatchID != batch || r.ResultRevision != r.ExpectedRevision+1 {
		t.Fatalf("操作记录=%+v，错误=%v", r, err)
	}
	requireExternalJSON(t, r.Origin, operations.Origin{Source: "curation", ProviderKind: "source", Provider: source.Provider, Basis: source.Basis})
	return r
}

func externalArtworkState(t *testing.T, app *integrationTestApplication, release, owner string, stored artwork.Stored) {
	t.Helper()
	if owner == "" {
		if n := countRows(t, app, `SELECT count(*) FROM release_external_artworks WHERE release_id=$1`, release); n != 0 {
			t.Fatalf("发行 %s 的外部封面行数=%d，期望 0", release, n)
		}
		return
	}
	var got artwork.Stored
	var op string
	err := app.database.QueryRow(`SELECT operation_id::text,storage_key,content_hash,mime_type,width,height
		FROM release_external_artworks WHERE release_id=$1`, release).Scan(&op, &got.Key, &got.Hash, &got.MIMEType, &got.Width, &got.Height)
	if err != nil || op != owner || got != stored {
		t.Fatalf("发行 %s 的外部封面=(%s,%+v,%v)，期望=(%s,%+v)", release, op, got, err, owner, stored)
	}
}

func TestPostgreSQLExternalArtworkApply(t *testing.T) {
	f := newCurationFixture(t)
	app := f.application
	req := externalArtworkRequest(f.a)
	stored, path := externalArtworkFile(t, app, req.Data)
	reject := func(t *testing.T, req curation.ArtworkRequest, status int, code, skipped string) {
		t.Helper()
		before := countRows(t, app, "SELECT count(*) FROM operations")
		r, err := f.writer.ApplyArtwork(t.Context(), req)
		requireExternalError(t, r, err, status, code, skipped)
		if n := countRows(t, app, "SELECT count(*) FROM operations"); n != before {
			t.Fatalf("拒绝写入后操作数=%d，期望 %d", n, before)
		}
	}
	reject(t, req, 409, "curation_disabled", "")
	requireExternalFile(t, path, nil)
	f.enable(t)
	mustExec(t, app, "UPDATE users SET disabled_at=now() WHERE id=$1", f.admin.id)
	reject(t, req, 409, "curation_disabled", "")
	requireExternalFile(t, path, nil)
	mustExec(t, app, "UPDATE users SET disabled_at=NULL WHERE id=$1", f.admin.id)
	closed := f.batch(t, "program")
	if err := f.writer.CloseBatch(t.Context(), closed); err != nil {
		t.Fatal(err)
	}
	req.BatchID = closed
	reject(t, req, 409, "batch_closed", "")
	requireExternalFile(t, path, nil)
	req.BatchID = f.batch(t, "program")
	for _, invalid := range []struct {
		name string
		data []byte
	}{
		{"超过16MiB", append(bytes.Clone(req.Data), make([]byte, 16<<20)...)},
		{"格式不支持", []byte("%PDF-1.7")},
		{"尺寸读不出", []byte("GIF89a")},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			bad := req
			bad.Data = invalid.data
			reject(t, bad, 400, "invalid_input", "")
			files, err := filepath.Glob(filepath.Join(app.dataDirectory, "artwork", "*"))
			if err != nil || len(files) != 0 {
				t.Fatalf("无效图片留下文件 %v，错误=%v", files, err)
			}
		})
	}
	local := append(bytes.Clone(req.Data), 10)
	store := artwork.NewStore(app.dataDirectory)
	if err := store.Save(t.Context(), app.database, f.a, local, "embedded"); err != nil {
		t.Fatal(err)
	}
	reject(t, req, 409, "nothing_to_apply", "local_present")
	requireExternalFile(t, path, nil)
	if err := store.Remove(t.Context(), app.database, f.a); err != nil {
		t.Fatal(err)
	}
	id := applyExternalArtwork(t, f.writer, req)
	record := externalRecord(t, f, id, f.a, operations.TypeReleaseExternalArtwork, req.BatchID, req.Source)
	requireExternalJSON(t, record.Before, map[string]any{"artwork": nil})
	requireExternalJSON(t, record.After, map[string]any{"artwork": map[string]any{
		"storage_key": stored.Key, "content_hash": stored.Hash, "mime_type": "image/gif",
		"width": 1, "height": 1, "source": req.Source.Provider, "external_id": req.ExternalID,
	}})
	externalArtworkState(t, app, f.a, id, stored)
	requireExternalFile(t, path, req.Data)

	replacement := req
	replacement.Key, replacement.ExternalID = uuid.New(), "fixture-release/43"
	replacement.Data = append(bytes.Clone(req.Data), 11)
	next, nextPath := externalArtworkFile(t, app, replacement.Data)
	reject(t, replacement, 409, "nothing_to_apply", "occupied")
	requireExternalFile(t, nextPath, nil)
	externalArtworkState(t, app, f.a, id, stored)

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if replay := applyExternalArtwork(t, f.writer, req); replay != id {
		t.Fatalf("重放操作=%s，期望 %s", replay, id)
	}
	requireExternalFile(t, path, nil)
	if n := countRows(t, app, "SELECT count(*) FROM operations"); n != 1 {
		t.Fatalf("重放后操作数=%d，期望 1", n)
	}
	second := applyExternalArtwork(t, f.writer, replacement)
	record = externalRecord(t, f, second, f.a, operations.TypeReleaseExternalArtwork, req.BatchID, req.Source)
	requireExternalJSON(t, record.Before, map[string]any{"artwork": map[string]string{
		"storage_key": stored.Key, "content_hash": stored.Hash, "source": req.Source.Provider,
		"external_id": req.ExternalID, "operation_id": id,
	}})
	requireAPIError(t, f.revert(t, &f.admin, id, uuid.New()), 409, "revert_conflict")
	externalArtworkState(t, app, f.a, second, next)
	requireExternalFile(t, nextPath, replacement.Data)
	items, _ := f.list(t, "?release_id="+f.a)
	if len(items.Items) != 2 || items.Items[0].ID != second || !items.Items[0].Revertible ||
		items.Items[1].ID != id || items.Items[1].Revertible || items.Items[1].RevertedBy != nil {
		t.Fatalf("原操作失去归属后的列表=%+v", items.Items)
	}
	revertExternal(t, f, second, f.a, 1)
	externalArtworkState(t, app, f.a, "", artwork.Stored{})
	requireExternalFile(t, nextPath, nil)
	requireExternalFile(t, path, nil)
}

func TestPostgreSQLExternalArtworkApplyRollback(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	app := f.application
	req := externalArtworkRequest(f.b)
	stored, path := externalArtworkFile(t, app, req.Data)
	id := applyExternalArtwork(t, f.writer, req)
	mustExec(t, app, `CREATE FUNCTION reject_external_artwork_operation() RETURNS trigger LANGUAGE plpgsql
		AS $$ BEGIN RAISE EXCEPTION 'fixture rejected artwork record'; END $$`)
	mustExec(t, app, `CREATE TRIGGER reject_external_artwork_operation BEFORE INSERT ON operations
		FOR EACH ROW WHEN (NEW.operation_type='release_external_artwork') EXECUTE FUNCTION reject_external_artwork_operation()`)
	req.ReleaseID, req.Key = f.a, uuid.New()
	unique := req
	unique.Data, unique.Key = append(bytes.Clone(req.Data), 3), uuid.New()
	_, uniquePath := externalArtworkFile(t, app, unique.Data)
	for _, request := range []curation.ArtworkRequest{unique, req} {
		r, err := f.writer.ApplyArtwork(t.Context(), request)
		requireExternalError(t, r, err, 500, "operation_failed", "")
		externalArtworkState(t, app, f.a, "", artwork.Stored{})
	}
	requireExternalFile(t, uniquePath, nil)
	requireExternalFile(t, path, req.Data)
	// 补回已有引用的缺失文件后，即使本次关联回滚，清理也应保留补回的文件。
	removeExternalFixture(t, path)
	r, err := f.writer.ApplyArtwork(t.Context(), req)
	requireExternalError(t, r, err, 500, "operation_failed", "")
	externalArtworkState(t, app, f.a, "", artwork.Stored{})
	requireExternalFile(t, path, req.Data)
	externalArtworkState(t, app, f.b, id, stored)
	if n := countRows(t, app, "SELECT count(*) FROM operations"); n != 1 {
		t.Fatalf("回滚后操作数=%d，期望 1", n)
	}
}

func TestPostgreSQLExternalLyricsApply(t *testing.T) {
	f := newCurationFixture(t)
	app := f.application
	track, title := externalTrack(t, app, f.a)
	req := externalLyricsRequest(track)
	reject := func(req curation.LyricsRequest, status int, code, skipped string) {
		t.Helper()
		before := countRows(t, app, "SELECT count(*) FROM operations")
		rows := countRows(t, app, "SELECT count(*) FROM track_external_lyrics")
		r, err := f.writer.ApplyLyrics(t.Context(), req)
		requireExternalError(t, r, err, status, code, skipped)
		if n := countRows(t, app, "SELECT count(*) FROM operations"); n != before {
			t.Fatalf("拒绝歌词写入后操作数=%d，期望 %d", n, before)
		}
		if n := countRows(t, app, "SELECT count(*) FROM track_external_lyrics"); n != rows {
			t.Fatalf("拒绝歌词写入后行数=%d，期望 %d", n, rows)
		}
	}
	reject(req, 409, "curation_disabled", "")
	f.enable(t)
	mustExec(t, app, "UPDATE users SET disabled_at=now() WHERE id=$1", f.admin.id)
	reject(req, 409, "curation_disabled", "")
	mustExec(t, app, "UPDATE users SET disabled_at=NULL WHERE id=$1", f.admin.id)
	req.BatchID = f.batch(t, "program")
	if err := f.writer.CloseBatch(t.Context(), req.BatchID); err != nil {
		t.Fatal(err)
	}
	reject(req, 409, "batch_closed", "")
	req.BatchID = f.batch(t, "program")
	for _, invalid := range []string{strings.Repeat("a", (64<<10)+1), "\uFEFF \r\n", "[ti:只有元标签]"} {
		bad := req
		bad.Text = invalid
		reject(bad, 400, "invalid_input", "")
	}
	mustExec(t, app, "UPDATE tracks SET source_status='missing' WHERE id=$1", track)
	reject(req, 404, "not_found", "")
	mustExec(t, app, "UPDATE tracks SET source_status='present',lyrics='本地歌词',lyrics_source='lrc_file' WHERE id=$1", track)
	reject(req, 409, "nothing_to_apply", "local_present")
	mustExec(t, app, "UPDATE tracks SET lyrics='[ti:只有元标签]' WHERE id=$1", track)
	id := applyExternalLyrics(t, f.writer, req)
	r := externalRecord(t, f, id, f.a, operations.TypeTrackExternalLyrics, req.BatchID, req.Source)
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(externalNormalizedLyrics)))
	requireExternalJSON(t, r.Before, map[string]any{"track_id": track, "lyrics": nil})
	requireExternalJSON(t, r.After, map[string]any{"track_id": track, "track_title": title, "lyrics": map[string]any{
		"content_hash": hash, "kind": "synced", "lines": 2, "source": "lrclib", "external_id": "42",
	}})
	var stored, digest, owner, source, extID string
	if err := app.database.QueryRow(`SELECT lyrics,content_hash,operation_id::text,source,external_id FROM track_external_lyrics
		WHERE track_id=$1`, track).Scan(&stored, &digest, &owner, &source, &extID); err != nil ||
		stored != externalNormalizedLyrics || digest != hash || owner != id || source != "lrclib" || extID != "42" {
		t.Fatalf("歌词行=(%q,%s,%s,%s,%s,%v)", stored, digest, owner, source, extID, err)
	}
	if replay := applyExternalLyrics(t, f.writer, req); replay != id {
		t.Fatalf("歌词重放操作=%s，期望 %s", replay, id)
	}
	other := req
	other.Key, other.Text = uuid.New(), "被占用时的新歌词"
	reject(other, 409, "nothing_to_apply", "occupied")
	if n := countRows(t, app, "SELECT count(*) FROM operations"); n != 1 {
		t.Fatalf("歌词重放后操作数=%d，期望 1", n)
	}
}
