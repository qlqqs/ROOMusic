package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

func TestArtistFavoritesRoutesGuardAndMethods(t *testing.T) {
	mux := http.NewServeMux()
	New(identity.NewGuard(nil, false, ""), nil).Register(mux)
	httpserver.RegisterAPIFallback(mux, httpserver.APINotFoundHandler(mux))
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		path := "/api/v1/me/favorites/artists"
		if method != "GET" {
			path += "/invalid"
		}
		for _, origin := range []string{"", "http://evil.test", "http://roomusic.test"} {
			r := httptest.NewRequest(method, path, strings.NewReader("{"))
			r.Host = "roomusic.test"
			r.Header.Set("Origin", origin)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			want := 401
			if method != "GET" && origin != "http://roomusic.test" {
				want = 403
			}
			if w.Code != want || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("%s %s: %d %s", method, origin, w.Code, w.Body)
			}
		}
	}
	for _, tc := range []struct{ method, path, allow string }{
		{"POST", "/api/v1/me/favorites/artists", "GET, HEAD"},
		{"GET", "/api/v1/me/favorites/artists/10000000-0000-4000-8000-000000000001", "PUT, DELETE"},
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != 405 || w.Header().Get("Allow") != tc.allow {
			t.Fatalf("method: %d %s %s", w.Code, w.Header().Get("Allow"), w.Body)
		}
	}
}

func TestArtistFavoritesLookupStrictInput(t *testing.T) {
	id := "10000000-0000-4000-8000-000000000001"
	for _, body := range []string{`{"artist_ids":null}`, `{"artist_ids":[null]}`, `{"artist_ids":["bad"]}`, `{"artist_ids":[],"artist_ids":[]}`, `{"artist_ids":[],"user_id":"other"}`, `{"artist_ids":[]} {}`, `{"artist_ids":["` + strings.Repeat(id+`","`, 1000) + id + `"]}`} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		(&Handlers{}).lookup(w, r, id)
		if w.Code != 400 {
			t.Fatalf("invalid lookup: %d %s", w.Code, w.Body)
		}
	}
}

func TestArtistFavoritesPreparingErrors(t *testing.T) {
	for _, tc := range []struct {
		err         error
		status      int
		code, retry string
	}{
		{personal.ErrArtistCatalogPreparing, 503, "artist_catalog_preparing", "3"},
		{personal.ErrArtistCatalogNotReady, 409, "artist_catalog_not_ready", ""},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/", nil)
		writeError(w, r, fmt.Errorf("private context: %w", tc.err))
		if w.Code != tc.status || w.Header().Get("Retry-After") != tc.retry || !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) || strings.Contains(w.Body.String(), "private context") {
			t.Fatalf("error: %d %s", w.Code, w.Body)
		}
	}
}
