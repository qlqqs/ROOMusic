package postgrestest

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// analyzeDatabase 同时接受连接池和已固定的连接，保留调用方的统计刷新/读取会话。
type analyzeDatabase interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// AnalyzeSchema 只更新当前隔离 schema 的统计；裸 ANALYZE 会污染同库并行测试的统计前提。
// 不传表名时分析全部表；传入时只分析指定的未限定原始表名，重复项只执行一次。
// 所有指定表必须存在于当前 schema 的普通表/分区表集合；先完整验证，再更新统计。
func AnalyzeSchema(t testing.TB, ctx context.Context, db analyzeDatabase, onlyTables ...string) {
	t.Helper()
	tables, err := schemaAnalyzeTables(ctx, db, onlyTables)
	if err != nil {
		t.Fatalf("读取当前测试 schema 的表：%v", err)
	}
	for _, table := range tables {
		// table 仅来自 PostgreSQL format(%I)，包含已转义的 schema 与表标识符。
		if _, err := db.ExecContext(ctx, "ANALYZE "+table); err != nil {
			t.Fatalf("更新当前测试 schema 的统计：%v", err)
		}
	}
}

func schemaAnalyzeTables(ctx context.Context, db analyzeDatabase, onlyTables []string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT c.relname,format('%I.%I',n.nspname,c.relname)
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname=current_schema() AND c.relkind IN ('r','p') ORDER BY c.relname`)
	if err != nil {
		return nil, err
	}
	requested := make(map[string]bool, len(onlyTables))
	for _, name := range onlyTables {
		requested[name] = true
	}
	var tables []string
	for rows.Next() {
		var name, qualified string
		if err := rows.Scan(&name, &qualified); err != nil {
			rows.Close()
			return nil, err
		}
		if len(onlyTables) == 0 || requested[name] {
			tables = append(tables, qualified)
			delete(requested, name)
		}
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(requested) > 0 {
		missing := make([]string, 0, len(requested))
		for name := range requested {
			missing = append(missing, fmt.Sprintf("%q", name))
		}
		sort.Strings(missing)
		return nil, fmt.Errorf("指定表不在当前测试 schema 的可分析表中：%s", strings.Join(missing, ", "))
	}
	return tables, nil
}
