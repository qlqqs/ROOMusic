package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

func TestArtistReadParameterValidation(t *testing.T) {
	for _, tc := range []struct{ query, code string }{
		{"", ""}, {"role=composer&initial=kana&sort=album_count&page=2&page_size=100", ""},
		{"role=album_artist", ""}, {"role=", "invalid_artist_role"}, {"role=album_artist&role=composer", "invalid_artist_role"},
		{"initial=a", "invalid_artist_initial"}, {"initial=%23", ""}, {"initial=AA", "invalid_artist_initial"},
		{"sort=", "invalid_artist_sort"}, {"sort=title", "invalid_artist_sort"}, {"sort=name&sort=name", "invalid_artist_sort"},
		{"q=%01", "invalid_query"}, {"page_size=101", "invalid_pagination"},
	} {
		r := httptest.NewRequest("GET", "/api/v1/artists?"+tc.query, nil)
		w := httptest.NewRecorder()
		_, ok := parseArtistListParams(w, r)
		if ok != (tc.code == "") {
			t.Fatalf("%s: ok=%v body=%s", tc.query, ok, w.Body.String())
		}
		if tc.code != "" && (!strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) || w.Code != 400) {
			t.Fatalf("%s: %d %s", tc.query, w.Code, w.Body.String())
		}
	}
}

func TestArtistQueueParameterValidation(t *testing.T) {
	id := "A0000000-0000-4000-8000-000000000001"
	for _, tc := range []struct {
		query, order string
		seed, valid  bool
	}{
		{"", "album", false, true}, {"order=random&seed=" + id, "random", true, true},
		{"order=random", "random", false, false}, {"seed=bad", "album", false, false},
		{"order=other", "", false, false}, {"order=random&seed=" + id + "&seed=" + id, "random", false, false},
		{"order=album&seed=" + id, "album", false, true},
	} {
		order, seed, err := parseArtistQueue(httptest.NewRequest("GET", "/api/v1/artists/id/queue?"+tc.query, nil))
		if (err == nil) != tc.valid || order != tc.order || (seed != nil) != tc.seed {
			t.Fatalf("%s: %s %v %v", tc.query, order, seed, err)
		}
		if seed != nil && *seed != strings.ToLower(id) {
			t.Fatalf("seed not canonical: %s", *seed)
		}
	}
}

func TestArtistReadPreparingErrorPreserved(t *testing.T) {
	h := &Handlers{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for _, search := range []bool{false, true} {
		r := httptest.NewRequest("GET", "/api/v1/artists", nil)
		w := httptest.NewRecorder()
		err := fmt.Errorf("wrapped: %w", catalog.ErrArtistCatalogPreparing)
		if search {
			h.writeSearchError(w, r, err)
		} else {
			h.writeArtistReadError(w, r, err)
		}
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(w.Body.Bytes(), &body) != nil || w.Code != 503 || w.Header().Get("Retry-After") != "3" || body.Error.Code != "artist_catalog_preparing" {
			t.Fatalf("%d %v %s", w.Code, w.Header(), w.Body.String())
		}
	}
}
