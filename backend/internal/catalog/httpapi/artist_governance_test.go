package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/identity"
)

func TestArtistCommandStrictJSON(t *testing.T) {
	for _, body := range []string{`null`, `[]`, `{} {}`, `{"mode":"auto","mode":"set"}`, `{"unexpected":1}`, `{"mode":"auto","name":null}`, `{"mode":"auto","expected_revision":1,"name":"x"}`, `{"mode":"set","name":"x","target":{"kind":"new","kind":"restore"}}`, strings.Repeat(" ", 65537), string([]byte{'{', '"', 0xff, '"', ':', '1', '}'})} {
		var destination governance.ArtistRenameRequest
		w := httptest.NewRecorder()
		r := httptest.NewRequest("PATCH", "/", strings.NewReader(body))
		if decodeArtistCommand(w, r, &destination) || w.Code != 400 {
			t.Fatalf("accepted %q: %s", body, w.Body.String())
		}
	}
	for _, body := range []string{`{"expected_revision":1,"mode":"auto"}`, `{"expected_revision":1,"mode":"set","name":"甲"}`} {
		var d governance.ArtistRenameRequest
		w := httptest.NewRecorder()
		if !decodeArtistCommand(w, httptest.NewRequest("PATCH", "/", strings.NewReader(body)), &d) {
			t.Fatalf("rejected %s: %s", body, w.Body.String())
		}
	}
}
func TestArtistGovernanceOriginBeforeAuthentication(t *testing.T) {
	mux := http.NewServeMux()
	NewArtistGovernance(nil, identity.NewGuard(nil, false, ""), nil, nil).Register(mux)
	for _, route := range []struct{ method, path string }{{"POST", "merge"}, {"POST", "split-preview"}, {"POST", "split"}, {"PATCH", "name"}, {"POST", "candidate-ignore"}} {
		path := "/api/v1/artists/not-a-uuid/" + route.path
		for _, origin := range []string{"", "http://example.com"} {
			r := httptest.NewRequest(route.method, path, strings.NewReader(`{}`))
			if origin != "" {
				r.Header.Set("Origin", origin)
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			want := 403
			if origin != "" {
				want = 401
			}
			if w.Code != want {
				t.Fatalf("%s origin=%q status=%d body=%s", route.path, origin, w.Code, w.Body.String())
			}
		}
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/artist-candidates?page=bad", nil))
	if w.Code != 401 {
		t.Fatalf("candidates=%d", w.Code)
	}
}
