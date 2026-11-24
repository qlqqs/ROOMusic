package sources_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/artwork"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/acoustid"
	"github.com/qlqq/roomusic/backend/internal/sources/commons"
	"github.com/qlqq/roomusic/backend/internal/sources/coverartarchive"
	"github.com/qlqq/roomusic/backend/internal/sources/fanarttv"
	"github.com/qlqq/roomusic/backend/internal/sources/lrclib"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
	"github.com/qlqq/roomusic/backend/internal/sources/theaudiodb"
	"github.com/qlqq/roomusic/backend/internal/sources/wikidata"
	"github.com/qlqq/roomusic/backend/internal/sources/wikipedia"
	"github.com/qlqq/roomusic/backend/internal/upstreammock"
)

const mockReleaseID = "30000000-0000-4000-8000-000000000001"
const mockRecordingID = "40000000-0000-4000-8000-000000000001"

type mockLookup struct {
	service, basePath, official string
	newLookup                   func(sources.Options) func(context.Context) (bool, error)
	missing                     error
}

func mockLookups() []mockLookup {
	return []mockLookup{
		{"musicbrainz", "/musicbrainz/ws/2", "https://musicbrainz.org/ws/2/release/" + mockReleaseID, func(opts sources.Options) func(context.Context) (bool, error) {
			client := musicbrainz.New(opts)
			return func(ctx context.Context) (bool, error) {
				got, err := client.ReleaseByID(ctx, mockReleaseID)
				return got.ID == mockReleaseID && got.Title == "潮线以北" && got.Complete &&
					len(got.Media) == 1 && len(got.Media[0].Tracks) == 2 &&
					got.Media[0].Tracks[0].RecordingID == mockRecordingID, err
			}
		}, sources.ErrNotFound},
		{"coverartarchive", "/coverartarchive", "https://coverartarchive.org/release/" + mockReleaseID + "/", func(opts sources.Options) func(context.Context) (bool, error) {
			client := coverartarchive.New(coverartarchive.Options{
				BaseURL: opts.BaseURL, MockHost: opts.MockHost, DataDirectory: opts.DataDirectory, Logger: opts.Logger,
				Limiter: opts.Limiter, ManifestLimiter: opts.Limiter, NewClient: opts.NewClient, Now: opts.Now, After: opts.After,
			})
			return func(ctx context.Context) (bool, error) {
				got, err := client.FrontCover(ctx, sources.CoverTarget{Kind: "release", ID: mockReleaseID})
				if err != nil {
					return false, err
				}
				mime, err := artwork.ValidateImage(got.Data)
				return strings.HasPrefix(mime, "image/") && got.Image.ReleaseID == mockReleaseID && got.Image.Front && got.Size == "1200", err
			}
		}, sources.ErrNotFound},
		{"lrclib", "/lrclib", "https://lrclib.net/api/get", func(opts sources.Options) func(context.Context) (bool, error) {
			client := lrclib.New(opts)
			return func(ctx context.Context) (bool, error) {
				got, err := client.LyricsFor(ctx, sources.LyricsQuery{Title: "潮汐信箱", Artist: "纸屿", Album: "潮线以北", DurationSeconds: 180})
				return got.Title == "潮汐信箱" && got.DurationSeconds == 180 && got.Synced == "[00:00.00]把潮声折进信封", err
			}
		}, sources.ErrNotFound},
		{"acoustid", "/acoustid", "https://api.acoustid.org/v2/lookup", func(opts sources.Options) func(context.Context) (bool, error) {
			client := acoustid.New(acoustid.Options{
				BaseURL: opts.BaseURL, MockHost: opts.MockHost, DataDirectory: opts.DataDirectory, Logger: opts.Logger,
				Limiter: opts.Limiter, NewClient: opts.NewClient, Now: opts.Now, After: opts.After,
				Keys: func(context.Context) (map[string]string, error) {
					return map[string]string{"application": "synthetic-app-key", "user": "synthetic-user-key"}, nil
				},
			})
			return func(ctx context.Context) (bool, error) {
				got, err := client.LookupFingerprint(ctx, sources.Fingerprint{Value: "mock-tide", DurationSeconds: 180})
				if err != nil || len(got) != 1 || len(got[0].Recordings) != 1 {
					return false, err
				}
				rec := got[0].Recordings[0]
				return rec.ID == mockRecordingID && rec.LengthMS != nil && *rec.LengthMS == 180000 &&
					len(rec.Releases) == 2 && rec.Releases[0].ID == mockReleaseID, nil
			}
		}, nil},
	}
}

func mockArtistMediaLookups() []mockLookup {
	const saltWindMBID = "10000000-0000-4000-8000-000000000004"
	keys := func(context.Context) (map[string]string, error) {
		return map[string]string{"project": "mock-project", "personal": "mock-personal", "api_key": "mock-audio"}, nil
	}
	return []mockLookup{
		{"wikidata", "/wikidata", "", func(opts sources.Options) func(context.Context) (bool, error) {
			client := wikidata.New(opts)
			return func(ctx context.Context) (bool, error) {
				got, err := client.ArtistLinks(ctx, sources.ArtistLinks{MBID: saltWindMBID})
				return got.WikidataID == "Q900004" && got.CommonsFile == "Salt Wind Trio.png", err
			}
		}, sources.ErrNotFound},
		{"commons", "/commons", "", func(opts sources.Options) func(context.Context) (bool, error) {
			client := commons.New(opts)
			return func(ctx context.Context) (bool, error) {
				got, err := client.ArtistImage(ctx, sources.ArtistLinks{CommonsFile: "Salt Wind Trio.png"})
				if err != nil {
					return false, err
				}
				_, err = artwork.ValidateImage(got.Data)
				return got.Author == "合成影像小组" && got.License == "CC BY 4.0" && got.LicenseURL != "", err
			}
		}, sources.ErrNotFound},
		{"wikipedia", "/wikipedia", "", func(opts sources.Options) func(context.Context) (bool, error) {
			client := wikipedia.New(opts)
			return func(ctx context.Context) (bool, error) {
				got, err := client.ArtistBio(ctx, sources.ArtistLinks{WikipediaZH: "纸屿（二重奏）"}, "zh")
				return got.Lang == "zh" && got.License == "CC BY-SA 4.0" &&
					got.Text == "纸屿是一支虚构的海岸二重奏，以录制潮声与木吉他合奏为特色。", err
			}
		}, sources.ErrNotFound},
		{"fanarttv", "/fanarttv", "", func(opts sources.Options) func(context.Context) (bool, error) {
			opts.Keys = keys
			client := fanarttv.New(opts)
			return func(ctx context.Context) (bool, error) {
				got, err := client.ArtistImage(ctx, sources.ArtistLinks{MBID: mediaMBID})
				if err != nil {
					return false, err
				}
				_, err = artwork.ValidateImage(got.Data)
				return got.ID == "9100012" && got.Source == "fanarttv", err
			}
		}, sources.ErrNotFound},
		{"theaudiodb", "/theaudiodb", "", func(opts sources.Options) func(context.Context) (bool, error) {
			opts.Keys = keys
			client := theaudiodb.New(opts)
			return func(ctx context.Context) (bool, error) {
				links := sources.ArtistLinks{MBID: mediaMBID}
				bio, err := client.ArtistBio(ctx, links, "zh")
				if err != nil {
					return false, err
				}
				image, err := client.ArtistImage(ctx, links)
				if err != nil {
					return false, err
				}
				_, err = artwork.ValidateImage(image.Data)
				return bio.ID == "920001" && bio.Text == "纸屿以海岸为主题创作音乐。" && image.Source == "theaudiodb", err
			}
		}, sources.ErrNotFound},
	}
}

func TestUpstreamMockSourceScenarios(t *testing.T) {
	for _, lookup := range append(mockLookups(), mockArtistMediaLookups()...) {
		t.Run(lookup.service, func(t *testing.T) {
			t.Parallel()
			for _, scenario := range []string{"ok", "slow", "not_found", "unavailable", "rate_limited", "timeout", "malformed", "drop", "offline"} {
				t.Run(scenario, func(t *testing.T) {
					h, err := upstreammock.New(upstreammock.Options{})
					if err != nil {
						t.Fatal(err)
					}
					serverCtx, stopServer := context.WithCancel(t.Context())
					server := httptest.NewUnstartedServer(h)
					server.Config.BaseContext = func(net.Listener) context.Context { return serverCtx }
					server.Start()
					defer func() {
						stopServer()
						server.Close()
					}()
					if scenario != "offline" {
						setMockSourceScenario(t, server, lookup.service, scenario)
					}
					var delays []time.Duration
					now := time.Now()
					dir := t.TempDir()
					base := server.URL
					if scenario == "offline" {
						server.Close()
					}
					call := lookup.newLookup(sources.Options{
						BaseURL: base + lookup.basePath, MockHost: sources.NewMockHost(base), DataDirectory: dir,
						Limiter: sources.NewLimiter(0, nil, nil), Logger: slog.New(slog.DiscardHandler),
						Now: func() time.Time { return now },
						NewClient: func(opts egress.Options) (*http.Client, error) {
							if scenario == "timeout" {
								opts.Timeout = 100 * time.Millisecond
							}
							return egress.NewClient(opts)
						},
						After: func(delay time.Duration) <-chan time.Time {
							delays = append(delays, delay)
							ready := make(chan time.Time, 1)
							ready <- now
							return ready
						},
					})
					ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
					defer cancel()
					ctx = sources.WithBackground(ctx)
					matched, err := call(ctx)
					switch scenario {
					case "ok", "slow":
						if !matched || err != nil || len(delays) != 0 {
							t.Fatalf("命中=%v，错误=%v，退避=%v", matched, err, delays)
						}
					case "not_found":
						if matched || !errors.Is(err, lookup.missing) || len(delays) != 0 {
							t.Fatalf("查不到时命中=%v，错误=%v，退避=%v", matched, err, delays)
						}
					default:
						if matched || !errors.Is(err, sources.ErrTryLater) {
							t.Fatalf("故障时命中=%v，错误=%v，期望 ErrTryLater", matched, err)
						}
						files, globErr := fs.Glob(os.DirFS(dir), "sources/cache/"+lookup.service+"/*/*.json")
						if globErr != nil || len(files) != 0 {
							t.Fatalf("失败响应写入缓存 %v，检查错误 %v", files, globErr)
						}
					}
					switch scenario {
					case "unavailable", "rate_limited":
						want := []time.Duration{7 * time.Second, 7 * time.Second, 7 * time.Second, 7 * time.Second}
						if !reflect.DeepEqual(delays, want) {
							t.Errorf("退避=%v，期望每次依照 Retry-After 等待 7 秒", delays)
						}
					case "timeout", "drop", "offline":
						if len(delays) != 4 {
							t.Errorf("传输错误退避=%v，期望重试四次", delays)
						}
					case "malformed":
						if len(delays) != 0 {
							t.Errorf("坏响应触发了重试: %v", delays)
						}
						setMockSourceScenario(t, server, lookup.service, "ok")
						if matched, err := call(ctx); !matched || err != nil {
							t.Errorf("上游修复后命中=%v，错误=%v", matched, err)
						}
					}
				})
			}
		})
	}
}

func setMockSourceScenario(t *testing.T, server *httptest.Server, service, scenario string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, server.URL+"/_mock/scenarios/"+service,
		strings.NewReader(fmt.Sprintf(`{"scenario":%q,"retry_after":7,"delay_ms":10}`, scenario)))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("设置 %s 的 %s 情景返回 HTTP %d", service, scenario, resp.StatusCode)
	}
}

func TestUpstreamMockMalformedCoverImageIsNotStored(t *testing.T) {
	h, err := upstreammock.New(upstreammock.Options{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	dir := t.TempDir()
	client := coverartarchive.New(coverartarchive.Options{
		BaseURL: server.URL + "/coverartarchive", MockHost: sources.NewMockHost(server.URL),
		ManifestLimiter: sources.NewLimiter(0, nil, nil), Limiter: sources.NewLimiter(0, nil, nil),
		DataDirectory: dir,
	})
	ctx := sources.WithBackground(t.Context())
	target := sources.CoverTarget{Kind: "release", ID: mockReleaseID}
	// 缓存有效清单后切换情景，令 FrontCover 下载坏图片。
	if images, err := client.Covers(ctx, target); err != nil || len(images) != 1 || !images[0].Front {
		t.Fatalf("准备封面清单失败: %+v, %v", images, err)
	}
	setMockSourceScenario(t, server, "coverartarchive", "malformed")
	file, err := client.FrontCover(ctx, target)
	if err != nil || len(file.Data) == 0 || file.Image.ReleaseID != target.ID {
		t.Fatalf("下载测试封面失败: %+v, %v", file, err)
	}
	writer := curation.NewWriter(nil, dir, nil)
	result, err := writer.ApplyArtwork(ctx, curation.ArtworkRequest{
		ReleaseID: target.ID, Data: file.Data, ExternalID: file.Image.ID,
		Source: curation.Source{Kind: "source", Provider: "coverartarchive", Basis: "合成封面"},
	})
	var failure *operations.Failure
	if !errors.As(err, &failure) || failure.Code != "invalid_input" || result.OperationID != "" {
		t.Fatalf("坏封面的写入结果 = %+v，错误 %v", result, err)
	}
	entries, err := fs.Glob(os.DirFS(dir), "artwork/*")
	if err != nil || len(entries) != 0 {
		t.Fatalf("坏封面落盘 %v，检查错误 %v", entries, err)
	}
}

type mockRoundTripper func(*http.Request) (*http.Response, error)

func (rt mockRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return rt(r) }

func TestSourcesDefaultToOfficialPublicEndpoints(t *testing.T) {
	t.Setenv("ROOMUSIC_UPSTREAM_MOCK_URL", "")
	for _, lookup := range mockLookups() {
		t.Run(lookup.service, func(t *testing.T) {
			var gotURL string
			call := lookup.newLookup(sources.Options{
				Limiter: sources.NewLimiter(0, nil, nil), Logger: slog.New(slog.DiscardHandler),
				NewClient: func(opts egress.Options) (*http.Client, error) {
					if opts.Policy != egress.PublicOnly || opts.Allowlist != nil {
						t.Errorf("默认出站策略 = %v，期望 PublicOnly 且无放行名单", opts.Policy)
					}
					return &http.Client{Transport: mockRoundTripper(func(r *http.Request) (*http.Response, error) {
						gotURL = r.URL.Scheme + "://" + r.URL.Host + r.URL.Path
						return nil, errors.New("测试拦截官方地址")
					})}, nil
				},
			})
			if matched, err := call(t.Context()); matched || !errors.Is(err, sources.ErrUnavailable) {
				t.Fatalf("断网时命中=%v，错误=%v，期望 ErrUnavailable", matched, err)
			}
			if gotURL != lookup.official {
				t.Errorf("上游地址=%q，期望 %q", gotURL, lookup.official)
			}
		})
	}
}

func TestUpstreamMockAllowsOnlyConfiguredPrivateHostAndPort(t *testing.T) {
	h, err := upstreammock.New(upstreammock.Options{})
	if err != nil {
		t.Fatal(err)
	}
	mock := httptest.NewServer(h)
	defer mock.Close()
	otherPort := httptest.NewServer(h)
	defer otherPort.Close()
	own, err := url.Parse(mock.URL)
	if err != nil {
		t.Fatal(err)
	}
	const otherPrivateIP = "192.168.42.2"
	for _, tc := range []struct {
		name, base string
		configured bool
		allowed    bool
	}{
		{"mock", mock.URL, true, true},
		{"same_host_different_port", otherPort.URL, true, false},
		{"different_loopback_same_port", "http://127.0.0.2:" + own.Port(), true, false},
		{"private_network_same_port", "http://" + otherPrivateIP + ":" + own.Port(), true, false},
		{"default_public_only", mock.URL, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var host sources.MockHost
			if tc.configured {
				host = sources.NewMockHost(mock.URL)
			}
			requester := sources.NewRequester(musicbrainz.Declaration(), sources.Options{
				MockHost: host, Limiter: sources.NewLimiter(0, nil, nil), MaxRedirects: 1,
			})
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			var got string
			err := requester.GetJSON(ctx, tc.base+"/musicbrainz/ws/2/release/"+mockReleaseID, func(body []byte) (bool, bool) {
				var row struct{ ID string }
				parseErr := json.Unmarshal(body, &row)
				got = row.ID
				return false, parseErr == nil && row.ID != ""
			})
			if tc.allowed {
				if err != nil || got != mockReleaseID {
					t.Fatalf("允许的 mock 发行=%q，错误=%v", got, err)
				}
			} else if !errors.Is(err, sources.ErrUnavailable) || !errors.Is(err, egress.ErrDenied) || got != "" {
				t.Fatalf("未放行的私网地址发行=%q，错误=%v", got, err)
			}
		})
	}
}
