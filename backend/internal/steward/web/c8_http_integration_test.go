package web_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/httpapi"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
	"github.com/qlqq/roomusic/backend/internal/steward/secret"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
	"github.com/qlqq/roomusic/backend/internal/steward/web"
)

type c8HTTPTransport func(*http.Request) (*http.Response, error)

func (f c8HTTPTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPostgreSQLC8TavilyQuotaHTTPChain(t *testing.T) {
	db, err := postgres.Open(t.Context(), postgrestest.NewSchema(t, "c8_quota_"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	userID, token := uuid.New(), identity.GenerateSessionToken()
	if _, err := db.Exec(`INSERT INTO users(id,username,password_hash,role) VALUES($1,'synthetic-quota','synthetic-only','user')`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '1 hour')`, identity.HashSessionToken(token), userID); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	store := runtime.NewStore(db)
	secrets := secret.New(filepath.Join(t.TempDir(), "instance.key"))
	service := web.New(store, secrets, nil, logger)
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer synthetic-quota-key-5678" {
			t.Error("Tavily 请求缺少方法或认证")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["query"] != "MusicBrainz" {
			t.Error("未使用固定测试查询")
		}
		w.WriteHeader(432)
		io.WriteString(w, `{"detail":"synthetic-private-upstream"}`)
	}))
	defer server.Close()
	target, _ := url.Parse(server.URL)
	web.C8TestClient(service, func(o egress.Options) (*http.Client, error) {
		if o.Policy != egress.PublicOnly {
			t.Error("Tavily 未使用仅公网策略")
		}
		return &http.Client{Transport: c8HTTPTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != "https://api.tavily.com/search" {
				t.Errorf("固定地址=%s", r.URL)
			}
			clone := r.Clone(context.Background())
			localURL := *r.URL
			localURL.Scheme = target.Scheme
			localURL.Host = target.Host
			clone.URL = &localURL
			return server.Client().Transport.RoundTrip(clone)
		})}, nil
	})
	handlers := httpapi.New(identity.NewGuard(db, false, "").WithLogger(logger), store, secrets, runtime.NewRunner(store, tool.NewRegistry(), logger), logger).WithWeb(service)
	mux := http.NewServeMux()
	handlers.Register(mux)
	handler := httpserver.Middleware(logger, mux)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://roomusic.test/api/v1/me/steward/"+path, strings.NewReader(body))
		r.Header.Set("Origin", "http://roomusic.test")
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: token})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	w := request("PUT", "settings", `{"search_source":"tavily","search_key":"synthetic-quota-key-5678"}`)
	if w.Code != 200 {
		t.Fatalf("保存设置=%d %s", w.Code, w.Body)
	}
	w = request("POST", "settings/search-test", `{}`)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || w.Header().Get("X-Request-ID") == "" {
		t.Fatalf("响应=%d %s %v", w.Code, w.Body, w.Header())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if hits != 1 || len(body) != 2 || body["ok"] != false || body["code"] != "search_quota_exceeded" {
		t.Fatalf("额度整链响应=%v hits=%d", body, hits)
	}
	for _, value := range []string{"synthetic-quota-key-5678", "synthetic-private-upstream", "MusicBrainz"} {
		if strings.Contains(logs.String(), value) || strings.Contains(w.Body.String(), value) {
			t.Errorf("公开输出泄漏 %s", value)
		}
	}
}
