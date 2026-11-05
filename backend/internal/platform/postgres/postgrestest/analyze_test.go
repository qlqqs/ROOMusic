package postgrestest

import (
	"context"
	"database/sql"
	"testing"
)

func TestPostgreSQLAnalyzeSchemaKeepsOtherSchemasUntouched(t *testing.T) {
	open := func() *sql.DB {
		t.Helper()
		db, err := sql.Open("pgx", NewSchema(t, "analyze_"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		db.SetMaxOpenConns(1)
		if _, err := db.Exec(`CREATE TABLE "统计表"(id int) WITH (autovacuum_enabled=false); INSERT INTO "统计表" VALUES(1)`); err != nil {
			t.Fatal(err)
		}
		return db
	}
	target, other := open(), open()
	AnalyzeSchema(t, context.Background(), target)
	if _, err := target.Exec(`SELECT pg_stat_force_next_flush()`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		db     *sql.DB
		tuples float64
		count  int64
	}{{target, 1, 1}, {other, -1, 0}} {
		var tuples float64
		var count int64
		if err := tc.db.QueryRow(`SELECT c.reltuples,s.analyze_count FROM pg_class c JOIN pg_stat_all_tables s ON s.relid=c.oid
 WHERE c.relnamespace=current_schema()::regnamespace AND c.relname='统计表'`).Scan(&tuples, &count); err != nil {
			t.Fatal(err)
		}
		if tuples != tc.tuples || count != tc.count {
			t.Fatalf("schema 统计隔离失效：tuples=%v count=%d，期望 %v/%d", tuples, count, tc.tuples, tc.count)
		}
	}
}

func TestPostgreSQLAnalyzeSchemaAllowlistPreservesStages(t *testing.T) {
	db, err := sql.Open("pgx", NewSchema(t, "analyze_stages_"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `CREATE TABLE core(id int) WITH (autovacuum_enabled=false);
 CREATE TABLE "辅助.表"(id int) WITH (autovacuum_enabled=false);
 CREATE TABLE "含""引号"(id int) WITH (autovacuum_enabled=false);
 INSERT INTO core VALUES(1),(2)`); err != nil {
		t.Fatal(err)
	}
	check := func(table string, wantTuples float64, wantCount int64) {
		t.Helper()
		if _, err := conn.ExecContext(ctx, `SELECT pg_stat_force_next_flush()`); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.ExecContext(ctx, `SELECT pg_stat_clear_snapshot()`); err != nil {
			t.Fatal(err)
		}
		var tuples float64
		var count int64
		if err := conn.QueryRowContext(ctx, `SELECT c.reltuples,s.analyze_count FROM pg_class c
 JOIN pg_stat_all_tables s ON s.relid=c.oid
 WHERE c.relnamespace=current_schema()::regnamespace AND c.relname=$1`, table).Scan(&tuples, &count); err != nil {
			t.Fatal(err)
		}
		if tuples != wantTuples || count != wantCount {
			t.Fatalf("阶段统计错误 %q：tuples=%v count=%d，期望 %v/%d", table, tuples, count, wantTuples, wantCount)
		}
	}
	// 重复指定核心表只执行一次，辅助表必须保持未分析。
	AnalyzeSchema(t, ctx, conn, "core", "core")
	check("core", 2, 1)
	check("辅助.表", -1, 0)
	check("含\"引号", -1, 0)
	// 表名中的点和双引号是名称本身，不允许被当作 SQL 或 schema 限定符。
	AnalyzeSchema(t, ctx, conn, "辅助.表", "含\"引号")
	check("core", 2, 1)
	check("辅助.表", 0, 1)
	check("含\"引号", 0, 1)
}

func TestPostgreSQLAnalyzeSchemaAllowlistRejectsMissingTables(t *testing.T) {
	ctx := context.Background()
	open := func() *sql.DB {
		t.Helper()
		db, err := sql.Open("pgx", NewSchema(t, "analyze_allowlist_"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		db.SetMaxOpenConns(1)
		return db
	}
	target, other := open(), open()
	if _, err := target.Exec(`CREATE TABLE local_table(id int) WITH (autovacuum_enabled=false);
 CREATE VIEW local_view AS SELECT * FROM local_table`); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Exec(`CREATE TABLE remote_only(id int) WITH (autovacuum_enabled=false)`); err != nil {
		t.Fatal(err)
	}
	var otherSchema string
	if err := other.QueryRow(`SELECT current_schema()`).Scan(&otherSchema); err != nil {
		t.Fatal(err)
	}
	for _, missing := range []string{"missing", "", "local_view", "remote_only", otherSchema + ".remote_only", `local_table; ANALYZE remote_only`, "LOCAL_TABLE"} {
		t.Run(missing, func(t *testing.T) {
			// 列表含有效表也必须整体拒绝，不能先分析有效项再发现拼写错误。
			tables, err := schemaAnalyzeTables(ctx, target, []string{"local_table", missing})
			if err == nil || tables != nil {
				t.Fatalf("非法名单未整体拒绝：tables=%v err=%v", tables, err)
			}
		})
	}
	var tuples float64
	if err := target.QueryRow(`SELECT reltuples FROM pg_class WHERE relnamespace=current_schema()::regnamespace AND relname='local_table'`).Scan(&tuples); err != nil || tuples != -1 {
		t.Fatalf("验证失败不能提前更新统计：tuples=%v err=%v", tuples, err)
	}
}
