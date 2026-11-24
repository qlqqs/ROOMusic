package app

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/upstreammock"
)

const (
	meridianMBID = "10000000-0000-4000-8000-000000000002"
	noNameMBID   = "10000000-0000-4000-8000-000000000099"
)

type artistLinkFixture struct {
	*artistMediaProgram
	mu   sync.Mutex
	hits map[string]int
}

func newArtistLinkFixture(t *testing.T) *artistLinkFixture {
	t.Helper()
	mock, err := upstreammock.New(upstreammock.Options{})
	requireCurationOK(t, err)
	f := &artistLinkFixture{hits: make(map[string]int)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		service := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")[0]
		f.mu.Lock()
		f.hits[service]++
		f.mu.Unlock()
		if r.URL.Path == "/musicbrainz/ws/2/artist/"+noNameMBID {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":%q,"name":"","relations":[]}`, noNameMBID)
			return
		}
		mock.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	app := newConfiguredIntegrationTestApplication(t, slog.New(slog.NewTextHandler(io.Discard, nil)), config.Server{UpstreamMockURL: server.URL})
	groups := newGroupEnvironmentForApp(t, app)
	writer := curation.NewWriter(app.database, app.dataDirectory, nil).WithArtists(governance.NewArtistGovernance(artistResolutionAdapter{}))
	curationEnv := &curationFixture{&operationsEnvironment{groups}, writer}
	f.artistMediaProgram = &artistMediaProgram{programFixture: &programFixture{curationFixture: curationEnv, clock: newProgramClock()}}
	f.gate = artistMediaSourceGate(f.artistMediaProgram, server, "/musicbrainz/ws/2")
	mustExec(t, app, `UPDATE releases SET artist='Paper Islet',album_artist='Paper Islet' WHERE id=$1`, f.a)
	publishSearch(t, app)
	f.artist = artistIDByName(t, f.programFixture, "Paper Islet")
	f.enable(t)
	return f
}

func (f *artistLinkFixture) calls(service string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[service]
}

func (f *artistLinkFixture) putLink(t *testing.T, source, external, key string, revision int64) *httptest.ResponseRecorder {
	t.Helper()
	return artistMediaCommand(t, f.application, &f.admin, "PUT", f.artist, "/links", key,
		map[string]any{"source": source, "external_id": external, "expected_revision": revision})
}

func (f *artistLinkFixture) setLink(t *testing.T, source, id string) string {
	t.Helper()
	revision := f.media(t).Revision
	return requireArtistMediaEdit(t, f.putLink(t, source, id, uuid.New(), revision), f.artist, "", revision+1)
}

func (f *artistLinkFixture) requireLinkRows(t *testing.T, want map[string][2]string) {
	t.Helper()
	links, err := curation.LinksOf(t.Context(), f.application.database, "artist", []string{f.artist})
	requireCurationOK(t, err)
	got := make(map[string][2]string)
	for _, l := range links {
		got[l.Source] = [2]string{l.ExternalID, l.OperationID}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("艺人对应（外部 ID、写入者）=%v，期望 %v", got, want)
	}
}

func TestPostgreSQLArtistLinkPreview(t *testing.T) {
	f := newArtistLinkFixture(t)
	app := f.application
	preview := func(t *testing.T, artist, input string) *httptest.ResponseRecorder {
		t.Helper()
		return artistMediaCommand(t, app, &f.admin, "POST", artist, "/link-preview", "", map[string]string{"url": input})
	}
	for _, tc := range []struct{ name, input, source, id string }{
		{"musicbrainz_url", "https://beta.musicbrainz.org/artist/" + artistMediaMBID + "/relationships", "musicbrainz", artistMediaMBID},
		{"mbid", artistMediaMBID, "musicbrainz", artistMediaMBID},
		{"wikidata_url", "https://www.wikidata.org/entity/Q900001", "wikidata", "Q900001"},
		{"qid", "Q900001", "wikidata", "Q900001"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requirePreview := func(artist string, match bool) {
				t.Helper()
				response := preview(t, artist, tc.input)
				if response.Code != 200 {
					t.Fatalf("预览 HTTP %d：%s", response.Code, response.Body)
				}
				got := decodeJSONMap(t, response)
				want := map[string]any{"source": tc.source, "external_id": tc.id, "name": "纸屿", "description": "海岸二重奏",
					"musicbrainz": artistMediaMBID, "wikidata": "Q900001", "name_matches": match}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("预览=%v，期望 %v", got, want)
				}
			}
			requirePreview(f.artist, true)
			requirePreview(artistIDByName(t, f.programFixture, "Artist B"), false)
		})
	}
	response := preview(t, f.artist, noNameMBID)
	if response.Code != 200 {
		t.Fatalf("缺名条目的预览 HTTP %d：%s", response.Code, response.Body)
	}
	if got := decodeJSONMap(t, response); got["name"] != noNameMBID || got["name_matches"] != false {
		t.Errorf("无名字的 MusicBrainz 条目应回退到 MBID，得到 %v", got)
	}
	for _, input := range []string{"https://example.test/wiki/Q900001", "Q0", "Q1234567890123"} {
		requireAPIError(t, preview(t, f.artist, input), 400, "invalid_input")
	}
	requireAPIError(t, preview(t, f.artist, "Q123456789012"), 404, "not_found")
	requireAPIError(t, preview(t, f.artist, "Q999999"), 404, "not_found")
	requireAPIError(t, preview(t, f.artist, "10000000-0000-4000-8000-000000000098"), 404, "not_found")
	requireAPIError(t, preview(t, uuid.New(), artistMediaMBID), 404, "not_found")
	requireAPIError(t, f.putLink(t, "wikidata", "Q1234567890123", uuid.New(), f.media(t).Revision), 400, "invalid_input")
	for source, input := range map[string]string{"musicbrainz": artistMediaMBID, "wikidata": "Q900001"} {
		f.sourceEnabled(t, source, false)
		calls := f.calls(source)
		requireAPIError(t, preview(t, f.artist, input), 409, "source_inactive")
		if f.calls(source) != calls {
			t.Errorf("%s 已关闭仍收到预览请求", source)
		}
	}
	f.assertCount(t, 0, "SELECT count(*) FROM operations WHERE target_id=$1", f.artist)
	f.requireLinkRows(t, map[string][2]string{})
}

func TestPostgreSQLArtistLinkOnlyWikidata(t *testing.T) {
	f := newArtistLinkFixture(t)
	f.key(t, "fanarttv", "project", "synthetic-project")
	f.key(t, "theaudiodb", "api_key", "synthetic-audio")
	settings, err := f.gate.List(t.Context())
	requireCurationOK(t, err)
	active := make(map[string]bool)
	for _, source := range settings {
		active[source.ID] = source.Active
	}
	if !active["fanarttv"] || !active["theaudiodb"] {
		t.Fatalf("需要 MBID 的两种来源应处于启用态：%v", active)
	}
	op := f.setLink(t, "wikidata", "Q900005")
	f.requireLinkRows(t, map[string][2]string{"wikidata": {"Q900005", op}})
	f.run(t)
	detail := f.media(t)
	if detail.Links.MusicBrainz != nil || detail.Links.Wikidata == nil || *detail.Links.Wikidata != "Q900005" ||
		detail.Image == nil || detail.Image.Source != "commons" || detail.Bio == nil || detail.Bio.Source != "wikipedia" ||
		detail.Bio.Lang != "zh" || detail.Bio.Text != "浅海回声是虚构的潮间带合奏组。" {
		t.Fatalf("仅指定 Q 号时的资料不符：对应=%+v，图片=%+v，简介=%+v", detail.Links, detail.Image, detail.Bio)
	}
	for _, table := range []string{"artist_external_artworks", "artist_external_bios"} {
		f.assertCount(t, 1, "SELECT count(*) FROM "+table+" WHERE artist_id=$1 AND basis_source='wikidata' AND basis_id='Q900005'", f.artist)
	}
	if f.calls("commons") == 0 || f.calls("wikipedia") == 0 || f.calls("fanarttv") != 0 || f.calls("theaudiodb") != 0 {
		t.Fatalf("Q 号补资料的请求不符：Commons=%d Wikipedia=%d Fanart.tv=%d TheAudioDB=%d",
			f.calls("commons"), f.calls("wikipedia"), f.calls("fanarttv"), f.calls("theaudiodb"))
	}
}

func TestPostgreSQLArtistLinkChangeAndUndoMedia(t *testing.T) {
	f := newArtistLinkFixture(t)
	app := f.application
	f.key(t, "fanarttv", "project", "synthetic-project")
	f.key(t, "theaudiodb", "api_key", "synthetic-audio")
	linkArtistMedia(t, app, f.artist, artistMediaMBID)
	f.run(t)
	before := f.media(t)
	if before.Image == nil || before.Image.Source != "fanarttv" || before.Bio == nil || before.Bio.Source != "wikipedia" {
		t.Fatalf("旧对应应有 Fanart.tv 图片与维基导语：图片=%+v 简介=%+v", before.Image, before.Bio)
	}
	firstState := f.stateFor(t, "artist_media", f.artist)
	photoOp := programString(t, app, "SELECT operation_id::text FROM artist_external_artworks WHERE artist_id=$1", f.artist)
	bioOp := programString(t, app, "SELECT operation_id::text FROM artist_external_bios WHERE artist_id=$1", f.artist)
	newLink := f.setLink(t, "musicbrainz", meridianMBID)
	changed := f.media(t)
	if changed.Image != nil || changed.Bio != nil || changed.Links.MusicBrainz == nil || *changed.Links.MusicBrainz != meridianMBID {
		t.Fatalf("依据失效的旧资料未清除：图片=%+v 简介=%+v 对应=%+v", changed.Image, changed.Bio, changed.Links)
	}
	f.assertCount(t, 1, "SELECT count(*) FROM artwork_retained WHERE storage_key=$1 AND operation_id=$2", before.Image.Resource, newLink)
	if picture := app.request(t, "GET", "/api/v1/artworks/"+before.Image.Resource, nil, &f.admin, nil); picture.Code != 200 {
		t.Errorf("清除后旧图片应可读取，HTTP %d：%s", picture.Code, picture.Body)
	}
	f.clock.advance(t, 6*time.Hour)
	next := f.media(t)
	nextState := f.stateFor(t, "artist_media", f.artist)
	if nextState.Generation <= firstState.Generation || nextState.InputDigest == firstState.InputDigest ||
		next.Image == nil || next.Image.Source != "theaudiodb" || next.Bio == nil || next.Bio.Source != "theaudiodb" ||
		next.Bio.Text != "空心子午线是一支虚构的夜车合奏组。" {
		t.Fatalf("程序应按新 MBID 补资料：状态=%+v，图片=%+v，简介=%+v", nextState, next.Image, next.Bio)
	}
	for _, table := range []string{"artist_external_artworks", "artist_external_bios"} {
		f.assertCount(t, 1, "SELECT count(*) FROM "+table+" WHERE artist_id=$1 AND basis_source='musicbrainz' AND basis_id=$2", f.artist, meridianMBID)
	}
	f.requireLinkOperation(t, newLink, true, nil)
	f.undo(t, newLink)
	f.requireLinkOperation(t, newLink, false, nil)
	f.requireLinkRows(t, map[string][2]string{"musicbrainz": {artistMediaMBID, ""}})
	restored := f.media(t)
	if !reflect.DeepEqual(before.Image, restored.Image) || !reflect.DeepEqual(before.Bio, restored.Bio) {
		t.Fatalf("撤销应恢复旧资料：图片=%+v，简介=%+v", restored.Image, restored.Bio)
	}
	f.assertCount(t, 1, "SELECT count(*) FROM artist_external_artworks WHERE artist_id=$1 AND operation_id=$2 AND basis_id=$3",
		f.artist, photoOp, artistMediaMBID)
	f.assertCount(t, 1, "SELECT count(*) FROM artist_external_bios WHERE artist_id=$1 AND operation_id=$2 AND basis_id=$3",
		f.artist, bioOp, artistMediaMBID)
	f.assertCount(t, 0, "SELECT count(*) FROM curation_suppressions WHERE target_id=$1", f.artist)
}

func (f *artistLinkFixture) requireLinkOperation(t *testing.T, op string, revertible bool, skipped *int) {
	t.Helper()
	var listed struct {
		Items []struct {
			ID         string `json:"id"`
			Revertible bool   `json:"revertible"`
			Changes    struct {
				Skipped int `json:"skipped_artist_links"`
			} `json:"changes"`
		} `json:"items"`
	}
	getJSON(t, f.application, &f.admin, "/api/v1/operations", &listed)
	for _, item := range listed.Items {
		if item.ID == op {
			if item.Revertible != revertible || skipped != nil && item.Changes.Skipped != *skipped {
				t.Errorf("操作 %s 的列表描述=%+v，期望可撤销=%v、跳过数=%v", op, item, revertible, skipped)
			}
			return
		}
	}
	t.Errorf("操作列表缺少 %s", op)
}

func TestPostgreSQLArtistLinkKeepsLegacyMediaWithoutBasis(t *testing.T) {
	f := newArtistLinkFixture(t)
	app := f.application
	linkArtistMedia(t, app, f.artist, artistMediaMBID)
	photoOp := seedArtistPhoto(t, f.artistMediaProgram, externalFixturePNG(t, 2, 2))
	bio, err := f.writer.ApplyArtistBio(t.Context(), curation.ArtistBioRequest{ArtistID: f.artist, MBID: artistMediaMBID, Key: uuid.New(),
		Bio: sources.ArtistBio{ID: "synthetic-bio", Text: "旧简介", Lang: "zh",
			Attribution: sources.Attribution{Source: "wikipedia", SourceURL: "https://zh.wikipedia.org/wiki/Fixture"}}})
	requireCurationOK(t, err)
	for _, table := range []string{"artist_external_artworks", "artist_external_bios"} {
		mustExec(t, app, "UPDATE "+table+" SET basis_source=NULL,basis_id=NULL WHERE artist_id=$1", f.artist)
	}
	before := f.media(t)
	op := f.setLink(t, "musicbrainz", meridianMBID)
	for _, stage := range []string{"改对应后", "撤销后"} {
		if stage == "撤销后" {
			f.undo(t, op)
		}
		after := f.media(t)
		if !reflect.DeepEqual(before.Image, after.Image) || !reflect.DeepEqual(before.Bio, after.Bio) {
			t.Errorf("%s应保留没有依据的旧资料：图片=%+v，简介=%+v", stage, after.Image, after.Bio)
		}
		f.assertCount(t, 1, "SELECT count(*) FROM artist_external_artworks WHERE artist_id=$1 AND operation_id=$2 AND basis_source IS NULL",
			f.artist, photoOp)
		f.assertCount(t, 1, "SELECT count(*) FROM artist_external_bios WHERE artist_id=$1 AND operation_id=$2 AND basis_source IS NULL",
			f.artist, bio.OperationID)
	}
}

func TestPostgreSQLArtistLinkRevertConflictWithUnrelatedMedia(t *testing.T) {
	f := newArtistLinkFixture(t)
	f.setLink(t, "wikidata", "Q900005")
	linkArtistMedia(t, f.application, f.artist, artistMediaMBID)
	seedArtistPhoto(t, f.artistMediaProgram, externalFixturePNG(t, 2, 2))
	change := f.setLink(t, "musicbrainz", meridianMBID)
	applied, err := f.writer.ApplyArtistImage(t.Context(), curation.ArtistImageRequest{
		ArtistID: f.artist, Link: curation.ArtistMediaLink{Source: "wikidata", ExternalID: "Q900005"}, Key: uuid.New(),
		Image: sources.ArtistImage{ID: "Fixture.png", Data: externalFixturePNG(t, 3, 3),
			Attribution: sources.Attribution{Source: "commons", SourceURL: "https://commons.wikimedia.org/wiki/File:Fixture.png", License: "CC BY 4.0"}},
	})
	current := requireExternalApply(t, applied, err)
	f.requireLinkOperation(t, change, false, nil)
	requireAPIError(t, f.revert(t, &f.admin, change, uuid.New()), 409, "revert_conflict")
	f.assertCount(t, 1, "SELECT count(*) FROM artist_external_artworks WHERE artist_id=$1 AND operation_id=$2 AND basis_id='Q900005'", f.artist, current)
	f.assertCount(t, 1, "SELECT count(*) FROM external_links WHERE object_id=$1 AND operation_id=$2 AND external_id=$3", f.artist, change, meridianMBID)
}

func TestPostgreSQLArtistLinkOldReleaseRevert(t *testing.T) {
	f := newArtistLinkFixture(t)
	req := artistLinkRequest(f.a, f.artist, meridianMBID)
	req.Links = append(req.Links, curationCLink("release", f.a, "synthetic-release"))
	auto := curationCApplyLinks(t, f.curationFixture, req).OperationID
	initial := f.media(t).Revision
	requireAPIError(t, f.putLink(t, "musicbrainz", meridianMBID, uuid.New(), initial), 409, "nothing_to_apply")
	f.requireLinkRows(t, map[string][2]string{"musicbrainz": {meridianMBID, auto}})
	if got := f.media(t).Revision; got != initial {
		t.Fatalf("相同对应却改变了版本 %d→%d", initial, got)
	}

	op := f.setLink(t, "musicbrainz", artistMediaMBID)
	skipped := 1
	f.requireLinkOperation(t, auto, true, &skipped)
	revert := f.undo(t, auto)["operation_id"].(string)
	f.requireLinkOperation(t, revert, false, &skipped)
	f.requireLinkRows(t, map[string][2]string{"musicbrainz": {artistMediaMBID, op}, "wikidata": {"Q900001", op}})
	curationCRequireLink(t, f.curationFixture, req.Links[1], "")
	f.assertCount(t, 0, "SELECT count(*) FROM curation_suppressions WHERE target_id=$1 AND subject='external_link'", f.artist)
	curationCRequireMarker(t, f.curationFixture, "release", f.a, "external_link", "musicbrainz\x00synthetic-release", revert,
		map[string]string{"source": "musicbrainz", "external_id": "synthetic-release"})

	f.undo(t, op)
	f.requireLinkRows(t, map[string][2]string{})
}

func TestPostgreSQLArtistManualMediaStaysAtEffectiveArtistAfterSplit(t *testing.T) {
	f := newArtistLinkFixture(t)
	app := f.application
	alias := artistIDByName(t, f.programFixture, "Artist B")
	keyID := programString(t, app, "SELECT id FROM artist_name_keys WHERE initial_artist_id=$1", alias)
	governance := &artistMergeFixture{programFixture: f.programFixture}
	governance.manualMerge(t, f.artist, alias)
	revision := f.media(t).Revision
	requireArtistMediaEdit(t, artistImageUpload(t, app, &f.admin, alias, uuid.New(), revision, externalFixturePNG(t, 2, 2)), f.artist, "", revision+1)
	bio := artistMediaCommand(t, app, &f.admin, "PUT", alias, "/bio", uuid.New(),
		map[string]any{"expected_revision": revision + 1, "text": "有效身份上的人工简介", "lang": "zh"})
	requireArtistMediaEdit(t, bio, f.artist, "", revision+2)
	link := artistMediaCommand(t, app, &f.admin, "PUT", alias, "/links", uuid.New(),
		map[string]any{"expected_revision": revision + 2, "source": "wikidata", "external_id": "Q900005"})
	linkOp := requireArtistMediaEdit(t, link, f.artist, "", revision+3)
	before := f.media(t)
	if before.Image == nil || before.Bio == nil || before.Links.Wikidata == nil {
		t.Fatalf("通过重定向写入后，有效艺人缺少资料：%+v", before)
	}

	nameKeys := []string{keyID}
	preview := governance.postArtist(t, f.artist, "split-preview", map[string]any{"mode": "aliases", "name_key_ids": nameKeys})
	result := governance.postArtist(t, f.artist, "split", map[string]any{
		"expected_revision": before.Revision, "mode": "aliases", "name_key_ids": nameKeys, "basis": preview["basis"],
		"target": map[string]any{"kind": "restore", "artist_id": alias, "expected_revision": governance.revision(t, alias)},
	})
	if result["target"].(map[string]any)["id"] != alias {
		t.Fatalf("拆分应恢复身份 %s，得到 %v", alias, result["target"])
	}
	restored := readArtistMedia(t, app, &f.admin, alias)
	if restored.RedirectArtistID != nil || restored.Image != nil || restored.Bio != nil || restored.Links.Wikidata != nil {
		t.Errorf("拆分恢复的身份不应继承写入有效艺人的资料：%+v", restored)
	}
	if after := f.media(t); !reflect.DeepEqual(after.Image, before.Image) || !reflect.DeepEqual(after.Bio, before.Bio) || !reflect.DeepEqual(after.Links, before.Links) {
		t.Errorf("拆分后有效艺人的资料发生变化：%+v", after)
	}
	f.requireLinkRows(t, map[string][2]string{"wikidata": {"Q900005", linkOp}})
}

func TestPostgreSQLArtistLinkHTTPReplayAndConflict(t *testing.T) {
	f := newArtistLinkFixture(t)
	revision := f.media(t).Revision
	key := uuid.New()
	first := f.putLink(t, "wikidata", "Q900005", key, revision)
	op := requireArtistMediaEdit(t, first, f.artist, "", revision+1)
	f.sourceEnabled(t, "wikidata", false)
	seen := f.calls("wikidata")
	replay := f.putLink(t, "wikidata", "Q900005", key, revision)
	if got := requireArtistMediaEdit(t, replay, f.artist, "", revision+1); got != op || !reflect.DeepEqual(decodeJSONMap(t, first), decodeJSONMap(t, replay)) {
		t.Fatalf("指定对应的重放回执发生变化：%s / %s", first.Body, replay.Body)
	}
	requireAPIError(t, f.putLink(t, "wikidata", "Q900001", key, revision), 409, "idempotency_conflict")
	if f.calls("wikidata") != seen {
		t.Error("同键调用重复查询外部来源")
	}
	f.sourceEnabled(t, "wikidata", true)
	requireAPIError(t, f.putLink(t, "wikidata", "Q900001", uuid.New(), revision), 409, "revision_conflict")
	replacement := f.setLink(t, "wikidata", "Q900001")
	f.requireLinkOperation(t, op, false, nil)
	requireAPIError(t, f.revert(t, &f.admin, op, uuid.New()), 409, "revert_conflict")
	f.undo(t, replacement)
	f.requireLinkRows(t, map[string][2]string{"wikidata": {"Q900005", op}})
	f.undo(t, op)
	f.requireLinkRows(t, map[string][2]string{})
}

func TestPostgreSQLArtistLinkModelOccupiedAndSuppressed(t *testing.T) {
	f := newArtistLinkFixture(t)
	manual := f.setLink(t, "wikidata", "Q900005")
	conversation := uuid.New()
	mustExec(t, f.application, `INSERT INTO steward_conversations(id,user_id,title,model) VALUES($1,$2,'合成会话','fixture-model')`,
		conversation, f.admin.id)
	request := curation.ArtistLinkRequest{
		ArtistID: f.artist, Key: uuid.New(), ExpectedRevision: f.media(t).Revision,
		ExternalSource: "wikidata", ExternalID: "Q900004",
		Source:  curation.Source{Kind: "model", Provider: "fixture-model", Basis: "合成来源依据"},
		BatchID: f.batch(t, "model"), ConversationID: conversation,
	}
	_, err := f.writer.ApplyArtistLink(t.Context(), f.gate, request)
	curationCRequireProblem(t, err, 409, "occupied")
	f.requireLinkRows(t, map[string][2]string{"wikidata": {"Q900005", manual}})
	f.undo(t, manual)
	request.Key, request.ExpectedRevision, request.ExternalID = uuid.New(), f.media(t).Revision, "Q900005"
	applied, err := f.writer.ApplyArtistLink(t.Context(), f.gate, request)
	requireCurationOK(t, err)
	f.requireLinkRows(t, map[string][2]string{"wikidata": {"Q900005", applied.OperationID}})
	f.assertCount(t, 1, `SELECT count(*) FROM operations WHERE id=$1 AND batch_id=$2 AND origin->>'conversation_id'=$3
		AND origin->>'provider_kind'='model' AND origin->>'provider'='fixture-model'`, applied.OperationID, request.BatchID, conversation)
	reverted := f.undo(t, applied.OperationID)["operation_id"].(string)
	f.requireLinkRows(t, map[string][2]string{})
	curationCRequireMarker(t, f.curationFixture, "artist", f.artist, "external_link", "wikidata\x00Q900005", reverted,
		map[string]string{"source": "wikidata", "external_id": "Q900005"})
	request.Key, request.ExpectedRevision = uuid.New(), f.media(t).Revision
	_, err = f.writer.ApplyArtistLink(t.Context(), f.gate, request)
	curationCRequireProblem(t, err, 409, "suppressed")
}
