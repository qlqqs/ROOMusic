package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
	"github.com/qlqq/roomusic/backend/internal/steward/secret"
	"github.com/qlqq/roomusic/backend/internal/upstreammock"
)

func TestWebSearchWithUpstreamMock(t *testing.T) {
	h, err := upstreammock.New(upstreammock.Options{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()

	user := identity.User{ID: "synthetic-user", Role: "admin"}
	secrets := secret.New(t.TempDir() + "/key")
	sealed, err := secrets.Seal(user.ID, secret.SearchKeyPurpose, []byte("synthetic-tavily-key"))
	if err != nil {
		t.Fatal(err)
	}
	other := httptest.NewServer(h)
	defer other.Close()
	for _, name := range []string{"searxng", "tavily"} {
		t.Run(name, func(t *testing.T) {
			settings := &c8Settings{runtime.UserSettings{
				SearchSource: name,
				SearXNGURL:   server.URL + "/searxng",
				SearchKey:    &runtime.EncryptedKey{Ciphertext: sealed},
			}}
			svc := New(settings, secrets, func(context.Context, identity.User) (*http.Client, error) {
				return egress.NewClient(egress.Options{Policy: egress.Unrestricted})
			}, nil).WithTavily(server.URL+"/tavily/search", sources.NewMockHost(server.URL))
			results, err := svc.Search(t.Context(), user, "纸屿", 1)
			want := SearchResult{
				Title: "纸屿的潮汐信箱", URL: "https://catalog.example.invalid/paper-islet", Snippet: "合成目录记录了纸屿的发行与录音。",
			}
			if err != nil || len(results) != 1 || results[0] != want {
				t.Fatalf("搜索结果 = %+v，错误 %v", results, err)
			}
			if name == "tavily" {
				svc.WithTavily(other.URL+"/tavily/search", sources.NewMockHost(server.URL))
				results, err := svc.Search(t.Context(), user, "纸屿", 1)
				c8Code(t, err, "egress_denied")
				if len(results) != 0 {
					t.Errorf("其他本机端口返回了搜索结果: %+v", results)
				}
			}
		})
	}
}

func TestTavilyDefaultsToOfficialPublicEndpoint(t *testing.T) {
	const userID = "synthetic-user"
	secrets := secret.New(t.TempDir() + "/key")
	sealed, err := secrets.Seal(userID, secret.SearchKeyPurpose, []byte("synthetic-tavily-key"))
	if err != nil {
		t.Fatal(err)
	}
	svc := New(&c8Settings{runtime.UserSettings{SearchSource: "tavily", SearchKey: &runtime.EncryptedKey{Ciphertext: sealed}}}, secrets, nil, nil)
	var address string
	svc.newClient = func(opts egress.Options) (*http.Client, error) {
		if opts.Policy != egress.PublicOnly || opts.Allowlist != nil {
			t.Errorf("Tavily 默认策略 = %v，期望 PublicOnly 且无放行名单", opts.Policy)
		}
		return &http.Client{Transport: c8Transport(func(r *http.Request) (*http.Response, error) {
			address = r.URL.String()
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"results":[{"title":"合成结果","url":"https://fixture.example.invalid","content":"合成摘要"}]}`))}, nil
		})}, nil
	}
	results, err := svc.Search(t.Context(), identity.User{ID: userID, Role: "admin"}, "合成查询", 1)
	if err != nil || len(results) != 1 || results[0].Title != "合成结果" || address != "https://api.tavily.com/search" {
		t.Fatalf("Tavily 默认地址=%q，结果=%+v，错误=%v", address, results, err)
	}
}
