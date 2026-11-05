package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
)

// BenchmarkPostgreSQLSearchPlans 仅在显式指定已恢复的合成 schema 时运行，不建库、不回填、不改事实。
// 真实 HTTP 时延仍由 app 的基准测量；这里使用生产构造器及同一 RR/JIT-off 事务定位计划。
func BenchmarkPostgreSQLSearchPlans(b *testing.B) {
	schema := os.Getenv("ROOMUSIC_SEARCH_PLAN_SCHEMA")
	output := os.Getenv("ROOMUSIC_SEARCH_PLAN_OUTPUT")
	if b.N != 1 || !regexp.MustCompile(`^(search_scale_[a-z0-9_]+|i10_search_[a-f0-9]{32})$`).MatchString(schema) || output == "" {
		b.Fatal("必须指定合成 ROOMUSIC_SEARCH_PLAN_SCHEMA、ROOMUSIC_SEARCH_PLAN_OUTPUT，并使用 -benchtime=1x")
	}
	scoped, err := postgrestest.ScopedURL(postgrestest.BaseURL(b), schema)
	if err != nil {
		b.Fatal(err)
	}
	db, err := sql.Open("pgx", scoped)
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	if err := os.MkdirAll(output, 0755); err != nil {
		b.Fatal(err)
	}
	queries := []string{"交响曲 第00999号", "周杰伦", "爱"}
	kinds, operations := []string{"albums", "tracks", "releases"}, []string{"count", "page"}
	exactCountsOnly := os.Getenv("EXACT_COUNTS_ONLY") == "1"
	if exactCountsOnly {
		// 定向对比旧精确计数的生产 SQL，不访问旧路由；禁止额外词或分页混入证据。
		queries, kinds, operations = []string{"周杰伦"}, []string{"albums", "releases"}, []string{"count"}
	} else if raw := os.Getenv("ROOMUSIC_SEARCH_PLAN_QUERIES"); raw != "" {
		queries = strings.Split(raw, "|")
	}
	type record struct {
		Query     string          `json:"query"`
		Kind      string          `json:"kind"`
		Operation string          `json:"operation"`
		SQL       string          `json:"sql"`
		Args      []any           `json:"args"`
		Plan      json.RawMessage `json:"plan"`
		WallMS    float64         `json:"wall_ms"`
	}
	records := []record{}
	for _, query := range queries {
		for _, kind := range kinds {
			for _, op := range operations {
				ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
				_, err := searchSnapshot(ctx, NewStore(db), func(snapshot *Store, state CatalogState, generation int64) (bool, error) {
					if state.SearchMode != "normalized" {
						b.Fatal("恢复的合成 schema 必须已发布搜索投影")
					}
					plan := newSearchPlan(query, kind, state.SearchMode, generation)
					plan.artistAliases = state.ArtistsState == "ready"
					p, err := snapshot.prepareSearchPlan(ctx, plan)
					if err != nil {
						return false, err
					}
					sqlText, args := p.countSQL(!exactCountsOnly && kind != "releases", false), append([]any(nil), p.args...)
					if op == "page" {
						sqlText, args = p.pageSQL(false), append(args, 51, 0)
					}
					r := record{Query: query, Kind: kind, Operation: op, SQL: sqlText, Args: args}
					start := time.Now()
					if err := snapshot.database.QueryRowContext(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+sqlText, args...).Scan(&r.Plan); err != nil {
						return false, err
					}
					r.WallMS = float64(time.Since(start)) / float64(time.Millisecond)
					records = append(records, r)
					b.Logf("q=%q %s %s explain=%.2fms", query, kind, op, r.WallMS)
					return true, nil
				})
				cancel()
				if err != nil {
					b.Fatal(err)
				}
				raw, err := json.MarshalIndent(records, "", "  ")
				if err != nil {
					b.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(output, "plans.json"), append(raw, '\n'), 0644); err != nil {
					b.Fatal(err)
				}
			}
		}
	}
}
