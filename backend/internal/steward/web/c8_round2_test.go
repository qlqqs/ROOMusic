package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
	"github.com/qlqq/roomusic/backend/internal/steward/secret"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func TestC8PerUserToolRateLimits(t *testing.T) {
	for _, tc := range []struct {
		name  string
		names []string
		limit int
	}{
		{"fetch_page", []string{"fetch_page"}, 30}, {"music_shared", []string{"search_music_metadata", "get_music_metadata"}, 20}, {"web_search", []string{"web_search"}, 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
			old := globalUserLimiter
			globalUserLimiter = &userLimiter{now: func() time.Time { return now }}
			t.Cleanup(func() { globalUserLimiter = old })
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"results":[],"artists":[],"id":"synthetic-id"}`)
			}))
			defer server.Close()
			settings := &c8Settings{value: runtime.UserSettings{SearchSource: "searxng", SearXNGURL: server.URL}}
			services := []*Service{New(settings, nil, func(context.Context, identity.User) (*http.Client, error) { return server.Client(), nil }, nil), New(settings, nil, func(context.Context, identity.User) (*http.Client, error) { return server.Client(), nil }, nil)}
			defs := make([]map[string]tool.Definition, 2)
			for i, s := range services {
				s.newClient = func(o egress.Options) (*http.Client, error) { return c8LocalClient(t, server, o), nil }
				tick := now
				limiter := sources.NewLimiter(time.Second, func() time.Time { tick = tick.Add(2 * time.Second); return tick }, nil)
				s.WithMusic(musicbrainz.New(musicbrainz.Options{Limiter: limiter, NewClient: s.newClient}))
				defs[i] = map[string]tool.Definition{}
				for _, d := range s.StewardTools() {
					defs[i][d.Name] = d
				}
			}
			inputs := map[string]string{"web_search": `{"q":"合成"}`, "fetch_page": `{"url":"https://synthetic.invalid/"}`, "search_music_metadata": `{"entity":"artist","query":"合成"}`, "get_music_metadata": `{"entity":"artist","mbid":"11111111-1111-4111-8111-111111111111"}`}
			call := func(user string, i int) error {
				name := tc.names[i%len(tc.names)]
				_, err := defs[i%2][name].Handler(context.Background(), tool.Caller{User: identity.User{ID: user, Role: "user"}}, json.RawMessage(inputs[name]))
				return err
			}
			for i := 0; i < tc.limit; i++ {
				if err := call("user-a", i); err != nil {
					t.Fatalf("第 %d 次提前拒绝：%v", i+1, err)
				}
			}
			for i := 0; i < len(tc.names); i++ {
				c8Code(t, call("user-a", tc.limit+i), "rate_limited")
			}
			if err := call("user-b", 0); err != nil {
				t.Fatalf("用户额度相互影响：%v", err)
			}
			now = now.Add(time.Minute)
			if err := call("user-a", 0); err != nil {
				t.Fatalf("窗口到期未恢复：%v", err)
			}
			if len(globalUserLimiter.requests) != 1 {
				t.Fatalf("过期用户项未清理：%v", globalUserLimiter.requests)
			}
		})
	}
}

func TestC8UserRateWindowPrunesPartialHistory(t *testing.T) {
	now := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	l := &userLimiter{now: func() time.Time { return now }}
	if err := l.allow("user", "fetch_page", 2); err != nil {
		t.Fatal(err)
	}
	now = now.Add(30 * time.Second)
	if err := l.allow("user", "fetch_page", 2); err != nil {
		t.Fatal(err)
	}
	c8Code(t, l.allow("user", "fetch_page", 2), "rate_limited")
	now = now.Add(30 * time.Second)
	if err := l.allow("user", "fetch_page", 2); err != nil {
		t.Fatal(err)
	}
	c8Code(t, l.allow("user", "fetch_page", 2), "rate_limited")
	stamps := l.requests[userLimitKey{userID: "user", group: "fetch_page"}]
	if len(stamps) != 2 || !stamps[0].Equal(now.Add(-30*time.Second)) {
		t.Fatalf("旧时间戳未清理=%v", stamps)
	}
}

func TestC8FetchSelfClosingScriptAndStyle(t *testing.T) {
	for _, tag := range []string{"script", "style"} {
		t.Run(tag, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprintf(w, "<title>标题</title><p>之前</p><%s/>私密脚本指令</%s><p>之后</p>", tag, tag)
			}))
			defer server.Close()
			s := New(nil, nil, nil, nil)
			s.newClient = func(o egress.Options) (*http.Client, error) { return c8LocalClient(t, server, o), nil }
			page, err := s.FetchPage(context.Background(), "https://synthetic.invalid/")
			if err != nil {
				t.Fatal(err)
			}
			if page.Title != "标题" || page.Text != "之前 之后" {
				t.Fatalf("正文=%+v", page)
			}
		})
	}
}

func TestC8FixedSourceClientsPublicOnly(t *testing.T) {
	for _, source := range []string{"tavily", "musicbrainz", "cover_art_archive"} {
		t.Run(source, func(t *testing.T) {
			secrets := secret.New(t.TempDir() + "/key")
			sealed, err := secrets.Seal("user", secret.SearchKeyPurpose, []byte("synthetic-key"))
			if err != nil {
				t.Fatal(err)
			}
			settings := &c8Settings{value: runtime.UserSettings{SearchSource: "tavily", SearchKey: &runtime.EncryptedKey{Ciphertext: sealed}}}
			s := New(settings, secrets, nil, nil)
			hits := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				io.WriteString(w, `{"results":[],"artists":[],"images":[]}`)
			}))
			defer server.Close()
			s.newClient = func(o egress.Options) (*http.Client, error) {
				if o.Policy != egress.PublicOnly {
					t.Errorf("%s 策略=%+v", source, o)
				}
				return c8LocalClient(t, server, o), nil
			}
			s.WithMusic(musicbrainz.New(musicbrainz.Options{Limiter: sources.NewLimiter(time.Second, nil, nil), NewClient: s.newClient}))
			invoke := func() error {
				switch source {
				case "tavily":
					_, err := s.Search(context.Background(), identity.User{ID: "user", Role: "admin"}, "合成", 1)
					return err
				case "musicbrainz":
					_, err := s.SearchMusic(context.Background(), "artist", "合成", 1)
					return err
				default:
					_, err := s.coverArt(context.Background(), "synthetic")
					return err
				}
			}
			if err := invoke(); err != nil || hits != 1 {
				t.Fatalf("公网合成服务=%v hits=%d", err, hits)
			}
			// 保留生产策略与 DNS 检查，只把固定域名解析为环回，禁止建立连接。
			s.newClient = func(o egress.Options) (*http.Client, error) {
				if o.Policy != egress.PublicOnly {
					t.Errorf("策略=%v", o.Policy)
				}
				o.LookupNetIP = func(context.Context, string, string) ([]netip.Addr, error) {
					return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
				}
				return egress.NewClient(o)
			}
			s.WithMusic(musicbrainz.New(musicbrainz.Options{Limiter: sources.NewLimiter(time.Second, nil, nil), NewClient: s.newClient}))
			if err := invoke(); err == nil || hits != 1 {
				t.Fatalf("固定域名私网解析被放行：%v hits=%d", err, hits)
			}
		})
	}
}
