package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

func TestPostgreSQLCatalogStats(t *testing.T) {
	fixture := newArtistSamples(t)
	fixture.publish()

	call := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		return fixture.app.request(t, http.MethodGet, path, nil, &fixture.admin, nil)
	}
	readStats := func() catalog.CatalogStats {
		t.Helper()
		response := call("/api/v1/catalog/stats")
		if response.Code != http.StatusOK {
			t.Fatalf("get catalog stats: HTTP %d: %s", response.Code, response.Body.String())
		}
		var stats catalog.CatalogStats
		if err := json.Unmarshal(response.Body.Bytes(), &stats); err != nil {
			t.Fatalf("decode catalog stats: %v", err)
		}
		return stats
	}
	readTotal := func(path string) int64 {
		t.Helper()
		response := call(path)
		if response.Code != http.StatusOK {
			t.Fatalf("get %s: HTTP %d: %s", path, response.Code, response.Body.String())
		}
		var result struct {
			Pagination struct {
				Total int64 `json:"total"`
			} `json:"pagination"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatalf("decode %s pagination: %v", path, err)
		}
		return result.Pagination.Total
	}

	if got := fixture.app.request(t, http.MethodGet, "/api/v1/catalog/stats", nil, nil, nil); got.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated catalog stats: HTTP %d: %s", got.Code, got.Body.String())
	}

	stats := readStats()
	if stats.Artists == nil || stats.Composers == nil {
		t.Fatalf("ready artist catalog returned null counts: %+v", stats)
	}
	if stats.Albums != readTotal("/api/v1/releases") {
		t.Fatalf("album count differs from release list total: stats=%d", stats.Albums)
	}
	var presentTracks int64
	if err := fixture.app.database.QueryRow(`SELECT count(*) FROM tracks WHERE source_status='present'`).Scan(&presentTracks); err != nil {
		t.Fatalf("count present tracks: %v", err)
	}
	if stats.Tracks != presentTracks {
		t.Fatalf("track count differs from present tracks: stats=%d present=%d", stats.Tracks, presentTracks)
	}
	if *stats.Artists != readTotal("/api/v1/artists") {
		t.Fatalf("artist count differs from artist list total: stats=%d", *stats.Artists)
	}
	if *stats.Composers != readTotal("/api/v1/artists?role=composer") {
		t.Fatalf("composer count differs from composer list total: stats=%d", *stats.Composers)
	}

	mustExec(t, fixture.app, `UPDATE catalog_projection_state SET target_generation=target_generation+1,build_id=gen_random_uuid(),status='preparing',manifest_sealed=FALSE,catalog_epoch=catalog_epoch+1`)
	preparing := readStats()
	if preparing.Artists != nil || preparing.Composers != nil {
		t.Fatalf("preparing artist catalog returned counts: %+v", preparing)
	}
	if preparing.Albums == 0 || preparing.Tracks == 0 {
		t.Fatalf("preparing artist catalog lost album/track counts: %+v", preparing)
	}
}
