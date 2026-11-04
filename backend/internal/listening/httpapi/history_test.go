package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/identity"
)

func TestHistoryRoutesCheckOriginAndAuthenticationBeforeParameters(t *testing.T) {
	mux := http.NewServeMux()
	New(identity.NewGuard(nil, false, ""), nil).Register(mux)
	for _, tc := range []struct {
		method, path, origin string
		status               int
		code                 string
	}{
		{"GET", "/api/v1/me/plays/tracks?page=0", "", 401, "unauthorized"},
		{"GET", "/api/v1/me/plays/queue", "http://untrusted.example", 401, "unauthorized"},
		{"DELETE", "/api/v1/me/plays/tracks/invalid", "", 403, "origin_required"},
		{"DELETE", "/api/v1/me/plays/tracks/invalid", "http://untrusted.example", 403, "origin_forbidden"},
		{"DELETE", "/api/v1/me/plays/tracks/invalid", "http://roomusic.test", 401, "unauthorized"},
	} {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		r.Host = "roomusic.test"
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}
