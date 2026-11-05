package maintenance

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/searchindex"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/writegate"
)

func isArtistProfileDiagnosticSQL(q string) bool {
	return strings.Contains(q, "popular AS (") && strings.Contains(q, "artist_effective_credits")
}

// 主名与分支探测分别记录；不把探测误算成一次主名执行。
func isArtistProbeDiagnosticSQL(q string) bool {
	return strings.Contains(q, "artist_effective_credits") && strings.Contains(q, "OFFSET 4096 LIMIT 1")
}
func isArtistPlanDiagnosticSQL(q string) bool {
	return isArtistProfileDiagnosticSQL(q) || isArtistProbeDiagnosticSQL(q)
}
func artistPlanDiagnosticKind(q string) string {
	if isArtistProbeDiagnosticSQL(q) {
		return "probe"
	}
	return "profile"
}

type profilePreparedCounter struct {
	Name           string `json:"name"`
	Generic        int64  `json:"generic_plans"`
	Custom         int64  `json:"custom_plans"`
	ParameterTypes string `json:"parameter_types"`
}

func (r *artistBatchDiagnosticRunner) preparedProfile(conn *sql.Conn, q artistBatchDiagnosticQuery) profilePreparedCounter {
	r.t.Helper()
	var c profilePreparedCounter
	err := conn.QueryRowContext(r.ctx, `SELECT name,generic_plans,custom_plans,parameter_types::text
 FROM pg_prepared_statements WHERE statement=$1`, q.SQL).Scan(&c.Name, &c.Generic, &c.Custom, &c.ParameterTypes)
	if err != nil {
		r.t.Fatalf("主名prepared计数：%v", err)
	}
	return c
}
func (r *artistBatchDiagnosticRunner) profilePlanCounters(sequence int, qs []artistBatchDiagnosticQuery) {
	for _, q := range qs {
		if isArtistPlanDiagnosticSQL(q.SQL) {
			r.write(fmt.Sprintf("%s-counter-%03d.json", artistPlanDiagnosticKind(q.SQL), sequence), map[string]any{"sequence": sequence, "query": q, "prepared": r.preparedProfile(r.conn, q), "note": "真实process提交后只读计数；本窗口尚未EXPLAIN，不改变主名计划轨迹"})
		}
	}
}

// 先完成24个真实process批才采计划；独立连接回放第1/前6次主名参数，不扰动原24批。
// 回放使用采集时完整实际参数和相同SQL；不是新建小夹具，也不改变任何planner/JIT参数。
func (r *artistBatchDiagnosticRunner) profileSerialPlans() {
	r.t.Helper()
	qs := []artistBatchDiagnosticQuery{}
	for _, s := range r.samples {
		if s.Window != "hot" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(r.output, fmt.Sprintf("batch-%03d-queries.json", s.Sequence)))
		if err != nil {
			r.t.Fatal(err)
		}
		var batch []artistBatchDiagnosticQuery
		if err = json.Unmarshal(data, &batch); err != nil {
			r.t.Fatal(err)
		}
		for _, q := range batch {
			if isArtistPlanDiagnosticSQL(q.SQL) {
				// JSON回读恢复驱动所需的uuid[]参数，不重新计算受影响身份。
				var args [][]string
				raw, err := json.Marshal(q.Arguments)
				if err != nil {
					r.t.Fatal(err)
				}
				if err = json.Unmarshal(raw, &args); err != nil || len(args) != 1 {
					r.t.Fatalf("主名参数格式：%v", err)
				}
				q.Arguments = []any{args[0]}
				qs = append(qs, q)
			}
		}
	}
	for _, kind := range []string{"profile", "probe"} {
		selected := []artistBatchDiagnosticQuery{}
		for _, q := range qs {
			if artistPlanDiagnosticKind(q.SQL) == kind {
				selected = append(selected, q)
			}
		}
		r.profileSerialQueryPlans(selected)
	}
}
func (r *artistBatchDiagnosticRunner) profileSerialQueryPlans(qs []artistBatchDiagnosticQuery) {
	if len(qs) < 6 {
		r.t.Fatal("不足6次真实主名执行，不能判断默认串行计划")
	}
	r.explainPreparedProfile(r.conn, qs[len(qs)-1], "serial-final")
	r.db.SetMaxIdleConns(0) // 独立样本结束时丢弃连接；原始24批专用连接仍保持。
	for _, n := range []int{1, 6} {
		conn, err := r.db.Conn(r.ctx)
		if err != nil {
			r.t.Fatal(err)
		}
		samples := []map[string]any{}
		for i := 0; i < n; i++ {
			// 强制新物理连接而非使用上个独立样本的空闲连接：上一样本在结束时显式关闭。
			start := time.Now()
			rows, err := conn.QueryContext(r.ctx, qs[i].SQL, qs[i].Arguments...)
			if err != nil {
				conn.Close()
				r.t.Fatal(err)
			}
			for rows.Next() {
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				conn.Close()
				r.t.Fatal(err)
			}
			samples = append(samples, map[string]any{"ordinal": i + 1, "elapsed_ms": artistDiagnosticMS(time.Since(start)), "prepared": r.preparedProfile(conn, qs[i]), "query": qs[i]})
		}
		label := fmt.Sprintf("independent-%d", n)
		r.write(artistPlanDiagnosticKind(qs[0].SQL)+"-"+label+"-executions.json", samples)
		r.explainPreparedProfile(conn, qs[n-1], label)
		conn.Close()
	}
	if artistPlanDiagnosticKind(qs[0].SQL) == "profile" && os.Getenv("ROOMUSIC_ARTIST_BATCH_DIAGNOSTIC_VARIANTS") == "1" {
		r.profileVariants(qs)
	}
}
func (r *artistBatchDiagnosticRunner) explainPreparedProfile(conn *sql.Conn, q artistBatchDiagnosticQuery, label string) {
	r.t.Helper()
	before := r.preparedProfile(conn, q)
	ids, ok := q.Arguments[0].([]string)
	if !ok {
		r.t.Fatal("缺少主名uuid数组参数")
	}
	literals := make([]string, len(ids))
	for i, id := range ids {
		literals[i] = "'" + strings.ReplaceAll(id, "'", "''") + "'::uuid"
	}
	// EXECUTE引用当前连接真正的prepared statement；不把普通EXPLAIN新语句误记成通用计划。
	explain := `EXPLAIN(ANALYZE,BUFFERS,FORMAT JSON) EXECUTE ` + pgx.Identifier{before.Name}.Sanitize() + `(ARRAY[` + strings.Join(literals, ",") + `]::uuid[])`
	tx, err := conn.BeginTx(r.ctx, nil)
	if err != nil {
		r.t.Fatal(err)
	}
	if err = writegate.Lock(r.ctx, tx); err != nil {
		tx.Rollback()
		r.t.Fatal(err)
	}
	var plan json.RawMessage
	if err = tx.QueryRowContext(r.ctx, explain).Scan(&plan); err != nil {
		tx.Rollback()
		r.t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		r.t.Fatal(err)
	}
	var pid int
	if err = conn.QueryRowContext(r.ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		r.t.Fatal(err)
	}
	r.write(artistPlanDiagnosticKind(q.SQL)+"-plan-"+label+".json", map[string]any{"pid": pid, "query": q, "prepared_before_explain": before, "prepared_after_explain": r.preparedProfile(conn, q), "plan": plan, "note": "EXPLAIN发生于该窗口所有原始执行之后；含实际参数、cost/JIT/buffers，不用于替换真实CATP计时"})
}

// 窗口结束后按真实末次参数取计划；附加执行不计入真实批时长或完成进度。
func (r *artistBatchDiagnosticRunner) profileOtherWindowPlans() {
	for _, window := range []string{"ordinary", "ordinary-to-missing", "profiles"} {
		for i := len(r.samples) - 1; i >= 0; i-- {
			s := r.samples[i]
			if s.Window != window {
				continue
			}
			data, err := os.ReadFile(filepath.Join(r.output, fmt.Sprintf("batch-%03d-queries.json", s.Sequence)))
			if err != nil {
				r.t.Fatal(err)
			}
			var batch []artistBatchDiagnosticQuery
			if err = json.Unmarshal(data, &batch); err != nil {
				r.t.Fatal(err)
			}
			found := false
			for _, q := range batch {
				if isArtistPlanDiagnosticSQL(q.SQL) {
					raw, _ := json.Marshal(q.Arguments)
					var args [][]string
					if err = json.Unmarshal(raw, &args); err != nil || len(args) != 1 {
						r.t.Fatal("主名参数格式")
					}
					q.Arguments = []any{args[0]}
					r.explainPreparedProfile(r.conn, q, "window-"+window)
					found = true
				}
			}
			if found {
				break
			}
		}
	}
}

func (r *artistBatchDiagnosticRunner) profileRows(conn *sql.Conn, q artistBatchDiagnosticQuery) string {
	r.t.Helper()
	rows, err := conn.QueryContext(r.ctx, q.SQL, q.Arguments...)
	if err != nil {
		r.t.Fatal(err)
	}
	result := map[string]any{}
	for rows.Next() {
		var id, auto string
		var manual sql.NullString
		var raw []byte
		if err = rows.Scan(&id, &auto, &manual, &raw); err != nil {
			rows.Close()
			r.t.Fatal(err)
		}
		var names []map[string]any
		if err = json.Unmarshal(raw, &names); err != nil {
			rows.Close()
			r.t.Fatal(err)
		}
		sorted := []string{}
		for _, name := range names {
			encoded, _ := json.Marshal(name)
			sorted = append(sorted, string(encoded))
		}
		sort.Strings(sorted)
		result[id] = []any{auto, manual, sorted}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		r.t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		r.t.Fatal(err)
	}
	return string(raw)
}

// 仅诊断候选；按完整数据校验主名/人工名/全部别名等价，不能冒充候选已在真实worker上线。
func (r *artistBatchDiagnosticRunner) profileVariants(qs []artistBatchDiagnosticQuery) {
	base := qs[0].SQL
	end := strings.Index(base, ", release_votes AS MATERIALIZED")
	if end < 0 {
		r.t.Fatal("缺少计票诊断边界")
	}
	prefix := `WITH absent_tracks AS MATERIALIZED (SELECT id FROM tracks WHERE source_status<>'present'),
 track_counts AS (SELECT c.artist_id,c.name_id,count(DISTINCT c.track_id) AS votes FROM artist_effective_credits c
 WHERE c.artist_id=ANY($1::uuid[]) AND c.track_id IS NOT NULL AND `
	for name, predicate := range map[string]string{
		"anti_exists":   `NOT EXISTS(SELECT 1 FROM absent_tracks t WHERE t.id=c.track_id)`,
		"anti_distinct": `NOT EXISTS(SELECT 1 FROM (SELECT DISTINCT id FROM absent_tracks) t WHERE t.id=c.track_id)`,
		"not_in":        `c.track_id NOT IN (SELECT id FROM absent_tracks)`,
	} {
		conn, err := r.db.Conn(r.ctx)
		if err != nil {
			r.t.Fatal(err)
		}
		q := qs[0]
		q.SQL = prefix + predicate + ` GROUP BY c.artist_id,c.name_id)` + base[end:]
		q.SHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(q.SQL)))
		samples := []map[string]any{}
		for i := 0; i < 6; i++ {
			want := r.profileRows(conn, qs[i])
			q.Arguments = qs[i].Arguments
			q.ArgumentsSHA256 = qs[i].ArgumentsSHA256
			start := time.Now()
			got := r.profileRows(conn, q)
			elapsed := artistDiagnosticMS(time.Since(start))
			if got != want {
				conn.Close()
				r.t.Fatalf("候选%s改变了主名/别名语义", name)
			}
			samples = append(samples, map[string]any{"ordinal": i + 1, "elapsed_ms": elapsed, "equal": true, "prepared": r.preparedProfile(conn, q), "query": q})
		}
		r.write("variant-"+name+"-executions.json", samples)
		r.explainPreparedProfile(conn, q, "variant-"+name)
		conn.Close()
	}
}

// 只读本隔离schema物理/估算统计；不触发ANALYZE、VACUUM或修改planner设置。
// n_dead_tup等是异步估计，不能当作精确数量或单独用于定因。
func (r *artistBatchDiagnosticRunner) physicalSnapshot(label string) {
	r.t.Helper()
	var data json.RawMessage
	err := r.conn.QueryRowContext(r.ctx, `WITH rels AS (
 SELECT c.oid,c.relname,c.relkind,c.relpages,c.reltuples,c.relallvisible,c.reloptions
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname=current_schema() AND c.relkind='r' AND c.relname=ANY(ARRAY[
 'tracks','track_credits','media','artist_credits','artist_effective_credits','artists','artist_names','artist_name_keys',
 'artist_profiles','catalog_search_values','catalog_projection_subject_state','catalog_projection_work'])
 ) SELECT jsonb_build_object(
 'observed_at',clock_timestamp(),'schema',current_schema(),'pid',pg_backend_pid(),
 'note','只读估计统计；无VACUUM/ANALYZE；大小包含真实完整search背景',
 'relations',(SELECT jsonb_agg(to_jsonb(x)) FROM (
 SELECT r.*,pg_relation_size(r.oid) heap_bytes,pg_table_size(r.oid) table_bytes,pg_indexes_size(r.oid) indexes_bytes,
 pg_total_relation_size(r.oid) total_bytes,s.n_live_tup,s.n_dead_tup,s.n_tup_ins,s.n_tup_upd,s.n_tup_del,s.n_tup_hot_upd,
 s.n_mod_since_analyze,s.last_analyze,s.last_autoanalyze,s.last_vacuum,s.last_autovacuum,s.vacuum_count,s.autovacuum_count
 FROM rels r LEFT JOIN pg_stat_all_tables s ON s.relid=r.oid ORDER BY r.relname) x),
 'indexes',(SELECT jsonb_agg(to_jsonb(x)) FROM (
 SELECT r.relname table_name,c.relname index_name,c.relpages,c.reltuples,pg_relation_size(c.oid) bytes,
 i.indisvalid,i.indisready,pg_get_indexdef(c.oid) definition
 FROM rels r JOIN pg_index i ON i.indrelid=r.oid JOIN pg_class c ON c.oid=i.indexrelid ORDER BY r.relname,c.relname) x),
 'columns',(SELECT jsonb_agg(to_jsonb(x)) FROM (
 SELECT s.tablename,s.attname,s.null_frac,s.avg_width,s.n_distinct,s.correlation,
 s.most_common_vals::text most_common_vals,s.most_common_freqs,s.histogram_bounds::text histogram_bounds
 FROM pg_stats s WHERE s.schemaname=current_schema() AND s.tablename IN(SELECT relname FROM rels)
 AND s.attname IN('artist_id','name_id','track_id','release_id','medium_id','source_status','role','subject_kind','field')
 ORDER BY s.tablename,s.attname) x),
 'settings',(SELECT jsonb_object_agg(name,setting) FROM pg_settings WHERE name=ANY(ARRAY[
 'plan_cache_mode','jit','jit_above_cost','jit_inline_above_cost','jit_optimize_above_cost','work_mem','shared_buffers',
 'effective_cache_size','random_page_cost','seq_page_cost','default_statistics_target','synchronous_commit','autovacuum'])))`).Scan(&data)
	if err != nil {
		r.t.Fatal(err)
	}
	r.write("physical-"+label+".json", data)
}

// 仅诊断连接层替换一条稠密计票SQL，其他owner逻辑/事务/CATP/提交原样执行。
// 不进入生产连接，不改SQL参数或值；tracer记录替换后的实际SQL及SHA。
type artistDiagnosticRewrite struct{ candidate string }
type artistDiagnosticConnector struct {
	driver.Connector
	rewrite *artistDiagnosticRewrite
}

func (c artistDiagnosticConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return artistDiagnosticConn{Conn: conn, rewrite: c.rewrite}, nil
}

type artistDiagnosticConn struct {
	driver.Conn
	rewrite *artistDiagnosticRewrite
}

func (c artistDiagnosticConn) rewriteSQL(q string) string {
	if c.rewrite.candidate != "" && isArtistProfileDiagnosticSQL(q) && strings.Contains(q, "WITH absent_tracks AS MATERIALIZED") {
		return artistOrderedSQL(q, c.rewrite.candidate)
	}
	return q
}
func (c artistDiagnosticConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, c.rewriteSQL(q), args)
}
func (c artistDiagnosticConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, c.rewriteSQL(q), args)
}
func (c artistDiagnosticConn) CheckNamedValue(v *driver.NamedValue) error {
	return c.Conn.(driver.NamedValueChecker).CheckNamedValue(v)
}
func (c artistDiagnosticConn) BeginTx(ctx context.Context, o driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, o)
}
func (c artistDiagnosticConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	return c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, c.rewriteSQL(q))
}
func (c artistDiagnosticConn) ResetSession(ctx context.Context) error {
	return c.Conn.(driver.SessionResetter).ResetSession(ctx)
}

func artistOrderedSQL(base, name string) string {
	start := strings.Index(base, "all_track_counts AS (")
	end := strings.Index(base, ", absent_track_counts AS (")
	if start < 0 || end < start {
		panic("诊断候选缺少计票边界")
	}
	var body string
	switch name {
	case "lateral_count":
		body = `SELECT a.id AS artist_id,c.name_id,c.votes FROM unnest($1::uuid[]) a(id)
 CROSS JOIN LATERAL (
 SELECT name_id,count(DISTINCT track_id) AS votes FROM artist_effective_credits
 WHERE artist_id=a.id AND track_id IS NOT NULL GROUP BY name_id ORDER BY name_id
 ) c`
	case "lateral_dedup":
		body = `SELECT a.id AS artist_id,c.name_id,c.votes FROM unnest($1::uuid[]) a(id)
 CROSS JOIN LATERAL (
 SELECT name_id,count(*) AS votes FROM (
 SELECT DISTINCT name_id,track_id FROM artist_effective_credits
 WHERE artist_id=a.id AND track_id IS NOT NULL ORDER BY name_id,track_id
 ) d GROUP BY name_id ORDER BY name_id
 ) c`
	case "ordered_dedup":
		body = `SELECT artist_id,name_id,count(*) AS votes FROM (
 SELECT DISTINCT artist_id,name_id,track_id FROM artist_effective_credits
 WHERE artist_id=ANY($1::uuid[]) AND track_id IS NOT NULL ORDER BY artist_id,name_id,track_id
 ) d GROUP BY artist_id,name_id`
	default:
		panic("未知诊断候选")
	}
	return base[:start] + "all_track_counts AS (\n " + body + "\n )" + base[end:]
}

type artistDiagnosticTrackRaw struct {
	ID     string `json:"id"`
	Artist string `json:"artist"`
}

func (r *artistBatchDiagnosticRunner) captureHotRaw() {
	rows, err := r.conn.QueryContext(r.ctx, `SELECT t.id::text,t.artist FROM tracks t JOIN media m ON m.id=t.medium_id
 WHERE m.release_id>='30000000-0000-4000-8000-000000000101' AND m.release_id<='30000000-0000-4000-8000-000000000124' ORDER BY t.id`)
	if err != nil {
		r.t.Fatal(err)
	}
	for rows.Next() {
		var v artistDiagnosticTrackRaw
		if err = rows.Scan(&v.ID, &v.Artist); err != nil {
			rows.Close()
			r.t.Fatal(err)
		}
		r.hotRaw = append(r.hotRaw, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		r.t.Fatal(err)
	}
	if len(r.hotRaw) != 240 {
		r.t.Fatal("热点原始轨数非240")
	}
	r.write("hot-raw-before.json", r.hotRaw)
}
func (r *artistBatchDiagnosticRunner) profileOrderedCandidates() {
	// 使用同schema已完成真实hot24的实际参数；每个候选独立默认连接执行6次。
	qs := []artistBatchDiagnosticQuery{}
	for i := 1; i <= 24; i++ {
		data, err := os.ReadFile(filepath.Join(r.output, fmt.Sprintf("profile-counter-%03d.json", i)))
		if err != nil {
			r.t.Fatal(err)
		}
		var v struct {
			Query artistBatchDiagnosticQuery `json:"query"`
		}
		if err = json.Unmarshal(data, &v); err != nil {
			r.t.Fatal(err)
		}
		raw, _ := json.Marshal(v.Query.Arguments)
		var args [][]string
		if err = json.Unmarshal(raw, &args); err != nil || len(args) != 1 {
			r.t.Fatal("候选参数格式")
		}
		v.Query.Arguments = []any{args[0]}
		qs = append(qs, v.Query)
	}
	winner := ""
	best := 1e100
	for _, name := range []string{"lateral_count", "lateral_dedup", "ordered_dedup"} {
		conn, err := r.db.Conn(r.ctx)
		if err != nil {
			r.t.Fatal(err)
		}
		samples := []map[string]any{}
		total, baseline := 0.0, 0.0
		var q artistBatchDiagnosticQuery
		for i := 0; i < 6; i++ {
			start := time.Now()
			want := r.profileRows(conn, qs[i])
			baseMS := artistDiagnosticMS(time.Since(start))
			baseline += baseMS
			q = qs[i]
			q.SQL = artistOrderedSQL(q.SQL, name)
			q.SHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(q.SQL)))
			start = time.Now()
			got := r.profileRows(conn, q)
			ms := artistDiagnosticMS(time.Since(start))
			total += ms
			if got != want {
				conn.Close()
				r.t.Fatalf("候选%s改变完整主名/人工名/别名结果", name)
			}
			samples = append(samples, map[string]any{"ordinal": i + 1, "elapsed_ms": ms, "baseline_ms": baseMS, "equal": true, "query": q, "prepared": r.preparedProfile(conn, q)})
		}
		r.write("ordered-"+name+"-executions.json", samples)
		r.explainPreparedProfile(conn, q, "ordered-"+name)
		conn.Close()
		if total < baseline*0.9 && total < best {
			winner = name
			best = total
		}
	}
	r.write("ordered-selection.json", map[string]any{"winner": winner, "six_query_ms": best, "criterion": "6次完整结果等价，驱动墙钟相对交错基线改善至少10%；仅诊断选路，不替代实际CATP"})
	if winner == "" {
		return
	}
	// 同一完整背景跑基线重放及获胜候选；恢复原文时使用真实search owner同步，不伪标marker。
	for _, name := range []string{"baseline_replay", winner} {
		tx, err := r.conn.BeginTx(r.ctx, nil)
		if err != nil {
			r.t.Fatal(err)
		}
		if err = writegate.Lock(r.ctx, tx); err != nil {
			tx.Rollback()
			r.t.Fatal(err)
		}
		data, _ := json.Marshal(r.hotRaw)
		if _, err = tx.ExecContext(r.ctx, `UPDATE tracks t SET artist=v.artist FROM jsonb_to_recordset($1::jsonb) v(id uuid,artist text) WHERE t.id=v.id`, string(data)); err != nil {
			tx.Rollback()
			r.t.Fatal(err)
		}
		ids := []string{}
		for _, v := range r.hotRaw {
			ids = append(ids, v.ID)
		}
		if err = searchindex.RefreshTracks(r.ctx, tx, ids); err != nil {
			tx.Rollback()
			r.t.Fatal(err)
		}
		// 新诊断代次使全部旧marker自然过期，禁止把未完成对象假标为current。
		if _, err = tx.ExecContext(r.ctx, `UPDATE catalog_projection_state SET target_generation=target_generation+1,build_id=gen_random_uuid(),status='preparing',manifest_sealed=FALSE WHERE capability='artists'`); err != nil {
			tx.Rollback()
			r.t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			r.t.Fatal(err)
		}
		r.state, err = loadArtistState(r.ctx, r.conn)
		if err != nil {
			r.t.Fatal(err)
		}
		if err = discoverArtists(r.ctx, r.conn, r.state, false); err != nil {
			r.t.Fatal(err)
		}
		r.releaseLimit, r.profileLimit, r.budget = 10, 10, initialArtistReleaseBudget()
		r.rewrite.candidate = ""
		if name != "baseline_replay" {
			r.rewrite.candidate = name
		}
		r.physicalSnapshot("before-ordered-process-" + name)
		r.releaseWindow("ordered-"+name, 101, 24)
		r.rewrite.candidate = ""
		r.write("ordered-process-samples.json", r.samples)
		r.searchBackground("search-background-ordered-" + name + ".json")
	}
	r.physicalSnapshot("after-ordered-process")
}
