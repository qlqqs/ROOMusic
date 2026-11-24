package fanarttv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

func TestArtistMediaFanartTVKeysAndTopThumb(t *testing.T) {
	const mbid = "10000000-0000-4000-8000-000000000001"
	old := bundledProjectKey
	t.Cleanup(func() { bundledProjectKey = old })
	for _, tc := range []struct {
		name, bundled, project, personal, sent string
	}{
		{name: "project_required"},
		{name: "bundled", bundled: "fixture-bundled", sent: "fixture-bundled"},
		{name: "configured_with_personal", bundled: "fixture-bundled", project: "fixture-project", personal: "fixture-personal", sent: "fixture-project"},
		{name: "personal_is_optional", project: "fixture-project", sent: "fixture-project"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bundledProjectKey = tc.bundled
			declaration := Declaration()
			if slices.Contains(declaration.OptionalKeys, "project") != (tc.bundled != "") ||
				!slices.Contains(declaration.OptionalKeys, "personal") {
				t.Fatalf("密钥声明=%+v", declaration)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.sent == "" {
					t.Error("没有项目密钥仍向 Fanart.tv 发送请求")
				}
				if r.Method != http.MethodGet || r.URL.RawQuery != "" || !strings.HasPrefix(r.UserAgent(), "ROOMusic/") {
					t.Errorf("意外的请求 %s %s，User-Agent=%q", r.Method, r.URL, r.UserAgent())
				}
				switch r.URL.Path {
				case "/v3/music/" + mbid:
					if r.Header.Get("api-key") != tc.sent || r.Header.Get("client-key") != tc.personal {
						t.Errorf("密钥请求头=%v", r.Header)
					}
					fmt.Fprintf(w, `{"mbid_id":%q,"artistthumb":[{"id":"a","url":"http://%s/less-liked","likes":"2"},{"id":"b","url":"http://%s/top","likes":"12"}],"artistbackground":[{"id":"c","url":"http://%s/background","likes":"500"}]}`, mbid, r.Host, r.Host, r.Host)
				case "/top":
					if r.Header.Get("api-key") != "" || r.Header.Get("client-key") != "" {
						t.Error("下载图片时发送了 API 密钥")
					}
					io.WriteString(w, "top-artist-thumb")
				default:
					t.Errorf("下载了非首选头像 %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := New(Options{BaseURL: server.URL, MockHost: sources.NewMockHost(server.URL), Limiter: sources.NewLimiter(0, nil, nil),
				NewClient: func(egress.Options) (*http.Client, error) { return server.Client(), nil },
				Keys: func(context.Context) (map[string]string, error) {
					return map[string]string{"project": tc.project, "personal": tc.personal}, nil
				},
			})
			got, err := client.ArtistImage(t.Context(), sources.ArtistLinks{MBID: mbid})
			if tc.sent == "" {
				var keyErr *sources.InvalidKeyError
				if !errors.As(err, &keyErr) || keyErr.Name != "project" || got.ID != "" {
					t.Fatalf("缺项目密钥时图片=%+v, err=%v", got, err)
				}
				return
			}
			if err != nil || got.ID != "b" || string(got.Data) != "top-artist-thumb" || got.Source != "fanarttv" ||
				got.SourceURL != "https://fanart.tv/artist/"+mbid+"/" {
				t.Fatalf("首选头像=%+v, err=%v", got, err)
			}
		})
	}
}
