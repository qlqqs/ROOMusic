package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
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

func TestPostgreSQL整理网页媒体校验与独立审查(t *testing.T) {
	f := newControlledArtistWebSearch(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/searxng/artists/coast":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<!doctype html><html><body><p>海岸来信。%s</p><img src="portrait.png"><img src="/test-small.gif"><img src="/test-not-image"></body></html>`, webSearchBio)
		case "/test-small.gif":
			w.Header().Set("Content-Type", "image/gif")
			w.Write(externalArtworkRequest("").Data)
		case "/test-not-image":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "合成非图片响应")
		default:
			return false
		}
		return true
	})

	artist := f.addArtist(t, "海岸来信")
	item := f.pendingArtist(t, artist, "海岸来信")
	page := f.endpoint + "/searxng/artists/coast"
	imageURL := f.endpoint + "/searxng/artists/portrait.png"
	f.enable(t)
	next := f.next(t, false)
	conversationID := f.item(t, item.ID).ConversationID
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"图片网页不在引用中", func(input map[string]any) { input["image"].(map[string]string)["page_url"] = page + "-other" }},
		{"简介超长", func(input map[string]any) { input["bio"].(map[string]string)["text"] = strings.Repeat("字", 4001) }},
		{"没有引用", func(input map[string]any) { input["citations"] = []any{} }},
		{"引用超过五条", func(input map[string]any) {
			input["citations"] = makeWebSearchCitations(page, 6, "合成引用")
		}},
		{"引用标题超过五百字", func(input map[string]any) {
			input["citations"] = makeWebSearchCitations(page, 1, strings.Repeat("字", 501))
		}},
		{"注册表拒绝未知属性", func(input map[string]any) { input["unexpected"] = true }},
	} {
		input := webSearchMediaInput(item.ID, imageURL, page, true)
		tc.change(input)
		next.tool(t, "submit_artist_media", input)
		next = f.next(t, false)
		if got := next.lastToolResult(t); !strings.Contains(got, "invalid_input") && !strings.Contains(got, "validation_failed") {
			t.Errorf("%s 未返回输入校验错误：%s", tc.name, got)
		}
		b3RequireCount(t, f.application, 0, `SELECT count(*) FROM operations WHERE target_id=$1 AND target_type='artist'`, artist)
		f.status(t, artist, "running", 0)
	}
	next.tool(t, "web_search", map[string]string{"q": "海岸来信"})
	next = f.next(t, false)
	for _, asset := range []string{"/test-small.gif", "/test-not-image"} {
		next.tool(t, "submit_artist_media", webSearchMediaInput(item.ID, f.endpoint+asset, page, true))
		f.next(t, true).review(t, "approved")
		next = f.next(t, false)
		if got := next.lastToolResult(t); !strings.Contains(got, "invalid_input") {
			t.Fatalf("无效图片 %s 应在写入前拒绝：%s", asset, got)
		}
		b3RequireCount(t, f.application, 0, `SELECT count(*) FROM operations WHERE target_id=$1 AND target_type='artist'`, artist)
	}

	next.tool(t, "submit_artist_media", webSearchMediaInput(item.ID, imageURL, page, true))
	review := f.next(t, true)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM operations WHERE target_id=$1 AND target_type='artist'`, artist)
	review.review(t, "approved")
	f.waitArtistResolution(t, item.ID, "model_media")
	f.requireWebSearchMedia(t, artist, "合成艺人名录")
	conversation, err := runtime.NewStore(f.application.database).GetConversation(t.Context(), f.admin.id, conversationID)
	requireCurationOK(t, err)
	if len(conversation.Plans) != 3 || conversation.Plans[0].Review == nil {
		t.Fatalf("网页媒体的审查方案 %+v", conversation.Plans)
	}
}

func TestPostgreSQL整理未证实的媒体出处不写入(t *testing.T) {
	f := newControlledArtistWebSearch(t, nil)
	artist := f.addArtist(t, "海岸来信")
	item := f.pendingArtist(t, artist, "海岸来信")
	page := f.endpoint + "/searxng/artists/coast"
	var privateCalls atomic.Int64
	private := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		privateCalls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(private.Close)
	privateURL, err := url.Parse(private.URL)
	requireCurationOK(t, err)
	port, err := strconv.Atoi(privateURL.Port())
	requireCurationOK(t, err)
	f.update(t, http.MethodPost, "/api/v1/admin/steward/egress-allowlist", map[string]any{"host": privateURL.Hostname(), "port": port})
	f.enable(t)
	f.next(t, false).tool(t, "web_search", map[string]string{"q": "海岸来信"})
	f.next(t, false).tool(t, "submit_artist_media", webSearchMediaInput(item.ID, private.URL+"/portrait.png", page, true))
	f.next(t, true).review(t, "approved")
	f.waitArtistResolution(t, item.ID, "suggestion_recorded")
	if privateCalls.Load() != 0 {
		t.Fatal("媒体提交连接了内网图片")
	}
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM operations WHERE target_id=$1 AND target_type='artist'`, artist)
}

func TestPostgreSQLArtistWebSearchMediaCompletesSingleField(t *testing.T) {
	const artistName = "只查到简介的乐团"
	f := newControlledArtistWebSearch(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/searxng/search":
			w.Header().Set("Content-Type", "application/json")
			results := make([]map[string]string, 0, 5)
			for _, citation := range makeWebSearchCitations("http://"+r.Host+"/searxng/artists/coast", 5, "合成名录") {
				results = append(results, map[string]string{"url": citation["url"], "title": citation["title"], "content": webSearchBio})
			}
			json.NewEncoder(w).Encode(map[string]any{"results": results})
		case "/searxng/artists/coast":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<!doctype html><html><body><p>%s。%s</p></body></html>`, artistName, webSearchBio)
		default:
			return false
		}
		return true
	})
	artist := f.addArtist(t, artistName)
	item := f.pendingArtist(t, artist, artistName)
	f.enable(t)
	page := f.endpoint + "/searxng/artists/coast"
	input := webSearchMediaInput(item.ID, "", page, true)
	title := strings.Repeat("字", 500)
	input["citations"] = makeWebSearchCitations(page, 5, title)
	f.next(t, false).tool(t, "web_search", map[string]string{"q": artistName})
	f.next(t, false).tool(t, "submit_artist_media", input)
	f.next(t, true).review(t, "approved")
	f.waitArtistResolution(t, item.ID, "model_media")
	got := readArtistMedia(t, f.application, &f.admin, artist)
	if got.Image != nil || got.Bio == nil || got.Bio.Text != webSearchBio || len(got.Bio.Citations) != 5 || got.Bio.Citations[0].Title != title {
		t.Fatalf("单项提交后的资料图片=%+v 简介=%+v", got.Image, got.Bio)
	}
	f.status(t, artist, "none", 0)
}

func makeWebSearchCitations(page string, count int, title string) []map[string]string {
	citations := make([]map[string]string, count)
	for i := range citations {
		citations[i] = map[string]string{"url": page, "title": title}
		if i != 0 {
			citations[i]["url"] += "?fixture=" + strconv.Itoa(i)
		}
	}
	return citations
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
	f := newControlledArtistWebSearch(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/searxng/search":
			w.Header().Set("Content-Type", "application/json")
			page := "http://" + r.Host + "/searxng/artists/coast"
			json.NewEncoder(w).Encode(map[string]any{"results": []map[string]string{
				{"url": page, "title": "合成名录", "content": webSearchBio},
				{"url": page + "?new-citation=1", "title": "合成名录", "content": webSearchBio},
			}})
		case "/searxng/artists/coast":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<!doctype html><html><body><p>合成资料抑制乐团。%s这次引用的网页和简介内容均已改变。</p><img src="portrait.png"><img src="portrait.png?new-image=1"></body></html>`, webSearchBio)
		default:
			return false
		}
		return true
	})
	f.addRelease(t, "合成资料抑制乐团")
	f.addRelease(t, "合成插件抑制乐团")
	publishSearch(t, f.application)
	artist := f.artistID(t, "合成资料抑制乐团")
	item := f.pendingArtist(t, artist, "合成资料抑制乐团")
	page := f.endpoint + "/searxng/artists/coast"
	imageURL := f.endpoint + "/searxng/artists/portrait.png"
	f.enable(t)
	f.next(t, false).tool(t, "web_search", map[string]string{"q": "合成资料抑制乐团"})
	f.next(t, false).tool(t, "submit_artist_media", webSearchMediaInput(item.ID, imageURL, page, true))
	f.next(t, true).review(t, "approved")
	f.waitArtistResolution(t, item.ID, "model_media")
	f.rejectMedia(t, artist, "image")
	f.rejectMedia(t, artist, "bio")
	for _, marker := range []struct{ field, id string }{{"artist_image", sources.ImageURLID(imageURL)}, {"artist_bio", "artist"}} {
		b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_suppressions WHERE target_type='artist' AND target_id=$1
			AND subject=$2 AND value->>'source'='web_search' AND value->>'external_id'=$3`, artist, marker.field, marker.id)
	}

	item = f.pendingArtist(t, artist, "合成资料抑制乐团")
	input := webSearchMediaInput(item.ID, imageURL, page+"?new-citation=1", true)
	input["bio"].(map[string]string)["text"] = webSearchBio + "这次引用的网页和简介内容均已改变。"
	f.next(t, false).tool(t, "web_search", map[string]string{"q": "合成资料抑制乐团"})
	f.next(t, false).tool(t, "submit_artist_media", input)
	f.next(t, true).review(t, "approved")
	next := f.next(t, false)
	if got := next.lastToolResult(t); strings.Count(got, "suppressed") != 2 {
		t.Fatalf("相同图片地址与不同简介均应受抑制，返回 %s", got)
	}
	first := readArtistMedia(t, f.application, &f.admin, artist)
	if first.Image != nil || first.Bio != nil {
		t.Fatalf("抑制后资料重新出现：图片=%+v 简介=%+v", first.Image, first.Bio)
	}
	next.tool(t, "submit_artist_media", webSearchMediaInput(item.ID, imageURL+"?new-image=1", page, false))
	f.next(t, true).review(t, "approved")
	f.waitArtistResolution(t, item.ID, "model_media")
	detail := readArtistMedia(t, f.application, &f.admin, artist)
	if detail.Image == nil || detail.Bio != nil {
		t.Fatalf("新图片地址应可写而简介应仍空：图片=%+v 简介=%+v", detail.Image, detail.Bio)
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
					item := modelClaim(t, f.curationFixture, c, f.pendingArtist(t, artist, name))
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
