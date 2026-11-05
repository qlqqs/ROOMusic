package maintenance

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/searchindex"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
)

type artistBatchDiagnosticSample struct {
	Window               string                 `json:"window"`
	Sequence             int                    `json:"sequence"`
	Phase                string                 `json:"phase"`
	Kind                 string                 `json:"kind"`
	BatchSize            int                    `json:"batch_size"`
	Written              int                    `json:"written"`
	ActualTracks         int                    `json:"actual_tracks"`
	ReleaseLimit         int                    `json:"release_limit"`
	ProfileLimit         int                    `json:"profile_limit"`
	Budget               artistReleaseBudget    `json:"budget"`
	Selected             artistReleaseSelection `json:"selected"`
	NextReleaseLimit     int                    `json:"next_release_limit"`
	NextProfileLimit     int                    `json:"next_profile_limit"`
	NextBudget           artistReleaseBudget    `json:"next_budget"`
	ProcessMS            float64                `json:"process_ms"`
	BatchDurationMS      float64                `json:"batch_duration_ms"`
	PlannedYieldMS       float64                `json:"planned_yield_ms"`
	YieldMS              float64                `json:"yield_ms"`
	DiagnosticOverheadMS float64                `json:"diagnostic_overhead_ms"`
	SQLCount             int                    `json:"sql_count"`
	artistBatchDiagnosticTiming
}

type artistBatchDiagnosticRunner struct {
	t                          *testing.B
	ctx                        context.Context
	db                         *sql.DB
	conn                       *sql.Conn
	state                      artistState
	trace                      *artistBatchDiagnosticTrace
	output                     string
	samples                    []artistBatchDiagnosticSample
	releaseLimit, profileLimit int
	budget                     artistReleaseBudget
	rewrite                    *artistDiagnosticRewrite
	hotRaw                     []artistDiagnosticTrackRaw
}

func (r *artistBatchDiagnosticRunner) write(name string, value any) {
	r.t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		r.t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(r.output, name), append(data, '\n'), 0600); err != nil {
		r.t.Fatal(err)
	}
}
func (r *artistBatchDiagnosticRunner) exec(query string, args ...any) {
	r.t.Helper()
	if _, err := r.conn.ExecContext(r.ctx, query, args...); err != nil {
		r.t.Fatal(err)
	}
}

// 全规模、少批的提交诊断，不运行publish，不冒充完整worker/在线联测或整轮分位数。
// 运行：ROOMUSIC_ARTIST_BATCH_DIAGNOSTIC_DIR=<仓外绝对目录> go test ./internal/catalog/maintenance
// -run '^$' -bench '^BenchmarkPostgreSQLArtistProcessBatch$' -benchtime=1x -count=1 -timeout=20m。
// baseline另传Go -overlay及ROOMUSIC_ARTIST_BATCH_DIAGNOSTIC_SOURCE_DIR=<baseline-source>。
func BenchmarkPostgreSQLArtistProcessBatch(t *testing.B) {
	output := os.Getenv("ROOMUSIC_ARTIST_BATCH_DIAGNOSTIC_DIR")
	if output == "" {
		t.Skip("完整规模诊断须显式启用仓外证据目录")
	}
	if !filepath.IsAbs(output) {
		t.Fatal("证据目录须为仓外绝对路径")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repository := filepath.Clean(filepath.Join(cwd, "../../../.."))
	relative, err := filepath.Rel(repository, filepath.Clean(output))
	if err != nil || relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
		t.Fatal("不允许向仓内写巨型诊断证据")
	}
	if _, err = os.Stat(filepath.Join(output, "source-sha256.json")); err == nil {
		t.Fatal("证据目录已有运行，拒绝覆盖历史")
	}
	if err = os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	if t.N != 1 {
		t.Fatal("诊断仅支持-benchtime=1x")
	}
	// 全量search前置另给独立阶段预算，不改变任何生产请求/批超时。
	setupTimeout := 15 * time.Minute
	searchBackground := os.Getenv("ROOMUSIC_ARTIST_BATCH_DIAGNOSTIC_SEARCH_READY") == "1"
	if searchBackground {
		setupTimeout = 35 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), setupTimeout)
	defer cancel()
	setupStarted := time.Now()
	url := postgrestest.NewSchema(t, "artist_batch_diagnostic_")
	migrated, err := postgres.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	migrated.Close()
	config, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	trace := &artistBatchDiagnosticTrace{captureProfileParams: os.Getenv("ROOMUSIC_ARTIST_BATCH_DIAGNOSTIC_PLANS") == "1"}
	config.Tracer = trace
	rewrite := &artistDiagnosticRewrite{}
	db := sql.OpenDB(artistDiagnosticConnector{Connector: stdlib.GetConnector(*config), rewrite: rewrite})
	defer db.Close()
	if err = Initialize(ctx, db); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	key, acquired, err := acquireLease(ctx, conn)
	if err != nil || !acquired {
		conn.Close()
		t.Fatalf("诊断未取得CATM：%v acquired=%v", err, acquired)
	}
	defer releaseLease(conn, key)
	r := &artistBatchDiagnosticRunner{t: t, ctx: ctx, db: db, conn: conn, trace: trace, output: output, releaseLimit: 10, profileLimit: 10, budget: initialArtistReleaseBudget(), rewrite: rewrite}
	fixture, err := os.ReadFile("../../app/testdata/search-scale.sql")
	if err != nil {
		t.Fatal(err)
	}
	r.write("source-sha256.json", artistBatchDiagnosticSources(t, fixture))
	r.exec(string(fixture))
	if searchBackground {
		// 真实search owner生成全部字段并核验发布；不复制归一、不空标ready。
		postgrestest.AnalyzeSchema(t, ctx, conn)
		logfile, err := os.OpenFile(filepath.Join(output, "search-background-worker.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		err = runSearchLocked(ctx, conn, slog.New(slog.NewJSONHandler(logfile, nil)))
		closeErr := logfile.Close()
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		state, err := searchindex.LoadState(ctx, conn)
		if err != nil || !state.Ready() {
			t.Fatalf("真实search前置未发布：%+v %v", state, err)
		}
		r.write("search-background-duration.json", map[string]any{"elapsed_ms": artistDiagnosticMS(time.Since(started)), "mode": "真实runSearchLocked完整阶段，非本次artist代表批计时"})
	}
	counts := prepareArtistBatchDiagnosticFixture(t, ctx, db)
	r.write("fixture-counts.json", counts)
	r.searchBackground("search-background-before.json")
	r.state, err = loadArtistState(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	if err = discoverArtists(ctx, conn, r.state, false); err != nil {
		t.Fatal(err)
	}
	postgrestest.AnalyzeSchema(t, ctx, conn)
	var syncCommit, version string
	if err = conn.QueryRowContext(ctx, `SHOW synchronous_commit`).Scan(&syncCommit); err != nil {
		t.Fatal(err)
	}
	if err = conn.QueryRowContext(ctx, `SHOW server_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	r.write("run.json", map[string]any{"mode": "集合预置诊断，非正式回填", "fixture_counts": counts, "synchronous_commit": syncCommit, "server_version": version, "setup_ms": artistDiagnosticMS(time.Since(setupStarted)), "release_build_id": r.state.BuildID, "release_batch_write_audit": "身份/名字键/scan事实/可能恢复tracks.artist及搜索标记，不允许直接synchronous_commit=off", "timing": "pgx驱动SQL含结果消费；CATP从锁语句返回至COMMIT返回，残差仅代表Go/调度", "pacing": "真实wait(batchYield(result.Duration))，有效duration<200ms按duration/10限制10..50ms，duration<=0或>=200ms保守50ms；planned_yield_ms为计划、yield_ms为实测；校验和证据写入另计；代表窗口之间沿用自适应预算"})
	if trace.captureProfileParams {
		r.physicalSnapshot("before-hot")
	}
	t.ResetTimer()
	measuredStarted := time.Now()
	// 每个窗口完整执行诊断清单；原始全库清单保留未完成，不虚报ready或完成分母。
	for _, window := range []struct {
		name        string
		first, size int
	}{
		{"hot", 101, 24}, {"ordinary", 6101, 24}, {"ordinary-to-missing", 9891, 24},
	} {
		if window.name == "hot" && os.Getenv("ROOMUSIC_ARTIST_BATCH_DIAGNOSTIC_ORDERED") == "1" {
			r.captureHotRaw()
		}
		r.releaseWindow(window.name, window.first, window.size)
		if window.name == "hot" && trace.captureProfileParams {
			r.physicalSnapshot("after-hot-before-explain")
			r.profileSerialPlans()
		}
	}
	var originalPending int64
	if err = conn.QueryRowContext(ctx, `SELECT count(*) FROM catalog_projection_work WHERE build_id=$1::uuid AND phase='artist_releases' AND NOT completed`, r.state.BuildID).Scan(&originalPending); err != nil {
		t.Fatal(err)
	}
	if originalPending != 10000 {
		t.Fatalf("原始全清单被虚标完成：pending=%d", originalPending)
	}
	// profiles使用明确独立的诊断build，保留旧build全部进度，不假完成发行来抢过阶段顺序。
	// 只登记全量身份清单并取前12批；不发布该诊断build。
	originalBuild := r.state.BuildID
	r.exec(`UPDATE catalog_projection_state SET build_id=gen_random_uuid(),status='building',manifest_sealed=FALSE WHERE capability='artists'`)
	r.state, err = loadArtistState(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = discoverArtistProfiles(ctx, tx, r.state, "diagnostic_profiles", false); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		r.batch("profiles", "diagnostic_profiles")
	}
	t.StopTimer()
	measuredMS := artistDiagnosticMS(time.Since(measuredStarted))
	processMS, yieldMS, plannedYieldMS := 0.0, 0.0, 0.0
	for _, sample := range r.samples {
		processMS += sample.ProcessMS
		yieldMS += sample.YieldMS
		plannedYieldMS += sample.PlannedYieldMS
	}
	r.write("measured-wall.json", map[string]any{"wall_ms": measuredMS, "process_ms": processMS, "yield_ms": yieldMS, "planned_yield_ms": plannedYieldMS, "diagnostic_overhead_ms": measuredMS - processMS - yieldMS, "overhead_includes": "语义校验、额外读取、阶段准备、tracer管理与所有证据写入"})
	r.searchBackground("search-background-after.json")
	var status string
	if err = conn.QueryRowContext(ctx, `SELECT status FROM catalog_projection_state WHERE capability='artists'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status == "ready" {
		t.Fatal("诊断不能发布ready")
	}
	r.write("manifest.json", map[string]any{"release_build_id": originalBuild, "release_original_pending": originalPending, "profiles_diagnostic_build_id": r.state.BuildID, "status": status, "sample_count": len(r.samples), "facts_and_effective_equal": true, "scope": "三个完整发行窗口+独立profiles清单的12批；没有将其余对象标完成；不能用于整轮p95/p99验收"})
	r.write("summary.json", r.samples)
	if trace.captureProfileParams {
		r.physicalSnapshot("after-windows-before-explain")
		r.profileOtherWindowPlans()
		if os.Getenv("ROOMUSIC_ARTIST_BATCH_DIAGNOSTIC_ORDERED") == "1" {
			r.profileOrderedCandidates()
		}
	}
}

func (r *artistBatchDiagnosticRunner) releaseWindow(name string, first, size int) {
	r.t.Helper()
	ids := make([]string, size)
	for i := range ids {
		ids[i] = fmt.Sprintf("30000000-0000-4000-8000-%012d", first+i)
	}
	phase := "000_diagnostic_" + name
	r.exec(`WITH inserted AS (
 INSERT INTO catalog_projection_work(build_id,phase,subject_kind,subject_id)
 SELECT $1::uuid,$2,'release',id FROM releases WHERE id=ANY($3::uuid[]) RETURNING subject_id)
 INSERT INTO catalog_projection_phases(build_id,phase,subject_kind,total) SELECT $1::uuid,$2,'release',count(*) FROM inserted`, r.state.BuildID, phase, ids)
	before := r.snapshot(ids)
	for completed := 0; completed < size; {
		sample := r.batch(name, phase)
		if sample.BatchSize < 1 {
			r.t.Fatal("代表窗口未执行完整subject")
		}
		completed += sample.BatchSize
	}
	after := r.snapshot(ids)
	if before != after {
		r.t.Fatalf("%s真实process批与集合预置业务署名不等价", name)
	}
	r.write(name+"-equivalence.json", map[string]any{"release_ids": ids, "before_sha256": before, "after_sha256": after, "equal": true})
}

func (r *artistBatchDiagnosticRunner) snapshot(ids []string) string {
	r.t.Helper()
	const query = `WITH scoped AS (SELECT t.id FROM tracks t JOIN media m ON m.id=t.medium_id WHERE m.release_id=ANY($1::uuid[])),
 facts AS (SELECT jsonb_build_array(c.release_id,c.track_id,c.role,c.name_id,c.position,c.layer)::text v FROM artist_credits c WHERE c.release_id=ANY($1::uuid[]) OR c.track_id IN(SELECT id FROM scoped)),
 effective AS (SELECT jsonb_build_array(c.release_id,c.track_id,c.role,c.name_id,c.artist_id,c.position)::text v FROM artist_effective_credits c WHERE c.release_id=ANY($1::uuid[]) OR c.track_id IN(SELECT id FROM scoped))
 SELECT (SELECT md5(string_agg(v,E'\n' ORDER BY v)) FROM facts)||(SELECT md5(string_agg(v,E'\n' ORDER BY v)) FROM effective)`
	var digest string
	if err := r.conn.QueryRowContext(r.ctx, query, ids).Scan(&digest); err != nil {
		r.t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(digest)))
}

func (r *artistBatchDiagnosticRunner) batch(window, phase string) artistBatchDiagnosticSample {
	r.t.Helper()
	iteration := time.Now()
	work := &artistReleaseBatch{Budget: r.budget}
	r.trace.start()
	started := time.Now()
	result, err := processArtistBatch(r.ctx, r.conn, r.state, r.releaseLimit, r.profileLimit, work)
	elapsed := time.Since(started)
	queries := r.trace.stop()
	sequence := len(r.samples) + 1
	if r.trace.captureProfileParams {
		r.profilePlanCounters(sequence, queries)
	}
	r.write(fmt.Sprintf("batch-%03d-queries.json", sequence), queries)
	if err != nil {
		r.t.Fatalf("真实batch %d: %v", sequence, err)
	}
	if result.Phase != phase {
		r.t.Fatalf("诊断阶段串入其他清单：%q want %q", result.Phase, phase)
	}
	sample := artistBatchDiagnosticSample{Window: window, Sequence: sequence, Phase: result.Phase, Kind: result.Kind, BatchSize: result.BatchSize, Written: result.Written, ReleaseLimit: r.releaseLimit, ProfileLimit: r.profileLimit, Budget: r.budget, Selected: work.Selected, ProcessMS: artistDiagnosticMS(elapsed), BatchDurationMS: artistDiagnosticMS(result.Duration), PlannedYieldMS: artistDiagnosticMS(batchYield(result.Duration)), SQLCount: len(queries), artistBatchDiagnosticTiming: artistBatchDiagnosticTimings(queries, elapsed)}
	if !sample.CATPIncludesCommit {
		r.t.Fatal("tracer缺少成功CATP/COMMIT，不能报告持锁")
	}
	if result.Kind == "release" {
		if err = r.conn.QueryRowContext(r.ctx, `SELECT count(*) FROM tracks t JOIN media m ON m.id=t.medium_id WHERE m.release_id=ANY($1::uuid[])`, work.Selected.Refresh).Scan(&sample.ActualTracks); err != nil {
			r.t.Fatal(err)
		}
		if sample.ActualTracks != work.Selected.Tracks {
			r.t.Fatalf("工作量估计漏轨：%d != %d", sample.ActualTracks, work.Selected.Tracks)
		}
		r.releaseLimit = adaptLimit(r.releaseLimit, 100, result.Duration)
		if result.Written > 0 {
			r.budget = adaptArtistReleaseBudget(r.budget, result.Duration)
		}
	} else {
		r.profileLimit = adaptLimit(r.profileLimit, 100, result.Duration)
	}
	sample.NextReleaseLimit = r.releaseLimit
	sample.NextProfileLimit = r.profileLimit
	sample.NextBudget = r.budget
	yielding := time.Now()
	if err = wait(r.ctx, batchYield(result.Duration)); err != nil {
		r.t.Fatal(err)
	}
	sample.YieldMS = artistDiagnosticMS(time.Since(yielding))
	sample.DiagnosticOverheadMS = artistDiagnosticMS(time.Since(iteration)) - sample.ProcessMS - sample.YieldMS
	r.samples = append(r.samples, sample)
	r.write("summary.json", r.samples)
	r.t.Logf("%s batch=%d size=%d written=%d tracks=%d budget=%+v -> %+v process=%.2fms CATP=%.2fms commit=%.2fms planned_yield=%.2fms actual_yield=%.2fms", window, sequence, result.BatchSize, result.Written, sample.ActualTracks, sample.Budget, sample.NextBudget, sample.ProcessMS, sample.CATPHoldMS, sample.CategoriesMS["commit"], sample.PlannedYieldMS, sample.YieldMS)
	return sample
}

func artistBatchDiagnosticSources(t testing.TB, fixture []byte) map[string]string {
	t.Helper()
	hashes := map[string]string{"app/testdata/search-scale.sql": fmt.Sprintf("%x", sha256.Sum256(fixture))}
	for _, pkg := range []string{"maintenance", "artistgraph", "searchindex"} {
		dir := "."
		if pkg != "maintenance" {
			dir = filepath.Join("../internal", pkg)
		}
		if root := os.Getenv("ROOMUSIC_ARTIST_BATCH_DIAGNOSTIC_SOURCE_DIR"); root != "" {
			dir = filepath.Join(root, pkg)
		}
		paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil || len(paths) == 0 {
			t.Fatalf("源码SHA目录 %s: %v", dir, err)
		}
		for _, path := range paths {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			hashes[pkg+"/"+filepath.Base(path)] = fmt.Sprintf("%x", sha256.Sum256(data))
		}
	}
	for _, pattern := range []string{"artist_batch_diagnostic*_test.go", "../../../migrations/*.sql"} {
		paths, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			hashes[path] = fmt.Sprintf("%x", sha256.Sum256(data))
		}
	}
	return hashes
}

// 背景必须实测；空search的同条件N+1比较不外推正式36分钟预算。
func (r *artistBatchDiagnosticRunner) searchBackground(name string) {
	r.t.Helper()
	var data json.RawMessage
	err := r.conn.QueryRowContext(r.ctx, `SELECT jsonb_build_object(
 'observed_at',now(),
 'search_state',(SELECT to_jsonb(s) FROM catalog_projection_state s WHERE capability='search'),
 'search_values',(SELECT count(*) FROM catalog_search_values),
 'subjects',(SELECT count(*) FROM catalog_projection_subject_state),
 'subject_kinds',(SELECT jsonb_object_agg(subject_kind,n) FROM (SELECT subject_kind,count(*) n FROM catalog_projection_subject_state GROUP BY subject_kind) c),
 'subject_gaps',(SELECT count(*) FROM catalog_projection_subject_state s CROSS JOIN catalog_projection_state p WHERE p.capability='search' AND (s.search_applied_generation<>p.target_generation OR s.search_applied_input_revision<>s.input_revision)),
 'values_fields',(SELECT jsonb_object_agg(field,n) FROM (SELECT field,count(*) n FROM catalog_search_values GROUP BY field) c))`).Scan(&data)
	if err != nil {
		r.t.Fatal(err)
	}
	r.write(name, data)
	if os.Getenv("ROOMUSIC_ARTIST_BATCH_DIAGNOSTIC_SEARCH_READY") == "1" {
		var background struct {
			Subjects int64 `json:"subjects"`
			Values   int64 `json:"search_values"`
			Gaps     int64 `json:"subject_gaps"`
			State    struct {
				Status string `json:"status"`
			} `json:"search_state"`
			Fields map[string]int64 `json:"values_fields"`
		}
		if err = json.Unmarshal(data, &background); err != nil {
			r.t.Fatal(err)
		}
		if background.Subjects != 120000 || background.Values != 820000 || background.Gaps != 0 || background.State.Status != "ready" {
			r.t.Fatalf("完整search背景缺失：%+v", background)
		}
		for field, want := range map[string]int64{"release_title": 10000, "release_artist": 10000, "album_artist": 10000, "label": 10000, "catalog_number": 10000, "track_title": 110000, "track_artist": 220000, "credit": 440000} {
			if background.Fields[field] != want {
				r.t.Fatalf("完整search字段%s=%d want %d", field, background.Fields[field], want)
			}
		}
	}
}
