package postgrestest

import (
	"context"
	"database/sql"
	"testing"
)

// AnalyzeSchema 只更新当前隔离 schema 的统计；裸 ANALYZE 会污染同库并行测试的统计前提。
func AnalyzeSchema(t testing.TB, ctx context.Context, db *sql.DB) {
	t.Helper()
	rows, err := db.QueryContext(ctx, `SELECT format('%I.%I',n.nspname,c.relname)
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname=current_schema() AND c.relkind IN ('r','p') ORDER BY c.relname`)
	if err != nil {
		t.Fatalf("读取当前测试 schema 的表：%v", err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		// table 仅来自 PostgreSQL format(%I)，包含已转义的 schema 与表标识符。
		if _, err := db.ExecContext(ctx, "ANALYZE "+table); err != nil {
			t.Fatalf("更新当前测试 schema 的统计：%v", err)
		}
	}
}
