package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/identity"
)

const scalePopularArtist = "a0000000-0000-4000-8000-000000000001"

type scaleQueueCancelKey struct{}
type scaleQueueCancelWatch struct {
	once    sync.Once
	started chan uint32
}

type scaleQueueReport struct {
	Order               string
	Latency             scaleLatency
	DatabaseMS          scaleDatabaseLatency
	ResponseBytes       int
	Total, MissingTotal int64
	Returned            int
	CancelMS            float64
	CancelStatus        int
	PlanFile            string
}

type scaleRequest func(string, string, []byte, map[string]string) (*httptest.ResponseRecorder, float64, float64)

// 丢弃空闲连接后恢复正常池策略；同一请求内仍可复用连接，不人为惩罚每条SQL。
func scaleColdConnections(db *sql.DB) { db.SetMaxIdleConns(0); db.SetMaxIdleConns(12) }

func scaleRAMBytes() uint64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		var kib uint64
		if _, err := fmt.Sscanf(line, "MemTotal: %d kB", &kib); err == nil {
			return kib * 1024
		}
	}
	return 0
}
func scaleSampleHeap(ctx context.Context, peak *atomic.Uint64) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var memory runtime.MemStats
			runtime.ReadMemStats(&memory)
			peak.Store(max(peak.Load(), memory.HeapAlloc))
		}
	}
}

// 结构断言防止基准以空壳艺人、缺失别名或缩小署名展开规模换取通过。
func scaleAssertArtistFixture(ctx context.Context, db *sql.DB, report *scaleReport) error {
	checks := []struct {
		name, query      string
		minimum, maximum int64
	}{
		{"participating_pool_artists", `SELECT count(DISTINCT e.artist_id) FROM artist_effective_credits e JOIN tracks t ON t.id=e.track_id WHERE t.source_status='present' AND e.artist_id::text LIKE '60000000-0000-4000-8000-%'`, 20000, 20000},
		{"pool_artists_with_distinct_key_aliases", `SELECT count(*) FROM (SELECT initial_artist_id FROM artist_name_keys WHERE initial_artist_id::text LIKE '60000000-0000-4000-8000-%' GROUP BY initial_artist_id HAVING count(*)>=2) aliases`, 4000, 4000},
		{"participating_pool_alias_keys", `SELECT count(DISTINCT n.key_id) FROM artist_effective_credits e JOIN artist_names n ON n.id=e.name_id JOIN tracks t ON t.id=e.track_id WHERE t.source_status='present' AND n.key_id::text LIKE '62000000-0000-4000-8000-%'`, 4001, 4001},
		{"synthetic_tracks_with_two_artists", `SELECT count(*) FROM (SELECT c.track_id FROM artist_credits c JOIN tracks t ON t.id=c.track_id WHERE t.source_root_id='10000000-0000-4000-8000-000000000001' AND c.role='track_artist' GROUP BY c.track_id HAVING count(*)=2) subjects`, 110000, 110000},
		{"synthetic_tracks_with_four_credits", `SELECT count(*) FROM (SELECT c.track_id FROM track_credits c JOIN tracks t ON t.id=c.track_id WHERE t.source_root_id='10000000-0000-4000-8000-000000000001' GROUP BY c.track_id HAVING count(*)=4) subjects`, 110000, 110000},
		{"popular_artist_present_tracks", `SELECT count(DISTINCT t.id) FROM artist_effective_credits e JOIN tracks t ON t.id=e.track_id WHERE e.artist_id='a0000000-0000-4000-8000-000000000001' AND t.source_status='present'`, 50000, 50000},
		{"popular_label_present_tracks", `SELECT count(*) FROM tracks t JOIN media m ON m.id=t.medium_id JOIN release_effective r ON r.id=m.release_id WHERE t.source_status='present' AND r.label='热门厂牌'`, 50000, 50000},
		{"missing_only_releases", `SELECT count(*) FROM releases r WHERE NOT EXISTS(SELECT 1 FROM media m JOIN tracks t ON t.medium_id=m.id WHERE m.release_id=r.id AND t.source_status='present')`, 100, 100},
	}
	for _, check := range checks {
		var count int64
		if err := db.QueryRowContext(ctx, check.query).Scan(&count); err != nil {
			return fmt.Errorf("scale shape %s: %w", check.name, err)
		}
		report.Counts[check.name] = count
		if count < check.minimum || count > check.maximum {
			return fmt.Errorf("scale shape %s=%d, expected [%d,%d]", check.name, count, check.minimum, check.maximum)
		}
	}
	for key, minimum := range map[string]int64{"tracks_present": 100000, "tracks_missing": 10000, "releases": 10000, "release_groups": 8000, "artists": 20000, "catalog_search_values": 700000, "artist_credits": 680000} {
		if report.Counts[key] < minimum {
			return fmt.Errorf("scale shape %s=%d below %d", key, report.Counts[key], minimum)
		}
	}
	if report.Counts["artists"] > 25000 {
		return fmt.Errorf("shared artist pool unexpectedly expanded to %d identities", report.Counts["artists"])
	}
	return nil
}

func scaleValidateSuggestions(body []byte) error {
	var response catalog.SearchSuggestions
	if err := json.Unmarshal(body, &response); err != nil {
		return err
	}
	if response.CatalogState.SearchMode != "normalized" || response.CatalogState.ArtistsState != "ready" {
		return fmt.Errorf("suggestions not fully published: %+v", response.CatalogState)
	}
	if len(response.AvailableTypes) != 3 || response.Artists.Items == nil || response.Albums.Items == nil || response.Tracks.Items == nil {
		return fmt.Errorf("suggestions must expose three non-null sections")
	}
	if len(response.Artists.Items) > 5 || len(response.Albums.Items) > 5 || len(response.Tracks.Items) > 5 {
		return fmt.Errorf("suggestion limit exceeded")
	}
	return nil
}
func scaleValidateSearch(q, kind string, body []byte) error {
	var response struct {
		Type         string                   `json:"type"`
		CatalogState catalog.CatalogState     `json:"catalog_state"`
		Items        []json.RawMessage        `json:"items"`
		Pagination   catalog.SearchPagination `json:"pagination"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return err
	}
	p := response.Pagination
	if response.Type != kind || response.CatalogState.ArtistsState != "ready" || response.CatalogState.SearchMode != "normalized" || response.Items == nil || len(response.Items) > 50 || p.Page != 1 || p.PageSize != 50 {
		return fmt.Errorf("invalid %s scale search projection", kind)
	}
	if p.Total < 0 || p.Total > 1000 || (p.TotalRelation != "eq" && p.TotalRelation != "gte") || (p.TotalRelation == "gte" && p.Total != 1000) {
		return fmt.Errorf("invalid bounded count: %+v", p)
	}
	if q == "missingonlyneedle" || q == "不存在的长查询xyz" || q == "厂牌 9900" {
		if len(response.Items) != 0 || p.Total != 0 || p.HasMore || p.TotalRelation != "eq" {
			return fmt.Errorf("hidden/old/unknown term unexpectedly matched %s: %+v", kind, p)
		}
	}
	if kind == "tracks" {
		expected, ok := map[string]int64{"boundary999": 999, "boundary1000": 1000, "boundary1001": 1000, "热门厂牌": 1000, "Teresa Teng": 1000}[q]
		if ok {
			relation := "eq"
			if q == "boundary1001" || q == "热门厂牌" || q == "Teresa Teng" {
				relation = "gte"
			}
			if p.Total != expected || p.TotalRelation != relation || !p.HasMore || len(response.Items) != 50 {
				return fmt.Errorf("threshold %q: %+v items=%d", q, p, len(response.Items))
			}
		}
		if q == "交响曲 第00999号" {
			if len(response.Items) != 3 {
				return fmt.Errorf("ranking fixture returned %d instead of 3 tracks", len(response.Items))
			}
			for i, want := range []string{"exact", "prefix", "contains"} {
				var item catalog.SearchTrackItem
				if err := json.Unmarshal(response.Items[i], &item); err != nil {
					return err
				}
				if item.Match.Kind != want {
					return fmt.Errorf("global relevance[%d]=%q, want %q", i, item.Match.Kind, want)
				}
			}
		}
	}
	if kind == "albums" && q == "非代表命中" {
		if len(response.Items) != 1 {
			return fmt.Errorf("non-representative match count=%d", len(response.Items))
		}
		var item catalog.SearchAlbumItem
		if err := json.Unmarshal(response.Items[0], &item); err != nil {
			return err
		}
		if item.MatchedReleaseID == item.Group.Representative.ID {
			return fmt.Errorf("fixture lost non-representative evidence")
		}
	}
	if kind == "artists" && q == "Teresa Teng" {
		if len(response.Items) != 1 {
			return fmt.Errorf("alias artist count=%d", len(response.Items))
		}
		var item catalog.SearchArtistItem
		if err := json.Unmarshal(response.Items[0], &item); err != nil {
			return err
		}
		if item.Artist.ID != scalePopularArtist || item.Match.Alias == nil {
			return fmt.Errorf("alias did not resolve to popular artist")
		}
	}
	return nil
}

func scaleSavePlans(ctx context.Context, db *sql.DB, queries []scaleQuery, path string) error {
	plans := make([]map[string]any, 0, len(queries))
	for _, query := range queries {
		start := time.Now()
		raw, err := scaleExplain(ctx, db, query)
		if err != nil {
			return err
		}
		plans = append(plans, map[string]any{"query": query, "plan": raw, "wall_ms": float64(time.Since(start)) / float64(time.Millisecond)})
	}
	body, err := json.MarshalIndent(plans, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(body, '\n'), 0644)
}

func scaleMeasureArtistQueues(b *testing.B, ctx context.Context, db *sql.DB, tracer *scaleTracer, handler http.Handler, token, output string, request scaleRequest) []scaleQueueReport {
	b.Helper()
	reports := []scaleQueueReport{}
	persist := func() {
		body, err := json.MarshalIndent(reports, "", "  ")
		if err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(output, "queues.json"), append(body, '\n'), 0644); err != nil {
			b.Fatal(err)
		}
	}
	defer persist()
	for _, order := range []string{"album", "random"} {
		path := "/api/v1/artists/" + scalePopularArtist + "/queue?order=" + order
		if order == "random" {
			path += "&seed=90000000-0000-4000-8000-000000000001"
		}
		item := scaleQueueReport{Order: order, PlanFile: "explain-queue-" + order + ".json"}
		var decoded catalog.ArtistQueue
		measured := func(validate bool) (float64, float64) {
			response, ms, dbMS := request(http.MethodGet, path, nil, nil)
			if response.Code != 200 {
				b.Fatalf("queue %s: %d %s", order, response.Code, response.Body)
			}
			item.ResponseBytes = max(item.ResponseBytes, response.Body.Len())
			if validate {
				if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
					b.Fatal(err)
				}
				if decoded.Returned != 10000 || len(decoded.Items) != 10000 || decoded.Limit != 10000 || decoded.Total != 50000 || !decoded.Truncated {
					b.Fatalf("queue shape: returned=%d total=%d limit=%d", decoded.Returned, decoded.Total, decoded.Limit)
				}
				seen := map[string]bool{}
				for _, track := range decoded.Items {
					if !track.Present || seen[track.TrackID] {
						b.Fatal("queue duplicates or missing track")
					}
					seen[track.TrackID] = true
				}
			}
			return ms, dbMS
		}
		var cold float64
		queries := tracer.collect(func() { scaleColdConnections(db); cold, _ = measured(true) })
		if err := scaleSavePlans(ctx, db, queries, filepath.Join(output, item.PlanFile)); err != nil {
			b.Fatal(err)
		}
		for i := 0; i < 5; i++ {
			measured(false)
		}
		values, dbValues := make([]float64, 30), make([]float64, 30)
		for i := range values {
			values[i], dbValues[i] = measured(false)
		}
		item.Total, item.MissingTotal, item.Returned = decoded.Total, decoded.MissingTotal, decoded.Returned
		item.Latency, item.DatabaseMS = scaleStats(cold, values, 0), scaleDatabaseStats(dbValues)
		reports = append(reports, item)
		persist()
		cancelMS, status, err := scaleCancelArtistQueue(ctx, db, handler, token, path)
		reports[len(reports)-1].CancelMS, reports[len(reports)-1].CancelStatus = cancelMS, status
		persist()
		if err != nil {
			b.Fatal(err)
		}
	}
	return reports
}

// 在真实候选查询到达 PostgreSQL 锁等待后取消；不以请求前已取消代替在途取消。
func scaleCancelArtistQueue(ctx context.Context, db *sql.DB, handler http.Handler, token, path string) (float64, int, error) {
	blocker, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer blocker.Rollback()
	if _, err := blocker.ExecContext(ctx, `LOCK TABLE artist_effective_credits IN ACCESS EXCLUSIVE MODE`); err != nil {
		return 0, 0, err
	}
	watch := &scaleQueueCancelWatch{started: make(chan uint32, 1)}
	requestCtx, cancel := context.WithCancel(context.WithValue(ctx, scaleQueueCancelKey{}, watch))
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, path, nil).WithContext(requestCtx)
	request.Host = "roomusic.test"
	request.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: token})
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { response := httptest.NewRecorder(); handler.ServeHTTP(response, request); done <- response }()
	waitCtx, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	var pid uint32
	select {
	case pid = <-watch.started:
	case response := <-done:
		return 0, response.Code, fmt.Errorf("queue completed before candidate query")
	case <-waitCtx.Done():
		return 0, 0, waitCtx.Err()
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := db.QueryRowContext(waitCtx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='relation' AND NOT granted)`, pid).Scan(&waiting); err != nil {
			return 0, 0, err
		}
		if waiting {
			break
		}
		select {
		case <-ticker.C:
		case <-waitCtx.Done():
			return 0, 0, waitCtx.Err()
		}
	}
	start := time.Now()
	cancel()
	select {
	case response := <-done:
		elapsed := float64(time.Since(start)) / float64(time.Millisecond)
		if response.Code == 200 || strings.Contains(response.Body.String(), `"items":`) {
			return elapsed, response.Code, fmt.Errorf("canceled queue returned partial success")
		}
		if err := blocker.Rollback(); err != nil {
			return elapsed, response.Code, err
		}
		if err := db.PingContext(waitCtx); err != nil {
			return elapsed, response.Code, err
		}
		return elapsed, response.Code, nil
	case <-waitCtx.Done():
		return float64(time.Since(start)) / float64(time.Millisecond), 0, fmt.Errorf("queue ignored in-flight cancellation: %w", waitCtx.Err())
	}
}

func TestArtistScaleValidation(t *testing.T) {
	values := make([]float64, 30)
	for i := range values {
		values[i] = float64(30 - i)
	}
	stats := scaleStats(70, values, 28)
	if stats.ColdMS != 70 || stats.Samples != 30 || stats.P95MS != 29 || stats.MedianMS != 15.5 || stats.Pass {
		t.Fatalf("p95 gate: %+v", stats)
	}
	ready := catalog.CatalogState{Epoch: 1, SearchMode: "normalized", ArtistsState: "ready"}
	suggestions := catalog.SearchSuggestions{CatalogState: ready, AvailableTypes: []string{"artists", "albums", "tracks"}, Artists: catalog.SearchSuggestionSection[catalog.SearchArtistItem]{Items: []catalog.SearchArtistItem{}}, Albums: catalog.SearchSuggestionSection[catalog.SearchAlbumItem]{Items: []catalog.SearchAlbumItem{}}, Tracks: catalog.SearchSuggestionSection[catalog.SearchTrackItem]{Items: []catalog.SearchTrackItem{}}}
	body, err := json.Marshal(suggestions)
	if err != nil {
		t.Fatal(err)
	}
	if err := scaleValidateSuggestions(body); err != nil {
		t.Fatal(err)
	}
	suggestions.Artists.Items = nil
	body, _ = json.Marshal(suggestions)
	if err := scaleValidateSuggestions(body); err == nil {
		t.Fatal("missing artist section accepted")
	}
	for _, tc := range []struct {
		q        string
		total    int64
		relation string
		valid    bool
	}{
		{"boundary999", 999, "eq", true}, {"boundary1000", 1000, "eq", true}, {"boundary1001", 1000, "gte", true},
		{"boundary1001", 1000, "eq", false}, {"boundary999", 998, "eq", false},
	} {
		result := catalog.SearchResult{Type: "tracks", CatalogState: ready, Items: make([]catalog.SearchTrackItem, 50), Pagination: catalog.SearchPagination{Page: 1, PageSize: 50, Total: tc.total, TotalRelation: tc.relation, HasMore: true}}
		body, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if err := scaleValidateSearch(tc.q, "tracks", body); (err == nil) != tc.valid {
			t.Fatalf("%s valid=%v err=%v", tc.q, tc.valid, err)
		}
	}
}
