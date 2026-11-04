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
