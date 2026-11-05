package artistgraph_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/artistgraph"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/writegate"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
)

type backfillSQLKey struct{}
type backfillSQLQuery struct {
	SQL    string `json:"sql"`
	SHA256 string `json:"sha256"`
	Args   []any  `json:"args"`
}
type backfillSQLTrace struct {
	mu      sync.Mutex
	queries []backfillSQLQuery
}

func (tr *backfillSQLTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if ctx.Value(backfillSQLKey{}) == nil {
		return ctx
	}
	args := []any{}
	for _, arg := range data.Args {
		switch arg.(type) {
		case pgx.QueryExecMode, pgx.QueryResultFormats, pgx.QueryResultFormatsByOID:
			continue
		}
		args = append(args, arg)
	}
	sum := sha256.Sum256([]byte(data.SQL))
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.queries = append(tr.queries, backfillSQLQuery{data.SQL, hex.EncodeToString(sum[:]), args})
	return ctx
}
func (*backfillSQLTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// 只用于 SQL 计划诊断，不是正式回填：使用完整规模与真实名称路由，集合预置事实以避开数小时旧回填。
// 正式 benchmark 仍须从空派生调用生产 worker，本测试不能作为回填时间或在线 CATP 达标声明。
func BenchmarkPostgreSQLArtistBackfillSQL(t *testing.B) {
	output := os.Getenv("ROOMUSIC_ARTIST_SQL_DIAGNOSTIC_DIR")
	if output == "" {
		t.Skip("仅显式指定仓外目录时执行完整规模 SQL 诊断")
	}
	if !filepath.IsAbs(output) {
		t.Fatal("诊断目录必须是绝对路径")
	}
	if err := os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	url := postgrestest.NewSchema(t, "artist_sql_diagnostic_")
	migrated, err := postgres.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	migrated.Close()
	config, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	trace := &backfillSQLTrace{}
	config.Tracer = trace
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() { db.Close() })
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name string, v any) {
		t.Helper()
		data, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(output, name), append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	fixture, err := os.ReadFile("../../../app/testdata/search-scale.sql")
	if err != nil {
		t.Fatal(err)
	}
	hashes := map[string]string{}
	for _, name := range []string{"refresh.go", "facts.go", "profiles.go", "names.go", "backfill_sql_diagnostic_test.go"} {
		path := name
		// 用 Go overlay 重放已核对 SHA 的旧源码时，运行期读取同一份源码元数据。
		if dir := os.Getenv("ROOMUSIC_ARTIST_SQL_DIAGNOSTIC_SOURCE_DIR"); dir != "" && name != "backfill_sql_diagnostic_test.go" {
			path = filepath.Join(dir, name)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		hashes[name] = hex.EncodeToString(sum[:])
	}
	sum := sha256.Sum256(fixture)
	hashes["search-scale.sql"] = hex.EncodeToString(sum[:])
	write("source-sha256.json", hashes)
	exec(string(fixture))
	// 先让真实图解析创建夹具唯一未预置的作曲家及其规范化键，不复制生产归一算法。
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = writegate.Lock(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err = artistgraph.Refresh(ctx, tx, []string{"30000000-0000-4000-8000-000000000001"}); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	// 首次刷新可能按展示顺序重排 track.artist；还原此发行原串，使集合构造与第一次真实扫描的 position 相同。
	if _, err = tx.ExecContext(ctx, `UPDATE tracks SET artist=CASE WHEN position%10=0 THEN 'Teresa Teng' ELSE '周杰倫' END||' / Artist '||lpad((1+(position+9999)%20000)::text,5,'0') WHERE medium_id='40000000-0000-4000-8000-000000000001'`); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	exec(`DELETE FROM artist_credits`)
	exec(`INSERT INTO artist_credits(id,release_id,track_id,role,name_id,position,layer)
 WITH raw AS (
 SELECT r.id AS release_id,NULL::uuid AS track_id,v.role,v.name,1::int AS position FROM releases r
 CROSS JOIN LATERAL(VALUES('release_artist',r.artist),('album_artist',r.album_artist)) v(role,name)
 UNION ALL SELECT NULL,t.id,'track_artist',v.name,v.position::int FROM tracks t
 CROSS JOIN LATERAL unnest(string_to_array(t.artist,' / ')) WITH ORDINALITY v(name,position)
 UNION ALL SELECT NULL,c.track_id,c.role,c.name,1 FROM track_credits c
 ) SELECT gen_random_uuid(),r.release_id,r.track_id,r.role,n.id,r.position,'scan' FROM raw r JOIN artist_names n ON n.spelling=r.name`)
	exec(`INSERT INTO artist_effective_credits(credit_id,release_id,track_id,role,name_id,artist_id,position)
 SELECT c.id,c.release_id,c.track_id,c.role,c.name_id,k.initial_artist_id,c.position FROM artist_credits c
 JOIN artist_names n ON n.id=c.name_id JOIN artist_name_keys k ON k.id=n.key_id`)
	counts := map[string]int64{}
	for _, check := range []struct {
		name, sql string
		want      int64
	}{
		{"present", `SELECT count(*) FROM tracks WHERE source_status='present'`, 100000},
		{"missing", `SELECT count(*) FROM tracks WHERE source_status='missing'`, 10000},
		{"releases", `SELECT count(*) FROM releases`, 10000},
		{"groups", `SELECT count(*) FROM release_groups`, 8000},
		{"facts", `SELECT count(*) FROM artist_credits`, 680000},
		{"effective", `SELECT count(*) FROM artist_effective_credits`, 680000},
		{"two_artists", `SELECT count(*) FROM (SELECT track_id FROM artist_credits WHERE role='track_artist' GROUP BY track_id HAVING count(*)=2) s`, 110000},
		{"four_credits", `SELECT count(*) FROM (SELECT track_id FROM artist_credits WHERE role IN ('composer','conductor','performer','producer') GROUP BY track_id HAVING count(*)=4) s`, 110000},
		{"participating_pool", `SELECT count(DISTINCT e.artist_id) FROM artist_effective_credits e JOIN tracks t ON t.id=e.track_id WHERE t.source_status='present' AND e.artist_id::text LIKE '60000000-%'`, 20000},
		{"pool_aliases", `SELECT count(*) FROM artist_name_keys WHERE id::text LIKE '62000000-%' AND initial_artist_id::text LIKE '60000000-%'`, 4000},
		{"popular_tracks", `SELECT count(DISTINCT t.id) FROM artist_effective_credits e JOIN tracks t ON t.id=e.track_id WHERE e.artist_id='a0000000-0000-4000-8000-000000000001' AND t.source_status='present'`, 50000},
	} {
		var n int64
		if err = db.QueryRowContext(ctx, check.sql).Scan(&n); err != nil {
			t.Fatal(err)
		}
		counts[check.name] = n
		if n != check.want {
			t.Fatalf("诊断夹具 %s=%d，预期 %d", check.name, n, check.want)
		}
	}
	write("fixture-counts.json", counts)
	if os.Getenv("ROOMUSIC_ARTIST_SQL_DIAGNOSTIC_INDEX") == "1" {
		exec(`CREATE INDEX IF NOT EXISTS artist_effective_backfill_idx ON artist_effective_credits(artist_id,name_id,track_id,release_id)`)
	}
	// 0029 落地后默认走生产迁移；仍记录完整规模索引大小，不把迁移误记成临时候选。
	var indexBytes int64
	if err = db.QueryRowContext(ctx, `SELECT COALESCE(pg_relation_size(to_regclass('artist_effective_backfill_idx')),0)`).Scan(&indexBytes); err != nil {
		t.Fatal(err)
	}
	write("index.json", map[string]any{"name": "artist_effective_backfill_idx", "bytes": indexBytes})
	postgrestest.AnalyzeSchema(t, ctx, db)
	summaries := []map[string]any{}
	for _, sample := range []struct{ first, size int }{{1, 1}, {10, 1}, {6000, 1}, {9901, 1}, {101, 10}, {6101, 10}, {9911, 10}} {
		ids := make([]string, sample.size)
		for i := range ids {
			ids[i] = fmt.Sprintf("30000000-0000-4000-8000-%012d", sample.first+i)
		}
		label := fmt.Sprintf("release-%d-batch-%d", sample.first, sample.size)
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = writegate.Lock(ctx, tx); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		// 真实刷新前后按业务字段做双向集合比较，不把随机 credit UUID 当作语义。
		const snapshot = `SELECT jsonb_agg(jsonb_build_array(c.release_id,c.track_id,c.role,c.name_id,c.position,c.layer) ORDER BY c.release_id,c.track_id,c.role,c.name_id)::text
 FROM artist_credits c WHERE c.release_id=ANY($1::uuid[]) OR c.track_id IN (SELECT t.id FROM tracks t JOIN media m ON m.id=t.medium_id WHERE m.release_id=ANY($1::uuid[]))`
		const effectiveSnapshot = `SELECT jsonb_agg(jsonb_build_array(c.release_id,c.track_id,c.role,c.name_id,c.artist_id,c.position) ORDER BY c.release_id,c.track_id,c.role,c.name_id)::text
 FROM artist_effective_credits c WHERE c.release_id=ANY($1::uuid[]) OR c.track_id IN (SELECT t.id FROM tracks t JOIN media m ON m.id=t.medium_id WHERE m.release_id=ANY($1::uuid[]))`
		var before, after, beforeEffective, afterEffective string
		if err = tx.QueryRowContext(ctx, snapshot, ids).Scan(&before); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.QueryRowContext(ctx, effectiveSnapshot, ids).Scan(&beforeEffective); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		trace.queries = nil
		started := time.Now()
		if err = artistgraph.Refresh(context.WithValue(ctx, backfillSQLKey{}, true), tx, ids); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		elapsed := time.Since(started)
		if err = tx.QueryRowContext(ctx, snapshot, ids).Scan(&after); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.QueryRowContext(ctx, effectiveSnapshot, ids).Scan(&afterEffective); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if before != after || beforeEffective != afterEffective {
			tx.Rollback()
			t.Fatal("集合诊断数据与真实 Refresh 业务投影不等价")
		}
		queries := trace.queries
		if err = tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		write(label+"-production-queries.json", queries)
		tx, err = db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = writegate.Lock(ctx, tx); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		plans := []map[string]any{}
		for i, q := range queries {
			// 顺序重放原事务语句，使 INSERT 的前置 DELETE、facts 参数及临时创建身份完全一致。
			if strings.Contains(q.SQL, "artist_effective_credits") || strings.Contains(q.SQL, "WITH votes AS") || strings.Contains(q.SQL, "WITH owned AS") || strings.Contains(q.SQL, "DELETE FROM artist_credits") {
				var plan json.RawMessage
				if err = tx.QueryRowContext(ctx, "EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) "+q.SQL, q.Args...).Scan(&plan); err != nil {
					tx.Rollback()
					t.Fatalf("explain %d: %v", i, err)
				}
				plans = append(plans, map[string]any{"sequence": i, "query": q, "plan": plan})
			} else {
				if _, err = tx.ExecContext(ctx, q.SQL, q.Args...); err != nil {
					tx.Rollback()
					t.Fatalf("replay %d: %v", i, err)
				}
			}
		}
		if err = tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		write(label+"-plans.json", plans)
		// 另执行一次真实提交，分别保存业务执行、COMMIT 与完整持锁时间，不能用回滚样本冒充提交时延。
		tx, err = db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = writegate.Lock(ctx, tx); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		held := time.Now()
		if err = artistgraph.Refresh(ctx, tx, ids); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		executed := time.Now()
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		committed := time.Now()
		summaries = append(summaries, map[string]any{"release_ids": ids, "refresh_ms": float64(elapsed) / float64(time.Millisecond), "queries": len(queries), "facts_and_effective_equal": true,
			"committed_execution_ms": float64(executed.Sub(held)) / float64(time.Millisecond), "commit_ms": float64(committed.Sub(executed)) / float64(time.Millisecond), "catp_hold_ms": float64(committed.Sub(held)) / float64(time.Millisecond)})
		write("summary.json", summaries)
		t.Logf("%s Refresh=%s statements=%d", label, elapsed, len(queries))
	}
}
