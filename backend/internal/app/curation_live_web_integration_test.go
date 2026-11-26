package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
	"github.com/qlqq/roomusic/backend/internal/steward/web"
)

// 网页内容只取曲库清单里的艺人和题名，不提供发行编号、轨表或版本认定。
type liveWebFixture struct {
	server          *httptest.Server
	base, mode      string
	albums          []liveAlbum
	searches, pages atomic.Int64
}

func (f *liveWebFixture) prepare(t *testing.T, mode string) {
	t.Helper()
	switch mode {
	case "", "long":
		f.mode = "long"
	case "empty":
		f.mode = mode
	default:
		t.Fatalf("ROOMUSIC_LIVE_WEB 只接受 long 或 empty，收到 %q", mode)
	}
	f.server = httptest.NewUnstartedServer(http.HandlerFunc(f.serve))
	f.base = "http://" + f.server.Listener.Addr().String()
	t.Cleanup(f.server.Close)
}

func (f *liveWebFixture) start(albums []liveAlbum) {
	f.albums = albums
	f.server.Start()
}

const liveWebParagraph = "这份合成的编目笔记记录听众如何按题名、署名和载体整理唱片。不同版本可以有相似的封面和曲目；读者可以对照封底的碟面标记、各面的排列和实际播放时间。馆藏索引把艺人与作品分别列出，原样保留印刷品上的大小写与标点。"

func liveWebText(album liveAlbum, repetitions int) string {
	return fmt.Sprintf("《%s》 · %s\n", album.Title, album.Artist) + strings.Repeat(liveWebParagraph+"\n", repetitions)
}

func (f *liveWebFixture) pageURL(index int) string {
	return f.base + "/pages/" + strconv.Itoa(index+1)
}

func (f *liveWebFixture) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Path == "/searxng/search" {
		f.searches.Add(1)
		results := []map[string]string{}
		if f.mode == "long" {
			q := strings.ToLower(r.URL.Query().Get("q"))
			// 先列出命中的题名或署名，再用目录中的相邻记录补齐固定长度的结果页。
			var matches, rest []int
			for i, album := range f.albums {
				if strings.Contains(q, strings.ToLower(album.Title)) || strings.Contains(q, strings.ToLower(album.Artist)) || album.LocalTitle != "" && strings.Contains(q, strings.ToLower(album.LocalTitle)) {
					matches = append(matches, i)
				} else {
					rest = append(rest, i)
				}
			}
			order := append(matches, rest...)
			for _, i := range order[:min(len(order), 5)] {
				album := f.albums[i]
				results = append(results, map[string]string{"title": album.Title + " · " + album.Artist, "url": f.pageURL(i), "content": liveWebText(album, 6)})
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
		return
	}
	for i, album := range f.albums {
		if f.mode == "long" && r.Method == http.MethodGet && f.base+r.URL.RequestURI() == f.pageURL(i) {
			f.pages.Add(1)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprint(w, liveWebText(album, 64))
			return
		}
	}
	http.NotFound(w, r)
}

type liveWebSettings struct{ base string }

func (s liveWebSettings) GetUserSettings(ctx context.Context, _ string) (runtime.UserSettings, error) {
	return runtime.UserSettings{SearchSource: "searxng", SearXNGURL: s.base + "/searxng"}, ctx.Err()
}

func TestLiveWebFixture(t *testing.T) {
	albums := []liveAlbum{{ID: "合成发行编号甲", Title: "山脊铃", Artist: "暮铎"}, {ID: "合成发行编号乙", Title: "沙港", Artist: "寒鹭"}}
	for _, mode := range []string{"long", "empty"} {
		t.Run(mode, func(t *testing.T) {
			var f liveWebFixture
			f.prepare(t, mode)
			f.start(albums)
			svc := web.New(liveWebSettings{f.base}, nil, func(context.Context, identity.User) (*http.Client, error) { return f.server.Client(), nil }, nil).WithTavily(f.base+"/tavily/search", sources.NewMockHost(f.base))
			handlers := map[string]tool.Definition{}
			for _, d := range svc.StewardTools() {
				handlers[d.Name] = d
			}
			caller := tool.Caller{Source: tool.SourceCuration, User: identity.User{ID: "合成网页测试" + mode}}
			search := func() []byte {
				raw, err := handlers["web_search"].Handler(t.Context(), caller, json.RawMessage(`{"q":"沙港 唱片","max_results":2}`))
				requireCurationOK(t, err)
				return raw
			}
			raw := search()
			if f.searches.Load() != 1 {
				t.Fatal("搜索未进入回环服务")
			}
			var result struct{ Results []web.SearchResult }
			requireCurationOK(t, json.Unmarshal(raw, &result))
			if mode == "empty" {
				if result.Results == nil || len(result.Results) != 0 {
					t.Fatalf("空结果模式返回 %s", raw)
				}
				return
			}
			if len(result.Results) != 2 || result.Results[0].URL != f.pageURL(1) || !strings.Contains(result.Results[0].Snippet, albums[1].Artist) || len(result.Results[0].Snippet) < 1500 || bytes.Contains(raw, []byte(albums[1].ID)) || !bytes.Equal(raw, search()) {
				t.Fatal("回环命中、固定长摘要或编号隔离不符")
			}
			for i, hit := range result.Results {
				args, err := json.Marshal(map[string]string{"url": hit.URL})
				requireCurationOK(t, err)
				out, err := handlers["fetch_page"].Handler(t.Context(), caller, args)
				requireCurationOK(t, err)
				var page web.Page
				requireCurationOK(t, json.Unmarshal(out, &page))
				if page.FinalURL != hit.URL || len(page.Text) < 16000 || !strings.HasPrefix(page.Text, hit.Snippet) || f.pages.Load() != int64(i+1) {
					t.Fatal("正文未通过正常工具读取固定长页")
				}
			}
		})
	}
}

func TestLiveWebFixtureMusicDestination(t *testing.T) {
	var f liveWebFixture
	f.prepare(t, "")
	if f.mode != "long" {
		t.Fatal("默认应使用长结果")
	}
	const query = "?query=recording%3A%22Test%22&limit=10"
	transport := liveTransport{webHost: sources.NewMockHost(f.base)}
	for _, tc := range []struct {
		address string
		allow   bool
	}{
		{f.base + "/musicbrainz/ws/2/recording" + query, true},
		{"https://musicbrainz.org/ws/2/recording" + query, true},
		{f.base + "/pages/1", false},
		{"https://other.invalid/musicbrainz/ws/2/recording" + query, false},
	} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, tc.address, nil)
		requireCurationOK(t, err)
		req.Header.Set("User-Agent", sources.UserAgent())
		out, err := transport.musicRequest(req)
		if !tc.allow {
			if err == nil {
				t.Fatalf("允许了其它目的地 %s", tc.address)
			}
			continue
		}
		if err != nil || out.URL.String() != "https://musicbrainz.org/ws/2/recording"+query || req.URL.String() != tc.address || out.Header.Get("User-Agent") != sources.UserAgent() {
			t.Fatalf("音乐出站地址、原查询或程序标识丢失：%v", err)
		}
	}
}
