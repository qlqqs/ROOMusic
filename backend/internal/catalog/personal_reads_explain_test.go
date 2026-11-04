package catalog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPostgreSQLPersonalCatalogExplain 是显式启用的诊断，不给普通测试附加十万行夹具。
// 必须由调用方提供隔离库；只执行真实读取 SQL，不调低 JIT 或禁用顺序扫描来制造结果。
func TestPostgreSQLPersonalCatalogExplain(t *testing.T) {
	if os.Getenv("ROOMUSIC_CATALOG_EXPLAIN") != "1" {
		t.Skip("set ROOMUSIC_CATALOG_EXPLAIN=1 to measure the isolated catalog fixture")
	}
	f := newPlacementFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var version string
	if err := f.database.QueryRowContext(ctx, `SELECT version()`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	// 前 1,000 首共用一个发行；接下来 1,000 首各占一个发行，其余 98,000 首缺失。
	f.exec(`INSERT INTO release_groups(id)
	 SELECT md5('personal-explain-group-'||i)::uuid FROM generate_series(0,1000) i`)
	f.exec(`INSERT INTO releases(id,group_id,title,artist,album_artist,year,source_root_id,relative_directory,candidate_anchor,candidate_kind)
	 SELECT md5('personal-explain-release-'||i)::uuid,md5('personal-explain-group-'||i)::uuid,
	 '合成发行'||i,'合成艺人','合成专辑艺人',1980+i%40,$1::uuid,'explain-'||i,'explain:'||i,'ordinary_directory'
	 FROM generate_series(0,1000) i`, f.rootID)
	f.exec(`INSERT INTO media(id,release_id,position,title)
	 SELECT md5('personal-explain-medium-'||i)::uuid,md5('personal-explain-release-'||i)::uuid,1,'合成介质'
	 FROM generate_series(0,1000) i`)
	f.exec(`INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,duration_seconds)
	 SELECT md5('personal-explain-track-'||i)::uuid,
	 md5('personal-explain-medium-'||(CASE WHEN i<=1000 THEN 0 ELSE (i-1001)%1000+1 END))::uuid,
	 i,'合成曲目'||i,'合成艺人',1,$1::uuid,'explain-'||i||'.flac',
	 CASE WHEN i<=2000 THEN 'present' ELSE 'missing' END,NOW(),'physical',
	 CASE WHEN i%10=0 THEN NULL ELSE 240.5 END
	 FROM generate_series(1,100000) i`, f.rootID)
	f.exec(`INSERT INTO release_artworks(release_id,content_hash,mime_type,width,height,storage_key,source_type)
	 SELECT md5('personal-explain-release-'||i)::uuid,repeat('a',64),'image/jpeg',600,600,'safe-'||i||'.jpg','folder'
	 FROM generate_series(0,1000,2) i`)
	for _, table := range []string{"tracks", "media", "releases", "release_groups", "release_artworks", "release_corrections", "release_issue_reviews", "release_field_decisions", "release_grouping_evidence"} {
		f.exec("ANALYZE " + table)
	}
	output := os.Getenv("ROOMUSIC_CATALOG_EXPLAIN_DIR")
	if output == "" {
		output = t.TempDir()
	}
	if err := os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	t.Logf("PostgreSQL=%s; tracks=100000 present=2000 missing=98000 releases=1001 groups=1001 artwork=501; plans=%s", version, output)

	for _, test := range []struct {
		name, sql, idsSQL string
		rows              int
	}{
		{"facts-10000", loadTrackFactsSQL, `SELECT md5('personal-explain-track-'||i)::uuid::text FROM generate_series(1,10000) i`, 10000},
		{"groups-80", loadPersonalReleaseGroupSummariesSQL, `SELECT md5('personal-explain-group-'||i)::uuid::text FROM generate_series(0,79) i`, 80},
		{"summaries-shared-1000", loadTrackSummariesSQL, `SELECT md5('personal-explain-track-'||i)::uuid::text FROM generate_series(1,1000) i`, 1000},
		{"summaries-distinct-1000", loadTrackSummariesSQL, `SELECT md5('personal-explain-track-'||i)::uuid::text FROM generate_series(1001,2000) i`, 1000},
	} {
		t.Run(test.name, func(t *testing.T) {
			rows, err := f.database.QueryContext(ctx, test.idsSQL)
			if err != nil {
				t.Fatal(err)
			}
			ids := make([]string, 0, test.rows)
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				ids = append(ids, id)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				t.Fatal(err)
			}
			for _, pass := range []string{"first", "warm"} {
				var raw []byte
				start := time.Now()
				if err := f.database.QueryRowContext(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+test.sql, ids).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				elapsed := time.Since(start)
				var plans []map[string]any
				if err := json.Unmarshal(raw, &plans); err != nil || len(plans) != 1 {
					t.Fatalf("invalid plan: %v", err)
				}
				plan := plans[0]["Plan"].(map[string]any)
				if plan["Actual Rows"] != float64(test.rows) {
					t.Fatalf("wrong result cardinality: %v", plan["Actual Rows"])
				}
				file := filepath.Join(output, test.name+"-"+pass+".json")
				if err := os.WriteFile(file, raw, 0600); err != nil {
					t.Fatal(err)
				}
				t.Logf("%s planning_ms=%v execution_ms=%v wall_ms=%.3f rows=%v shared_hit=%v shared_read=%v temp_read=%v temp_written=%v JIT=%v plan=%s",
					pass, plans[0]["Planning Time"], plans[0]["Execution Time"], float64(elapsed.Microseconds())/1000,
					plan["Actual Rows"], plan["Shared Hit Blocks"], plan["Shared Read Blocks"], plan["Temp Read Blocks"], plan["Temp Written Blocks"], plans[0]["JIT"], file)
			}
		})
	}
}

// TestPostgreSQLPersonalCatalogLargeReleaseExplain 补测超大单发行，避免把一千轨发行当作十万轨发行的性能证据。
func TestPostgreSQLPersonalCatalogLargeReleaseExplain(t *testing.T) {
	if os.Getenv("ROOMUSIC_CATALOG_EXPLAIN") != "1" {
		t.Skip("set ROOMUSIC_CATALOG_EXPLAIN=1 to measure the isolated large release")
	}
	f := newPlacementFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	r := f.release("十万轨单发行")
	f.exec(`INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,duration_seconds)
	 SELECT md5('personal-huge-track-'||i)::uuid,$1::uuid,i,'合成曲目'||i,'合成艺人',1,$2::uuid,'huge-'||i||'.flac',
	 CASE WHEN i%5=0 THEN 'present' ELSE 'missing' END,NOW(),'physical',240.5
	 FROM generate_series(1,100000) i`, r.mediumID, f.rootID)
	f.exec(`INSERT INTO release_groups(id) SELECT md5('personal-huge-empty-group-'||i)::uuid FROM generate_series(1,1000) i`)
	for _, table := range []string{"tracks", "media", "releases", "release_groups"} {
		f.exec("ANALYZE " + table)
	}
	ids := []string{}
	rows, err := f.database.QueryContext(ctx, `SELECT md5('personal-huge-track-'||(i*5))::uuid::text FROM generate_series(1,1000) i`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	output := os.Getenv("ROOMUSIC_CATALOG_EXPLAIN_DIR")
	if output == "" {
		output = t.TempDir()
	}
	if err := os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	t.Log("fixture: tracks=100000 present=20000 missing=80000 releases=1 media=1; requested summaries=1000 present tracks; review table empty")
	runPlans := func(stage string) {
		for _, test := range []struct {
			name, sql string
			ids       []string
			rows      int
		}{
			{"summaries-single-release-100000", loadTrackSummariesSQL, ids, 1000},
			{"group-single-release-100000", loadPersonalReleaseGroupSummariesSQL, []string{r.groupID}, 1},
		} {
			t.Run(stage+"/"+test.name, func(t *testing.T) {
				for _, pass := range []string{"first", "warm"} {
					var raw []byte
					start := time.Now()
					if err := f.database.QueryRowContext(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+test.sql, test.ids).Scan(&raw); err != nil {
						t.Fatal(err)
					}
					var plans []map[string]any
					if err := json.Unmarshal(raw, &plans); err != nil || len(plans) != 1 {
						t.Fatalf("invalid plan: %v", err)
					}
					plan := plans[0]["Plan"].(map[string]any)
					if plan["Actual Rows"] != float64(test.rows) {
						t.Fatalf("wrong rows: %v", plan["Actual Rows"])
					}
					file := filepath.Join(output, stage+"-"+test.name+"-"+pass+".json")
					if err := os.WriteFile(file, raw, 0600); err != nil {
						t.Fatal(err)
					}
					t.Logf("%s planning_ms=%v execution_ms=%v wall_ms=%.3f shared_hit=%v shared_read=%v temp_read=%v temp_written=%v cost=%v JIT=%v plan=%s",
						pass, plans[0]["Planning Time"], plans[0]["Execution Time"], float64(time.Since(start).Microseconds())/1000,
						plan["Shared Hit Blocks"], plan["Shared Read Blocks"], plan["Temp Read Blocks"], plan["Temp Written Blocks"], plan["Total Cost"], plans[0]["JIT"], file)
				}
			})
		}
	}
	runPlans("default-stats")
	for _, table := range []string{"release_artworks", "release_corrections", "release_issue_reviews", "release_field_decisions", "release_grouping_evidence"} {
		f.exec("ANALYZE " + table)
	}
	runPlans("analyzed")

}
