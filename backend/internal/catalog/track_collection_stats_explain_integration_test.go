package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPostgreSQLTrackCollectionStatsExplain 只在显式启用时建立真实形状的独立夹具。
// 完整 HTTP 门槛由 app 验证；这里记录实际目录 SQL 的计划，不把局部耗时当作 HTTP 延迟。
func TestPostgreSQLTrackCollectionStatsExplain(t *testing.T) {
	if os.Getenv("ROOMUSIC_CATALOG_EXPLAIN") != "1" {
		t.Skip("设置 ROOMUSIC_CATALOG_EXPLAIN=1 后运行隔离库诊断")
	}
	f := newPlacementFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := f.database.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}

	// 每发行恰有十首，每第二个发行的末轨 missing，共 5,000 首；前 2,000 组
	// 各有两个发行，其余各一个。元数据辅助表保持空表，不用 ANALYZE 掩盖初始估算。
	exec(`INSERT INTO release_groups(id)
		SELECT ('00000003-0000-4000-8000-'||lpad(to_hex(i),12,'0'))::uuid
		FROM generate_series(1,8000) i`)
	exec(`INSERT INTO releases(id,group_id,title,artist,album_artist,year,source_root_id,relative_directory,candidate_anchor,candidate_kind)
		SELECT ('00000002-0000-4000-8000-'||lpad(to_hex(i),12,'0'))::uuid,
		       ('00000003-0000-4000-8000-'||lpad(to_hex((i-1)%8000+1),12,'0'))::uuid,
		       '合成发行'||i,'合成艺人','合成专辑艺人',1980+i%40,$1::uuid,
		       'realistic-cards-'||i,'realistic-cards:'||i,'ordinary_directory'
		FROM generate_series(1,10000) i`, f.rootID)
	exec(`INSERT INTO media(id,release_id,position,title)
		SELECT ('00000004-0000-4000-8000-'||lpad(to_hex(i),12,'0'))::uuid,
		       ('00000002-0000-4000-8000-'||lpad(to_hex(i),12,'0'))::uuid,1,'合成介质'
		FROM generate_series(1,10000) i`)
	exec(`INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,duration_seconds,codec,bit_depth,sample_rate,channels)
		SELECT ('00000001-0000-4000-8000-'||lpad(to_hex(i),12,'0'))::uuid,
		       ('00000004-0000-4000-8000-'||lpad(to_hex((i-1)/10+1),12,'0'))::uuid,
		       (i-1)%10+1,'合成曲目'||i,'合成艺人',1,$1::uuid,'realistic-cards-'||i||'.flac',
		       CASE WHEN i%20=0 THEN 'missing' ELSE 'present' END,NOW(),'physical',
		       CASE WHEN i%7=0 THEN NULL ELSE 120 END,'flac',24,96000,2
		FROM generate_series(1,100000) i`, f.rootID)
	for _, table := range []string{"tracks", "media", "releases", "release_groups"} {
		exec("ANALYZE " + table)
	}
	var tracks, releases, groups, missing, badReleaseSizes int
	if err := f.database.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM tracks),(SELECT COUNT(*) FROM releases),(SELECT COUNT(*) FROM release_groups),
		(SELECT COUNT(*) FROM tracks WHERE source_status='missing'),
		(SELECT COUNT(*) FROM (SELECT m.release_id FROM media m JOIN tracks t ON t.medium_id=m.id GROUP BY m.release_id HAVING COUNT(*)<>10) invalid_sizes)`).
		Scan(&tracks, &releases, &groups, &missing, &badReleaseSizes); err != nil {
		t.Fatal(err)
	}
	if tracks != 100000 || releases != 10000 || groups != 8000 || missing != 5000 || badReleaseSizes != 0 {
		t.Fatalf("夹具形状错误: tracks=%d releases=%d groups=%d missing=%d bad_release_sizes=%d", tracks, releases, groups, missing, badReleaseSizes)
	}

	ids := make([]string, 10000)
	wantStats := TrackCollectionStats{MatchedCount: 10000, MissingCount: 500}
	for i := range ids {
		track := i*10 + 1
		// 每发行选一首，所选集合也保留 5% missing，避免只测 present 子集。
		if (i+1)%20 == 0 {
			track = (i + 1) * 10
		}
		ids[i] = fmt.Sprintf("00000001-0000-4000-8000-%012x", track)
		if track%7 == 0 {
			wantStats.UnknownDurationCount++
		} else {
			wantStats.KnownDurationSeconds += 120
		}
	}
	groupIDs := make([]string, 80)
	for i := range groupIDs {
		groupIDs[i] = fmt.Sprintf("00000003-0000-4000-8000-%012x", i+1)
	}
	output := os.Getenv("ROOMUSIC_CATALOG_EXPLAIN_DIR")
	if output == "" {
		output = t.TempDir()
	}
	output = filepath.Join(output, "realistic-cards")
	if err := os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}

	// 独占同一连接验证 SET LOCAL 的生命周期，避免用连接池另一个会话冒充恢复证据。
	conn, err := f.database.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var version, baselineJIT string
	if err := conn.QueryRowContext(ctx, `SELECT version(),current_setting('jit')`).Scan(&version, &baselineJIT); err != nil {
		t.Fatal(err)
	}
	if baselineJIT != "on" {
		t.Fatalf("诊断需要数据库默认 jit=on，不主动修改会话或全局设置: jit=%q", baselineJIT)
	}
	t.Logf("PostgreSQL=%s; tracks=%d releases=%d groups=%d missing=%d; 每发行10轨; 首次/热测不清缓存; output=%s", version, tracks, releases, groups, missing, output)
	auxiliary := []string{"release_artworks", "release_corrections", "release_issue_reviews", "release_field_decisions", "release_grouping_evidence"}
	for _, stage := range []string{"default-stats", "analyzed"} {
		for _, table := range auxiliary {
			if stage == "analyzed" {
				if _, err := conn.ExecContext(ctx, "ANALYZE "+table); err != nil {
					t.Fatal(err)
				}
			}
			var count int
			var estimatedRows float64
			if err := conn.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM "+table+"),reltuples FROM pg_class WHERE oid=$1::regclass", table).Scan(&count, &estimatedRows); err != nil {
				t.Fatal(err)
			}
			wantEstimate := float64(-1)
			if stage == "analyzed" {
				wantEstimate = 0
			}
			if count != 0 || estimatedRows != wantEstimate {
				t.Fatalf("%s 辅助表 %s: count=%d reltuples=%v，要求空表估算 %v", stage, table, count, estimatedRows, wantEstimate)
			}
		}
		for _, query := range []struct {
			name, sql string
			ids       []string
			rows      int
			summary   bool
		}{
			{"stats-10000-distinct-releases", loadTrackCollectionStatsSQL, ids, 1, false},
			{"facts-32", loadTrackFactsSQL, ids[:32], 32, false},
			{"facts-10000", loadTrackFactsSQL, ids, 10000, false},
			{"summaries-1000-distinct-releases", loadTrackSummariesSQL, ids[:1000], 1000, true},
			{"groups-80", loadPersonalReleaseGroupSummariesSQL, groupIDs, 80, true},
		} {
			jitModes := []string{"default"}
			if query.summary {
				// 仅摘要增加关闭 JIT 的诊断对照；默认设置的计划仍完整记录，不以对照替代门槛。
				jitModes = append(jitModes, "local-off")
			}
			for _, jitMode := range jitModes {
				t.Run(stage+"/"+query.name+"/"+jitMode, func(t *testing.T) {
					tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					if jitMode == "local-off" {
						if _, err := tx.ExecContext(ctx, `SET LOCAL jit = off`); err != nil {
							t.Fatal(err)
						}
					}
					var currentJIT string
					if err := tx.QueryRowContext(ctx, `SHOW jit`).Scan(&currentJIT); err != nil {
						t.Fatal(err)
					}
					if (jitMode == "default" && currentJIT != baselineJIT) || (jitMode == "local-off" && currentJIT != "off") {
						t.Fatalf("事务内 JIT 设置错误: mode=%s actual=%s", jitMode, currentJIT)
					}
					for _, pass := range []string{"first", "warm"} {
						var raw []byte
						started := time.Now()
						if err := tx.QueryRowContext(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+query.sql, query.ids).Scan(&raw); err != nil {
							t.Fatal(err)
						}
						elapsed := time.Since(started)
						var plans []map[string]any
						if err := json.Unmarshal(raw, &plans); err != nil || len(plans) != 1 {
							t.Fatalf("无效执行计划: %v", err)
						}
						plan, ok := plans[0]["Plan"].(map[string]any)
						if !ok || plan["Actual Rows"] != float64(query.rows) {
							t.Fatalf("计划输出行数错误: got=%v want=%d", plan["Actual Rows"], query.rows)
						}
						file := filepath.Join(output, stage+"-"+query.name+"-"+jitMode+"-"+pass+".json")
						encoded, err := json.MarshalIndent(sanitizeCollectionStatsExplain(plans), "", "  ")
						if err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(file, append(encoded, '\n'), 0600); err != nil {
							t.Fatal(err)
						}
						t.Logf("%s jit_setting=%s planning_ms=%v execution_ms=%v wall_ms=%.3f rows=%v cost=%v shared_hit=%v shared_read=%v temp_read=%v temp_written=%v JIT=%v plan=%s",
							pass, currentJIT, plans[0]["Planning Time"], plans[0]["Execution Time"], float64(elapsed.Microseconds())/1000,
							plan["Actual Rows"], plan["Total Cost"], plan["Shared Hit Blocks"], plan["Shared Read Blocks"], plan["Temp Read Blocks"], plan["Temp Written Blocks"], plans[0]["JIT"], file)
					}
					if query.sql == loadTrackCollectionStatsSQL {
						got, err := NewStore(tx).LoadTrackCollectionStats(ctx, query.ids)
						if err != nil || got != wantStats {
							t.Fatalf("实际聚合结果错误: got=%+v want=%+v err=%v", got, wantStats, err)
						}
					}
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
				})
				var afterJIT string
				if err := conn.QueryRowContext(ctx, `SHOW jit`).Scan(&afterJIT); err != nil {
					t.Fatal(err)
				}
				if afterJIT != baselineJIT {
					t.Fatalf("事务外 JIT 设置泄漏: before=%s after=%s", baselineJIT, afterJIT)
				}
			}
		}
	}
}

// sanitizeCollectionStatsExplain 仅缩短计划中的巨大合成 UUID 数组，保留其他表达式、
// 估算/实际行数、loops、buffers、排序与 JIT 数据；不保存连接 URL 或请求凭据。
func sanitizeCollectionStatsExplain(value any) any {
	switch v := value.(type) {
	case []map[string]any:
		for _, item := range v {
			sanitizeCollectionStatsExplain(item)
		}
	case []any:
		for i, item := range v {
			v[i] = sanitizeCollectionStatsExplain(item)
		}
	case map[string]any:
		for key, item := range v {
			v[key] = sanitizeCollectionStatsExplain(item)
		}
	case string:
		if start, end := strings.Index(v, "'{"), strings.LastIndex(v, "}'"); start >= 0 && end > start && end-start > 600 {
			return v[:start] + "'{[合成 UUID 数组已省略]}'" + v[end+2:]
		}
	}
	return value
}
