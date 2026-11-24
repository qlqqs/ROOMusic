package plugins

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/sources"
)

func TestPluginSourceValidatesResults(t *testing.T) {
	engine, dir := pluginTestEngine(t)
	wasm := buildPluginFixture(t, dir, "../../plugins/examples/lyrics-demo/main.go")
	const goodURL = "https://fixture.invalid/catalog"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var result map[string]string
		switch r.URL.Path {
		case "/plugins/validation/main/lyrics":
			result = map[string]string{"kind": "plain", "text": "第一行\n第二行", "source_url": goodURL, "external_id": "fixture-lyrics"}
			switch r.URL.Query().Get("title") {
			case "non_http":
				result["source_url"] = "file:///fixture"
			case "long_url":
				result["source_url"] = goodURL + "/" + strings.Repeat("a", 2048)
			case "long_text":
				result["text"] = strings.Repeat("a", (64<<10)+1)
			case "many_lines":
				result["text"] = strings.Repeat("a\n", 2001)
			case "long_id":
				result["external_id"] = strings.Repeat("a", 129)
			}
		case "/plugins/validation/main/artist_bio":
			result = map[string]string{
				"text": "<p><b>" + strings.Repeat("潮", 4010) + "</b>[27]</p><p>第二段</p>",
				"lang": "zh", "source_url": goodURL, "external_id": "fixture-bio",
			}
			if r.URL.Query().Get("mbid") == "non_http" {
				result["source_url"] = "file:///fixture"
			}
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixtureJSON(t, result))
	}))
	defer server.Close()

	st := &Store{dir: dir, engine: engine, mock: server.URL, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	m := Manifest{
		ID: "validation", Name: "返回值校验夹具", Author: "ROOMusic", Version: "1", ABI: 1,
		Upstreams: []Upstream{{ID: "main", BaseURL: goodURL}},
	}
	raw := fixtureJSON(t, m)
	p := &Source{Metadata: Metadata{Manifest: m}, store: st, wasmHash: checksum(wasm), manifestHash: checksum(raw)}
	st.installed = []*Source{p}
	p.initEndpoints()
	base := filepath.Join(dir, m.ID)
	if err := os.Mkdir(base, 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"manifest.json": raw, "plugin.wasm": wasm} {
		if err := os.WriteFile(filepath.Join(base, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("lyrics", func(t *testing.T) {
		for _, title := range []string{"non_http", "long_url", "long_text", "many_lines", "long_id"} {
			t.Run(title, func(t *testing.T) {
				got, err := p.LyricsFor(t.Context(), sources.LyricsQuery{Title: title})
				if !errors.Is(err, sources.ErrTryLater) || got.ID != "" || got.Plain != "" || got.Synced != "" {
					t.Errorf("无效歌词应被拒绝，标识 %q，错误 %v", got.ID, err)
				}
			})
		}
		got, err := p.LyricsFor(t.Context(), sources.LyricsQuery{Title: "valid"})
		if err != nil || got.ID != "fixture-lyrics" || got.Plain != "第一行\n第二行" {
			t.Errorf("有效歌词无法读取 %+v，错误 %v", got, err)
		}
	})
	t.Run("artist_bio", func(t *testing.T) {
		if got, err := p.ArtistBio(t.Context(), sources.ArtistLinks{MBID: "non_http"}, "zh"); !errors.Is(err, sources.ErrTryLater) || got.Text != "" {
			t.Errorf("非 HTTP(S) 来源的简介应被拒绝，标识 %q，错误 %v", got.ID, err)
		}
		got, err := p.ArtistBio(t.Context(), sources.ArtistLinks{MBID: "valid"}, "zh")
		if err != nil || got.Text != strings.Repeat("潮", 4000) || got.Source != "plugin:validation" || got.SourceURL != goodURL {
			t.Errorf("简介未去 HTML、取首段和截断，长度 %d，来源 %q，错误 %v", len([]rune(got.Text)), got.Source, err)
		}
	})
}
