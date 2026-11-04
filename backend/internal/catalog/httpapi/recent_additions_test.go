package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

func TestReleaseGroupSortAllowlist(t *testing.T) {
	for _, test := range []struct {
		query string
		want  catalog.ReleaseGroupSort
		valid bool
	}{
		{"", catalog.ReleaseGroupSortTitle, true}, {"?sort=title", catalog.ReleaseGroupSortTitle, true},
		{"?sort=added_at_desc", catalog.ReleaseGroupSortAddedAtDesc, true},
		{"?sort=", "", false}, {"?sort=unknown", "", false}, {"?sort=title&sort=title", "", false}, {"?sort=title&sort=added_at_desc", "", false},
	} {
		t.Run(test.query, func(t *testing.T) {
			w := httptest.NewRecorder()
			got, ok := parseReleaseGroupSort(w, httptest.NewRequest("GET", "/api/v1/release-groups"+test.query, nil))
			if ok != test.valid || got != test.want {
				t.Fatalf("sort = %q, %v", got, ok)
			}
			if !ok && (w.Code != 400 || !strings.Contains(w.Body.String(), `"invalid_sort"`)) {
				t.Fatalf("error = %d %s", w.Code, w.Body.String())
			}
		})
	}
}
