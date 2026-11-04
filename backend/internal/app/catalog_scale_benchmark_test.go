package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/maintenance"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
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
}

func (s *scaleTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
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
	if s.enabled && (strings.Contains(data.SQL, "catalog_search_values") || strings.Contains(data.SQL, "selected_tracks") || strings.Contains(data.SQL, "release_effective")) {
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

func (s *scaleTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
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
	return scaleLatency{cold, values[len(values)/2], values[i], len(values), target, values[i] <= target}
}

// SQL累计墙钟覆盖鉴权、状态、事务、搜索与补全；连接池等待仍由完整HTTP计时覆盖。
// 两栏并发的SQL工作量取和，不冒充整屏关键路径。
type scaleDatabaseLatency struct{ MedianMS, P95MS float64 }

func scaleDatabaseStats(values []float64) scaleDatabaseLatency {
	stats := scaleStats(0, values, 0)
	return scaleDatabaseLatency{stats.MedianMS, stats.P95MS}
}

type scaleCase struct {
	Query                                                    string
	Suggestions, Results                                     scaleLatency
	Albums, Tracks                                           scaleLatency
	OldReleasesMS, OldGroupsMS                               float64
	SuggestionsDBMS, AlbumsDBMS, TracksDBMS, ResultsDBWorkMS scaleDatabaseLatency
}
type scaleReport struct {
	CPUCount   int
	BackfillMS float64
	Counts     map[string]int64
	Sizes      map[string]int64
	PG         map[string]string
	Foreground map[string]scaleLatency
	Cases      []scaleCase
	PlanFiles  []string
	HeapBytes  uint64
	Notes      []string
}

// BenchmarkPostgreSQLSearchScale 是显式 opt-in 的合成库验收，不进入普通 go test。
// 使用 -run '^$' -bench '^BenchmarkPostgreSQLSearchScale$' -benchtime=1x；输出目录通过环境变量指定。
func BenchmarkPostgreSQLSearchScale(b *testing.B) {
	if b.N != 1 {
		b.Fatal("请使用 -benchtime=1x，避免重复回填十万首")
	}
	output := os.Getenv("ROOMUSIC_SEARCH_SCALE_OUTPUT")
	if output == "" {
		b.Fatal("必须指定 ROOMUSIC_SEARCH_SCALE_OUTPUT")
	}
	if err := os.MkdirAll(output, 0755); err != nil {
		b.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	// 仅复用本线独立测试库中的合成 schema；用于查询优化，不能冒充本轮回填计时。
	reuseSchema := os.Getenv("ROOMUSIC_SEARCH_SCALE_REUSE_SCHEMA")
	var scoped string
	if reuseSchema == "" {
		scoped = postgrestest.NewSchema(b, "search_scale_")
	} else {
		if !regexp.MustCompile(`^search_scale_[a-f0-9]{32}$`).MatchString(reuseSchema) {
			b.Fatal("复用 schema 必须是本基准产生的 search_scale_<32位小写十六进制>")
		}
		var err error
		scoped, err = postgrestest.ScopedURL(postgrestest.BaseURL(b), reuseSchema)
		if err != nil {
			b.Fatal(err)
		}
	}
	// 复用分支只测查询，不重放或改写开发期迁移校验和；正式首填仍从空schema执行迁移。
	if reuseSchema == "" {
		migrated, err := postgres.Open(ctx, scoped)
		if err != nil {
			b.Fatal(err)
		}
		if err = migrated.Close(); err != nil {
			b.Fatal(err)
		}
	}
	tracer := &scaleTracer{}
	pgconfig, err := pgx.ParseConfig(scoped)
	if err != nil {
		b.Fatal(err)
	}
	pgconfig.Tracer = tracer
	db := stdlib.OpenDB(*pgconfig)
	db.SetMaxOpenConns(12)
	defer db.Close()
	exec := func(query string, args ...any) {
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			b.Fatal(err)
		}
	}
	if reuseSchema == "" {
		fixture, err := os.ReadFile("testdata/search-scale.sql")
		if err != nil {
			b.Fatal(err)
		}
		exec(string(fixture))
	}
	root := b.TempDir()
	data := b.TempDir()
	if reuseSchema == "" {
		music := filepath.Join(root, "probe")
		if err = os.Mkdir(music, 0755); err != nil {
			b.Fatal(err)
		}
		content := parsertest.FLACWithAudio(b, [16]byte{41}, bytes.Repeat([]byte{41}, 4096), "TITLE=在线探针", "ARTIST=探针艺人", "ALBUM=在线探针专辑")
		if err = os.WriteFile(filepath.Join(music, "probe.flac"), content, 0644); err != nil {
			b.Fatal(err)
		}
		exec(`INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,$2,'active',1)`, uuid.New(), music)
	}
	userID, token := uuid.New(), identity.GenerateSessionToken()
	exec(`INSERT INTO users(id,username,password_hash,role) VALUES($1::uuid,$2,'test-only','admin')`, userID, "scale-"+userID)
	exec(`INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2::uuid,NOW()+INTERVAL '2 hours')`, identity.HashSessionToken(token), userID)
	if err = InitializeCatalog(ctx, db); err != nil {
		b.Fatal(err)
	}
	life, stop := context.WithCancel(ctx)
	defer stop()
	application := New(life, config.Server{AllowedLibraryRoots: []string{root}, DataDirectory: data}, db, testFrontend(), slog.New(slog.NewTextHandler(io.Discard, nil)), func(o *options) { o.pauseCatalogMaintenance = true })
	defer func() {
		stop()
		wait, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		if err := application.WaitForScans(wait); err != nil {
			b.Error(err)
		}
	}()
	request := func(method, path string, body []byte, headers map[string]string) (*httptest.ResponseRecorder, float64, float64) {
		requestCtx, requestCancel := context.WithTimeout(ctx, 45*time.Second)
		defer requestCancel()
		dbTime := &atomic.Int64{}
		requestCtx = context.WithValue(requestCtx, scaleRequestKey{}, dbTime)
		req := httptest.NewRequest(method, path, bytes.NewReader(body)).WithContext(requestCtx)
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
		return response, float64(time.Since(start)) / float64(time.Millisecond), float64(dbTime.Load()) / float64(time.Millisecond)
	}
	getTimed := func(path string) (float64, float64) {
		r, ms, dbMS := request(http.MethodGet, path, nil, nil)
		if r.Code != 200 {
			b.Fatalf("GET %s: %d %s", path, r.Code, r.Body.String())
		}
		return ms, dbMS
	}
	get := func(path string) float64 { ms, _ := getTimed(path); return ms }
	logFile, err := os.Create(filepath.Join(output, "backfill.jsonl"))
	if err != nil {
		b.Fatal(err)
	}
	defer logFile.Close()
	report := scaleReport{CPUCount: runtime.NumCPU(), Counts: map[string]int64{}, Sizes: map[string]int64{}, PG: map[string]string{}, Foreground: map[string]scaleLatency{}, Notes: []string{"④不创建艺人实体/别名；夹具包含两万姓名键、每轨两个拼接艺人、四类credits与五万轨热门署名。", "生产 handler 计时包括鉴权、数据库等待、计数、分页、摘要补全和序列化；本机 httptest 不含外部网络。", "合成十万轨根标为 disabled，只扫描独立探针根，避免不存在的合成音频被负向对账；present-only 搜索仍覆盖十万轨。"}}
	if reuseSchema == "" {
		tracer.mu.Lock()
		tracer.monitorGates = true
		tracer.mu.Unlock()
		start := time.Now()
		worker := make(chan error, 1)
		go func() { worker <- maintenance.Run(life, db, slog.New(slog.NewJSONHandler(logFile, nil))) }()
		// 在线回填中运行真实扫描，并用扫描产出的实际文件持续验证播放。
		scan, scanMS, _ := request(http.MethodPost, "/api/v1/scans", []byte(`{}`), nil)
		if scan.Code != 202 {
			b.Fatalf("start probe scan: %s", scan.Body.String())
		}
		var scanResponse struct {
			ID string `json:"id"`
		}
		if err = json.Unmarshal(scan.Body.Bytes(), &scanResponse); err != nil {
			b.Fatal(err)
		}
		probeTrack := ""
		scanDone := false
		corrected := false
		foreground := map[string][]float64{"ready": {}, "legacy_suggestions": {}, "audio": {}, "scan_start": {scanMS}, "correction": {}}
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		finished := false
		for !finished {
			select {
			case err := <-worker:
				if err != nil {
					b.Fatal(err)
				}
				finished = true
			case <-ctx.Done():
				b.Fatal(ctx.Err())
			case <-ticker.C:
				foreground["ready"] = append(foreground["ready"], get("/readyz"))
				foreground["legacy_suggestions"] = append(foreground["legacy_suggestions"], get("/api/v1/search/suggestions?q="+url.QueryEscape("第00999")))
				if !scanDone {
					var state string
					if err := db.QueryRowContext(ctx, `SELECT status FROM scan_runs WHERE id=$1::uuid`, scanResponse.ID).Scan(&state); err != nil {
						b.Fatal(err)
					}
					if state == "succeeded" {
						scanDone = true
						if err = db.QueryRowContext(ctx, `SELECT id::text FROM tracks WHERE title='在线探针'`).Scan(&probeTrack); err != nil {
							b.Fatal(err)
						}
					} else if state != "running" && state != "pending" {
						b.Fatalf("probe scan: %s", state)
					}
				}
				if probeTrack != "" {
					r, ms, _ := request(http.MethodGet, audioPath(probeTrack), nil, map[string]string{"Range": "bytes=0-63"})
					if r.Code != 206 {
						b.Fatalf("probe audio: %d %s", r.Code, r.Body.String())
					}
					foreground["audio"] = append(foreground["audio"], ms)
				}
				if !corrected {
					r, ms, _ := request(http.MethodPatch, "/api/v1/releases/30000000-0000-4000-8000-000000000001/corrections", []byte(`{"expected_revision":1,"changes":{"label":{"mode":"set","value":"在线修正厂牌"}}}`), map[string]string{"Idempotency-Key": uuid.New()})
					if r.Code != 200 {
						b.Fatalf("correction during backfill: %d %s", r.Code, r.Body.String())
					}
					foreground["correction"] = append(foreground["correction"], ms)
					corrected = true
				}
			}
		}
		report.BackfillMS = float64(time.Since(start)) / float64(time.Millisecond)
		tracer.mu.Lock()
		gates := append([]float64(nil), tracer.gateWaits...)
		tracer.monitorGates = false
		tracer.mu.Unlock()
		if len(gates) > 0 {
			report.Foreground["catp_acquire_including_roundtrip"] = scaleStats(gates[0], gates, 200)
		}
		if !scanDone || !corrected || len(foreground["audio"]) == 0 {
			b.Fatal("回填期并发扫描/修正/播放证据不完整")
		}
		for name, values := range foreground {
			if len(values) > 0 {
				report.Foreground[name] = scaleStats(values[0], values, 1000)
			}
		}
	} else {
		state, err := catalog.NewStore(db).LoadCatalogState(ctx)
		if err != nil || state.SearchMode != "normalized" {
			b.Fatalf("复用库未发布：%+v %v", state, err)
		}
		report.Notes = append(report.Notes, "本轮复用已完成回填的合成 schema；BackfillMS=0 不代表回填瞬时完成，实际回填计时见首填报告。")
	}
	postgrestest.AnalyzeSchema(b, ctx, db)
	for _, table := range []string{"tracks", "releases", "release_groups", "track_credits", "catalog_search_values", "catalog_projection_subject_state"} {
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
	for _, index := range []string{"catalog_search_values_trgm_idx", "catalog_search_values_short_idx", "catalog_search_values_exact_idx", "catalog_projection_title_order_idx", "catalog_search_release_group_idx"} {
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
	for _, setting := range []string{"server_version", "shared_buffers", "work_mem", "max_connections", "server_encoding", "jit"} {
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
	iterations := 30
	if v := os.Getenv("ROOMUSIC_SEARCH_SCALE_ITERATIONS"); v != "" {
		iterations, err = strconv.Atoi(v)
		if err != nil || iterations < 1 {
			b.Fatal("iterations 必须是正整数")
		}
	}
	queries := []string{"交响曲 第00999号", "交响曲 第009", "周杰倫", "周杰伦", "爱", "周", "交响", "a", "%", "_", "~", "不存在的长查询xyz", "久石譲", "热门厂牌", "SRCL-1234", "srcl 1234", "在线修正厂牌"}
	if v := os.Getenv("ROOMUSIC_SEARCH_SCALE_QUERIES"); v != "" {
		queries = strings.Split(v, "|")
	}
	writeReport := func() {
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		report.HeapBytes = memory.HeapAlloc
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			b.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(output, "summary.json"), append(body, '\n'), 0644); err != nil {
			b.Fatal(err)
		}
	}
	writeReport()
	for caseIndex, q := range queries {
		escaped := url.QueryEscape(q)
		target := 300.0
		if len([]rune(q)) == 1 {
			target = 1000
		}
		lastSuggestionDB := 0.0
		suggestion := func() float64 {
			ms, dbMS := getTimed("/api/v1/search/suggestions?limit=5&q=" + escaped)
			lastSuggestionDB = dbMS
			return ms
		}
		lastAlbums, lastTracks := 0.0, 0.0
		lastAlbumsDB, lastTracksDB := 0.0, 0.0
		results := func() float64 {
			start := time.Now()
			type measuredResponse struct {
				kind string
				ms   float64
				dbMS float64
				err  error
			}
			failures := make(chan measuredResponse, 2)
			for _, kind := range []string{"albums", "tracks"} {
				go func(kind string) {
					path := "/api/v1/search?type=" + kind + "&page_size=50&q=" + escaped
					response, elapsed, dbMS := request(http.MethodGet, path, nil, nil)
					if response.Code != http.StatusOK {
						failures <- measuredResponse{kind, elapsed, dbMS, fmt.Errorf("GET %s: %d %s", path, response.Code, response.Body.String())}
						return
					}
					failures <- measuredResponse{kind: kind, ms: elapsed, dbMS: dbMS}
				}(kind)
			}
			var failure error
			for range 2 {
				result := <-failures
				if result.err != nil {
					failure = result.err
				}
				if result.kind == "albums" {
					lastAlbums, lastAlbumsDB = result.ms, result.dbMS
				} else {
					lastTracks, lastTracksDB = result.ms, result.dbMS
				}
			}
			if failure != nil {
				b.Fatal(failure)
			}
			return float64(time.Since(start)) / float64(time.Millisecond)
		}
		item := scaleCase{Query: q}
		coldS, coldR := 0.0, 0.0
		captured := tracer.collect(func() {
			coldS = suggestion()
			coldR = results()
			item.OldReleasesMS = get("/api/v1/releases?q=" + escaped)
			item.OldGroupsMS = get("/api/v1/release-groups?q=" + escaped)
		})
		coldAlbums, coldTracks := lastAlbums, lastTracks
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
		for warm := 0; warm < 5; warm++ {
			suggestion()
			results()
		}
		svalues, rvalues := make([]float64, iterations), make([]float64, iterations)
		avalues, tvalues := make([]float64, iterations), make([]float64, iterations)
		sdb, adb, tdb, rdb := make([]float64, iterations), make([]float64, iterations), make([]float64, iterations), make([]float64, iterations)
		measuredQueries := tracer.collect(func() {
			for i := 0; i < iterations; i++ {
				svalues[i] = suggestion()
				rvalues[i] = results()
				avalues[i], tvalues[i] = lastAlbums, lastTracks
				sdb[i], adb[i], tdb[i], rdb[i] = lastSuggestionDB, lastAlbumsDB, lastTracksDB, lastAlbumsDB+lastTracksDB
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
		item.Albums, item.Tracks = scaleStats(coldAlbums, avalues, target), scaleStats(coldTracks, tvalues, target)
		item.SuggestionsDBMS, item.AlbumsDBMS, item.TracksDBMS, item.ResultsDBWorkMS = scaleDatabaseStats(sdb), scaleDatabaseStats(adb), scaleDatabaseStats(tdb), scaleDatabaseStats(rdb)
		report.Cases = append(report.Cases, item)
		writeReport()
		b.Logf("q=%q suggestions p95=%.2fms results p95=%.2fms target=%.0fms", q, item.Suggestions.P95MS, item.Results.P95MS, target)
	}
	deep := get("/api/v1/search?type=tracks&page=100&q=" + url.QueryEscape("交响"))
	report.Foreground["page_100"] = scaleStats(deep, []float64{deep}, 1000)
	writeReport()
	b.ReportMetric(report.BackfillMS, "backfill-ms")
	for _, item := range report.Cases {
		if !item.Suggestions.Pass || !item.Results.Pass {
			b.Errorf("性能未达标 q=%q：建议 p95=%.2fms，首屏 p95=%.2fms，目标=%.0fms", item.Query, item.Suggestions.P95MS, item.Results.P95MS, item.Results.TargetMS)
		}
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
