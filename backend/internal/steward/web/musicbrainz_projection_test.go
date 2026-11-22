package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func TestMusicToolsProjectOnlyPublicFields(t *testing.T) {
	const item = `{"id":"rel","aliases":[{"name":"别名"}],"media":[{"position":1,"discs":[{"id":"disc-id"}],"tracks":[{"id":"track","number":"1","title":"曲目","recording":{"id":"rec","isrcs":["USABC2300001"]}}]}]}`
	const wantItem = `{"mbid":"rel","media":[{"position":1,"tracks":[{"mbid":"track","number":"1","title":"曲目","recording":{"mbid":"rec"}}]}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/release") {
			io.WriteString(w, `{"releases":[`+item+`]}`)
		} else {
			io.WriteString(w, item)
		}
	}))
	defer server.Close()
	svc := New(nil, nil, nil, nil).WithMusic(musicbrainz.New(musicbrainz.Options{BaseURL: server.URL, Limiter: sources.NewLimiter(0, nil, nil), NewClient: func(egress.Options) (*http.Client, error) { return server.Client(), nil }}))
	caller := tool.Caller{User: identity.User{ID: t.Name()}}
	for _, tc := range []struct{ name, input, want string }{
		{"search_music_metadata", `{"query":"合成","entity":"release"}`, `{"results":[` + wantItem + `]}`},
		{"get_music_metadata", `{"mbid":"11111111-1111-3111-8111-111111111111","entity":"release","include":["recordings"]}`, `{"metadata":` + wantItem + `}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, def := range svc.StewardTools() {
				if def.Name == tc.name {
					out, err := def.Handler(t.Context(), caller, []byte(tc.input))
					if err != nil || string(out) != tc.want {
						t.Fatalf("工具输出=%s %v，期望 %s", out, err, tc.want)
					}
					return
				}
			}
			t.Fatal("缺少工具定义")
		})
	}
}
