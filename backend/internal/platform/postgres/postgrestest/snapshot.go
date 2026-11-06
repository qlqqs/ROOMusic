package postgrestest

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// SchemaSnapshot 仅描述当前测试 schema 的逻辑结构和合成数据，不包含 OID、物理列号或扩展。
type SchemaSnapshot struct {
	Structure map[string]string
	Data      map[string]string
}

// Snapshot 归一化 schema 名与列序号，忽略迁移记录的 applied_at。
func Snapshot(ctx context.Context, db *sql.DB) (SchemaSnapshot, error) {
	result := SchemaSnapshot{Structure: map[string]string{}, Data: map[string]string{}}
	var schema string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		return result, err
	}
	normalize := func(s string) string {
		s = strings.ReplaceAll(s, pgx.Identifier{schema}.Sanitize()+".", "$SCHEMA.")
		return strings.ReplaceAll(s, schema+".", "$SCHEMA.")
	}
	// 每个对象使用可读且稳定的逻辑键；不依赖 OID、统计、创建时间或物理存储。
	queries := []string{
		`SELECT 'relation/'||c.relname, c.relkind::text||':'||c.relpersistence::text FROM pg_class c WHERE c.relnamespace=current_schema()::regnamespace AND c.relkind IN ('r','p','v','m','S','f') AND NOT EXISTS(SELECT 1 FROM pg_depend d WHERE d.classid='pg_class'::regclass AND d.objid=c.oid AND d.deptype='e')`,
		`SELECT 'column/'||c.relname||'/'||a.attname, json_build_array(row_number() OVER(PARTITION BY c.oid ORDER BY a.attnum),format_type(a.atttypid,a.atttypmod),a.attnotnull,pg_get_expr(ad.adbin,ad.adrelid),a.attidentity,a.attgenerated,co.collname)::text FROM pg_class c JOIN pg_attribute a ON a.attrelid=c.oid LEFT JOIN pg_attrdef ad ON ad.adrelid=c.oid AND ad.adnum=a.attnum LEFT JOIN pg_collation co ON co.oid=a.attcollation WHERE c.relnamespace=current_schema()::regnamespace AND c.relkind IN ('r','p','v','m','f') AND a.attnum>0 AND NOT a.attisdropped`,
		`SELECT 'constraint/'||c.relname||'/'||x.conname,pg_get_constraintdef(x.oid) FROM pg_constraint x JOIN pg_class c ON c.oid=x.conrelid WHERE c.relnamespace=current_schema()::regnamespace`,
		`SELECT 'index/'||c.relname,pg_get_indexdef(c.oid) FROM pg_class c WHERE c.relnamespace=current_schema()::regnamespace AND c.relkind IN ('i','I')`,
		`SELECT 'view/'||c.relname,pg_get_viewdef(c.oid) FROM pg_class c WHERE c.relnamespace=current_schema()::regnamespace AND c.relkind IN ('v','m')`,
		`SELECT 'function/'||p.proname||'('||pg_get_function_identity_arguments(p.oid)||')',pg_get_functiondef(p.oid) FROM pg_proc p WHERE p.pronamespace=current_schema()::regnamespace AND p.prokind IN ('f','p') AND NOT EXISTS(SELECT 1 FROM pg_depend d WHERE d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.deptype='e')`,
		`SELECT 'trigger/'||c.relname||'/'||t.tgname,pg_get_triggerdef(t.oid) FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid WHERE c.relnamespace=current_schema()::regnamespace AND NOT t.tgisinternal`,
		`SELECT 'enum/'||t.typname,json_agg(e.enumlabel ORDER BY e.enumsortorder)::text FROM pg_type t JOIN pg_enum e ON e.enumtypid=t.oid WHERE t.typnamespace=current_schema()::regnamespace GROUP BY t.typname`,
		`SELECT 'domain/'||t.typname,json_build_array(format_type(t.typbasetype,t.typtypmod),t.typnotnull,t.typdefault,(SELECT json_agg(pg_get_constraintdef(c.oid) ORDER BY c.conname) FROM pg_constraint c WHERE c.contypid=t.oid))::text FROM pg_type t WHERE t.typnamespace=current_schema()::regnamespace AND t.typtype='d'`,
		`SELECT 'sequence/'||c.relname,json_build_array(format_type(s.seqtypid,NULL),s.seqstart,s.seqincrement,s.seqmax,s.seqmin,s.seqcache,s.seqcycle)::text FROM pg_sequence s JOIN pg_class c ON c.oid=s.seqrelid WHERE c.relnamespace=current_schema()::regnamespace`,
	}
	for _, query := range queries {
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			return result, fmt.Errorf("读取结构快照：%w", err)
		}
		for rows.Next() {
			var key, value string
			if err := rows.Scan(&key, &value); err != nil {
				rows.Close()
				return result, err
			}
			result.Structure[normalize(key)] = normalize(value)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return result, err
		}
	}
	rows, err := db.QueryContext(ctx, `SELECT relname FROM pg_class WHERE relnamespace=current_schema()::regnamespace AND relkind IN ('r','p') ORDER BY relname`)
	if err != nil {
		return result, err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return result, err
		}
		tables = append(tables, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	for _, name := range tables {
		table := pgx.Identifier{schema, name}.Sanitize()
		var count int64
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil {
			return result, err
		}
		value := fmt.Sprintf("rows=%d", count)
		if count <= 1000 {
			expression := "to_jsonb(t)"
			if name == "schema_migrations" {
				expression += " - 'applied_at'"
			}
			var digest string
			query := `SELECT md5(COALESCE(string_agg(j::text,E'\n' ORDER BY j::text),'')) FROM (SELECT ` + expression + ` AS j FROM ` + table + ` t) q`
			if err := db.QueryRowContext(ctx, query).Scan(&digest); err != nil {
				return result, err
			}
			value += " md5=" + digest
		}
		result.Data[name] = value
	}
	return result, nil
}

// Diff 给出确定顺序的结构与数据差异，便于定位漏回退对象。
func Diff(a, b SchemaSnapshot) []string {
	var result []string
	for _, section := range []struct {
		name string
		a, b map[string]string
	}{{"结构", a.Structure, b.Structure}, {"数据", a.Data, b.Data}} {
		keys := map[string]bool{}
		for key := range section.a {
			keys[key] = true
		}
		for key := range section.b {
			keys[key] = true
		}
		for key := range keys {
			av, aok := section.a[key]
			bv, bok := section.b[key]
			if av != bv || aok != bok {
				result = append(result, fmt.Sprintf("%s %s: %q → %q", section.name, key, av, bv))
			}
		}
	}
	sort.Strings(result)
	return result
}
