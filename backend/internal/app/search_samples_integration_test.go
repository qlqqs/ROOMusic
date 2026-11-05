package app

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 所有样本经真实 app handler 和既有 recorder 产生；独立夹具不改变其他 API 样本。
func recordSearchSamples(t *testing.T, samples *apiSampleRecorder) {
	t.Helper()
	app := newIntegrationTestApplication(t)
	user := app.createUser(t, "user")
	samples.forbid(user.token)
	samples.forbid(app.allowedRoot)
	samples.forbid(app.dataDirectory)
	root := searchRoot(t, app)
	representative := insertSearchRelease(t, app, root, groupedRelease{title: "原版专辑", artist: "原版艺人", year: 1980, tracks: []string{"present"}})
	fixture := insertSearchRelease(t, app, root, groupedRelease{groupID: representative.groupID, title: "ＡＢＣ 愛", artist: "Beyoncé", year: 2000, tracks: []string{"present", "present", "missing"}})
	mustExec(t, app, `UPDATE releases SET album_artist='さユり',label='Étiquette 唱片',catalog_number='ＳＲＣＬ－１２３４' WHERE id=$1::uuid`, fixture.releaseID)
	mustExec(t, app, `UPDATE tracks SET title='Summer',artist='钢琴演奏者',source_kind='flac_vorbis_comment',duration_seconds=183.5,codec='flac',bit_depth=24,sample_rate=96000,channels=2 WHERE id=$1::uuid`, fixture.trackIDs[0])
	mustExec(t, app, `UPDATE tracks SET title='无音频事实',artist='曲目藝人',source_kind='flac_vorbis_comment' WHERE id=$1::uuid`, fixture.trackIDs[1])
	for i, credit := range []struct{ role, name string }{{"composer", "久石譲"}, {"conductor", "指揮者"}, {"performer", "演奏者"}, {"producer", "製作人"}} {
		mustExec(t, app, `INSERT INTO track_credits(track_id,role,name,position) VALUES($1::uuid,$2,$3,$4)`, fixture.trackIDs[0], credit.role, credit.name, i+1)
	}
	for _, title := range []string{"Tune", "Tune extended", "A Tune inside", "literal%_~"} {
		insertSearchRelease(t, app, root, groupedRelease{title: title, artist: "合成艺人", tracks: []string{"present"}})
	}
	call := func(path string) *httptest.ResponseRecorder {
		return app.request(t, http.MethodGet, path, nil, &user, map[string]string{"X-Request-ID": sampleRequestID})
	}
	record := func(name, path string) { samples.record("search/"+name, call(path), 200) }
	record("catalog-status-legacy", "/api/v1/catalog/status")
	record("suggestions-legacy", "/api/v1/search/suggestions?q=Summer")
	record("albums-legacy", searchURL("albums", "Summer"))
	record("tracks-legacy", searchURL("tracks", "Summer"))
	record("legacy-releases-q", "/api/v1/releases?q=Tune")
	record("legacy-groups-q", "/api/v1/release-groups?q=Tune")
	publishSearch(t, app)
	record("catalog-status-ready", "/api/v1/catalog/status")
	for _, tc := range []struct{ name, path string }{
		{"suggestions", "/api/v1/search/suggestions?q=" + url.QueryEscape("爱")},
		{"suggestions-empty", "/api/v1/search/suggestions?q=" + url.QueryEscape("查无结果")},
		{"suggestions-limit", "/api/v1/search/suggestions?q=Tune&limit=1"},
		{"suggestions-single-character", "/api/v1/search/suggestions?q=" + url.QueryEscape("爱")},
		{"albums", searchURL("albums", "原版专辑")},
		{"albums-non-representative-match", searchURL("albums", "abc 爱")},
		{"tracks", searchURL("tracks", "summer")},
		{"tracks-null-facts", searchURL("tracks", "无音频事实")},
		{"tracks-credit-match", searchURL("tracks", "久石譲")},
		{"tracks-conductor-match", searchURL("tracks", "指挥者")},
		{"tracks-performer-match", searchURL("tracks", "演奏者")},
		{"tracks-producer-match", searchURL("tracks", "制作人")},
		{"tracks-label-match", searchURL("tracks", "etiquette 唱片")},
		{"tracks-catalog-number-match", searchURL("tracks", "srcl1234")},
		{"tracks-artist-match", searchURL("tracks", "曲目艺人")},
		{"results-empty", searchURL("albums", "查无结果")},
		{"results-blank", searchURL("tracks", " \t\n ")},
		{"results-out-of-range", searchURL("tracks", "summer") + "&page=2"},
		{"results-ranking", searchURL("albums", "Tune")},
		{"results-literal-wildcards", searchURL("tracks", "%_~")},
		{"results-single-character", searchURL("tracks", "爱")},
		{"results-after-publish", searchURL("tracks", "beyonce")},
		{"releases-q", "/api/v1/releases?q=Tune"},
		{"groups-q", "/api/v1/release-groups?q=Tune"},
		{"releases-no-q", "/api/v1/releases"},
		{"groups-no-q", "/api/v1/release-groups"},
	} {
		record(tc.name, tc.path)
	}
	// ⑤只补身份：四种署名必须仍只命中本轨，不能因为别名投影把同专辑的 sibling 带进来。
	for _, credit := range []struct{ query, role, original string }{
		{"久石譲", "composer", "久石譲"}, {"指挥者", "conductor", "指揮者"},
		{"演奏者", "performer", "演奏者"}, {"制作人", "producer", "製作人"},
	} {
		page := requireSearchTracks(t, app, &user, credit.query, fixture.trackIDs[0])
		match := page.Items[0].Match
		if match.Field != "credit" || match.Kind != "exact" || match.Role == nil || *match.Role != credit.role || match.Original != credit.original || match.ArtistID == nil || match.Alias != nil {
			t.Fatalf("⑤改变了④署名匹配语义：%+v", match)
		}
	}
	artistPage := requireSearchTracks(t, app, &user, "曲目艺人", fixture.trackIDs[1])
	if match := artistPage.Items[0].Match; match.Field != "track_artist" || match.Kind != "exact" || match.Original != "曲目藝人" || match.ArtistID == nil || match.Alias != nil {
		t.Fatalf("曲目艺人匹配改变：%+v", match)
	}
	releasePage := requireSearchTracks(t, app, &user, "beyonce", fixture.trackIDs[0], fixture.trackIDs[1])
	for _, item := range releasePage.Items {
		if match := item.Match; match.Field != "release_artist" || match.Kind != "exact" || match.Original != "Beyoncé" || match.ArtistID == nil || match.Alias != nil {
			t.Fatalf("发行艺人匹配改变：%+v", match)
		}
	}
	samples.record("search/errors/unauthorized", app.request(t, http.MethodGet, searchURL("tracks", "爱"), nil, nil, map[string]string{"X-Request-ID": sampleRequestID}), 401)
	for _, tc := range []struct{ name, path string }{
		{"invalid-query", searchURL("tracks", strings.Repeat("爱", 67))},
		{"invalid-utf8", searchURL("tracks", string([]byte{0xff}))},
		{"invalid-control", searchURL("tracks", "a\x00b")},
		{"invalid-limit", "/api/v1/search/suggestions?q=x&limit=9"},
		{"invalid-type", "/api/v1/search?type=unknown&q=x"},
		{"invalid-type-singular", "/api/v1/search?type=artist&q=x"},
		{"invalid-pagination", searchURL("tracks", "x") + "&page_size=101"},
	} {
		samples.record("search/errors/"+tc.name, call(tc.path), 400)
	}
	mustExec(t, app, `INSERT INTO release_artworks(release_id,content_hash,mime_type,width,height,storage_key,source_type) VALUES($1::uuid,$2,'image/webp',10,10,'/srv/private/cover.webp','embedded')`, fixture.releaseID, strings.Repeat("f", 64))
	samples.record("search/errors/database-unavailable", call(searchURL("tracks", "summer")), 503)
	recordSearchCountSamples(t, samples)
}

func recordSearchCountSamples(t *testing.T, samples *apiSampleRecorder) {
	t.Helper()
	app := newIntegrationTestApplication(t)
	user := app.createUser(t, "user")
	samples.forbid(user.token)
	samples.forbid(app.allowedRoot)
	insertSearchPaginationFixture(t, app)
	publishSearch(t, app)
	for _, tc := range []struct {
		name, query string
		page, size  int
	}{
		{"results-total-999", "九百", 1, 1},
		{"results-total-1000", "千条", 1, 1},
		{"results-total-1001", "千一", 1, 1},
		{"results-total-capped", "共有", 1, 1},
		{"results-capped-page-21", "千一", 21, 50},
		{"results-capped-out-of-range", "千一", 22, 50},
	} {
		path := searchURL("tracks", tc.query) + fmt.Sprintf("&page=%d&page_size=%d", tc.page, tc.size)
		samples.record("search/"+tc.name, searchResponse(t, app, &user, path), 200)
	}
}

// 搜索专用门禁复用 recorder 的响应校验/规范化，只读写 search 子树；不能调用会重写整个样本目录的 finish。
func TestPostgreSQLSearchResponseSamples(t *testing.T) {
	samples := newAPISampleRecorder(t, newIntegrationTestApplication(t))
	recordSearchSamples(t, samples)
	for _, name := range sortedKeys(samples.samples) {
		if !strings.HasPrefix(name, "search/") {
			t.Fatalf("越界样本：%s", name)
		}
		path := sampleFilePath(name)
		actual := samples.samples[name]
		if os.Getenv(updateAPISamplesEnv) == "1" {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, actual, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		expected, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("读取搜索样本 %s：%v", name, err)
			continue
		}
		if !bytes.Equal(expected, actual) {
			t.Errorf("搜索样本 %s 漂移：%s", name, firstDifference(expected, actual))
		}
	}
	entries, err := filepath.Glob(filepath.Join(apiSamplesDirectory, "search", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	errors, err := filepath.Glob(filepath.Join(apiSamplesDirectory, "search", "errors", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range append(entries, errors...) {
		name, err := filepath.Rel(apiSamplesDirectory, path)
		if err != nil {
			t.Fatal(err)
		}
		name = strings.TrimSuffix(filepath.ToSlash(name), ".json")
		if _, exists := samples.samples[name]; !exists && !isArtistAPISample(name) {
			t.Errorf("多余搜索样本：%s", path)
		}
	}
}
