package coverartarchive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
)

const fixtureRelease = "abcdef01-0000-4000-8000-000000000001"
const fixtureGroup = "abcdef02-0000-4000-8000-000000000002"
const fixtureImagePath = "/release/" + fixtureRelease + "/42"
const fixtureManifest = `{"release":"https://musicbrainz.org/release/` + fixtureRelease + `","images":[{"id":42,"front":true,"image":"http://coverartarchive.org` + fixtureImagePath + `.png","thumbnails":{"500":"http://coverartarchive.org` + fixtureImagePath + `-500.jpg"}}]}`

type fixtureTransport func(*http.Request) (*http.Response, error)

func (fn fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func coverFixture(t *testing.T, handler http.HandlerFunc) (Options, *[]string) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	local, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	var requests []string
	client := &http.Client{Transport: fixtureTransport(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.URL.String())
		accept := "image/*"
		if strings.HasSuffix(r.URL.Path, "/") || strings.HasSuffix(r.URL.Path, "/index.json") {
			accept = "application/json"
		}
		want := http.Header{"Accept": {accept}, "User-Agent": {sources.UserAgent()}}
		if r.Method != http.MethodGet || !reflect.DeepEqual(r.Header, want) {
			t.Errorf("请求 %s %s，头部 %v，期望 %v", r.Method, r.URL, r.Header, want)
		}
		request := r.Clone(r.Context())
		request.Host = r.URL.Host
		request.URL.Scheme, request.URL.Host = local.Scheme, local.Host
		return server.Client().Transport.RoundTrip(request)
	})}
	return Options{
		NewClient:       func(egress.Options) (*http.Client, error) { return client, nil },
		ManifestLimiter: sources.NewLimiter(0, nil, nil),
		Limiter:         sources.NewLimiter(0, nil, nil),
	}, &requests
}

func requireCoverRequests(t *testing.T, requests []string, want ...string) {
	t.Helper()
	if !slices.Equal(requests, want) {
		t.Fatalf("出站 %v，期望 %v", requests, want)
	}
}

func coverPaths(paths ...string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		out = append(out, "https://coverartarchive.org"+path)
	}
	return out
}

func TestCoversManifestTargetsAndCache(t *testing.T) {
	const origin = "https://coverartarchive.org"
	const archive = "https://archive.org/download/mbid-" + fixtureRelease + "/index.json"
	const node = "https://dn123.ca.archive.org/0/items/mbid-" + fixtureRelease + "/index.json"
	const body = `{"release":"https://musicbrainz.org/release/ABCDEF01-0000-4000-8000-000000000001","images":[
		{"id":9007199254740993,"front":true,"back":false,"types":["Front","Booklet"],"image":"http://coverartarchive.org` + fixtureImagePath + `.png","thumbnails":{"250":"http://coverartarchive.org` + fixtureImagePath + `-250.jpg","invalid":42}},
		{"id":"00042","back":true,"thumbnails":null},
		{"id":"not-numeric","types":["Other"]}
	]}`
	for _, tc := range []struct{ kind, id, path string }{
		{"release", fixtureRelease, "/release/" + fixtureRelease + "/"},
		{"release_group", fixtureGroup, "/release-group/" + fixtureGroup + "/"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			opts, requests := coverFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.Host {
				case "coverartarchive.org":
					http.Redirect(w, r, archive, http.StatusTemporaryRedirect)
				case "archive.org":
					http.Redirect(w, r, node, http.StatusFound)
				case "dn123.ca.archive.org":
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, body)
				default:
					t.Errorf("意外主机 %s", r.Host)
					w.WriteHeader(http.StatusNotFound)
				}
			})
			opts.DataDirectory = t.TempDir()
			client := New(opts)
			want := []sources.CoverImage{
				{ID: "9007199254740993", ReleaseID: fixtureRelease, Front: true, Types: []string{"Front", "Booklet"}, URL: "http://coverartarchive.org" + fixtureImagePath + ".png", Thumbnails: map[string]string{"250": "http://coverartarchive.org" + fixtureImagePath + "-250.jpg"}},
				{ID: "42", ReleaseID: fixtureRelease, Back: true},
				{ReleaseID: fixtureRelease, Types: []string{"Other"}},
			}
			for _, ctx := range []context.Context{t.Context(), sources.WithBackground(t.Context())} {
				got, err := client.Covers(ctx, sources.CoverTarget{Kind: tc.kind, ID: strings.ToUpper(tc.id)})
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("清单 %#v，错误 %v，期望 %#v", got, err, want)
				}
				requireCoverRequests(t, *requests, origin+tc.path, archive, node)
			}
		})
	}
}

func TestCoversRejectsInvalidTargetsAndReturnsEmptyFor404(t *testing.T) {
	opts, requests := coverFixture(t, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	opts.DataDirectory = t.TempDir()
	client := New(opts)
	for _, target := range []sources.CoverTarget{
		{Kind: "release", ID: "synthetic"},
		{Kind: "release_group", ID: fixtureGroup + "?token=fixture"},
		{Kind: "artist", ID: fixtureRelease},
	} {
		if got, err := client.Covers(t.Context(), target); !errors.Is(err, sources.ErrInvalidQuery) || got != nil {
			t.Errorf("非法目标 %+v 得到 %#v，错误 %v", target, got, err)
		}
		if got, err := client.FrontCover(t.Context(), target); !errors.Is(err, sources.ErrInvalidQuery) || !reflect.DeepEqual(got, sources.CoverFile{}) {
			t.Errorf("非法目标 %+v 下载得到 %#v，错误 %v", target, got, err)
		}
	}
	requireCoverRequests(t, *requests)
	for range 2 {
		got, err := client.Covers(t.Context(), sources.CoverTarget{Kind: "release", ID: fixtureRelease})
		if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("404 得到 %#v，错误 %v，期望空切片", got, err)
		}
	}
	requireCoverRequests(t, *requests, coverPaths("/release/"+fixtureRelease+"/")...)
}

func TestCoversManifestRejectsOtherHostsAndPaths(t *testing.T) {
	for _, tc := range []struct{ name, location string }{
		{"unofficial_host", "https://archive.org.synthetic.invalid/demo/index.json"},
		{"not_index", "https://archive.org/demo/front.jpg"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts, requests := coverFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Host == "coverartarchive.org" {
					http.Redirect(w, r, tc.location, http.StatusTemporaryRedirect)
					return
				}
				io.WriteString(w, `{"images":[]}`)
			})
			got, err := New(opts).Covers(sources.WithBackground(t.Context()), sources.CoverTarget{Kind: "release", ID: fixtureRelease})
			if !errors.Is(err, sources.ErrTryLater) || !errors.Is(err, egress.ErrDenied) || got != nil {
				t.Errorf("重定向到 %s 后得到 %#v，错误 %v", tc.location, got, err)
			}
			requireCoverRequests(t, *requests, coverPaths("/release/"+fixtureRelease+"/")...)
		})
	}
}

func TestFrontCoverSelection(t *testing.T) {
	const tagFront = `{"id":41,"types":["front"]}`
	const anotherTagFront = `{"id":43,"types":["Front"]}`
	const markedFront = `{"id":42,"front":true,"types":["Booklet"]}`
	for _, tc := range []struct{ name, images, chosen string }{
		{"front_flag_over_type", tagFront + "," + markedFront + `,{"id":44,"front":true}`, "42"},
		{"first_front_type", `{"id":"invalid","front":true},` + tagFront + "," + anotherTagFront, "41"},
		{"no_front", `{"id":41,"back":true,"types":["Back"]}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listPath := "/release/" + fixtureRelease + "/"
			opts, requests := coverFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == listPath {
					fmt.Fprintf(w, `{"release":"https://musicbrainz.org/release/%s","images":[%s]}`, fixtureRelease, tc.images)
					return
				}
				io.WriteString(w, "fixture image")
			})
			got, err := New(opts).FrontCover(t.Context(), sources.CoverTarget{Kind: "release", ID: fixtureRelease})
			if tc.chosen == "" {
				if !errors.Is(err, sources.ErrNotFound) || !reflect.DeepEqual(got, sources.CoverFile{}) {
					t.Errorf("无主封面时得到 %#v，错误 %v", got, err)
				}
				requireCoverRequests(t, *requests, coverPaths(listPath)...)
				return
			}
			if err != nil || got.Image.ID != tc.chosen || got.Image.ReleaseID != fixtureRelease || got.Size != "1200" || string(got.Data) != "fixture image" {
				t.Errorf("选图得到 %#v，错误 %v，期望图片 %s", got, err, tc.chosen)
			}
			requireCoverRequests(t, *requests, coverPaths(listPath, listPath+tc.chosen+"-1200")...)
		})
	}
}

func TestFrontCoverWithoutManifestRelease(t *testing.T) {
	for _, tc := range []struct{ kind, id, listPath string }{
		{"release", fixtureRelease, "/release/" + fixtureRelease + "/"},
		{"release_group", fixtureGroup, "/release-group/" + fixtureGroup + "/"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			opts, requests := coverFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == tc.listPath {
					io.WriteString(w, `{"images":[{"id":42,"front":true}]}`)
					return
				}
				io.WriteString(w, "fixture")
			})
			opts.DataDirectory = t.TempDir()
			client := New(opts)
			target := sources.CoverTarget{Kind: tc.kind, ID: strings.ToUpper(tc.id)}
			wantList := []sources.CoverImage{{ID: "42", Front: true}}
			if list, err := client.Covers(t.Context(), target); err != nil || !reflect.DeepEqual(list, wantList) {
				t.Fatalf("缺少 release 的清单得到 %#v，错误 %v，期望 %#v", list, err, wantList)
			}
			got, err := client.FrontCover(t.Context(), target)
			if tc.kind == "release_group" {
				if !errors.Is(err, sources.ErrNotFound) || !reflect.DeepEqual(got, sources.CoverFile{}) {
					t.Errorf("无发行组来源时得到 %#v，错误 %v", got, err)
				}
				requireCoverRequests(t, *requests, coverPaths(tc.listPath)...)
				return
			}
			if err != nil || got.Image.ReleaseID != fixtureRelease || got.Image.ID != "42" || got.Size != "1200" || string(got.Data) != "fixture" {
				t.Errorf("用发行目标下载得到 %#v，错误 %v", got, err)
			}
			requireCoverRequests(t, *requests, coverPaths(tc.listPath, fixtureImagePath+"-1200")...)
		})
	}
}

func TestFrontCoverDownloadFallback(t *testing.T) {
	const listPath = "/release-group/" + fixtureGroup + "/"
	paths := []string{fixtureImagePath + "-1200", fixtureImagePath + "-500", fixtureImagePath + "-250", fixtureImagePath}
	for _, tc := range []struct {
		name, size, typ, body string
		stop                  int
		status                int
		err                   error
	}{
		{name: "1200", stop: 0, size: "1200", typ: "image/jpeg", body: "large", status: http.StatusOK},
		{name: "500", stop: 1, size: "500", typ: "image/jpeg", body: "medium", status: http.StatusOK},
		{name: "250", stop: 2, size: "250", typ: "image/jpeg", body: "small", status: http.StatusOK},
		{name: "original", stop: 3, size: "original", typ: "image/png", body: "original bytes", status: http.StatusOK},
		{name: "all_missing", stop: 3, status: http.StatusNotFound, err: sources.ErrNotFound},
		{name: "pdf_type", stop: 3, typ: "application/pdf; version=1.7", body: "synthetic document", status: http.StatusOK, err: sources.ErrNotFound},
		{name: "pdf_prefix", stop: 3, typ: "application/octet-stream", body: "%PDF-1.7", status: http.StatusOK, err: sources.ErrNotFound},
		{name: "non_404", stop: 1, status: http.StatusForbidden, err: sources.ErrUnavailable},
		{name: "oversize", stop: 0, status: http.StatusOK, err: sources.ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts, requests := coverFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == listPath {
					io.WriteString(w, fixtureManifest)
					return
				}
				if r.URL.Path != paths[tc.stop] {
					http.NotFound(w, r)
					return
				}
				if tc.typ != "" {
					w.Header().Set("Content-Type", tc.typ)
				}
				if tc.name == "oversize" {
					w.Header().Set("Content-Length", "16777217")
				}
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			})
			got, err := New(opts).FrontCover(t.Context(), sources.CoverTarget{Kind: "release_group", ID: fixtureGroup})
			if !errors.Is(err, tc.err) {
				t.Errorf("下载得到 %#v，错误 %v，期望 %v", got, err, tc.err)
			}
			if tc.err != nil {
				if !reflect.DeepEqual(got, sources.CoverFile{}) {
					t.Errorf("失败仍返回 %#v", got)
				}
			} else if got.Size != tc.size || string(got.Data) != tc.body || got.Image.ID != "42" || got.Image.ReleaseID != fixtureRelease {
				t.Errorf("下载结果 %#v，期望档位 %q，内容 %q，发行 %s，图片编号 42", got, tc.size, tc.body, fixtureRelease)
			}
			requireCoverRequests(t, *requests, coverPaths(append([]string{listPath}, paths[:tc.stop+1]...)...)...)
		})
	}
}

func TestCoverArtLimiters(t *testing.T) {
	defaultClient := New(Options{})
	if defaultClient.ManifestLimiter() != sources.DefaultLimiter(Declaration()) ||
		defaultClient.ManifestLimiter() != defaultClient.images.Limiter() ||
		defaultClient.ManifestLimiter() == musicbrainz.New(musicbrainz.Options{}).Limiter() {
		t.Fatal("默认清单和图片应共用 CAA 的限速器，并独立于 MusicBrainz")
	}
	newLimiter := func() (*sources.Limiter, *int) {
		now := time.Unix(1700000000, 0)
		permits := 0
		return sources.NewLimiter(0, func() time.Time { return now }, func(d time.Duration) <-chan time.Time {
			if d != 10*time.Second {
				t.Errorf("对话限速计时 %v，期望 10 秒", d)
			}
			permits++
			return nil
		}), &permits
	}
	const listPath = "/release/" + fixtureRelease + "/"
	opts, requests := coverFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == listPath {
			io.WriteString(w, fixtureManifest)
			return
		}
		io.WriteString(w, "fixture")
	})
	opts.DataDirectory = t.TempDir()
	manifest, manifestPermits := newLimiter()
	images, imagePermits := newLimiter()
	opts.ManifestLimiter, opts.Limiter = manifest, images
	client := New(opts)
	for n := 1; n <= 2; n++ {
		got, err := client.FrontCover(t.Context(), sources.CoverTarget{Kind: "release", ID: fixtureRelease})
		if err != nil || got.Size != "1200" || string(got.Data) != "fixture" || *manifestPermits != 1 || *imagePermits != n {
			t.Fatalf("第 %d 次下载得到 %#v，错误 %v，清单许可 %d，图片许可 %d", n, got, err, *manifestPermits, *imagePermits)
		}
	}
	requireCoverRequests(t, *requests, coverPaths(listPath, fixtureImagePath+"-1200", fixtureImagePath+"-1200")...)
}
