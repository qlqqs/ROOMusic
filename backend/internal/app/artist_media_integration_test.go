package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/artwork"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/commons"
	"github.com/qlqq/roomusic/backend/internal/sources/fanarttv"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
	"github.com/qlqq/roomusic/backend/internal/sources/theaudiodb"
	"github.com/qlqq/roomusic/backend/internal/sources/wikidata"
	"github.com/qlqq/roomusic/backend/internal/sources/wikipedia"
)

const artistMediaMBID = "10000000-0000-4000-8000-000000000001"

func linkArtistMedia(t *testing.T, app *integrationTestApplication, artist, mbid string) {
	t.Helper()
	mustExec(t, app, `INSERT INTO external_links(id,source,object_type,object_id,external_id,method,confidence)
		VALUES(gen_random_uuid(),'musicbrainz','artist',$1,$2,'artist_credit','certain')`, artist, mbid)
}

func readArtistMedia(t *testing.T, app *integrationTestApplication, user *integrationTestUser, id string) catalog.ArtistDetail {
	t.Helper()
	r := app.request(t, http.MethodGet, "/api/v1/artists/"+id, nil, user, nil)
	if r.Code != http.StatusOK {
		t.Fatalf("艺人 HTTP %d %s", r.Code, r.Body)
	}
	var detail catalog.ArtistDetail
	if err := json.Unmarshal(r.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Image == nil && detail.Artist.Image != nil ||
		detail.Image != nil && (detail.Artist.Image == nil || *detail.Artist.Image != detail.Image.Resource) {
		t.Fatalf("艺人摘要图片与详情不一致：%s", r.Body)
	}
	return detail
}

type mediaUpstream struct {
	fanartStatus                        int
	audioImage, commonsImage, noLicense bool
	wikiZH, wikiEN, audioZH, audioEN    string
}

func (u mediaUpstream) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/ws/2/artist/" + artistMediaMBID:
		fmt.Fprintf(w, `{"id":%q,"name":"合成艺人","relations":[{"type":"wikipedia","url":{"resource":"https://zh.wikipedia.org/wiki/Fixture"}},{"type":"wikipedia","url":{"resource":"https://en.wikipedia.org/wiki/Fixture"}}]}`, artistMediaMBID)
	case "/wikidata/w/api.php":
		switch r.URL.Query().Get("action") {
		case "query":
			if r.URL.Query().Get("srsearch") != "haswbstatement:P434="+artistMediaMBID {
				t.Errorf("不是按艺人 MBID 反查：%s", r.URL)
			}
			fmt.Fprint(w, `{"query":{"search":[{"title":"Q123"}]}}`)
		case "wbgetentities":
			fmt.Fprint(w, `{"entities":{"Q123":{"id":"Q123","claims":{"P18":[{"rank":"normal","mainsnak":{"datavalue":{"value":"Fixture.png"}}}]}}}}`)
		default:
			t.Errorf("意外的 Wikidata 请求：%s", r.URL)
			http.NotFound(w, r)
		}
	case "/fanarttv/v3/music/" + artistMediaMBID:
		if r.Header.Get("api-key") == "" {
			t.Error("Fanart.tv 头像请求缺少项目密钥")
		}
		if u.fanartStatus != 0 && u.fanartStatus != http.StatusOK {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(u.fanartStatus)
			return
		}
		if u.fanartStatus == http.StatusOK {
			fmt.Fprintf(w, `{"artistthumb":[{"id":"fanart-thumb","url":"http://%s/image/fanart","likes":"3"}]}`, r.Host)
			return
		}
		http.NotFound(w, r)
	case "/theaudiodb/api/v1/json/synthetic-audio/artist-mb.php":
		if r.URL.Query().Get("i") != artistMediaMBID {
			t.Errorf("TheAudioDB 对应错误：%s", r.URL)
		}
		var thumb string
		if u.audioImage {
			thumb = "http://" + r.Host + "/image/audio"
		}
		fmt.Fprintf(w, `{"artists":[{"idArtist":"12","strMusicBrainzID":%q,"strArtistThumb":%q,"strBiographyCN":%q,"strBiography":%q}]}`,
			artistMediaMBID, thumb, u.audioZH, u.audioEN)
	case "/commons/w/api.php":
		if !u.commonsImage {
			fmt.Fprint(w, `{"query":{"pages":[{"missing":true}]}}`)
			return
		}
		license := "CC BY 4.0"
		if u.noLicense {
			license = ""
		}
		fmt.Fprintf(w, `{"query":{"pages":[{"title":"File:Fixture.png","imageinfo":[{"thumburl":"http://%s/image/commons","extmetadata":{"LicenseShortName":{"value":%q},"Artist":{"value":"<b>合成摄影组</b>"},"LicenseUrl":{"value":"https://creativecommons.org/licenses/by/4.0/"}}}]}]}}`, r.Host, license)
	case "/wikipedia/zh/api/rest_v1/page/summary/Fixture", "/wikipedia/en/api/rest_v1/page/summary/Fixture":
		lang, extract := "zh", u.wikiZH
		if strings.HasPrefix(r.URL.Path, "/wikipedia/en/") {
			lang, extract = "en", u.wikiEN
		}
		if extract == "" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"title":"Fixture","lang":%q,"extract":%q}`, lang, extract)
	case "/image/fanart", "/image/audio", "/image/commons":
		if r.URL.Path == "/image/commons" && u.noLicense {
			t.Error("下载了无许可图片")
		}
		w.Header().Set("Content-Type", "image/gif")
		w.Write(externalArtworkRequest("").Data)
	default:
		t.Errorf("意外的上游请求：%s %s", r.Method, r.URL)
		http.NotFound(w, r)
	}
}

type artistMediaProgram struct {
	*programFixture
	artist string
	hits   atomic.Int64
}

func newArtistMediaProgram(t *testing.T, upstream mediaUpstream) *artistMediaProgram {
	t.Helper()
	f := &artistMediaProgram{programFixture: newArtistProgram(t)}
	publishSearch(t, f.application)
	f.artist = artistIDByName(t, f.programFixture, "Artist A")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		upstream.serve(t, w, r)
	}))
	t.Cleanup(server.Close)
	f.gate = artistMediaSourceGate(f, server, "/ws/2")
	return f
}

func artistMediaSourceGate(f *artistMediaProgram, server *httptest.Server, musicPath string) *curation.Sources {
	opts := func(id, path string) sources.Options {
		return sources.Options{
			BaseURL: server.URL + path, MockHost: sources.NewMockHost(server.URL),
			NewClient: func(egress.Options) (*http.Client, error) { return server.Client(), nil },
			Limiter:   sources.NewLimiter(0, nil, nil),
			After: func(time.Duration) <-chan time.Time {
				ready := make(chan time.Time, 1)
				ready <- time.Now()
				return ready
			},
			Keys: func(ctx context.Context) (map[string]string, error) { return f.gate.Keys(ctx, id) },
		}
	}
	audio := theaudiodb.New(opts("theaudiodb", "/theaudiodb"))
	music := musicbrainz.New(opts("musicbrainz", musicPath))
	data := wikidata.New(opts("wikidata", "/wikidata"))
	return curation.NewSources(f.application.database,
		curation.SourceRegistration{Declaration: musicbrainz.Declaration(), Artists: music, ArtistLinks: music},
		curation.SourceRegistration{Declaration: wikidata.Declaration(), ArtistLinks: data, WikidataArtists: data},
		curation.SourceRegistration{Declaration: commons.Declaration(), ArtistImages: commons.New(opts("commons", "/commons"))},
		curation.SourceRegistration{Declaration: wikipedia.Declaration(), ArtistBios: wikipedia.New(opts("wikipedia", "/wikipedia"))},
		curation.SourceRegistration{Declaration: fanarttv.Declaration(), ArtistImages: fanarttv.New(opts("fanarttv", "/fanarttv"))},
		curation.SourceRegistration{Declaration: theaudiodb.Declaration(), ArtistImages: audio, ArtistBios: audio},
	).WithScanCheck(f.scanCheck).WithKeyStore(newInstanceSecrets(f.application.dataDirectory))
}

func (f *artistMediaProgram) key(t *testing.T, source, name, value string) {
	t.Helper()
	_, err := f.gate.SetKey(t.Context(), source, name, f.admin.id, value)
	requireCurationOK(t, err)
}

func (f *artistMediaProgram) media(t *testing.T) catalog.ArtistDetail {
	t.Helper()
	return readArtistMedia(t, f.application, &f.admin, f.artist)
}

func TestPostgreSQLArtistMediaProgramPriority(t *testing.T) {
	longLead := strings.Repeat("潮", 4010)
	for _, tc := range []struct {
		name                   string
		upstream               mediaUpstream
		image, bio, lang, text string
		state                  string
	}{
		{"fanart_and_chinese_wikipedia", mediaUpstream{fanartStatus: 200, audioImage: true, commonsImage: true, wikiZH: "<p><b>中文导语</b>[1]。</p><p>后文</p>", audioZH: "较低优先级", wikiEN: "English", audioEN: "Other"},
			"fanarttv", "wikipedia", "zh", "中文导语。", "done"},
		{"fanart_failure_and_audiodb_chinese", mediaUpstream{fanartStatus: 503, audioImage: true, commonsImage: true, audioZH: "<p><strong>" + longLead + "</strong>[27]</p><p>后文</p>", wikiEN: "English", audioEN: "Other"},
			"theaudiodb", "theaudiodb", "zh", strings.Repeat("潮", 4000), "done"},
		{"commons_and_english_wikipedia", mediaUpstream{commonsImage: true, wikiEN: "English lead.", audioEN: "Other"},
			"commons", "wikipedia", "en", "English lead.", "done"},
		{"unlicensed_commons_and_audiodb_english", mediaUpstream{commonsImage: true, noLicense: true, audioEN: "AudioDB lead."},
			"", "theaudiodb", "en", "AudioDB lead.", "done"},
		{"temporary_failure_with_unfilled_gap", mediaUpstream{fanartStatus: 503, wikiZH: "中文导语。"},
			"", "wikipedia", "zh", "中文导语。", "try_later"},
		{"all_active_sources_empty", mediaUpstream{}, "", "", "", "", "no_match"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newArtistMediaProgram(t, tc.upstream)
			linkArtistMedia(t, f.application, f.artist, artistMediaMBID)
			f.key(t, "fanarttv", "project", "synthetic-project")
			f.key(t, "theaudiodb", "api_key", "synthetic-audio")
			if before := f.media(t); before.Image != nil || before.Bio != nil {
				t.Fatalf("起始艺人已有资料：%+v", before)
			}
			f.run(t)
			detail := f.media(t)
			if tc.image == "" {
				if detail.Image != nil {
					t.Errorf("不应有图片，得到 %+v", detail.Image)
				}
			} else if detail.Image == nil || detail.Image.Source != tc.image || detail.Image.SourceURL == nil || *detail.Image.SourceURL == "" {
				t.Errorf("图片=%+v，期望有出处的 %s 图片", detail.Image, tc.image)
			} else if tc.image == "commons" {
				if detail.Image.Author == nil || *detail.Image.Author != "合成摄影组" ||
					detail.Image.License == nil || *detail.Image.License != "CC BY 4.0" ||
					detail.Image.LicenseURL == nil || *detail.Image.LicenseURL != "https://creativecommons.org/licenses/by/4.0/" {
					t.Errorf("Commons 署名不完整：%+v", detail.Image)
				}
			}
			if tc.bio == "" {
				if detail.Bio != nil {
					t.Errorf("不应有简介，得到 %+v", detail.Bio)
				}
			} else if detail.Bio == nil || detail.Bio.Source != tc.bio || detail.Bio.SourceURL == nil || *detail.Bio.SourceURL == "" ||
				detail.Bio.Lang != tc.lang || detail.Bio.Text != tc.text {
				t.Errorf("简介应来自 %s，语言 %s，字数 %d，得到 %+v", tc.bio, tc.lang, len([]rune(tc.text)), detail.Bio)
			} else if tc.bio == "wikipedia" && (detail.Bio.License == nil || *detail.Bio.License != "CC BY-SA 4.0" ||
				detail.Bio.LicenseURL == nil || *detail.Bio.LicenseURL != "https://creativecommons.org/licenses/by-sa/4.0/") {
				t.Errorf("维基百科许可不完整：%+v", detail.Bio)
			}
			state := f.stateFor(t, "artist_media", f.artist)
			if state.Status != tc.state || (state.NextAttemptAt != nil) != (tc.state == "try_later") {
				t.Errorf("艺人工作状态=%+v，期望 %s", state, tc.state)
			}
			want := 0
			if tc.image != "" {
				want++
			}
			if tc.bio != "" {
				want++
			}
			f.assertCount(t, want, `SELECT count(*) FROM operations o JOIN curation_batches b ON b.id=o.batch_id
				WHERE o.target_type='artist' AND o.target_id=$1 AND b.tier='program'
				  AND o.origin->>'source'='curation' AND o.origin->>'provider_kind'='source'
				  AND ((o.operation_type='artist_external_artwork' AND o.origin->>'provider'=$2 AND o.after_state->'image'->>'source_url'<>'')
				    OR (o.operation_type='artist_external_bio' AND o.origin->>'provider'=$3 AND o.after_state->'bio'->>'source_url'<>''))`,
				f.artist, tc.image, tc.bio)
			if want > 0 {
				f.assertCount(t, 1, `SELECT count(DISTINCT batch_id) FROM operations WHERE target_id=$1 AND operation_type IN ('artist_external_artwork','artist_external_bio')`, f.artist)
			} else {
				hits := f.hits.Load()
				f.clock.advance(t, 6*time.Hour)
				after := f.stateFor(t, "artist_media", f.artist)
				if state.Attempts != 1 || hits == 0 || after.Generation != state.Generation || f.hits.Load() != hits {
					t.Fatalf("空结果应只尝试一次：状态=%+v，将来=%+v，请求=%d/%d", state, after, hits, f.hits.Load())
				}
			}
		})
	}
}

func TestPostgreSQLArtistMediaProgramGateAndEvidence(t *testing.T) {
	f := newArtistMediaProgram(t, mediaUpstream{fanartStatus: 200, audioImage: true, audioZH: "补入密钥后的资料。"})
	f.sourceEnabled(t, "fanarttv", false)
	f.sourceEnabled(t, "wikidata", false)
	f.sourceEnabled(t, "commons", false)
	f.sourceEnabled(t, "wikipedia", false)
	f.key(t, "fanarttv", "project", "synthetic-project")
	// 只有艺人标签和其他对象类型的对应时，不能把资料挂到同名艺人。
	track, _ := externalTrack(t, f.application, f.a)
	programTag(t, f.application, track, "mb_artist", artistMediaMBID)
	mustExec(t, f.application, `INSERT INTO external_links(id,source,object_type,object_id,external_id,method,confidence)
		VALUES(gen_random_uuid(),'musicbrainz','track',$1,$2,'tag_id','certain')`, track, artistMediaMBID)
	f.run(t)
	if state := f.stateFor(t, "artist_media", f.artist); state.Status != "" || f.hits.Load() != 0 {
		t.Fatalf("无艺人对应时工作状态=%+v，请求数=%d", state, f.hits.Load())
	}

	// 先开启全部必要条件再关闭总开关，避免零请求断言由缺密钥空通过。
	linkArtistMedia(t, f.application, f.artist, artistMediaMBID)
	f.sourceEnabled(t, "fanarttv", true)
	_, err := f.writer.SetSettings(t.Context(), f.admin.id, false)
	requireCurationOK(t, err)
	f.clock.advance(t, time.Minute)
	if f.hits.Load() != 0 || f.media(t).Image != nil {
		t.Fatal("总开关关闭时发送了请求或写入了图片")
	}

	f.sourceEnabled(t, "fanarttv", false)
	f.enable(t)
	f.clock.advance(t, time.Minute)
	settings, err := f.gate.List(t.Context())
	requireCurationOK(t, err)
	index := slices.IndexFunc(settings, func(s curation.SourceSettings) bool { return s.ID == "theaudiodb" })
	if index < 0 {
		t.Fatal("缺少 TheAudioDB 设置")
	}
	b3Reason(t, settings[index], "key_missing")
	first := f.stateFor(t, "artist_media", f.artist)
	if first.Status != "no_match" || first.Attempts != 1 || first.InputDigest == "" || f.hits.Load() != 0 ||
		f.media(t).Image != nil || f.media(t).Bio != nil {
		t.Fatalf("所有来源无可用资料，状态=%+v，请求数=%d", first, f.hits.Load())
	}
	f.clock.advance(t, 6*time.Hour)
	if again := f.stateFor(t, "artist_media", f.artist); again.Generation != first.Generation || again.Attempts != first.Attempts {
		t.Fatalf("证据未变却重做：原=%+v，后=%+v", first, again)
	}

	mustExec(t, f.application, `UPDATE external_links SET external_id=$2 WHERE object_type='artist' AND object_id=$1`, f.artist, uuid.New())
	f.clock.advance(t, 6*time.Hour)
	second := f.stateFor(t, "artist_media", f.artist)
	if second.Status != "no_match" || second.InputDigest == first.InputDigest || second.Attempts != first.Attempts+1 {
		t.Fatalf("艺人对应变化后应重做：原=%+v，后=%+v", first, second)
	}
	mustExec(t, f.application, `UPDATE external_links SET external_id=$2 WHERE object_type='artist' AND object_id=$1`, f.artist, artistMediaMBID)
	f.clock.advance(t, 6*time.Hour)
	third := f.stateFor(t, "artist_media", f.artist)
	f.key(t, "theaudiodb", "api_key", "synthetic-audio")
	f.clock.advance(t, time.Minute)
	detail, done := f.media(t), f.stateFor(t, "artist_media", f.artist)
	if done.Status != "done" || done.InputDigest == third.InputDigest || done.Generation <= third.Generation ||
		detail.Image == nil || detail.Image.Source != "theaudiodb" || detail.Bio == nil || detail.Bio.Text != "补入密钥后的资料。" {
		t.Fatalf("只修改密钥后应补入资料：状态=%+v，图片=%+v，简介=%+v", done, detail.Image, detail.Bio)
	}
}

func TestPostgreSQLArtistMediaBundledKeyGate(t *testing.T) {
	f := newArtistMediaProgram(t, mediaUpstream{fanartStatus: 200})
	linkArtistMedia(t, f.application, f.artist, artistMediaMBID)
	for _, id := range []string{"wikidata", "commons", "wikipedia", "theaudiodb"} {
		f.sourceEnabled(t, id, false)
	}
	settings, err := f.gate.List(t.Context())
	requireCurationOK(t, err)
	withBundled := slices.Contains(fanarttv.Declaration().OptionalKeys, "project")
	index := slices.IndexFunc(settings, func(s curation.SourceSettings) bool { return s.ID == "fanarttv" })
	if index < 0 {
		t.Fatal("缺少 Fanart.tv 设置")
	}
	want := "key_missing"
	if withBundled {
		want = ""
	}
	b3Reason(t, settings[index], want)
	f.run(t)
	detail := f.media(t)
	if withBundled {
		if detail.Image == nil || detail.Image.Source != "fanarttv" {
			t.Fatalf("内置项目密钥应可直接补入 Fanart.tv 图片，得到 %+v", detail.Image)
		}
	} else if detail.Image != nil || f.hits.Load() != 0 {
		t.Fatalf("缺项目密钥却补图或发请求：图片=%+v，请求数=%d", detail.Image, f.hits.Load())
	}
}

func TestPostgreSQLArtistMediaApplyRevert(t *testing.T) {
	f := newArtistMediaProgram(t, mediaUpstream{
		fanartStatus: 200, audioImage: true, wikiZH: "维基百科导语。", audioZH: "另一来源的导语。",
	})
	app, ctx := f.application, t.Context()
	linkArtistMedia(t, app, f.artist, artistMediaMBID)
	f.key(t, "fanarttv", "project", "synthetic-project")
	f.key(t, "theaudiodb", "api_key", "synthetic-audio")
	f.run(t)
	first := f.media(t)
	if first.Image == nil || first.Image.Source != "fanarttv" || first.Image.SourceURL == nil ||
		first.Bio == nil || first.Bio.Source != "wikipedia" || first.Bio.SourceURL == nil {
		t.Fatalf("首轮图片=%+v，简介=%+v", first.Image, first.Bio)
	}
	originalImageOp := programString(t, app, "SELECT operation_id::text FROM artist_external_artworks WHERE artist_id=$1", f.artist)
	originalBioOp := programString(t, app, "SELECT operation_id::text FROM artist_external_bios WHERE artist_id=$1", f.artist)
	data := externalArtworkRequest("").Data
	stored, path := externalArtworkFile(t, app, data)
	requireExternalArtworkHTTP(t, app, &f.admin, stored, data)

	imageReq := curation.ArtistImageRequest{ArtistID: f.artist, MBID: artistMediaMBID,
		Image: sources.ArtistImage{ID: "another-thumb", Data: append(slices.Clone(data), 1),
			Attribution: sources.Attribution{Source: "theaudiodb", SourceURL: "https://www.theaudiodb.com/artist/12"}}}
	bioReq := curation.ArtistBioRequest{ArtistID: f.artist, MBID: artistMediaMBID,
		Bio: sources.ArtistBio{ID: "another-bio", Lang: "en", Text: "Different lead.",
			Attribution: sources.Attribution{Source: "theaudiodb", SourceURL: "https://www.theaudiodb.com/artist/12"}}}
	_, replacedPath := externalArtworkFile(t, app, imageReq.Image.Data)
	imageReq.Key = uuid.New()
	result, err := f.writer.ApplyArtistImage(ctx, imageReq)
	requireExternalError(t, result, err, http.StatusConflict, "nothing_to_apply", "occupied")
	requireExternalFile(t, replacedPath, nil)
	bioReq.Key = uuid.New()
	result, err = f.writer.ApplyArtistBio(ctx, bioReq)
	requireExternalError(t, result, err, http.StatusConflict, "nothing_to_apply", "occupied")
	if got := f.media(t); !reflect.DeepEqual(got.Image, first.Image) || !reflect.DeepEqual(got.Bio, first.Bio) {
		t.Fatalf("补缺覆盖了已有资料：图片=%+v，简介=%+v", got.Image, got.Bio)
	}

	// 发行本地封面和外部艺人图片共享一个存储键。
	store := artwork.NewStore(app.dataDirectory)
	requireCurationOK(t, store.Save(ctx, app.database, f.b, data, "embedded"))
	reverted := f.undo(t, originalImageOp)
	if reverted["target_id"] != f.artist || reverted["target_type"] != "artist" || reverted["revision"] != float64(first.Revision) {
		t.Fatalf("艺人图片的单条撤销回执=%+v", reverted)
	}
	if got := f.media(t); got.Image != nil || !reflect.DeepEqual(got.Bio, first.Bio) {
		t.Fatalf("撤销图片后的图片=%+v，简介=%+v", got.Image, got.Bio)
	}
	requireExternalFile(t, path, data)
	requireExternalArtworkHTTP(t, app, &f.admin, stored, data)
	reverted = f.undo(t, originalBioOp)
	if reverted["target_id"] != f.artist || reverted["target_type"] != "artist" || reverted["revision"] != float64(first.Revision) {
		t.Fatalf("艺人简介的单条撤销回执=%+v", reverted)
	}
	if got := f.media(t); got.Image != nil || got.Bio != nil {
		t.Fatalf("单条撤销后的图片=%+v，简介=%+v", got.Image, got.Bio)
	}
	// 验证写入端也拒绝同一条目，避免只在程序层隐藏。
	imageReq.Key, imageReq.Image.ID, imageReq.Image.Source, imageReq.Image.SourceURL, imageReq.Image.Data = uuid.New(), "fanart-thumb", first.Image.Source, *first.Image.SourceURL, data
	result, err = f.writer.ApplyArtistImage(ctx, imageReq)
	requireExternalError(t, result, err, http.StatusConflict, "nothing_to_apply", "suppressed")
	bioReq.Key = uuid.New()
	bioReq.Bio = sources.ArtistBio{ID: *first.Bio.SourceURL, Text: first.Bio.Text, Lang: first.Bio.Lang,
		Attribution: sources.Attribution{Source: first.Bio.Source, SourceURL: *first.Bio.SourceURL}}
	result, err = f.writer.ApplyArtistBio(ctx, bioReq)
	requireExternalError(t, result, err, http.StatusConflict, "nothing_to_apply", "suppressed")

	f.clock.advance(t, time.Minute)
	second := f.media(t)
	if second.Image == nil || second.Image.Source != "theaudiodb" || second.Image.Resource != stored.Key ||
		second.Bio == nil || second.Bio.Source != "theaudiodb" || second.Bio.Text != "另一来源的导语。" {
		t.Fatalf("重新整理应补入其他来源：图片=%+v，简介=%+v", second.Image, second.Bio)
	}
	requireCurationOK(t, store.Remove(ctx, app.database, f.b))
	requireExternalFile(t, path, data)
	requireExternalArtworkHTTP(t, app, &f.admin, stored, data)
	resultScope := f.scope(t, curation.Scope{Kind: "provider", Provider: "theaudiodb"}, "", uuid.New(), "artist-media-scope")
	if resultScope.Reverted != 2 || len(resultScope.Skipped) != 0 {
		t.Fatalf("按来源撤销=%+v，期望撤销两条", resultScope)
	}
	if got := f.media(t); got.Image != nil || got.Bio != nil {
		t.Fatalf("按来源撤销后的图片=%+v，简介=%+v", got.Image, got.Bio)
	}
	requireExternalFile(t, path, nil)
	if r := app.request(t, http.MethodGet, "/api/v1/artworks/"+stored.Key, nil, &f.admin, nil); r.Code != http.StatusNotFound {
		t.Fatalf("失去全部引用后的 artwork HTTP %d %s", r.Code, r.Body)
	}
	f.clock.advance(t, time.Minute)
	if got := f.media(t); got.Image != nil || got.Bio != nil {
		t.Fatalf("撤销全部来源后重新出现图片=%+v，简介=%+v", got.Image, got.Bio)
	}
	f.assertCount(t, 4, `SELECT count(*) FROM curation_suppressions WHERE target_type='artist' AND target_id=$1 AND subject IN ('artist_image','artist_bio')`, f.artist)
	f.assertCount(t, 4, `SELECT count(*) FROM operations WHERE target_type='artist' AND target_id=$1 AND operation_type IN ('artist_external_artwork','artist_external_bio')`, f.artist)
}

func TestPostgreSQLArtistMediaMergedReads(t *testing.T) {
	f := newArtistMergeFixture(t)
	app, ctx := f.application, t.Context()
	linkArtistMedia(t, app, f.alias, artistMediaMBID)
	if got := readArtistMedia(t, app, &f.admin, f.primary); got.Image != nil || got.Bio != nil {
		t.Fatalf("目标艺人起始已有资料：图片=%+v，简介=%+v", got.Image, got.Bio)
	}
	img := sources.ArtistImage{ID: "portrait", Data: externalArtworkRequest("").Data, Attribution: sources.Attribution{
		Source: "commons", SourceURL: "https://commons.wikimedia.org/wiki/File:Fixture.png",
		Author: "合成作者", License: "CC BY 4.0", LicenseURL: "https://creativecommons.org/licenses/by/4.0/",
	}}
	bio := sources.ArtistBio{ID: "wiki:alias", Text: "被并入艺人的合成简介。", Lang: "zh", Attribution: sources.Attribution{
		Source: "wikipedia", SourceURL: "https://zh.wikipedia.org/wiki/Fixture",
		License: "CC BY-SA 4.0", LicenseURL: "https://creativecommons.org/licenses/by-sa/4.0/",
	}}
	imageReq := curation.ArtistImageRequest{ArtistID: f.alias, MBID: artistMediaMBID, Key: uuid.New(), Image: img}
	bioReq := curation.ArtistBioRequest{ArtistID: f.alias, MBID: artistMediaMBID, Key: uuid.New(), Bio: bio}
	imageApplied, err := f.writer.ApplyArtistImage(ctx, imageReq)
	imageOp := requireExternalApply(t, imageApplied, err)
	bioApplied, err := f.writer.ApplyArtistBio(ctx, bioReq)
	requireExternalApply(t, bioApplied, err)
	original := readArtistMedia(t, app, &f.admin, f.alias)
	if original.Image == nil || original.Bio == nil {
		t.Fatalf("被并入艺人的初始资料缺失：图片=%+v，简介=%+v", original.Image, original.Bio)
	}

	f.manualMerge(t, f.primary, f.alias)
	detail := readArtistMedia(t, app, &f.admin, f.primary)
	if detail.Artist.ID != f.primary || !detail.Artist.Available || !reflect.DeepEqual(detail.Image, original.Image) ||
		!reflect.DeepEqual(detail.Bio, original.Bio) {
		t.Errorf("有效艺人的摘要=%+v，图片=%+v，简介=%+v", detail.Artist, detail.Image, detail.Bio)
	}
	redirect := readArtistMedia(t, app, &f.admin, f.alias)
	if redirect.Artist.ID != f.alias || redirect.RedirectArtistID == nil || *redirect.RedirectArtistID != f.primary {
		t.Errorf("原艺人地址的重定向不符：%+v", redirect)
	}
	r := app.request(t, http.MethodGet, "/api/v1/artists?sort=name", nil, &f.admin, nil)
	if r.Code != http.StatusOK {
		t.Fatalf("艺人列表 HTTP %d %s", r.Code, r.Body)
	}
	var list struct{ Items []catalog.ArtistSummary }
	if err := json.Unmarshal(r.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(list.Items, func(a catalog.ArtistSummary) bool { return a.ID == f.primary })
	if index < 0 || list.Items[index].Image == nil || *list.Items[index].Image != original.Image.Resource {
		t.Fatalf("列表缺少有效艺人的继承图片：%s", r.Body)
	}

	imageReq.ArtistID, imageReq.Key = f.primary, uuid.New()
	applied, err := f.writer.ApplyArtistImage(ctx, imageReq)
	requireExternalError(t, applied, err, http.StatusConflict, "nothing_to_apply", "occupied")
	f.undo(t, imageOp)
	if got := readArtistMedia(t, app, &f.admin, f.primary); got.Image != nil || !reflect.DeepEqual(got.Bio, original.Bio) {
		t.Fatalf("撤销被并入身份的图片后，有效艺人图片=%+v，简介=%+v", got.Image, got.Bio)
	}
}
