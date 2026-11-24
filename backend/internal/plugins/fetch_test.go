package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPluginUpstreamFetch(t *testing.T) {
	engine, dir := pluginTestEngine(t)
	wasm := buildPluginFixture(t, dir, "runtime/testdata/upstream_fetch.go")
	const headerKey = "synthetic-test-header"
	const queryKey = "synthetic-test-query"
	type observed struct {
		at                                 time.Time
		path, method, header, query, agent string
	}
	var mu sync.Mutex
	var requests []observed
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("token")
		key := r.Header.Get("X-Api-Key")
		mu.Lock()
		requests = append(requests, observed{time.Now(), r.URL.Path, r.Method, key, q, r.UserAgent()})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Upstream-Private", "private-metadata")
		switch {
		case strings.HasSuffix(r.URL.Path, "/echo_body"):
			fmt.Fprintf(w, `{"echo":%q}`, key+" "+q)
		case strings.HasSuffix(r.URL.Path, "/echo_header"):
			w.Header().Set("Content-Type", "text/plain; marker="+key)
			fmt.Fprint(w, `{"value":"visible"}`)
		case strings.HasSuffix(r.URL.Path, "/large"):
			w.Header().Set("Content-Length", "4194305")
			fmt.Fprint(w, strings.Repeat("a", 4194305))
		default:
			fmt.Fprint(w, `{"value":"visible"}`)
		}
	}))
	defer up.Close()
	s := &Store{dir: dir, mock: up.URL, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), keys: func(_ context.Context, id string) (map[string]string, error) {
		if id != "plugin:fixture" {
			return nil, fmt.Errorf("意外的源 %q", id)
		}
		return map[string]string{"header_key": headerKey, "query_key": queryKey}, nil
	}}
	p := &Source{store: s, Metadata: Metadata{Manifest: Manifest{
		ID: "fixture", Upstreams: []Upstream{{ID: "main", BaseURL: "https://fixture.invalid/api", MinIntervalMS: 750}},
		Keys: []Key{
			{Name: "header_key", Upstream: "main", Inject: Injection{In: "header", Name: "X-Api-Key"}},
			{Name: "query_key", Upstream: "main", Inject: Injection{In: "query", Name: "token"}},
		},
	}}}
	p.initEndpoints()
	call := func(t *testing.T, name, id string, args fetchArgs) []byte {
		t.Helper()
		got, err := engine.Call(t.Context(), id, func(context.Context) ([]byte, error) { return wasm, nil }, name, fixtureJSON(t, args), p.fetch)
		if err != nil || got.Code != 0 {
			t.Fatalf("WASM 调用失败，退出码 %d，错误 %v", got.Code, err)
		}
		return got.Output
	}
	prefix := "/plugins/fixture/main/"
	t.Run("request_boundary", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			args   fetchArgs
			reason string
		}{
			{"unknown_upstream", fetchArgs{Upstream: "undeclared", Path: "visible"}, "unknown_upstream"},
			{"post", fetchArgs{Upstream: "main", Method: "POST", Path: "visible"}, "invalid_request"},
			{"parent_path", fetchArgs{Upstream: "main", Path: "../outside"}, "invalid_request"},
			{"encoded_parent_path", fetchArgs{Upstream: "main", Path: "%2e%2e/outside"}, "invalid_request"},
			{"absolute_url", fetchArgs{Upstream: "main", Path: up.URL + "/outside"}, "invalid_request"},
			{"different_host", fetchArgs{Upstream: "main", Path: "//example.invalid/outside"}, "invalid_request"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got := call(t, "lyrics", "fixture", tc.args)
				var reply struct{ Error string }
				if err := json.Unmarshal(got, &reply); err != nil || reply.Error != tc.reason {
					t.Errorf("期望拒绝 %q，实际 %s，错误 %v", tc.reason, got, err)
				}
			})
		}
		mu.Lock()
		n := len(requests)
		mu.Unlock()
		if n != 0 {
			t.Errorf("非法插件请求实际出网 %d 次", n)
		}
	})
	t.Run("rate_cache_mock_and_secrets", func(t *testing.T) {
		first := fetchArgs{Upstream: "main", Path: "first", Query: map[string]string{"token": "guest-value"}}
		a := call(t, "lyrics", "another-plugin", first)
		first.Query["token"] = "another-guest-value"
		b := call(t, "lyrics", "another-plugin", first)
		c := call(t, "lyrics", "another-plugin", fetchArgs{Upstream: "main", Path: "second"})
		for _, got := range [][]byte{a, b, c} {
			var reply fetchResponse
			if err := json.Unmarshal(got, &reply); err != nil || reply.Status != 200 || string(reply.Body) != `{"value":"visible"}` ||
				len(reply.Headers) != 1 || reply.Headers["content-type"] != "application/json" {
				t.Errorf("插件所见响应不符合白名单，状态 %d，响应头数 %d，错误 %v", reply.Status, len(reply.Headers), err)
			}
		}
		if !bytes.Equal(a, b) {
			t.Error("同一公开请求命中缓存时，插件收到的响应发生变化")
		}
		mu.Lock()
		sent := append([]observed(nil), requests...)
		mu.Unlock()
		if len(sent) != 2 {
			t.Fatalf("两条公开请求应发出两次 HTTP，实际 %d 次", len(sent))
		}
		if sent[0].path != prefix+"first" || sent[1].path != prefix+"second" || sent[1].at.Sub(sent[0].at) < 700*time.Millisecond {
			t.Errorf("mock 地址或最小请求间隔不符，路径 %q、%q，间隔 %v", sent[0].path, sent[1].path, sent[1].at.Sub(sent[0].at))
		}
		for _, req := range sent {
			if req.method != "GET" || req.agent == "" || req.query != queryKey || req.header != headerKey {
				t.Errorf("上游未收到预期 GET、UA 与两种注入密钥，路径 %q", req.path)
			}
		}
	})
	for _, path := range []string{"echo_body", "echo_header", "large"} {
		t.Run(path, func(t *testing.T) {
			got := call(t, "lyrics", "fixture", fetchArgs{Upstream: "main", Path: path})
			var reply struct {
				Error string `json:"error"`
				Body  []byte `json:"body_base64"`
			}
			if err := json.Unmarshal(got, &reply); err != nil || reply.Error != "upstream_unavailable" || len(reply.Body) != 0 ||
				bytes.Contains(got, []byte(headerKey)) || bytes.Contains(got, []byte(queryKey)) {
				t.Errorf("插件应仅收到上游不可用，错误码 %q，响应体长度 %d，解析错误 %v", reply.Error, len(reply.Body), err)
			}
		})
	}
}
