package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLRecentlyAddedHTTPContract(t *testing.T) {
	database, err := postgres.Open(context.Background(), postgrestest.NewSchema(t, "roomusic_catalog_http_"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := database.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	cookies := map[string]string{}
	for _, role := range []string{"admin", "user"} {
		id, token := uuid.New(), identity.GenerateSessionToken()
		exec(`INSERT INTO users(id,username,password_hash,role) VALUES($1::uuid,$2,'test',$2)`, id, role)
		exec(`INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2::uuid,NOW()+INTERVAL '1 hour')`, identity.HashSessionToken(token), id)
		cookies[role] = token
	}
	handlers := New(database, identity.NewGuard(database, false, ""), t.TempDir(), nil)
	mux := http.NewServeMux()
	handlers.Register(mux)
	request := func(path, role string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		if role != "" {
			r.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: cookies[role]})
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/api/v1/release-groups/recently-added", "/api/v1/release-groups/recently-added?limit=8", "/api/v1/release-groups?sort=invalid"} {
		if w := request(path, ""); w.Code != 401 {
			t.Fatalf("unauthenticated %s = %d", path, w.Code)
		}
	}
	for _, role := range []string{"admin", "user"} {
		w := request("/api/v1/release-groups/recently-added", role)
		if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"items":[]}` {
			t.Fatalf("empty %s = %d %s", role, w.Code, w.Body.String())
		}
		for _, query := range []string{"limit=8", "page=1", "page_size=8", "sort=added_at_desc", "q=title", "attention=required", "unknown=1", "x=%zz"} {
			w = request("/api/v1/release-groups/recently-added?"+query, role)
			if w.Code != 400 || !strings.Contains(w.Body.String(), `"invalid_query"`) {
				t.Fatalf("query %s = %d %s", query, w.Code, w.Body.String())
			}
		}
		for _, query := range []string{"sort=", "sort=unknown", "sort=title&sort=title"} {
			w = request("/api/v1/release-groups?"+query, role)
			if w.Code != 400 || !strings.Contains(w.Body.String(), `"invalid_sort"`) {
				t.Fatalf("sort = %d %s", w.Code, w.Body.String())
			}
		}
	}
	root, group, release, medium := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,'/synthetic','active',1)`, root)
	exec(`INSERT INTO release_groups(id) VALUES($1::uuid)`, group)
	exec(`INSERT INTO releases(id,group_id,title,artist,created_at) VALUES($1::uuid,$2::uuid,'合成专辑','艺人','2026-01-01T00:00:00Z')`, release, group)
	exec(`INSERT INTO media(id,release_id,position,title) VALUES($1::uuid,$2::uuid,1,'碟')`, medium, release)
	exec(`INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,source_status,observed_at) VALUES($1::uuid,$2::uuid,1,'曲目','艺人',$3::uuid,'one.wav','present',NOW())`, uuid.New(), medium, root)
	for _, path := range []string{"/api/v1/release-groups/recently-added", "/api/v1/release-groups?sort=added_at_desc", "/api/v1/release-groups?sort=title", "/api/v1/release-groups/" + group} {
		w := request(path, "user")
		var payload any
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &payload) != nil || !strings.Contains(w.Body.String(), `"added_at":"2026-01-01T00:00:00Z"`) {
			t.Fatalf("response %s = %d %s", path, w.Code, w.Body.String())
		}
	}
	exec(`DROP VIEW release_effective`)
	w := request("/api/v1/release-groups/recently-added", "user")
	if w.Code != 503 || !strings.Contains(w.Body.String(), `"database_unavailable"`) || strings.Contains(w.Body.String(), "release_effective") {
		t.Fatalf("safe failure = %d %s", w.Code, w.Body.String())
	}
}
