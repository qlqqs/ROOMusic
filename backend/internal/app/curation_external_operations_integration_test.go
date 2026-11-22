package app

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/artwork"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/operations"
	operationtools "github.com/qlqq/roomusic/backend/internal/operations/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func TestPostgreSQLExternalOperationList(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	app := f.application
	track, title := externalTrack(t, app, f.a)
	req := externalArtworkRequest(f.a)
	stored, _ := externalArtworkFile(t, app, req.Data)
	artID := applyExternalArtwork(t, f.writer, req)
	lyricsID := applyExternalLyrics(t, f.writer, externalLyricsRequest(track))
	type operationItem struct {
		ID         string          `json:"id"`
		Type       string          `json:"type"`
		Changes    json.RawMessage `json:"changes"`
		Revertible bool            `json:"revertible"`
		Reverts    string          `json:"reverts"`
		RevertedBy string          `json:"reverted_by"`
		Release    struct {
			ID      string `json:"id"`
			Title   string `json:"title"`
			Visible bool   `json:"visible"`
		} `json:"release"`
	}
	list := func(wantCount int) map[string]operationItem {
		t.Helper()
		var response struct{ Items []operationItem }
		getJSON(t, app, &f.admin, "/api/v1/operations", &response)
		if len(response.Items) != wantCount {
			t.Fatalf("操作列表=%+v，期望 %d 项", response.Items, wantCount)
		}
		items := make(map[string]operationItem)
		for _, item := range response.Items {
			if item.Release.ID != f.a || item.Release.Title != "Album A" || !item.Release.Visible {
				t.Fatalf("操作 %s 的发行=%+v", item.ID, item.Release)
			}
			items[item.ID] = item
		}
		return items
	}
	wantArt := map[string]any{"kind": "release_external_artwork", "source": "coverartarchive", "external_id": req.ExternalID,
		"artwork": map[string]any{"resource_id": stored.Key, "mime": "image/gif", "width": 1, "height": 1}}
	wantLyrics := map[string]any{"kind": "track_external_lyrics", "source": "lrclib", "external_id": "42",
		"track": map[string]string{"id": track, "title": title}, "lyrics_kind": "synced", "lines": 2}
	// 展示使用操作发生时的曲目标题。
	mustExec(t, app, "UPDATE tracks SET title='后来改了标题' WHERE id=$1", track)
	items := list(2)
	for _, want := range []struct {
		id, kind string
		changes  any
	}{
		{artID, "release_external_artwork", wantArt},
		{lyricsID, "track_external_lyrics", wantLyrics},
	} {
		it := items[want.id]
		if it.Type != want.kind || !it.Revertible || it.Reverts != "" || it.RevertedBy != "" {
			t.Fatalf("补充操作=%+v，期望可撤销的 %s", it, want.kind)
		}
		requireExternalJSON(t, it.Changes, want.changes)
	}
	artRevert := revertExternal(t, f, artID, f.a, 1)
	lyricsRevert := revertExternal(t, f, lyricsID, f.a, 1)
	items = list(4)
	for _, want := range []struct {
		id, revert string
		changes    any
	}{{artID, artRevert, wantArt}, {lyricsID, lyricsRevert, wantLyrics}} {
		original, revert := items[want.id], items[want.revert]
		if original.Revertible || original.RevertedBy != want.revert || revert.Type != "revert" ||
			revert.Revertible || revert.Reverts != want.id {
			t.Fatalf("撤销关联 原操作=%+v，撤销=%+v", original, revert)
		}
		requireExternalJSON(t, original.Changes, want.changes)
		requireExternalJSON(t, revert.Changes, want.changes)
	}
	mustExec(t, app, `UPDATE operations SET after_state='{"lyrics":"bad"}'::jsonb WHERE id=$1`, lyricsID)
	items = list(4)
	requireExternalJSON(t, items[lyricsID].Changes, nil)
	requireExternalJSON(t, items[lyricsRevert].Changes, wantLyrics)
	requireExternalJSON(t, items[artID].Changes, wantArt)
}

func TestPostgreSQLExternalStewardRevert(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	app := f.application
	track, title := externalTrack(t, app, f.a)
	req := externalArtworkRequest(f.a)
	_, path := externalArtworkFile(t, app, req.Data)
	artID := applyExternalArtwork(t, f.writer, req)
	lyricsID := applyExternalLyrics(t, f.writer, externalLyricsRequest(track))
	registry := tool.NewRegistry()
	for _, def := range operationtools.RevertTools(app.database, curation.ExternalArtworkOperations(f.writer), curation.ExternalLyricsOperations(f.writer)) {
		if err := registry.Register(def); err != nil {
			t.Fatal(err)
		}
	}
	caller := tool.Caller{User: identity.User{ID: f.admin.id, Role: "admin"}, Source: tool.SourceChat,
		ConversationID: uuid.New(), SharedExecution: true, Mode: "operator", RequestID: "external-steward-revert"}
	for _, tc := range []struct {
		field, id string
		before    map[string]string
	}{
		{"artwork", artID, map[string]string{"source": "coverartarchive", "external_id": req.ExternalID}},
		{"lyrics", lyricsID, map[string]string{"source": "lrclib", "external_id": "42", "track_title": title}},
	} {
		input, err := json.Marshal(map[string]string{"operation_id": tc.id})
		if err != nil {
			t.Fatal(err)
		}
		before := countRows(t, app, "SELECT count(*) FROM operations")
		preview, err := registry.PreviewCall(t.Context(), caller, "revert_operation", input)
		want := tool.Preview{Target: "Album A", Summary: "撤销操作", Changes: []tool.Change{{Field: tc.field, Before: tc.before}}}
		if err != nil || !reflect.DeepEqual(preview, want) {
			t.Fatalf("%s 的管家预览=%+v，错误=%v，期望 %+v", tc.field, preview, err, want)
		}
		if n := countRows(t, app, "SELECT count(*) FROM operations"); n != before {
			t.Fatalf("预览后的操作数=%d，期望 %d", n, before)
		}
		caller.IdempotencyKey = uuid.New()
		result, err := registry.Invoke(t.Context(), caller, "revert_operation", input)
		if err != nil {
			t.Fatalf("管家撤销 %s：%v", tc.field, err)
		}
		revert := requireExternalRevert(t, result.Output, tc.id, f.a, 1)
		record, err := operations.Load(t.Context(), app.database, revert)
		if err != nil || record.RevertsID != tc.id || record.ActorID != f.admin.id {
			t.Fatalf("管家撤销记录=%+v，错误=%v", record, err)
		}
	}
	externalArtworkState(t, app, f.a, "", artwork.Stored{})
	requireExternalFile(t, path, nil)
	requireExternalLyricsHTTP(t, app, &f.admin, track, "", "", nil)
}

func recordExternalCurationSamples(t *testing.T, samples *apiSampleRecorder) {
	t.Helper()
	f := newCurationFixture(t)
	f.enable(t)
	app := f.application
	mustExec(t, app, "UPDATE users SET username='curator' WHERE id=$1", f.admin.id)
	samples.forbid(f.admin.token)
	samples.forbid(app.allowedRoot)
	samples.forbid(app.dataDirectory)
	track, _ := externalTrack(t, app, f.a)
	art := applyExternalArtwork(t, f.writer, externalArtworkRequest(f.a))
	lyrics := applyExternalLyrics(t, f.writer, externalLyricsRequest(track))
	samples.record("tracks/lyrics-online", app.request(t, "GET", "/api/v1/tracks/"+track+"/lyrics", nil, &f.admin, nil), 200)
	samples.record("operations/external-reverted", f.revert(t, &f.admin, art, uuid.New()), 200)
	revertExternal(t, f, lyrics, f.a, 1)
	samples.record("operations/external-list", app.request(t, "GET", "/api/v1/operations?release_id="+f.a, nil, &f.admin, nil), 200)
}
