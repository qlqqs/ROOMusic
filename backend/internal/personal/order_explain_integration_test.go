package personal

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPostgreSQLPersonalOrderRebalanceExplain(t *testing.T) {
	f := newPersonalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	user := f.user(t)
	ids := bulkPersonalTracks(t, f, 10000)
	p := createPersonal(t, f, user, ids...).Playlist
	results := map[string]any{}
	for _, phase := range []string{"fresh", "analyzed"} {
		if phase == "analyzed" {
			f.exec(t, `ANALYZE personal_playlist_items`)
		}
		tx, err := f.database.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		if _, err = tx.ExecContext(ctx, `SET CONSTRAINTS personal_playlist_items_order_uq DEFERRED`); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		var raw []byte
		err = tx.QueryRowContext(ctx, `EXPLAIN (ANALYZE, BUFFERS, WAL, FORMAT JSON) `+rebalanceOrderSQL, user, p.ID, orderStep).Scan(&raw)
		if err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if _, err = tx.ExecContext(ctx, `SET CONSTRAINTS personal_playlist_items_order_uq IMMEDIATE`); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		elapsed := time.Since(start)
		tx.Rollback()
		var plan any
		if err = json.Unmarshal(raw, &plan); err != nil {
			t.Fatal(err)
		}
		// 防止新表统计不足时复现 O(N²) 计划：两类重复过滤工作均应远小于 N²。
		root := plan.([]any)[0].(map[string]any)["Plan"].(map[string]any)
		var filtered float64
		var inspect func(map[string]any)
		inspect = func(node map[string]any) {
			loops, _ := node["Actual Loops"].(float64)
			for _, key := range []string{"Rows Removed by Filter", "Rows Removed by Join Filter"} {
				n, _ := node[key].(float64)
				filtered += n * loops
			}
			if children, ok := node["Plans"].([]any); ok {
				for _, child := range children {
					inspect(child.(map[string]any))
				}
			}
		}
		inspect(root)
		if filtered > 100000 {
			t.Fatalf("重整发生超线性重复过滤：%.0f", filtered)
		}
		results[phase] = map[string]any{"elapsed_ms": float64(elapsed) / float64(time.Millisecond), "plan": compactPlan(plan)}
		t.Logf("%s: %.3fms（包括恢复唯一约束）", phase, float64(elapsed)/float64(time.Millisecond))
	}
	if dir := os.Getenv("ROOMUSIC_PERSONAL_EXPLAIN_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		data, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "order-rebalance.json"), append(data, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
}
