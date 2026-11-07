package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/testsupport"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLArtistReadHTTPPreparingAndAuthentication(t *testing.T) {
	db, err := postgres.Open(context.Background(), postgrestest.NewSchema(t, "roomusic_artist_http_"))
	if err != nil {
		t.Fatal(err)
	}
	if err := testsupport.SetArtistProjectionVersionCurrent(context.Background(), db); err != nil {
		t.Fatalf("set current artist projection version: %v", err)
	}
	defer db.Close()
	id, token := uuid.New(), identity.GenerateSessionToken()
	if _, err := db.Exec(`INSERT INTO users(id,username,password_hash,role) VALUES($1::uuid,'member','test','user')`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2::uuid,now()+interval '1 hour')`, identity.HashSessionToken(token), id); err != nil {
		t.Fatal(err)
	}
	h := New(db, identity.NewGuard(db, false, ""), t.TempDir(), nil)
	mux := http.NewServeMux()
	// 本测试只装配当前切片，生产 Register 的统一装配由主会话负责。
	mux.HandleFunc("GET /api/v1/artists", h.listArtists)
	mux.HandleFunc("GET /api/v1/artists/{id}", h.artistDetail)
	mux.HandleFunc("GET /api/v1/artists/{id}/names", h.artistNames)
	mux.HandleFunc("GET /api/v1/artists/{id}/albums", h.artistAlbums)
	mux.HandleFunc("GET /api/v1/artists/{id}/tracks", h.artistTracks)
	mux.HandleFunc("GET /api/v1/artists/{id}/queue", h.artistQueue)
	mux.HandleFunc("GET /api/v1/search", h.search)
	request := func(path string, loggedIn bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if loggedIn {
			r.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: token})
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	artist := "/api/v1/artists/a0000000-0000-4000-8000-000000000001"
	for _, path := range []string{"/api/v1/artists", artist, artist + "/names", artist + "/albums", artist + "/tracks?role=composer", artist + "/queue", "/api/v1/search?type=artists&q=Jay"} {
		w := request(path, false)
		if w.Code != 401 {
			t.Fatalf("unauthenticated %s: %d %s", path, w.Code, w.Body.String())
		}
		w = request(path, true)
		if w.Code != 503 || w.Header().Get("Retry-After") != "3" || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), `"artist_catalog_preparing"`) {
			t.Fatalf("preparing %s: %d %v %s", path, w.Code, w.Header(), w.Body.String())
		}
	}
	for _, path := range []string{"/api/v1/artists/not-an-id", artist + "/tracks", artist + "/queue?order=random", "/api/v1/artists?initial=a"} {
		w := request(path, true)
		if w.Code != 400 {
			t.Fatalf("validation precedes preparing %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if _, err := db.Exec(`DROP TABLE artist_profiles CASCADE`); err != nil {
		t.Fatal(err)
	}
	// 整理状态无需读取半成品，缺失实体表也不能转成数据库故障或404。
	if w := request(artist, true); w.Code != 503 || !strings.Contains(w.Body.String(), `"artist_catalog_preparing"`) {
		t.Fatalf("partial projection read: %d %s", w.Code, w.Body.String())
	}
	if _, err := db.Exec(`UPDATE catalog_projection_state SET status='ready',manifest_sealed=true,published_generation=target_generation,published_textnorm_version=textnorm_version,published_search_projection_version=search_projection_version WHERE capability='artists'`); err != nil {
		t.Fatal(err)
	}
	w := request(artist, true)
	if w.Code != 503 || !strings.Contains(w.Body.String(), `"database_unavailable"`) || strings.Contains(w.Body.String(), "artist_profiles") || strings.Contains(w.Body.String(), "SELECT") {
		t.Fatalf("unsafe database failure: %d %s", w.Code, w.Body.String())
	}
}
