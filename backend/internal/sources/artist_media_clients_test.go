package sources_test

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/commons"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
	"github.com/qlqq/roomusic/backend/internal/sources/theaudiodb"
	"github.com/qlqq/roomusic/backend/internal/sources/wikidata"
	"github.com/qlqq/roomusic/backend/internal/sources/wikipedia"
)

const mediaMBID = "10000000-0000-4000-8000-000000000001"

func mediaServer(t *testing.T, handler http.HandlerFunc) (sources.Options, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	mock := sources.NewMockHost(server.URL)
	return sources.Options{BaseURL: server.URL, MockHost: mock, Limiter: sources.NewLimiter(0, nil, nil),
		NewClient: func(egress.Options) (*http.Client, error) { return server.Client(), nil }}, server
}

func wantMediaRequest(t *testing.T, r *http.Request, path string, query url.Values) {
	t.Helper()
	if r.Method != http.MethodGet || r.URL.EscapedPath() != path || !reflect.DeepEqual(r.URL.Query(), query) {
		t.Errorf("请求=%s %s，期望 GET %s %v", r.Method, r.URL, path, query)
	}
	if ua := r.UserAgent(); !strings.HasPrefix(ua, "ROOMusic/") || !strings.Contains(ua, "https://github.com/qlqqs/ROOMusic") {
		t.Errorf("User-Agent=%q", ua)
	}
}

func wantWikimediaHeaders(t *testing.T, r *http.Request, lang string) {
	t.Helper()
	if r.Header.Get("Accept-Encoding") != "gzip" || r.Header.Get("Accept-Language") != lang {
		t.Errorf("维基媒体请求头=%v，期望 gzip 和语言 %q", r.Header, lang)
	}
}

func TestArtistMediaMusicBrainzURLRelations(t *testing.T) {
	opts, _ := mediaServer(t, func(w http.ResponseWriter, r *http.Request) {
		wantMediaRequest(t, r, "/ws/2/artist/"+mediaMBID, url.Values{"fmt": {"json"}, "inc": {"aliases url-rels"}})
		fmt.Fprintf(w, `{"id":%q,"name":"合成艺人","relations":[
			{"type":"wikidata","url":{"resource":"https://www.wikidata.org/wiki/Q123"}},
			{"type":"wikipedia","url":{"resource":"https://zh.wikipedia.org/wiki/合成藝人"}},
			{"type":"wikipedia","url":{"resource":"https://en.wikipedia.org/wiki/Sample_Artist"}},
			{"type":"official homepage","url":{"resource":"https://www.wikidata.org/wiki/Q789"}},
			{"type":"wikipedia","url":{"resource":"https://en.wikipedia.org.example.test/wiki/Other"}}]}`, mediaMBID)
	})
	opts.BaseURL += "/ws/2"
	links, err := musicbrainz.New(opts).ArtistLinks(t.Context(), sources.ArtistLinks{MBID: mediaMBID})
	want := sources.ArtistLinks{MBID: mediaMBID, WikidataID: "Q123", WikipediaZH: "合成藝人", WikipediaEN: "Sample_Artist"}
	if err != nil || links != want {
		t.Fatalf("对应=%+v, err=%v，期望 %+v", links, err, want)
	}
}

func TestArtistMediaWikidataP434(t *testing.T) {
	for _, tc := range []struct {
		name, qid, search string
		wantErr           error
	}{
		{name: "known", qid: "Q123"},
		{name: "P434", search: `[{"title":"Q123"}]`},
		{name: "ambiguous", search: `[{"title":"Q123"},{"title":"Q789"}]`, wantErr: sources.ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts, _ := mediaServer(t, func(w http.ResponseWriter, r *http.Request) {
				wantWikimediaHeaders(t, r, "")
				if r.URL.Query().Get("action") == "query" {
					if tc.search == "" {
						t.Error("已有 Q 号却反查 P434")
					}
					wantMediaRequest(t, r, "/w/api.php", url.Values{"action": {"query"}, "list": {"search"}, "srsearch": {"haswbstatement:P434=" + mediaMBID}, "srlimit": {"2"}, "format": {"json"}})
					fmt.Fprintf(w, `{"query":{"search":%s}}`, tc.search)
					return
				}
				if tc.wantErr != nil {
					t.Error("P434 有多个结果却读取条目")
				}
				wantMediaRequest(t, r, "/w/api.php", url.Values{"action": {"wbgetentities"}, "ids": {"Q123"}, "props": {"claims|sitelinks"}, "sitefilter": {"zhwiki|enwiki"}, "format": {"json"}})
				io.WriteString(w, `{"entities":{"Q123":{"id":"Q123","claims":{"P18":[
				{"rank":"deprecated","mainsnak":{"datavalue":{"value":"Old.jpg"}}},
				{"rank":"normal","mainsnak":{"datavalue":{"value":"First.png"}}},
				{"rank":"preferred","mainsnak":{"datavalue":{"value":"Selected file.png"}}}]},
				"sitelinks":{"zhwiki":{"title":"合成艺人"},"enwiki":{"title":"Sample Artist"}}}}}`)
			})
			got, err := wikidata.New(opts).ArtistLinks(t.Context(), sources.ArtistLinks{MBID: mediaMBID, WikidataID: tc.qid})
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) || got.WikidataID != "" || got.CommonsFile != "" {
					t.Fatalf("对应=%+v, err=%v，期望 %v", got, err, tc.wantErr)
				}
				return
			}
			want := sources.ArtistLinks{MBID: mediaMBID, WikidataID: "Q123", CommonsFile: "Selected file.png", WikipediaZH: "合成艺人", WikipediaEN: "Sample Artist"}
			if err != nil || got != want {
				t.Fatalf("对应=%+v, err=%v，期望 %+v", got, err, want)
			}
		})
	}
}

func TestArtistMediaCommonsThumbnailAndLicense(t *testing.T) {
	for _, license := range []string{"CC BY 4.0", ""} {
		t.Run(license+"-license", func(t *testing.T) {
			opts, _ := mediaServer(t, func(w http.ResponseWriter, r *http.Request) {
				wantWikimediaHeaders(t, r, "")
				switch r.URL.Path {
				case "/w/api.php":
					wantMediaRequest(t, r, "/w/api.php", url.Values{"action": {"query"}, "titles": {"File:Sample Artist.png"}, "prop": {"imageinfo"},
						"iiprop": {"url|size|mime|extmetadata"}, "iiurlwidth": {"960"}, "iiextmetadatafilter": {"Artist|Attribution|Credit|LicenseShortName|LicenseUrl"}, "formatversion": {"2"}, "format": {"json"}})
					fmt.Fprintf(w, `{"query":{"pages":[{"title":"File:Sample Artist.png","imageinfo":[{"url":"http://%s/original","thumburl":"http://%s/960px-sample.png",
					"descriptionurl":"https://commons.wikimedia.org/wiki/File:Sample_Artist.png","extmetadata":{"Artist":{"value":"<b>甲 &amp; 乙</b>"},"LicenseShortName":{"value":%q},"LicenseUrl":{"value":"//creativecommons.org/licenses/by/4.0/"}}}]}]}}`, r.Host, r.Host, license)
				case "/960px-sample.png":
					if license == "" {
						t.Error("下载了没有署名许可的图片")
					}
					wantMediaRequest(t, r, "/960px-sample.png", url.Values{})
					io.WriteString(w, "thumbnail")
				default:
					t.Errorf("意外图片请求 %s", r.URL)
					http.NotFound(w, r)
				}
			})
			got, err := commons.New(opts).ArtistImage(t.Context(), sources.ArtistLinks{CommonsFile: "Sample Artist.png"})
			if license == "" {
				if !errors.Is(err, sources.ErrNotFound) || len(got.Data) != 0 {
					t.Fatalf("无许可图片=%+v, err=%v", got, err)
				}
				return
			}
			want := sources.Attribution{Source: "commons", SourceURL: "https://commons.wikimedia.org/wiki/File:Sample_Artist.png", Author: "甲 & 乙", License: license, LicenseURL: "https://creativecommons.org/licenses/by/4.0/"}
			if err != nil || got.Attribution != want || string(got.Data) != "thumbnail" || got.ID == "" {
				t.Fatalf("缩略图=%+v, err=%v，期望署名 %+v", got, err, want)
			}
		})
	}
}

func TestArtistMediaWikipediaSummary(t *testing.T) {
	opts, _ := mediaServer(t, func(w http.ResponseWriter, r *http.Request) {
		wantMediaRequest(t, r, "/zh/api/rest_v1/page/summary/"+url.PathEscape("合成艺人/乐队"), url.Values{})
		wantWikimediaHeaders(t, r, "zh-cn")
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		fmt.Fprintf(gz, `{"title":"合成艺人","lang":"zh","extract":%q,"content_urls":{"desktop":{"page":"https://zh.wikipedia.org/wiki/Sample"}}}`,
			"<p><b>合成艺人</b>[12]创作&amp;演奏。</p><p>另一段。</p>")
		if err := gz.Close(); err != nil {
			t.Error(err)
		}
	})
	got, err := wikipedia.New(opts).ArtistBio(t.Context(), sources.ArtistLinks{WikipediaZH: "合成艺人/乐队"}, "zh")
	if err != nil || got.ID != "https://zh.wikipedia.org/wiki/Sample" || got.SourceURL != got.ID ||
		got.Source != "wikipedia" || got.Text != "合成艺人创作&演奏。" || got.Lang != "zh" || got.License != "CC BY-SA 4.0" ||
		got.LicenseURL != "https://creativecommons.org/licenses/by-sa/4.0/" {
		t.Fatalf("简介=%+v, err=%v", got, err)
	}
}

func TestArtistMediaTheAudioDBKeyAndFields(t *testing.T) {
	opts, _ := mediaServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/json/test/key/artist-mb.php":
			wantMediaRequest(t, r, "/api/v1/json/test%2Fkey/artist-mb.php", url.Values{"i": {mediaMBID}})
			fmt.Fprintf(w, `{"artists":[{"idArtist":"99","strMusicBrainzID":"10000000-0000-4000-8000-000000000099","strBiographyCN":"同名艺人"},
			{"idArtist":"42","strMusicBrainzID":%q,"strArtistThumb":"http://%s/thumb","strBiographyCN":"<p>中文[1]简介。</p><p>正文</p>","strBiography":"English lead."}]}`, mediaMBID, r.Host)
		case "/thumb":
			wantMediaRequest(t, r, "/thumb", url.Values{})
			io.WriteString(w, "artist-thumb")
		default:
			t.Errorf("意外请求 %s", r.URL)
			http.NotFound(w, r)
		}
	})
	input := sources.ArtistLinks{MBID: mediaMBID}
	_, err := theaudiodb.New(opts).ArtistImage(t.Context(), input)
	var keyErr *sources.InvalidKeyError
	if !errors.As(err, &keyErr) || keyErr.Name != "api_key" {
		t.Fatalf("未填密钥时 err=%v", err)
	}
	opts.Keys = func(context.Context) (map[string]string, error) { return map[string]string{"api_key": "test/key"}, nil }
	client := theaudiodb.New(opts)
	want := sources.Attribution{Source: "theaudiodb", SourceURL: "https://www.theaudiodb.com/artist/42"}
	image, err := client.ArtistImage(t.Context(), input)
	if err != nil || image.Attribution != want || image.ID == "" || string(image.Data) != "artist-thumb" {
		t.Fatalf("头像=%+v, err=%v", image, err)
	}
	for lang, text := range map[string]string{"zh": "中文简介。", "en": "English lead."} {
		got, err := client.ArtistBio(t.Context(), input, lang)
		if err != nil || got.Attribution != want || got.ID != "42" || got.Text != text || got.Lang != lang {
			t.Errorf("%s 简介=%+v, err=%v", lang, got, err)
		}
	}
}

func TestArtistMediaRetryClassification(t *testing.T) {
	now := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, header string
		status       int
		delay        time.Duration
	}{
		{"rate_limited", "17", http.StatusTooManyRequests, 17 * time.Second},
		{"retry_date", now.Add(33 * time.Second).Format(http.TimeFormat), http.StatusServiceUnavailable, 33 * time.Second},
		{"default_minimum", "", http.StatusServiceUnavailable, 300 * time.Millisecond},
		{"offline", "", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts, server := mediaServer(t, func(w http.ResponseWriter, r *http.Request) {
				if tc.header != "" {
					w.Header().Set("Retry-After", tc.header)
				}
				w.WriteHeader(tc.status)
			})
			if tc.status == 0 {
				server.Close()
			}
			opts.Now = func() time.Time { return now }
			var delays []time.Duration
			opts.After = func(d time.Duration) <-chan time.Time {
				delays = append(delays, d)
				ch := make(chan time.Time, 1)
				ch <- now.Add(d)
				return ch
			}
			ctx := sources.WithBackground(t.Context())
			bio, err := wikipedia.New(opts).ArtistBio(ctx, sources.ArtistLinks{WikipediaZH: "Sample"}, "zh")
			wantWaits := 4
			if tc.delay > 10*time.Second {
				wantWaits = 0
			}
			if !errors.Is(err, sources.ErrTryLater) || bio.Text != "" || len(delays) != wantWaits {
				t.Fatalf("简介=%+v, err=%v, 退避=%v，期望稍后再试", bio, err, delays)
			}
			if wantWaits > 0 && tc.delay != 0 && delays[0] != tc.delay {
				t.Errorf("首次退避=%v，期望 %v", delays[0], tc.delay)
			}
			if tc.header != "" {
				for _, d := range delays {
					if d != tc.delay {
						t.Errorf("Retry-After 退避=%v，期望 %v", delays, tc.delay)
						break
					}
				}
			}
		})
	}
}
