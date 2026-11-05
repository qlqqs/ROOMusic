package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/qlqq/roomusic/backend/internal/catalog/maintenance"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type scaleQuery struct {
	SQL     string  `json:"sql"`
	Args    []any   `json:"args"`
	MaxMS   float64 `json:"max_ms"`
	Calls   int     `json:"calls"`
	TotalMS float64 `json:"total_ms"`
}
type scaleTracer struct {
	mu           sync.Mutex
	enabled      bool
	queries      map[string]scaleQuery
	monitorGates bool
	gateWaits    []float64
	diagnostics  *scaleDiagnostics
}

func (s *scaleTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	// 放在任何提前返回之前，覆盖后台事务和COMMIT/ROLLBACK。
	ctx = s.diagnostics.beginQuery(ctx, conn.PgConn().PID(), conn.PgConn().TxStatus(), data.SQL, data.Args)
	if watch, ok := ctx.Value(scaleQueueCancelKey{}).(*scaleQueueCancelWatch); ok && strings.Contains(data.SQL, "artist_effective_credits") {
		watch.once.Do(func() { watch.started <- conn.PgConn().PID() })
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dbTime, _ := ctx.Value(scaleRequestKey{}).(*atomic.Int64)
	if s.monitorGates && strings.Contains(data.SQL, "pg_advisory_xact_lock") {
		for _, arg := range data.Args {
			var key int64
			switch value := arg.(type) {
			case int:
				key = int64(value)
			case int32:
				key = int64(value)
			case int64:
				key = value
			}
			if key == 0x43415450 {
				return context.WithValue(ctx, scaleTraceKey{}, scaleTraceStart{at: time.Now(), gate: true, dbTime: dbTime})
			}
		}
	}
	if s.enabled && (strings.Contains(data.SQL, "catalog_search_values") || strings.Contains(data.SQL, "selected_tracks") || strings.Contains(data.SQL, "release_effective") || strings.Contains(data.SQL, "artist_")) {
		args := make([]any, 0, len(data.Args))
		for _, arg := range data.Args {
			switch arg.(type) {
			case pgx.QueryResultFormatsByOID, pgx.QueryResultFormats, pgx.QueryExecMode:
				continue
			}
			args = append(args, arg)
		}
		key := data.SQL + fmt.Sprint(args)
		if _, exists := s.queries[key]; !exists {
			s.queries[key] = scaleQuery{SQL: data.SQL, Args: args}
		}
		return context.WithValue(ctx, scaleTraceKey{}, scaleTraceStart{key: key, at: time.Now(), dbTime: dbTime})
	}
	if dbTime != nil {
		return context.WithValue(ctx, scaleTraceKey{}, scaleTraceStart{at: time.Now(), dbTime: dbTime})
	}
	return ctx
}

type scaleRequestKey struct{}
type scaleTraceKey struct{}
type scaleTraceStart struct {
	key    string
	at     time.Time
	gate   bool
	dbTime *atomic.Int64
}

func (s *scaleTracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	s.diagnostics.endQuery(ctx, conn.PgConn().TxStatus(), data.CommandTag.String(), data.Err)
	start, ok := ctx.Value(scaleTraceKey{}).(scaleTraceStart)
	if !ok {
		return
	}
	elapsed := time.Since(start.at)
	if start.dbTime != nil {
		start.dbTime.Add(int64(elapsed))
	}
	if !start.gate && start.key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if start.gate {
		s.gateWaits = append(s.gateWaits, float64(time.Since(start.at))/float64(time.Millisecond))
		return
	}
	query := s.queries[start.key]
	query.MaxMS = max(query.MaxMS, float64(time.Since(start.at))/float64(time.Millisecond))
	query.TotalMS += float64(elapsed) / float64(time.Millisecond)
	query.Calls++
	s.queries[start.key] = query
}
func (s *scaleTracer) collect(fn func()) []scaleQuery {
	s.mu.Lock()
	s.queries = map[string]scaleQuery{}
	s.enabled = true
	s.mu.Unlock()
	fn()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled = false
	out := make([]scaleQuery, 0, len(s.queries))
	for _, q := range s.queries {
		out = append(out, q)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SQL < out[j].SQL })
	return out
}

type scaleLatency struct {
	ColdMS, MedianMS, P95MS float64
	Samples                 int
	TargetMS                float64
	Pass                    bool
}

func scaleStats(cold float64, values []float64, target float64) scaleLatency {
	sort.Float64s(values)
	i := (95*len(values)+99)/100 - 1
	median := values[len(values)/2]
	if len(values)%2 == 0 {
		median = (values[len(values)/2-1] + median) / 2
	}
	return scaleLatency{cold, median, values[i], len(values), target, target <= 0 || values[i] <= target}
}

// SQL累计墙钟覆盖鉴权、状态、事务、搜索与补全；连接池等待仍由完整HTTP计时覆盖。
// 三栏并发的SQL工作量取和，不冒充整屏关键路径。
type scaleDatabaseLatency struct{ MedianMS, P95MS float64 }

func scaleDatabaseStats(values []float64) scaleDatabaseLatency {
	stats := scaleStats(0, values, 0)
	return scaleDatabaseLatency{stats.MedianMS, stats.P95MS}
}

type scaleCase struct {
	Query                                                                 string
	Suggestions, Results                                                  scaleLatency
	Albums, Tracks, Artists                                               scaleLatency
	OldReleasesMS, OldGroupsMS                                            float64
	SuggestionsDBMS, AlbumsDBMS, TracksDBMS, ArtistsDBMS, ResultsDBWorkMS scaleDatabaseLatency
	OldReleases, OldGroups                                                scaleLatency
	OldReleasesDBMS, OldGroupsDBMS                                        scaleDatabaseLatency
}
type scaleReport struct {
	CPUCount                                           int
	RAMBytes, PeakHeapBytes                            uint64
	PoolMax, ResultsConcurrency, Warmups, Measurements int
	BackfillMS                                         float64
	TotalBudgetMS, BackfillTargetMS                    float64
	BackfillComplete, BackfillPass                     bool
	WorkerStartedAt, BothReadyAt                       time.Time
	CommittedApplicationCATP                           scaleCATPHoldReport
	CommittedApplicationCATPByPhase                    map[string]scaleCATPPhaseReport
	OnlineLoad                                         scaleOnlineLoadReport
	OnlineAcceptance                                   scaleOnlineAcceptanceReport
	OnlineComplete                                     bool
	Checks                                             map[string]scaleAcceptanceCheck
	AcceptanceFailures                                 []scaleAcceptanceFailure
	FirstError                                         string
	Errors                                             []scaleRunError
	Finalized                                          bool
	Pass                                               bool
	UnconfirmedApplicationBatches                      []scaleCommittedGate
	Counts                                             map[string]int64
	Sizes                                              map[string]int64
	PG                                                 map[string]string
	Foreground                                         map[string]scaleLatency
	Cases                                              []scaleCase
	Queues                                             []scaleQueueReport
	PlanFiles                                          []string
	HeapBytes                                          uint64
	FirstFailure                                       string
	Complete                                           bool
	Notes                                              []string
}

// BenchmarkPostgreSQLSearchScale 是显式 opt-in 的合成库验收，不进入普通 go test。
// 使用 -run '^$' -bench '^BenchmarkPostgreSQLSearchScale$' -benchtime=1x；输出目录通过环境变量指定。
func BenchmarkPostgreSQLSearchScale(b *testing.B) {
	if b.N != 1 {
		b.Fatal("请使用 -benchtime=1x，避免重复回填十万首")
	}
	if os.Getenv("ROOMUSIC_SEARCH_SCALE_REUSE_SCHEMA") != "" {
		b.Fatal("⑤正式规模验收必须从空schema运行生产maintenance，不允许复用已回填schema")
	}
	if value := os.Getenv("ROOMUSIC_SEARCH_SCALE_ITERATIONS"); value != "" && value != "30" {
		b.Fatal("正式⑤验收固定测量30次，不允许缩减")
	}
	if os.Getenv("ROOMUSIC_SEARCH_SCALE_QUERIES") != "" {
		b.Fatal("正式⑤验收必须运行完整查询矩阵，不允许缩减")
	}
	output := os.Getenv("ROOMUSIC_SEARCH_SCALE_OUTPUT")
	if output == "" {
		b.Fatal("必须指定 ROOMUSIC_SEARCH_SCALE_OUTPUT")
	}
	if err := os.MkdirAll(output, 0755); err != nil {
		b.Fatal(err)
	}
	totalBudget, err := scaleExecutionBudget(os.Getenv("ROOMUSIC_SEARCH_SCALE_TIMEOUT"))
	if err != nil {
		b.Fatal(err)
	}

	var diagnostics *scaleDiagnostics
	var guard *scaleBackfillGuard
	report := scaleReport{TotalBudgetMS: float64(totalBudget / time.Millisecond), BackfillTargetMS: float64(scaleBackfillBudget / time.Millisecond), CPUCount: runtime.NumCPU(), RAMBytes: scaleRAMBytes(), PoolMax: 12, ResultsConcurrency: 3, Warmups: 5, Measurements: 30, Counts: map[string]int64{}, Sizes: map[string]int64{}, PG: map[string]string{}, Foreground: map[string]scaleLatency{}, Notes: []string{"⑤夹具含两万真实参与的共享身份、四千不同key别名，每轨两个艺人和四个credits；五万present参与同一热门艺人和热门厂牌。只预置初始姓名路由，事实与投影全部由生产maintenance回填。", "生产 handler 计时包括鉴权、数据库等待、计数、分页、摘要补全和序列化；本机 httptest 不含外部网络。", "合成十万轨根标为 disabled，只扫描独立探针根，避免不存在的合成音频被负向对账；present-only 搜索仍覆盖十万轨。"}}
	load := newScaleOnlineLoad()
	peakHeap := &atomic.Uint64{}
	syncGuardError := func() {
		if guard != nil {
			if reason := guard.reason(); reason != "" {
				report.recordRunError("operation", fmt.Errorf("%s", reason))
			}
		}
	}
	cleanupError := func(stage string, err error) {
		if err != nil {
			syncGuardError()
			report.recordRunError("cleanup."+stage, err)
			b.Errorf("cleanup %s: %v", stage, err)
		}
	}
	persistReport := func() error {
		load.copyTo(&report)
		if diagnostics != nil {
			report.UnconfirmedApplicationBatches = diagnostics.unconfirmedBatches()
			report.CommittedApplicationCATP, report.CommittedApplicationCATPByPhase = diagnostics.batchReports(report.BackfillComplete)
			report.OnlineAcceptance = diagnostics.onlineAcceptance(report.OnlineLoad, report.OnlineComplete)
		}
		syncGuardError()
		report.Pass = report.Complete && report.FirstError == "" && len(report.AcceptanceFailures) == 0
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		report.HeapBytes, report.PeakHeapBytes = memory.HeapAlloc, max(memory.HeapAlloc, peakHeap.Load())
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(output, "summary.json"), append(body, '\n'), 0644)
	}
	writeReport := func() {
		if err := persistReport(); err != nil {
			report.recordRunError("report.write", err)
			b.Fatal(err)
		}
	}
	// 必须早于NewSchema/TempDir及所有资源cleanup注册：最后才封口summary。
	scaleRegisterFinalReport(b.Cleanup, &report, b.Failed, persistReport, func(err error) { b.Error(err) })
	ctx, cancel := context.WithTimeout(context.Background(), totalBudget)
	defer cancel()
	schemaTB := &scaleReportingTB{TB: b, onError: func(stage string, err error) { report.recordRunError(stage, err) }}
	scoped := postgrestest.NewSchema(schemaTB, "search_scale_")
	migrated, err := postgres.Open(ctx, scoped)
	if err != nil {
		b.Fatal(err)
	}
	if err := migrated.Close(); err != nil {
		report.recordRunError("cleanup.migrations.close", err)
		b.Fatal(err)
	}
	// 精确CATP提交计时必开；逐SQL/activity重型取证仍显式opt-in。
	diagnostics, err = newScaleDiagnostics(output)
	if err != nil {
		b.Fatal(err)
	}
	diagnostics.detailed = os.Getenv("ROOMUSIC_SEARCH_SCALE_DIAGNOSTICS") == "1"
	diagnostics.recording.Store(false)
	defer func() { cleanupError("diagnostics.close", diagnostics.Close()) }()
	tracer := &scaleTracer{diagnostics: diagnostics}
	pgconfig, err := pgx.ParseConfig(scoped)
	if err != nil {
		b.Fatal(err)
	}
	pgconfig.Tracer = tracer
	diagnosticApplication := "roomusic-scale-" + uuid.New()
	if diagnostics != nil {
		pgconfig.RuntimeParams["application_name"] = diagnosticApplication
	}
	db := stdlib.OpenDB(*pgconfig)
	db.SetMaxOpenConns(12)
	db.SetMaxIdleConns(12)
	defer func() { cleanupError("database.close", db.Close()) }()
	exec := func(query string, args ...any) {
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			b.Fatal(err)
		}
	}
	{
		fixture, err := os.ReadFile("testdata/search-scale.sql")
		if err != nil {
			b.Fatal(err)
		}
		exec(string(fixture))
	}
	root := scaleTempDir(b, cleanupError)
	data := scaleTempDir(b, cleanupError)
	probeRootID := uuid.New()
	probeMusic := filepath.Join(root, "probe")
	probeFile := filepath.Join(probeMusic, "probe.flac")
	if err = os.Mkdir(probeMusic, 0755); err != nil {
		b.Fatal(err)
	}
	// 音频保持不变，避免探针自己制造source_changed；扫描仍真实解析并核验本轮观察。
	probeContent := parsertest.FLACWithAudio(b, [16]byte{41}, bytes.Repeat([]byte{41}, 4096), "TITLE=在线探针", "ARTIST=探针艺人", "ALBUM=在线探针专辑")
	if err = os.WriteFile(probeFile, probeContent, 0644); err != nil {
		b.Fatal(err)
	}
	exec(`INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,$2,'active',1)`, probeRootID, probeMusic)
	userID, token := uuid.New(), identity.GenerateSessionToken()
	exec(`INSERT INTO users(id,username,password_hash,role) VALUES($1::uuid,$2,'test-only','admin')`, userID, "scale-"+userID)
	exec(`INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2::uuid,NOW()+($3::double precision * INTERVAL '1 second'))`, identity.HashSessionToken(token), userID, (totalBudget + time.Hour).Seconds())
	if err = InitializeCatalog(ctx, db); err != nil {
		b.Fatal(err)
	}
	applicationFile, err := os.OpenFile(filepath.Join(output, "application.jsonl"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { cleanupError("application_log.close", applicationFile.Close()) }()
	logFile, err := os.OpenFile(filepath.Join(output, "backfill.jsonl"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { cleanupError("backfill_log.close", logFile.Close()) }()
	scanRequestID := "scale-scan-" + uuid.New()
	life, stop := context.WithCancel(ctx)
	defer stop()
	// 背景扫描关联同一合成负载会话；每轮HTTP与scan ID另记，不声称生产传播请求上下文。
	scanLife := scaleDiagnosticContext(life, "scan_background", scanRequestID)
	application := New(scanLife, config.Server{AllowedLibraryRoots: []string{root}, DataDirectory: data}, db, testFrontend(), slog.New(slog.NewJSONHandler(applicationFile, nil)), func(o *options) { o.pauseCatalogMaintenance = true })
	var workerDone <-chan struct{}
	defer func() {
		stop()
		wait, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		cleanupError("wait_for_scans", application.WaitForScans(wait))
		if workerDone != nil {
			select {
			case <-workerDone:
			case <-wait.Done():
				cleanupError("maintenance.join", fmt.Errorf("maintenance清理未完成: %w", wait.Err()))
			}
		}
	}()
	guard = &scaleBackfillGuard{stop: stop, diagnostics: diagnostics}
	diagnostics.onAcceptanceFailure = guard.fail
	request := func(method, path string, body []byte, headers map[string]string) (*httptest.ResponseRecorder, float64, float64) {
		requestCtx, requestCancel := context.WithTimeout(life, scaleRequestBudget)
		defer requestCancel()
		dbTime := &atomic.Int64{}
		requestCtx = context.WithValue(requestCtx, scaleRequestKey{}, dbTime)
		requestID := ""
		if diagnostics.enabled() {
			requestID = "scale-http-" + uuid.New()
			requestCtx = scaleDiagnosticContext(requestCtx, "http:"+scaleHTTPKind(method, path), requestID)
			diagnostics.httpStart(requestID, method, path)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(body)).WithContext(requestCtx)
		if requestID != "" {
			req.Header.Set("X-Request-ID", requestID)
		}
		req.Host = "roomusic.test"
		req.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: token})
		if method != http.MethodGet {
			req.Header.Set("Origin", "http://roomusic.test")
			req.Header.Set("Content-Type", "application/json")
		}
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		response := httptest.NewRecorder()
		start := time.Now()
		application.Handler().ServeHTTP(response, req)
		elapsed, databaseMS := float64(time.Since(start))/float64(time.Millisecond), float64(dbTime.Load())/float64(time.Millisecond)
		diagnostics.httpEnd(requestCtx, requestID, method, path, scaleDiagnosticRoute(method, path), response, elapsed, databaseMS)
		return response, elapsed, databaseMS
	}
	getTimed := func(path string) (float64, float64) {
		r, ms, dbMS := request(http.MethodGet, path, nil, nil)
		if r.Code != 200 {
			guard.fail(fmt.Sprintf("GET %s: %d %s", path, r.Code, r.Body.String()))
			b.Fatal(guard.reason())
		}
		return ms, dbMS
	}
	get := func(path string) float64 { ms, _ := getTimed(path); return ms }

	if diagnostics.detailed {
		report.Notes = append(report.Notes, "在线回填开启SQL/事务/CATP与100ms activity/locks取证；diagnostics-before-cleanup.json保留取消之前的未结束事务。发布成功后停止采样和详细trace，正式p95无诊断并发。scan_background使用合成负载会话ID；每轮独立HTTP request_id与scan ID另记，不能按旧的单次扫描关联推断。")
	}
	report.Notes = append(report.Notes, "I8：36分钟从worker调度开始至搜索与艺人双方ready，仍是严格验收线；总预算默认90分钟可单独配置，不改变45秒请求预算。CATP完整应用批分布p95≤200ms、p99≤500ms；max只记录事务/phase/PID、取得/提交时间和COMMIT墙钟分解，不能据此单独认定fsync原因。性能失败保留并继续全部搜索/计划/队列后统一判定；操作或证据错误仍立即停止。", "读探针每1秒，真实扫描+M3每30秒锚定worker启动调度；慢轮不并发追赶，实际时间全部记录。音频文件始终不变、每轮核验scan ID的观察与目录；M3 label交替，revision取真实GET；保留原9900新旧词用例。")
	memoryCtx, stopMemory := context.WithCancel(ctx)
	memoryDone := make(chan struct{})
	defer func() { stopMemory(); <-memoryDone }()
	go func() { defer close(memoryDone); scaleSampleHeap(memoryCtx, peakHeap) }()

	// 比应用生命周期清理更晚注册：先保存未结束gate/事务，再停止采样，最后才取消worker/scan。
	defer func() {
		cleanupError("diagnostics.checkpoint", guard.checkpointBeforeCleanup())
		cleanupError("diagnostics.stop_online", diagnostics.stopOnline())
	}()
	fail := func(message string) {
		guard.fail(message)
		message = guard.reason()
		report.recordRunError("operation", fmt.Errorf("%s", message))
		writeReport()
		b.Fatal(message)
	}
	writeReport()

	func() {
		diagnostics.recording.Store(true)
		if diagnostics.detailed {
			if err := diagnostics.startSampler(ctx, scoped, diagnosticApplication); err != nil {
				fail(err.Error())
			}
		}
		tracer.mu.Lock()
		tracer.monitorGates = true
		tracer.mu.Unlock()
		start := time.Now()
		report.WorkerStartedAt = start.UTC()
		defer func() {
			if report.BackfillMS == 0 {
				report.BackfillMS = float64(time.Since(start)) / float64(time.Millisecond)
			}
			tracer.mu.Lock()
			gates := append([]float64(nil), tracer.gateWaits...)
			tracer.monitorGates = false
			tracer.mu.Unlock()
			if len(gates) > 0 {
				report.Foreground["catp_acquire_including_roundtrip"] = scaleStats(gates[0], gates, 0)
			}
		}()
		type workerResult struct {
			at  time.Time
			err error
		}
		worker := make(chan workerResult, 1)
		done := make(chan struct{})
		workerDone = done
		logger := slog.New(&scaleBatchHandler{base: slog.NewJSONHandler(logFile, nil), diagnostics: diagnostics, guard: guard})
		go func() {
			defer close(done)
			err := maintenance.Run(scaleDiagnosticContext(life, "maintenance", ""), db, logger, maintenance.RunOptions{RefreshArtistResolutions: personal.RefreshArtistResolution})
			if err == nil {
				var ready bool
				err = db.QueryRowContext(life, `SELECT count(*)=2 AND bool_and(status='ready' AND target_generation=published_generation
     AND textnorm_version=published_textnorm_version AND search_projection_version=published_search_projection_version)
     FROM catalog_projection_state WHERE capability IN ('search','artists')`).Scan(&ready)
				if err == nil && !ready {
					err = fmt.Errorf("maintenance返回但搜索与艺人未双方ready")
				}
			}
			at := time.Now()

			worker <- workerResult{at: at, err: err}
		}()
		loadDone := make(chan error, 1)
		go func() {
			err := load.run(life, done, db, request, start, probeRootID)
			if err != nil {
				guard.fail("回填期扫描/M3负载失败: " + err.Error())
			}
			loadDone <- err
		}()
		loadJoined := false
		defer func() {
			if !loadJoined {
				cleanupError("probe.checkpoint", guard.checkpointBeforeCleanup())
				stop()
				select {
				case <-loadDone:
				case <-time.After(10 * time.Second):
					cleanupError("probe.join", fmt.Errorf("在线负载清理未完成"))
				}
			}
		}()
		foreground := map[string][]float64{"ready": {}, "legacy_suggestions": {}, "audio": {}, "correction": {}}
		defer func() {
			for name, values := range foreground {
				if len(values) > 0 {
					report.Foreground[name] = scaleStats(values[0], append([]float64(nil), values...), 0)
				}
			}
		}()
		ticker := time.NewTicker(scaleReadProbeInterval)
		defer ticker.Stop()
		corrected, finished := false, false
		for !finished {
			select {
			case result := <-worker:
				report.BackfillMS = float64(result.at.Sub(start)) / float64(time.Millisecond)
				if result.err == nil {
					report.BothReadyAt = result.at.UTC()
					report.BackfillComplete = true
					report.BackfillPass = report.BackfillMS <= report.BackfillTargetMS
				}
				if reason := guard.reason(); reason != "" {
					fail(reason)
				}
				if result.err != nil {
					fail(result.err.Error())
				}
				holds, _ := diagnostics.batchReports(true)
				if holds.Samples == 0 || holds.UnconfirmedBatches != 0 || holds.EvidenceError != "" {
					fail(fmt.Sprintf("应用批证据不完整: %+v", holds))
				}
				report.recordCheck("backfill", report.BackfillPass, fmt.Sprintf("%.3fms / %.0fms", report.BackfillMS, report.BackfillTargetMS))
				report.recordCheck("catp", holds.Pass, fmt.Sprintf("samples=%d p95=%.3fms/200ms p99=%.3fms/500ms max=%.3fms（仅记录）", holds.Samples, holds.P95MS, holds.P99MS, holds.MaxMS))
				writeReport()
				finished = true
			case err := <-loadDone:
				loadJoined = true
				if err != nil {
					fail(err.Error())
				}
				// worker刚关闭done时，可能先收到负载退出；仍需取正式worker完成时间。
				loadDone = nil
			case <-life.Done():
				fail(life.Err().Error())
			case <-ticker.C:
				foreground["ready"] = append(foreground["ready"], get("/readyz"))
				foreground["legacy_suggestions"] = append(foreground["legacy_suggestions"], get("/api/v1/search/suggestions?q="+url.QueryEscape("第00999")))
				if probeTrack := load.probeTrack(); probeTrack != "" {
					r, ms, _ := request(http.MethodGet, audioPath(probeTrack), nil, map[string]string{"Range": "bytes=0-63"})
					foreground["audio"] = append(foreground["audio"], ms)
					if r.Code != 206 {
						fail(fmt.Sprintf("probe audio: %d %s", r.Code, r.Body))
					}
				}
				if !corrected {
					r, ms, _ := request(http.MethodPatch, "/api/v1/releases/30000000-0000-4000-8000-000000009900/corrections", []byte(`{"expected_revision":1,"changes":{"label":{"mode":"set","value":"在线修正厂牌"}}}`), map[string]string{"Idempotency-Key": uuid.New()})
					foreground["correction"] = append(foreground["correction"], ms)
					if r.Code != 200 {
						fail(fmt.Sprintf("correction during backfill: %d %s", r.Code, r.Body))
					}
					corrected = true
				}
			}
		}
		if !loadJoined {
			select {
			case err := <-loadDone:
				loadJoined = true
				if err != nil {
					fail(err.Error())
				}
			case <-life.Done():
				fail(life.Err().Error())
			}
		}
		load.copyTo(&report)
		if !corrected || len(foreground["audio"]) == 0 || len(report.OnlineLoad.Cycles) < 2 || report.OnlineLoad.ArtistPhaseCycles == 0 {
			fail("回填期持续扫描/修正/播放证据不完整（含艺人building阶段至少一轮扫描+M3）")
		}
		for name, values := range foreground {
			if len(values) > 0 {
				report.Foreground[name] = scaleStats(values[0], append([]float64(nil), values...), 0)
			}
		}
	}()
	writeReport()

	// 扫描succeeded发生在其资源清理之前；正式计时还须等待后台扫描完全退出。
	if err := application.WaitForScans(life); err != nil {
		fail(err.Error())
	}
	report.OnlineComplete = true
	report.OnlineAcceptance = diagnostics.onlineAcceptance(report.OnlineLoad, true)
	online := report.OnlineAcceptance
	report.recordCheck("m3", online.M3.Pass, fmt.Sprintf("成功=%d/%d p95=%.3fms/1000ms", online.M3.Successful, online.M3.Requests, online.M3.Latency.P95MS))
	report.recordCheck("scans", online.Scans.Pass, fmt.Sprintf("接受=%d 成功=%d 目录核验=%d 总请求=%d", online.Scans.Accepted, online.Scans.Succeeded, online.Scans.Verified, online.Scans.Requests))
	report.recordCheck("audio", online.Audio.Pass, fmt.Sprintf("请求=%d 503=%d 其他错误=%d", online.Audio.Requests, online.Audio.ServiceUnavailable, online.Audio.OtherFailures))
	report.recordCheck("online_search", online.Search.Pass, fmt.Sprintf("请求=%d 503=%d 其他错误=%d", online.Search.Requests, online.Search.ServiceUnavailable, online.Search.OtherFailures))
	writeReport()
	// 与正式p95严格隔离：等采样退出并关闭独立连接，详细trace不再新增记录。
	if err := diagnostics.stopOnline(); err != nil {
		b.Fatal(err)
	}
	postgrestest.AnalyzeSchema(b, ctx, db)
	for _, table := range []string{"tracks", "releases", "release_groups", "track_credits", "catalog_search_values", "catalog_projection_subject_state", "artists", "artist_name_keys", "artist_names", "artist_credits", "artist_effective_credits", "artist_profiles", "artist_search_names", "artist_projection_releases"} {
		var count int64
		if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			b.Fatal(err)
		}
		report.Counts[table] = count
		var size int64
		if err = db.QueryRowContext(ctx, "SELECT pg_total_relation_size($1::regclass)", table).Scan(&size); err != nil {
			b.Fatal(err)
		}
		report.Sizes[table] = size
	}
	for _, index := range []string{"catalog_search_values_trgm_idx", "catalog_search_values_short_idx", "catalog_search_values_exact_idx", "catalog_projection_title_order_idx", "catalog_search_release_group_idx", "artist_effective_artist_idx", "artist_profiles_sort_idx", "artist_search_names_trgm_idx", "artist_search_names_short_idx", "artist_search_names_exact_idx"} {
		var size int64
		if err = db.QueryRowContext(ctx, "SELECT pg_relation_size($1::regclass)", index).Scan(&size); err != nil {
			b.Fatal(err)
		}
		report.Sizes[index] = size
	}
	for _, status := range []string{"present", "missing"} {
		var count int64
		if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tracks WHERE source_status=$1`, status).Scan(&count); err != nil {
			b.Fatal(err)
		}
		report.Counts["tracks_"+status] = count
	}
	for _, setting := range []string{"server_version", "shared_buffers", "work_mem", "max_connections", "server_encoding", "jit", "effective_cache_size", "random_page_cost", "max_parallel_workers_per_gather", "max_worker_processes"} {
		var value string
		if err = db.QueryRowContext(ctx, "SELECT current_setting($1)", setting).Scan(&value); err != nil {
			b.Fatal(err)
		}
		report.PG[setting] = value
	}
	var locale, ctype string
	if err = db.QueryRowContext(ctx, "SELECT datcollate,datctype FROM pg_database WHERE datname=current_database()").Scan(&locale, &ctype); err != nil {
		b.Fatal(err)
	}
	report.PG["collation"], report.PG["ctype"] = locale, ctype
	if err := scaleAssertArtistFixture(ctx, db, &report); err != nil {
		fail(err.Error())
	}
	report.recordCheck("fixture", true, "原规模/倾斜/别名及署名展开断言全部通过")
	if err := db.QueryRowContext(ctx, `SELECT e.extversion,n.nspname FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='pg_trgm'`).Scan(&locale, &ctype); err != nil {
		fail(err.Error())
	}
	report.PG["pg_trgm_version"], report.PG["pg_trgm_schema"] = locale, ctype
	report.PG["image_digest"] = os.Getenv("ROOMUSIC_TEST_IMAGE_DIGEST")
	report.Notes = append(report.Notes, "ColdMS为新数据库连接的首次HTTP值，不声称清空PostgreSQL/操作系统页缓存。前台按I8验收M3整轮p95≤1000ms、全部扫描成功及播放/搜索无503；队列不新增时延SLA；搜索首屏仍为300ms/单字1000ms。")
	iterations := 30
	queries := []string{"交响曲 第00999号", "交响曲 第009", "周杰倫", "周杰伦", "Teresa Teng", "Alias 01001", "爱", "周", "交响", "a", "%", "_", "~", "不存在的长查询xyz", "久石譲", "Artist 15001", "热门厂牌", "SRCL-1234", "srcl 1234", "非代表命中", "在线修正厂牌", "厂牌 9900", "missingonlyneedle", "boundary999", "boundary1000", "boundary1001"}
	writeReport()
	for caseIndex, q := range queries {
		escaped := url.QueryEscape(q)
		target := 300.0
		if len([]rune(strings.TrimSpace(q))) == 1 {
			target = 1000
		}
		lastSuggestionDB := 0.0
		validateSuggestions, validateResults := true, true
		suggestion := func() float64 {
			response, ms, dbMS := request(http.MethodGet, "/api/v1/search/suggestions?limit=5&q="+escaped, nil, nil)
			if response.Code != 200 {
				fail(fmt.Sprintf("suggestions q=%q: %d %s", q, response.Code, response.Body))
			}
			if validateSuggestions {
				if err := scaleValidateSuggestions(response.Body.Bytes()); err != nil {
					fail(err.Error())
				}
				validateSuggestions = false
			}
			lastSuggestionDB = dbMS
			return ms
		}
		lastAlbums, lastTracks, lastArtists := 0.0, 0.0, 0.0
		lastAlbumsDB, lastTracksDB, lastArtistsDB := 0.0, 0.0, 0.0
		results := func() float64 {
			start := time.Now()
			type measuredResponse struct {
				kind string
				ms   float64
				dbMS float64
				err  error
				body []byte
			}
			failures := make(chan measuredResponse, 3)
			for _, kind := range []string{"albums", "tracks", "artists"} {
				go func(kind string) {
					path := "/api/v1/search?type=" + kind + "&page_size=50&q=" + escaped
					response, elapsed, dbMS := request(http.MethodGet, path, nil, nil)
					if response.Code != http.StatusOK {
						failures <- measuredResponse{kind: kind, ms: elapsed, dbMS: dbMS, err: fmt.Errorf("GET %s: %d %s", path, response.Code, response.Body.String())}
						return
					}
					failures <- measuredResponse{kind: kind, ms: elapsed, dbMS: dbMS, body: response.Body.Bytes()}
				}(kind)
			}
			var failure error
			responses := make([]measuredResponse, 0, 3)
			for range 3 {
				result := <-failures
				responses = append(responses, result)
				if result.err != nil {
					failure = result.err
				}
				if result.kind == "albums" {
					lastAlbums, lastAlbumsDB = result.ms, result.dbMS
				} else if result.kind == "tracks" {
					lastTracks, lastTracksDB = result.ms, result.dbMS
				} else {
					lastArtists, lastArtistsDB = result.ms, result.dbMS
				}
			}
			elapsed := float64(time.Since(start)) / float64(time.Millisecond)
			if failure != nil {
				fail(failure.Error())
			}
			if validateResults {
				for _, response := range responses {
					if err := scaleValidateSearch(q, response.kind, response.body); err != nil {
						fail(err.Error())
					}
				}
				validateResults = false
			}
			return elapsed
		}
		item := scaleCase{Query: q}
		coldS, coldR := 0.0, 0.0
		captured := tracer.collect(func() {
			scaleColdConnections(db)
			coldS = suggestion()
			scaleColdConnections(db)
			coldR = results()
			scaleColdConnections(db)
			item.OldReleasesMS = get("/api/v1/releases?q=" + escaped)
			scaleColdConnections(db)
			item.OldGroupsMS = get("/api/v1/release-groups?q=" + escaped)
		})
		coldAlbums, coldTracks, coldArtists := lastAlbums, lastTracks, lastArtists
		plans := make([]map[string]any, 0, len(captured))
		for _, query := range captured {
			begin := time.Now()
			raw, explainErr := scaleExplain(ctx, db, query)
			if explainErr != nil {
				err = explainErr
				b.Fatalf("explain: %v\n%s", err, query.SQL)
			}
			plans = append(plans, map[string]any{"query": query, "plan": raw, "wall_ms": float64(time.Since(begin)) / float64(time.Millisecond)})
		}
		file := fmt.Sprintf("explain-%02d.json", caseIndex+1)
		encoded, err := json.MarshalIndent(plans, "", "  ")
		if err != nil {
			b.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(output, file), encoded, 0644); err != nil {
			b.Fatal(err)
		}
		report.PlanFiles = append(report.PlanFiles, file)
		report.recordCheck(fmt.Sprintf("search_%02d_explain", caseIndex+1), len(plans) > 0, fmt.Sprintf("生产SQL计划%d份，%s", len(plans), file))
		for warm := 0; warm < 5; warm++ {
			suggestion()
			results()
		}
		svalues, rvalues := make([]float64, iterations), make([]float64, iterations)
		avalues, tvalues, arvalues := make([]float64, iterations), make([]float64, iterations), make([]float64, iterations)
		sdb, adb, tdb, ardb, rdb := make([]float64, iterations), make([]float64, iterations), make([]float64, iterations), make([]float64, iterations), make([]float64, iterations)
		measuredQueries := tracer.collect(func() {
			for i := 0; i < iterations; i++ {
				svalues[i] = suggestion()
				rvalues[i] = results()
				avalues[i], tvalues[i], arvalues[i] = lastAlbums, lastTracks, lastArtists
				sdb[i], adb[i], tdb[i], ardb[i], rdb[i] = lastSuggestionDB, lastAlbumsDB, lastTracksDB, lastArtistsDB, lastAlbumsDB+lastTracksDB+lastArtistsDB
			}
		})
		queryTiming, err := json.MarshalIndent(measuredQueries, "", "  ")
		if err != nil {
			b.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(output, fmt.Sprintf("http-sql-%02d.json", caseIndex+1)), queryTiming, 0644); err != nil {
			b.Fatal(err)
		}
		item.Suggestions, item.Results = scaleStats(coldS, svalues, target), scaleStats(coldR, rvalues, target)
		item.Albums, item.Tracks, item.Artists = scaleStats(coldAlbums, avalues, target), scaleStats(coldTracks, tvalues, target), scaleStats(coldArtists, arvalues, target)
		item.SuggestionsDBMS, item.AlbumsDBMS, item.TracksDBMS, item.ArtistsDBMS, item.ResultsDBWorkMS = scaleDatabaseStats(sdb), scaleDatabaseStats(adb), scaleDatabaseStats(tdb), scaleDatabaseStats(ardb), scaleDatabaseStats(rdb)
		report.Cases = append(report.Cases, item)
		report.recordCheck(fmt.Sprintf("search_%02d_suggestions", caseIndex+1), item.Suggestions.Pass, fmt.Sprintf("q=%q p95=%.3fms target=%.0fms", q, item.Suggestions.P95MS, target))
		report.recordCheck(fmt.Sprintf("search_%02d_results", caseIndex+1), item.Results.Pass, fmt.Sprintf("q=%q p95=%.3fms target=%.0fms", q, item.Results.P95MS, target))
		writeReport()
		b.Logf("q=%q suggestions p95=%.2fms results p95=%.2fms target=%.0fms", q, item.Suggestions.P95MS, item.Results.P95MS, target)
		// I8性能失败不跳过旧路由、后续查询/EXPLAIN及队列；最后统一判定。
		oldReleasePath, oldGroupPath := "/api/v1/releases?q="+escaped, "/api/v1/release-groups?q="+escaped
		for warm := 0; warm < 5; warm++ {
			get(oldReleasePath)
			get(oldGroupPath)
		}
		oldReleases, oldGroups, oldReleasesDB, oldGroupsDB := make([]float64, 30), make([]float64, 30), make([]float64, 30), make([]float64, 30)
		for i := range oldReleases {
			oldReleases[i], oldReleasesDB[i] = getTimed(oldReleasePath)
			oldGroups[i], oldGroupsDB[i] = getTimed(oldGroupPath)
		}
		item.OldReleases, item.OldGroups = scaleStats(item.OldReleasesMS, oldReleases, 0), scaleStats(item.OldGroupsMS, oldGroups, 0)
		item.OldReleasesDBMS, item.OldGroupsDBMS = scaleDatabaseStats(oldReleasesDB), scaleDatabaseStats(oldGroupsDB)
		report.Cases[len(report.Cases)-1] = item
		writeReport()
	}
	deep := get("/api/v1/search?type=tracks&page=100&q=" + url.QueryEscape("交响"))
	report.Foreground["page_100"] = scaleStats(deep, []float64{deep}, 0)
	report.recordCheck("page_100", true, "深页退化记录完成，不新增首屏之外的SLA")
	writeReport()
	report.Queues = scaleMeasureArtistQueues(b, ctx, db, tracer, application.Handler(), token, output, request)
	for _, queue := range report.Queues {
		report.recordCheck("queue_"+queue.Order, true, "10000首完整形状、5次预热/30次测量、生产EXPLAIN及在途取消全部完成")
	}
	report.finishCollection(len(queries))
	writeReport()
	b.ReportMetric(report.BackfillMS, "backfill-ms")
	if !report.Pass {
		b.Errorf("I8统一验收FAIL：已完成全部测量，%d项未达标；详见summary.json的Checks/AcceptanceFailures", len(report.AcceptanceFailures))
	}

}

// scaleExplain 复用生产搜索的只读快照/JIT 设置，不能拿全局 JIT 的计划解释 HTTP 时延。
func scaleExplain(ctx context.Context, db *sql.DB, query scaleQuery) (json.RawMessage, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SET LOCAL jit = off"); err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err = tx.QueryRowContext(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+query.SQL, query.Args...).Scan(&raw); err != nil {
		return nil, err
	}
	return raw, tx.Commit()
}
