package personal

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

type measuredCatalog struct {
	Catalog
	facts, summaries, groups, statistics *int
}

func (c measuredCatalog) LoadTrackFacts(ctx context.Context, ids []string) (map[string]catalog.TrackFact, error) {
	*c.facts++
	return c.Catalog.LoadTrackFacts(ctx, ids)
}
func (c measuredCatalog) LoadTrackCollectionStats(ctx context.Context, ids []string) (catalog.TrackCollectionStats, error) {
	*c.statistics++
	return c.Catalog.LoadTrackCollectionStats(ctx, ids)
}
func (c measuredCatalog) LoadTrackSummaries(ctx context.Context, ids []string) (map[string]catalog.TrackSummary, error) {
	*c.summaries++
	return c.Catalog.LoadTrackSummaries(ctx, ids)
}
func (c measuredCatalog) LoadReleaseGroupSummaries(ctx context.Context, ids []string) (map[string]catalog.PersonalReleaseGroupSummary, error) {
	*c.groups++
	return c.Catalog.LoadReleaseGroupSummaries(ctx, ids)
}

type personalMeasurement struct {
	Name                      string  `json:"name"`
	Samples                   int     `json:"samples"`
	P50MS                     float64 `json:"p50_ms"`
	P95MS                     float64 `json:"p95_ms"`
	MaxResponseBytes          int     `json:"max_response_bytes"`
	MaxAllocatedBytes         uint64  `json:"max_allocated_bytes"`
	MaxSampledHeapGrowthBytes uint64  `json:"max_sampled_heap_growth_bytes"`
	CatalogFactsCalls         int     `json:"catalog_facts_calls"`
	CatalogSummaryCalls       int     `json:"catalog_summary_calls"`
	CatalogGroupCalls         int     `json:"catalog_group_calls"`
	CatalogStatisticsCalls    int     `json:"catalog_statistics_calls"`
}

// TestPostgreSQLPersonalPerformance 在隔离测试库复现实测；普通门禁也保留断言，不以 Skip 充当验证。
func TestPostgreSQLPersonalPerformance(t *testing.T) {
	f := newPersonalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	user, other := f.user(t), f.user(t)
	ids := bulkPersonalTracks(t, f, 100000)
	f.exec(t, `UPDATE tracks SET source_status=CASE WHEN position%10<2 THEN 'present' ELSE 'missing' END,duration_seconds=CASE WHEN position%7=0 THEN NULL ELSE 200 END`)
	f.exec(t, `INSERT INTO personal_favorite_state(user_id,revision) VALUES($1::uuid,1),($2::uuid,1)`, user, other)
	f.exec(t, `INSERT INTO favorite_tracks(user_id,track_id,created_at) SELECT $1::uuid,id,'2026-11-04T00:00:00Z'::timestamptz FROM tracks WHERE position<=20000 UNION ALL SELECT $2::uuid,id,'2026-11-04T00:00:00Z'::timestamptz FROM tracks`, user, other)
	f.exec(t, `INSERT INTO release_groups(id) SELECT md5('personal-perf-group-'||i)::uuid FROM generate_series(1,1000) i`)
	f.exec(t, `INSERT INTO favorite_albums(user_id,group_id) SELECT $1::uuid,md5('personal-perf-group-'||i)::uuid FROM generate_series(1,1000) i`, user)
	f.exec(t, `INSERT INTO personal_playlists(id,user_id,name,track_count,created_at,updated_at)
 SELECT md5('personal-perf-playlist-'||i)::uuid,$1::uuid,'合成歌单'||i,CASE WHEN i<=20 THEN 10000 ELSE 1 END,NOW(),NOW()+CASE WHEN i<=20 THEN interval '1 day' ELSE interval '0 day' END FROM generate_series(1,1000) i`, user)
	f.exec(t, `INSERT INTO personal_playlist_items(id,user_id,playlist_id,track_id,order_key)
 SELECT gen_random_uuid(),$1::uuid,md5('personal-perf-playlist-'||p)::uuid,t.id,t.position::bigint*4294967296 FROM generate_series(1,20) p CROSS JOIN tracks t WHERE t.position<=10000`, user)
	f.exec(t, `INSERT INTO personal_playlist_items(id,user_id,playlist_id,track_id,order_key)
 SELECT gen_random_uuid(),$1::uuid,md5('personal-perf-playlist-'||p)::uuid,$2::uuid,4294967296 FROM generate_series(21,1000) p`, user, ids[0])
	f.exec(t, `INSERT INTO personal_playlists(id,user_id,name) SELECT gen_random_uuid(),$1::uuid,'其他用户' FROM generate_series(1,1000)`, other)
	for _, table := range []string{"tracks", "media", "releases", "release_groups", "favorite_tracks", "favorite_albums", "personal_playlists", "personal_playlist_items", "personal_playlist_requests", "personal_playlist_undo"} {
		f.exec(t, "ANALYZE "+table)
	}
	var version, playlist string
	f.database.QueryRow(`SELECT version()`).Scan(&version)
	f.database.QueryRow(`SELECT md5('personal-perf-playlist-1')::uuid::text`).Scan(&playlist)
	facts, summaries, groups, statistics := 0, 0, 0, 0
	f.service.catalogFor = func(tx *sql.Tx) Catalog {
		return measuredCatalog{catalog.NewStore(tx), &facts, &summaries, &groups, &statistics}
	}
	report := struct {
		Version      string                `json:"postgresql"`
		Fixture      string                `json:"fixture"`
		Plans        map[string]any        `json:"plans"`
		Measurements []personalMeasurement `json:"measurements"`
		ReceiptBytes int                   `json:"receipt_bytes"`
	}{Version: version, Fixture: "100000曲目（20000 present/80000 missing）、同时间收藏、两用户、本人1000歌单、20张每张10000首、1000不可用组；极端阶段本人100000收藏；超限阶段20张每张10002首", Plans: map[string]any{}}
	measure := func(name string, call func() any) {
		t.Helper()
		var m personalMeasurement
		m.Name = name
		m.Samples = 1
		if os.Getenv("ROOMUSIC_PERSONAL_EXPLAIN_DIR") != "" {
			m.Samples = 5
		}
		times := []float64{}
		for i := 0; i < m.Samples; i++ {
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			var peak atomic.Uint64
			peak.Store(before.HeapAlloc)
			done := make(chan struct{})
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				ticker := time.NewTicker(5 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-done:
						return
					case <-ticker.C:
						var sample runtime.MemStats
						runtime.ReadMemStats(&sample)
						if sample.HeapAlloc > peak.Load() {
							peak.Store(sample.HeapAlloc)
						}
					}
				}
			}()
			facts, summaries, groups, statistics = 0, 0, 0, 0
			start := time.Now()
			body := call()
			encoded, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			elapsed := time.Since(start)
			close(done)
			<-finished
			runtime.ReadMemStats(&after)
			times = append(times, float64(elapsed)/float64(time.Millisecond))
			m.MaxResponseBytes = max(m.MaxResponseBytes, len(encoded))
			m.MaxAllocatedBytes = max(m.MaxAllocatedBytes, after.TotalAlloc-before.TotalAlloc)
			if peak.Load() > before.HeapAlloc {
				m.MaxSampledHeapGrowthBytes = max(m.MaxSampledHeapGrowthBytes, peak.Load()-before.HeapAlloc)
			}
			m.CatalogFactsCalls = max(m.CatalogFactsCalls, facts)
			m.CatalogSummaryCalls = max(m.CatalogSummaryCalls, summaries)
			m.CatalogGroupCalls = max(m.CatalogGroupCalls, groups)
			m.CatalogStatisticsCalls = max(m.CatalogStatisticsCalls, statistics)
		}
		sort.Float64s(times)
		m.P50MS = times[len(times)/2]
		m.P95MS = times[len(times)-1]
		report.Measurements = append(report.Measurements, m)
		t.Logf("%s p50=%.2fms p95=%.2fms bytes=%d alloc=%d heap_growth=%d catalog=%d/%d/%d", name, m.P50MS, m.P95MS, m.MaxResponseBytes, m.MaxAllocatedBytes, m.MaxSampledHeapGrowthBytes, m.CatalogFactsCalls, m.CatalogSummaryCalls, m.CatalogGroupCalls)
	}
	measure("cards-20x10000", func() any {
		v, err := f.service.Playlists(ctx, user, Page{Number: 1, Size: 20})
		if err != nil {
			t.Fatal(err)
		}
		if len(v.Items) != 20 || v.Items[0].TrackCount != 10000 {
			t.Fatal("card fixture")
		}
		return v
	})
	measure("favorites-20000-queue", func() any {
		v, err := f.service.FavoriteQueue(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		if v.Total != 20000 || v.EligibleCount != 4000 {
			t.Fatal("queue counts")
		}
		return v
	})
	measure("playlist-10000-queue", func() any {
		v, err := f.service.PlaylistQueue(ctx, user, playlist)
		if err != nil {
			t.Fatal(err)
		}
		if v.Total != 10000 || v.MissingCount != 8000 {
			t.Fatal("playlist counts")
		}
		return v
	})
	type explainCase struct {
		name, query string
		args        []any
	}
	cases := []explainCase{
		{"favorites-first", `SELECT track_id::text,created_at FROM favorite_tracks WHERE user_id=$1::uuid ORDER BY created_at DESC,favorite_tracks.track_id ASC LIMIT 50 OFFSET $2`, []any{user, 0}},
		{"favorites-middle", `SELECT track_id::text,created_at FROM favorite_tracks WHERE user_id=$1::uuid ORDER BY created_at DESC,favorite_tracks.track_id ASC LIMIT 50 OFFSET $2`, []any{user, 10000}},
		{"favorites-total", `SELECT count(*) FROM favorite_tracks WHERE user_id=$1::uuid`, []any{user}},
		{"favorites-keyset-same-time", `SELECT track_id::text,created_at FROM favorite_tracks WHERE user_id=$1::uuid AND created_at=$2 AND track_id>$3::uuid ORDER BY favorite_tracks.track_id ASC LIMIT $4`, []any{user, time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC), ids[9999], 10000}},
		{"favorites-keyset-older-time", `SELECT track_id::text,created_at FROM favorite_tracks WHERE user_id=$1::uuid AND created_at<$2 ORDER BY created_at DESC,favorite_tracks.track_id ASC LIMIT $3`, []any{user, time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC), 10000}},
		{"lookup-10000", `SELECT track_id::text FROM favorite_tracks WHERE user_id=$1::uuid AND track_id=ANY($2::text[]::uuid[])`, []any{user, ids[:10000]}},
		{"playlist-list", `SELECT ` + playlistColumns + ` FROM personal_playlists WHERE user_id=$1::uuid ORDER BY updated_at DESC,personal_playlists.id ASC LIMIT $2 OFFSET $3`, []any{user, 20, 0}},
		{"playlist-quota", `SELECT count(*) FROM personal_playlists WHERE user_id=$1::uuid`, []any{user}},
		{"playlist-items", `SELECT id::text,track_id::text,added_at FROM personal_playlist_items WHERE user_id=$1::uuid AND playlist_id=$2::uuid ORDER BY order_key ASC LIMIT $3 OFFSET $4`, []any{user, playlist, 51, 5000}},
		{"playlist-membership", `SELECT p.id::text,p.revision FROM personal_playlist_items i JOIN personal_playlists p ON p.user_id=i.user_id AND p.id=i.playlist_id WHERE i.user_id=$1::uuid AND p.user_id=$1::uuid AND i.track_id=$2::uuid ORDER BY p.id ASC LIMIT 1000`, []any{user, ids[0]}},
		{"playlist-existing", `SELECT track_id::text FROM personal_playlist_items WHERE user_id=$1::uuid AND playlist_id=$2::uuid AND track_id=ANY($3::text[]::uuid[])`, []any{user, playlist, ids[:10000]}},
		{"playlist-neighbor", `SELECT ` + itemColumns + ` FROM personal_playlist_items WHERE user_id=$1::uuid AND playlist_id=$2::uuid AND id<>$3::uuid AND order_key<$4 ORDER BY order_key DESC LIMIT 1`, []any{user, playlist, "00000000-0000-0000-0000-000000000000", int64(5000) * orderStep}},
	}
	explain := func(c explainCase) {
		t.Helper()
		for _, pass := range []string{"first", "warm"} {
			tx, err := f.database.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			var raw []byte
			err = tx.QueryRowContext(ctx, `EXPLAIN (ANALYZE, BUFFERS, WAL, FORMAT JSON) `+c.query, c.args...).Scan(&raw)
			tx.Rollback()
			if err != nil {
				t.Fatalf("explain %s: %v", c.name, err)
			}
			var plan any
			if err = json.Unmarshal(raw, &plan); err != nil {
				t.Fatal(err)
			}
			report.Plans[c.name+"-"+pass] = compactPlan(plan)
		}
	}
	for _, c := range cases {
		explain(c)
	}
	groupIDs := []string{}
	rows, err := f.database.QueryContext(ctx, `SELECT group_id::text FROM favorite_albums WHERE user_id=$1`, user)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		rows.Scan(&id)
		groupIDs = append(groupIDs, id)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	explain(explainCase{"lookup-albums-1000", `SELECT group_id::text FROM favorite_albums WHERE user_id=$1::uuid AND group_id=ANY($2::text[]::uuid[])`, []any{user, groupIDs}})
	playlistIDs := []string{}
	listRows, e := f.database.QueryContext(ctx, `SELECT id::text FROM personal_playlists WHERE user_id=$1 AND track_count=10000 ORDER BY personal_playlists.id`, user)
	if e != nil {
		t.Fatal(e)
	}
	for listRows.Next() {
		var id string
		if e = listRows.Scan(&id); e != nil {
			t.Fatal(e)
		}
		playlistIDs = append(playlistIDs, id)
	}
	if e = listRows.Err(); e != nil {
		t.Fatal(e)
	}
	listRows.Close()
	explain(explainCase{"playlist-card-batch", `SELECT playlist_id::text,track_id::text,order_key FROM personal_playlist_items WHERE user_id=$1::uuid AND playlist_id=ANY($2::text[]::uuid[]) ORDER BY personal_playlist_items.playlist_id ASC,order_key ASC LIMIT 10000`, []any{user, playlistIDs}})
	explain(explainCase{"playlist-queue-batch", `SELECT playlist_id::text,track_id::text,order_key FROM personal_playlist_items WHERE user_id=$1::uuid AND playlist_id=ANY($2::text[]::uuid[]) AND (playlist_id,order_key)>($3::uuid,$4::bigint) ORDER BY personal_playlist_items.playlist_id ASC,order_key ASC LIMIT 10000`, []any{user, []string{playlist}, playlist, int64(5000) * orderStep}})
	explain(explainCase{"playlist-lock", `SELECT ` + playlistColumns + ` FROM personal_playlists WHERE user_id=$1::uuid AND id=$2::uuid FOR UPDATE`, []any{user, playlist}})
	refs := playlistRefs(t, f, user, playlist)
	explain(explainCase{"playlist-update-one", `UPDATE personal_playlist_items SET order_key=$4 WHERE user_id=$1::uuid AND playlist_id=$2::uuid AND id=$3::uuid`, []any{user, playlist, refs[0].ID, int64(-orderStep)}})
	// 合法撤销收据与定位：SQL ANALYZE 的写试验均回滚。
	removed := executePersonal[RemoveResult](t, f, user, Command{Kind: "remove_item", PlaylistID: playlist, ItemID: refs[1].ID, ExpectedRevision: 1})
	f.database.QueryRow(`SELECT octet_length(response_body::text) FROM personal_playlist_requests WHERE user_id=$1 ORDER BY created_at DESC LIMIT 1`, user).Scan(&report.ReceiptBytes)
	explain(explainCase{"undo-token", `SELECT item_id::text,track_id::text,order_key,added_at,original_position,previous_track_id::text,removal_revision,restore_request_key::text FROM personal_playlist_undo WHERE user_id=$1::uuid AND playlist_id=$2::uuid AND token=$3::uuid`, []any{user, playlist, removed.Undo.Token}})
	explain(explainCase{"undo-predecessor", `SELECT ` + itemColumns + ` FROM personal_playlist_items WHERE user_id=$1::uuid AND playlist_id=$2::uuid AND track_id=$3::uuid`, []any{user, playlist, ids[0]}})
	explain(explainCase{"undo-original-index", `SELECT id::text FROM personal_playlist_items WHERE user_id=$1::uuid AND playlist_id=$2::uuid ORDER BY order_key LIMIT 1 OFFSET $3`, []any{user, playlist, 4999}})
	var receiptKey string
	if err = f.database.QueryRowContext(ctx, `SELECT idempotency_key::text FROM personal_playlist_requests WHERE user_id=$1 ORDER BY created_at DESC LIMIT 1`, user).Scan(&receiptKey); err != nil {
		t.Fatal(err)
	}
	explain(explainCase{"receipt-replay", `SELECT request_hash,response_status,response_body FROM personal_playlist_requests WHERE user_id=$1::uuid AND idempotency_key=$2::uuid`, []any{user, receiptKey}})
	executePersonal[RestoreResult](t, f, user, Command{Kind: "restore_item", PlaylistID: playlist, Token: removed.Undo.Token})
	f.exec(t, `INSERT INTO favorite_tracks(user_id,track_id,created_at) SELECT $1::uuid,id,'2026-11-04T00:00:00Z'::timestamptz FROM tracks WHERE position>20000`, user)
	f.exec(t, `ANALYZE favorite_tracks`)
	explain(explainCase{"favorites-deep-100000", cases[0].query, []any{user, 99950}})
	measure("favorites-100000-queue", func() any {
		v, err := f.service.FavoriteQueue(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		if v.Total != 100000 || v.MissingCount != 80000 || v.ReturnedCount != 10000 || v.OmittedCount != 10000 {
			t.Fatalf("extreme counts %+v", v.QueueTracks)
		}
		return v
	})
	// 统计投影用真实超限成员，不截统计；业务上达到该状态由独立恢复测试证明。
	f.exec(t, `INSERT INTO personal_playlist_items(id,user_id,playlist_id,track_id,order_key) SELECT gen_random_uuid(),$1::uuid,md5('personal-perf-playlist-'||p)::uuid,t.id,t.position::bigint*4294967296 FROM generate_series(1,20) p CROSS JOIN tracks t WHERE t.position BETWEEN 10001 AND 10002`, user)
	f.exec(t, `UPDATE personal_playlists SET track_count=10002 WHERE user_id=$1::uuid AND track_count=10000`, user)
	measure("cards-20x10002", func() any {
		v, err := f.service.Playlists(ctx, user, Page{Number: 1, Size: 20})
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range v.Items {
			if p.TrackCount != 10002 || p.OverLimitCount != 2 {
				t.Fatal("overlimit statistics truncated")
			}
		}
		return v
	})
	// 取消后同一服务/连接池可继续读取，不遗留事务。
	canceled, stop := context.WithCancel(ctx)
	stop()
	if _, err := f.service.FavoriteQueue(canceled, user); err == nil {
		t.Fatal("canceled queue succeeded")
	}
	if err = f.database.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	if directory := os.Getenv("ROOMUSIC_PERSONAL_EXPLAIN_DIR"); directory != "" {
		if err = os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(directory, "personal-performance.json"), append(body, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("PostgreSQL=%s；SQL计划=%d；成功移除收据=%d字节", version, len(report.Plans), report.ReceiptBytes)
}

// compactPlan 仅省略大型 IN 字面量，不改计划节点、成本、行数、缓冲、WAL 或 JIT 指标。
func compactPlan(value any) any {
	switch v := value.(type) {
	case map[string]any:
		for k, x := range v {
			v[k] = compactPlan(x)
		}
		return v
	case []any:
		for i, x := range v {
			v[i] = compactPlan(x)
		}
		return v
	case string:
		if len(v) > 600 {
			return fmt.Sprintf("%s…（省略 %d 字节字面量）", v[:150], len(v)-150)
		}
		return v
	default:
		return value
	}
}
