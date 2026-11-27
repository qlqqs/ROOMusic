package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

const webSearchBio = "合成音乐组合长期以沿岸生活为题材，创作并演奏原创音乐。"

func webSearchMediaInput(item, imageURL, page string, bio bool) map[string]any {
	input := map[string]any{"item_id": item, "citations": []map[string]string{{"url": page, "title": "合成艺人名录"}}}
	if imageURL != "" {
		input["image"] = map[string]string{"url": imageURL, "page_url": page}
	}
	if bio {
		input["bio"] = map[string]string{"text": webSearchBio, "lang": "zh"}
	}
	return input
}

// 网页媒体保留输入校验，艺人候选接口只采纳身份候选。
func TestPostgreSQLArtistWebSearchHistoricalMediaValidation(t *testing.T) {
	f := newControlledArtistWebSearch(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/searxng/artists/coast":
			fmt.Fprint(w, `<html><p>海岸来信</p><img src="portrait.png"><img src="/small.gif"><img src="/text"></html>`)
		case "/small.gif":
			w.Header().Set("Content-Type", "image/gif")
			w.Write(externalArtworkRequest("").Data)
		case "/text":
			fmt.Fprint(w, "非图片")
		default:
			return false
		}
		return true
	})
	f.enable(t)
	artist := f.addArtist(t, "海岸来信")
	item := f.pendingArtist(t, artist, "海岸来信")
	page := f.endpoint + "/searxng/artists/coast"
	imageURL := f.endpoint + "/searxng/artists/portrait.png"
	prepare := stewardtools.MediaConfirmationPreparer(sources.NewMockHost(f.endpoint))
	for _, invalid := range []func(map[string]any){
		func(m map[string]any) { m["image"].(map[string]string)["page_url"] += "-other" },
		func(m map[string]any) { m["bio"].(map[string]string)["text"] = strings.Repeat("字", 4001) },
		func(m map[string]any) { m["citations"] = nil },
		func(m map[string]any) {
			var refs []map[string]string
			for n := 0; n < 6; n++ {
				refs = append(refs, map[string]string{"url": page + "?n=" + strconv.Itoa(n), "title": "名录"})
			}
			m["citations"] = refs
		},
		func(m map[string]any) { m["citations"].([]map[string]string)[0]["title"] = strings.Repeat("字", 501) },
	} {
		m := webSearchMediaInput(item.ID, imageURL, page, true)
		invalid(m)
		raw, err := json.Marshal(m)
		requireCurationOK(t, err)
		if _, err = prepare(t.Context(), raw); err == nil {
			t.Fatal("历史建议接受了无效资料")
		}
	}
	conversation := modelConversation(t, f.curationFixture)
	mustExec(t, f.application, `UPDATE curation_pending_items SET status='in_progress',conversation_id=$2,claimed_at=now() WHERE id=$1`, item.ID, conversation)
	for _, asset := range []string{f.endpoint + "/small.gif", f.endpoint + "/text", imageURL} {
		raw, err := json.Marshal(webSearchMediaInput(item.ID, asset, page, true))
		requireCurationOK(t, err)
		preview, err := json.Marshal(map[string]any{"changes": []map[string]string{{"field": "image", "after": asset}, {"field": "bio", "after": webSearchBio}}})
		requireCurationOK(t, err)
		id, err := f.writer.CreateException(t.Context(), curation.ModelException{PendingItemID: item.ID, Tool: "submit_artist_media", Input: raw, Preview: preview, Provider: "合成模型", ConversationID: &conversation, ReasonCode: "no_evidence"})
		requireCurationOK(t, err)
		result := f.decide(t, &f.admin, id, "accept")
		requireAPIError(t, result, 404, "not_found")
		b3RequireCount(t, f.application, 0, `SELECT count(*) FROM operations WHERE target_id=$1 AND target_type='artist'`, artist)
	}
}

func TestPostgreSQLArtistWebSearchUnverifiedMediaSource(t *testing.T) {
	f := newControlledArtistWebSearch(t, nil)
	var calls atomic.Int64
	private := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	t.Cleanup(private.Close)
	artist := f.addArtist(t, "海岸来信")
	raw, err := json.Marshal(webSearchMediaInput(uuid.New(), private.URL+"/portrait.png", f.endpoint+"/searxng/artists/coast", true))
	requireCurationOK(t, err)
	prepare := stewardtools.MediaConfirmationPreparer(sources.NewMockHost(f.endpoint))
	if _, err = prepare(t.Context(), raw); err == nil || calls.Load() != 0 {
		t.Fatal("未证实的图片出处通过校验或触发了私网连接")
	}
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM operations WHERE target_id=$1 AND target_type='artist'`, artist)
}

func TestPostgreSQLArtistWebSearchMediaCompletesSingleField(t *testing.T) {
	f := newControlledArtistWebSearch(t, nil)
	artist := f.addArtist(t, "只查到简介的乐团")
	item := f.startArtist(t, artist)
	req := f.next(t, false)
	for _, spec := range req.Tools {
		if spec.Function.Name == "submit_artist_media" {
			t.Fatal("手动查身份开放了写资料工具")
		}
	}
	req.tool(t, "submit_artist_media", webSearchMediaInput(item.ID, "", f.endpoint+"/searxng/artists/coast", true))
	next := f.next(t, false)
	next.tool(t, "shelve_pending_item", map[string]string{"item_id": item.ID, "reason": "没有身份候选"})
	f.waitArtistResolution(t, item.ID, "no_result")
	got := readArtistMedia(t, f.application, &f.admin, artist)
	if got.Bio != nil || got.Image != nil {
		t.Fatal("查身份写入了艺人资料")
	}
}

func (f *artistWebSearchFixture) rejectMedia(t *testing.T, artist, field string) {
	t.Helper()
	revision := readArtistMedia(t, f.application, &f.admin, artist).Revision
	r := artistMediaCommand(t, f.application, &f.admin, http.MethodPost, artist, "/media/reject", uuid.New(),
		map[string]any{"expected_revision": revision, "field": field})
	if r.Code != http.StatusOK {
		t.Fatalf("标记 %s 不对返回 HTTP %d %s", field, r.Code, r.Body)
	}
}

func TestPostgreSQLArtistWebSearchSuppression(t *testing.T) {
	f := newArtistWebSearchFixture(t, "", false, false, nil)
	f.addRelease(t, "合成资料抑制乐团")
	f.addRelease(t, "合成插件抑制乐团")
	publishSearch(t, f.application)
	f.enable(t)
	page := f.endpoint + "/searxng/artists/coast"
	artist := f.artistID(t, "合成资料抑制乐团")
	item := f.pendingArtist(t, artist, "合成资料抑制乐团")
	conversation := modelConversation(t, f.curationFixture)
	mustExec(t, f.application, `UPDATE curation_pending_items SET status='in_progress',conversation_id=$2,claimed_at=now() WHERE id=$1`, item.ID, conversation)
	ctx := curation.WithModelRun(t.Context(), &curation.ModelRun{ActorID: f.admin.id, Model: "合成模型", ConversationID: conversation, PendingItemID: item.ID})
	batch := f.batch(t, "model")
	data, err := os.ReadFile(filepath.Join("..", "upstreammock", "testdata", "artist-portrait.png"))
	requireCurationOK(t, err)
	refs := []catalog.Citation{{URL: page, Title: "合成名录"}}
	imageURL := f.endpoint + "/searxng/artists/portrait.png"
	imageReq := curation.ArtistImageRequest{ArtistID: artist, Key: uuid.New(), BatchID: batch, Citations: refs, Image: sources.ArtistImage{ID: sources.ImageURLID(imageURL), Data: data, Attribution: sources.Attribution{Source: "web_search", SourceURL: page}}}
	bioReq := curation.ArtistBioRequest{ArtistID: artist, Key: uuid.New(), BatchID: batch, Citations: refs, Bio: sources.ArtistBio{ID: "artist", Lang: "zh", Text: webSearchBio, Attribution: sources.Attribution{Source: "web_search", SourceURL: page}}}
	image, imageErr := f.writer.ApplyArtistImage(ctx, imageReq)
	requireExternalApply(t, image, imageErr)
	bio, bioErr := f.writer.ApplyArtistBio(ctx, bioReq)
	requireExternalApply(t, bio, bioErr)
	f.rejectMedia(t, artist, "image")
	f.rejectMedia(t, artist, "bio")
	for _, marker := range []struct{ field, id string }{{"artist_image", imageReq.Image.ID}, {"artist_bio", "artist"}} {
		b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_suppressions WHERE target_type='artist' AND target_id=$1 AND subject=$2 AND value->>'source'='web_search' AND value->>'external_id'=$3`, artist, marker.field, marker.id)
	}
	imageReq.Key, bioReq.Key = uuid.New(), uuid.New()
	bioReq.Bio.Text += "这次引用的网页和简介内容均已改变。"
	refs[0].URL += "?new-citation=1"
	image, imageErr = f.writer.ApplyArtistImage(ctx, imageReq)
	requireExternalError(t, image, imageErr, 409, "nothing_to_apply", "suppressed")
	bio, bioErr = f.writer.ApplyArtistBio(ctx, bioReq)
	requireExternalError(t, bio, bioErr, 409, "nothing_to_apply", "suppressed")
	imageReq.Key, imageReq.Image.ID = uuid.New(), sources.ImageURLID(imageURL+"?new-image=1")
	image, imageErr = f.writer.ApplyArtistImage(ctx, imageReq)
	requireExternalApply(t, image, imageErr)
	detail := readArtistMedia(t, f.application, &f.admin, artist)
	if detail.Image == nil || detail.Bio != nil {
		t.Fatal("历史网页资料抑制不符")
	}
	pluginArtist := f.artistID(t, "合成插件抑制乐团")
	req := curation.ArtistBioRequest{ArtistID: pluginArtist, Name: "合成插件抑制乐团",
		Bio: sources.ArtistBio{ID: "entry-a", Lang: "zh", Text: webSearchBio,
			Attribution: sources.Attribution{Source: demoPluginSource, SourceURL: page}}, Key: uuid.New()}
	written, err := f.writer.ApplyArtistBio(t.Context(), req)
	requireExternalApply(t, written, err)
	f.rejectMedia(t, pluginArtist, "bio")
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_suppressions WHERE target_type='artist' AND target_id=$1
		AND subject='artist_bio' AND value->>'source'=$2 AND value->>'external_id'='entry-a'`, pluginArtist, demoPluginSource)
	req.Key, req.Bio.SourceURL = uuid.New(), page+"?new-plugin-page=1"
	written, err = f.writer.ApplyArtistBio(t.Context(), req)
	requireExternalError(t, written, err, 409, "nothing_to_apply", "suppressed")
	req.Key, req.Bio.ID = uuid.New(), "entry-b"
	written, err = f.writer.ApplyArtistBio(t.Context(), req)
	requireExternalApply(t, written, err)
}

func TestPostgreSQLArtistWebSearchMediaPrecedence(t *testing.T) {
	for _, tc := range []struct{ old, replacement string }{
		{"web_search", demoPluginSource},
		{"web_search", "licensed"},
		{demoPluginSource, "licensed"},
	} {
		t.Run(tc.old+"_to_"+tc.replacement, func(t *testing.T) {
			f := newArtistWebSearchFixture(t, "", false, false, nil)
			f.enable(t)
			const name = "合成来源优先级乐团"
			artist := f.addArtist(t, name)
			picture, err := os.ReadFile(filepath.Join("..", "upstreammock", "testdata", "artist-portrait.png"))
			requireCurationOK(t, err)
			page := f.endpoint + "/searxng/artists/coast"
			imageReq := curation.ArtistImageRequest{ArtistID: artist,
				Image: sources.ArtistImage{Data: picture, Attribution: sources.Attribution{SourceURL: page}}}
			bioReq := curation.ArtistBioRequest{ArtistID: artist,
				Bio: sources.ArtistBio{Text: webSearchBio, Lang: "zh", Attribution: sources.Attribution{SourceURL: page}}}
			apply := func(source, entry string, expectOccupied bool) [2]string {
				t.Helper()
				ctx := t.Context()
				imageReq.Key, bioReq.Key = uuid.New(), uuid.New()
				imageReq.Image.Source, bioReq.Bio.Source = source, source
				imageReq.Image.ID, bioReq.Bio.ID = entry, entry
				switch source {
				case "web_search":
					f.markArtistReleasesRecognized(t, artist)
					c := modelConversation(t, f.curationFixture)
					item := f.pendingArtist(t, artist, name)
					mustExec(t, f.application, `UPDATE curation_pending_items SET status='in_progress',conversation_id=$2,claimed_at=now() WHERE id=$1`, item.ID, c)
					ctx = curation.WithModelRun(ctx, &curation.ModelRun{ActorID: f.admin.id, Model: "合成模型", ConversationID: c, PendingItemID: item.ID})
					batch := f.batch(t, "model")
					imageReq.BatchID, bioReq.BatchID = batch, batch
					citations := []catalog.Citation{{URL: page, Title: "合成网页"}}
					imageReq.Citations, bioReq.Citations = citations, citations
					bioReq.Bio.ID = "artist"
				case demoPluginSource:
					imageReq.Name, bioReq.Name = name, name
					imageReq.BatchID, bioReq.BatchID = "", ""
					imageReq.Citations, bioReq.Citations = nil, nil
				case "licensed":
					imageReq.Image.Source, bioReq.Bio.Source = "commons", "wikipedia"
					imageReq.Image.License, bioReq.Bio.License = "CC BY 4.0", "CC BY-SA 4.0"
					imageReq.MBID, bioReq.MBID = artistMediaMBID, artistMediaMBID
					imageReq.BatchID, bioReq.BatchID = "", ""
					imageReq.Citations, bioReq.Citations = nil, nil
				}
				image, imageErr := f.writer.ApplyArtistImage(ctx, imageReq)
				bio, bioErr := f.writer.ApplyArtistBio(ctx, bioReq)
				if expectOccupied {
					requireExternalError(t, image, imageErr, 409, "nothing_to_apply", "occupied")
					requireExternalError(t, bio, bioErr, 409, "nothing_to_apply", "occupied")
					return [2]string{}
				}
				return [2]string{requireExternalApply(t, image, imageErr), requireExternalApply(t, bio, bioErr)}
			}

			old := apply(tc.old, "old-entry", false)
			tables := []string{"artist_external_artworks", "artist_external_bios"}
			var snapshots [2]string
			for i, table := range tables {
				snapshots[i] = programString(t, f.application, `SELECT to_jsonb(r)::text FROM `+table+` r WHERE operation_id=$1`, old[i])
			}
			if tc.replacement == "licensed" {
				linkArtistMedia(t, f.application, artist, artistMediaMBID)
			}
			newOps := apply(tc.replacement, "replacement", false)
			apply(tc.replacement, "same-rank-other-entry", true)

			for i, table := range tables {
				b3RequireCount(t, f.application, 0, `SELECT count(*) FROM `+table+` WHERE operation_id=$1`, old[i])
				b3RequireCount(t, f.application, 1, `SELECT count(*) FROM `+table+` WHERE artist_id=$1 AND operation_id=$2`, artist, newOps[i])
				b3RequireCount(t, f.application, 1, `SELECT count(*) FROM operations WHERE id=$1
					AND jsonb_array_length(after_state->'replaced')=1 AND after_state->'replaced'->0=$2::jsonb
					AND NOT (before_state ? 'replaced')`, newOps[i], snapshots[i])
			}
			f.undo(t, newOps[0])
			b3RequireCount(t, f.application, 0, `SELECT count(*) FROM artist_external_artworks WHERE artist_id=$1`, artist)
			b3RequireCount(t, f.application, 1, `SELECT count(*) FROM artist_external_bios WHERE artist_id=$1 AND operation_id=$2`, artist, newOps[1])
			f.undo(t, newOps[1])
			b3RequireCount(t, f.application, 0, `SELECT count(*) FROM artist_external_bios WHERE artist_id=$1`, artist)
		})
	}
}
