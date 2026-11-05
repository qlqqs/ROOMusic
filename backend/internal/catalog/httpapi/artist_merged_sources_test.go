package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/identity"
)

func TestArtistMergedSourcesAuthenticationBeforeInvalidID(t *testing.T) {
	h := New(nil, identity.NewGuard(nil, false, ""), t.TempDir(), nil)
	r := httptest.NewRequest("GET", "/api/v1/artists/bad/merged-sources", nil)
	r.SetPathValue("id", "bad")
	w := httptest.NewRecorder()
	h.artistMergedSources(w, r)
	if w.Code != 401 || !strings.Contains(w.Body.String(), `"unauthorized"`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
