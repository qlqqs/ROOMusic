package app

import (
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
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/maintenance"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

const (
	i10Warmups      = 5
	i10Measurements = 30
	i10HotTargetMS  = 1000.0
)

// 运行入口（由操作者提供隔离测试库，不启动容器）：
// ROOMUSIC_I10_MODE=create ROOMUSIC_I10_OUTPUT=/仓外/i10-search \
// go test ./internal/app -run '^$' -bench '^BenchmarkPostgreSQLSearchI10$' -benchtime=1x -timeout=160m
// 后续保持同一 OUTPUT/数据库，显式设置 ROOMUSIC_I10_SCHEMA=fixture.json 中的 schema：
// MODE=reuse 仅热门词 5 warm/30 sample + SQL 计划；MODE=explain 仅热门词实际 SQL 计划。
// MODE=cleanup 仅删除经 manifest/数据库标记双重验证的 schema，不删除任何仓外证据。
// create/reuse 均不自动删除 schema；最终 compose down -v 仍由主控执行。
func BenchmarkPostgreSQLSearchI10(b *testing.B) {
	if b.N != 1 {
		b.Fatal("I10 必须使用 -benchtime=1x")
	}
	actions, err := i10SelectActions(os.Getenv("ROOMUSIC_I10_MODE"))
	if err != nil {
		b.Fatal(err)
	}
	budget, err := i10Budget(os.Getenv("ROOMUSIC_I10_TIMEOUT"))
	if err != nil {
		b.Fatal(err)
	}
	repo, err := filepath.Abs("../../..")
	if err != nil {
		b.Fatal(err)
	}
	output, err := i10EvidenceDirectory(os.Getenv("ROOMUSIC_I10_OUTPUT"), repo)
	if err != nil {
		b.Fatal(err)
	}
	report := i10Report{Mode: os.Getenv("ROOMUSIC_I10_MODE"), CPUCount: runtime.NumCPU(), RAMBytes: scaleRAMBytes(), PoolMax: 12, ResultsConcurrency: 3, TotalBudget: budget.String(), Counts: map[string]int64{}, PG: map[string]string{}, Notes: []string{
		"I10 仅热门周杰伦/周杰倫建议与并发三栏首屏 p95≤1000ms；各 5 次预热、30 次测量。",
		"真实生产 Handler 含鉴权/计数/页查询/安全摘要/序列化；httptest 不含外部网络。ColdMS 仅新连接，不清空页缓存。",
		"原十万 present/一万 missing 夹具仅 create 构建一次并等待真实 maintenance ready；不计时、不诊断、不验收回填，无在线负载。",
		"爱/周、深页和两种 10000 首队列只在 create 少量记录，另做一次在途取消；保留每次响应字节数和计数，无性能门槛；reuse 不重复补测、不重建、不回填。",
		"性能失败继续收集其他热门词、补测与全部实际 SQL EXPLAIN；schema 永久保留直至显式 cleanup 或主控删除隔离卷。",
	}}
	// 先于资源 Cleanup 注册，最终报告包含关闭错误；没有自动 DROP。
	var runDir string
	b.Cleanup(func() {
		i10Finalize(&report, b.Failed())
		if runDir != "" {
			if err := i10WriteJSON(filepath.Join(runDir, "summary.json"), report); err != nil {
				b.Error(err)
			}
		}
	})
	cleanupError := func(stage string, err error) {
		if err != nil {
			report.Errors = append(report.Errors, "cleanup."+stage+": "+err.Error())
			b.Error(err)
		}
	}
	runDir, err = i10AttemptDirectory(output)
	if err != nil {
		b.Fatal(err)
	}
	runID := filepath.Base(runDir)
	report.Manifest = filepath.Join(output, "fixture.json")
	if err := i10WriteJSON(filepath.Join(runDir, "summary.json"), report); err != nil {
		b.Fatal(err)
	}
	b.Logf("I10 证据目录: %s", runDir)
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	tracer := &scaleTracer{} // nil diagnostics 的方法均安全；没有回填 trace/activity/CATP 采样。
	db, manifest := i10OpenSchema(b, ctx, output, report.Mode, actions, tracer, cleanupError)
	report.Schema = manifest.Schema
	if actions.Drop {
		report.Complete = true
		report.Pass = true
		return
	}
	must := func(err error) {
		if err != nil {
			report.Errors = append(report.Errors, err.Error())
			b.Fatal(err)
		}
	}
	save := func() {
		interim := report
		interim.Pass = false // 资源清理全部完成前，任何落盘摘要都不宣称最终通过。
		must(i10WriteJSON(filepath.Join(runDir, "summary.json"), interim))
	}
	record := func(err error) {
		if err != nil {
			report.Errors = append(report.Errors, err.Error())
			b.Log(err)
		}
	}
	save()
	fixture, err := os.ReadFile("testdata/search-scale.sql")
	must(err)
	must(i10ValidateFixtureHash(fixture, manifest.FixtureSHA256))
	err = i10BuildOnce(actions, func() error {
		_, err := db.ExecContext(ctx, string(fixture))
		return err
	}, func() error {
		if err := InitializeCatalog(ctx, db); err != nil {
			return err
		}
		// 不接历史性能 guard/logger；这里只等待完成，不产生回填性能证据。
		return maintenance.Run(ctx, db, slog.New(slog.NewJSONHandler(io.Discard, nil)), maintenance.RunOptions{RefreshArtistResolutions: personal.RefreshArtistResolution})
	}, func() error {
		var ready bool
		err := db.QueryRowContext(ctx, `SELECT count(*)=2 AND bool_and(status='ready' AND target_generation=published_generation
   AND textnorm_version=published_textnorm_version AND search_projection_version=published_search_projection_version)
   FROM catalog_projection_state WHERE capability IN ('search','artists')`).Scan(&ready)
		if err != nil {
			return err
		}
		if !ready {
			return fmt.Errorf("搜索/艺人未同时 ready；reuse 禁止重建或再次回填")
		}
		return nil
	})
	must(err)
	manifest.Ready = true
	must(i10WriteJSON(report.Manifest, manifest))
	// 仅测试夹具统计，严格限定当前 schema；生产路径不引入 ANALYZE。
	if actions.Build {
		postgrestest.AnalyzeSchema(b, ctx, db)
	}
	// 复用同样证明原规模、热门倾斜与别名覆盖；这里只 SELECT，不重填或改统计。
	must(i10FixtureFacts(ctx, db, &report))
	save()
	userID, token := uuid.New(), identity.GenerateSessionToken()
	_, err = db.ExecContext(ctx, `INSERT INTO users(id,username,password_hash,role) VALUES($1::uuid,$2,'test-only','admin')`, userID, "i10-"+userID)
	must(err)
	b.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := db.ExecContext(clean, `DELETE FROM sessions WHERE user_id=$1::uuid`, userID); err != nil {
			cleanupError("sessions", err)
		}
		if _, err := db.ExecContext(clean, `DELETE FROM users WHERE id=$1::uuid`, userID); err != nil {
			cleanupError("users", err)
		}
	})
	_, err = db.ExecContext(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2::uuid,NOW()+($3::double precision*INTERVAL '1 second'))`, identity.HashSessionToken(token), userID, (budget + time.Hour).Seconds())
	must(err)
	application := New(ctx, config.Server{DataDirectory: b.TempDir()}, db, testFrontend(), slog.New(slog.NewJSONHandler(io.Discard, nil)), func(o *options) { o.pauseCatalogMaintenance = true })
	b.Cleanup(func() {
		wait, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		if err := application.WaitForScans(wait); err != nil {
			cleanupError("scans", err)
		}
	})
	request := i10Requester(ctx, application.Handler(), token)
	for index, q := range i10HotQueries() {
		if actions.Measure {
			item := i10MeasureCase(q, i10Warmups, i10Measurements, i10HotTargetMS, db, request)
			i10CheckpointCase(&report, item, save)
			b.Logf("I10 %q suggestions p95=%.3fms results p95=%.3fms pass=%v", q, item.Suggestions.P95MS, item.Results.P95MS, item.Pass)
		}
		if actions.Explain {
			for _, kind := range []string{"suggestions", "albums", "tracks", "artists"} {
				path := i10SearchPath(q, kind, 1)
				var response i10Response
				captured := tracer.collect(func() { response = request(path) })
				record(response.Err)
				if kind == "suggestions" {
					record(i10ValidateSuggestions(q, response.Body))
				} else {
					record(i10ValidateSearch(q, kind, response.Body))
				}
				file := fmt.Sprintf("explain-hot-%02d-%s.json", index+1, kind)
				record(i10SavePlans(ctx, db, captured, filepath.Join(runDir, file), path))
				report.PlanFiles = append(report.PlanFiles, file)
			}
		}
		save()
	}
	// 性能失败只改变最终状态，不中断补测或 EXPLAIN。
	for _, q := range []string{"爱", "周"} {
		if i10ClaimSupplement(actions, manifest.Supplemental, "short-"+q, runID) {
			must(i10WriteJSON(report.Manifest, manifest))
			report.Supplemental = append(report.Supplemental, i10MeasureCase(q, 1, 3, 0, db, request))
			for _, kind := range []string{"suggestions", "albums", "tracks", "artists"} {
				path := i10SearchPath(q, kind, 1)
				var response i10Response
				captured := tracer.collect(func() { response = request(path) })
				record(response.Err)
				file := "explain-short-" + q + "-" + kind + ".json"
				record(i10SavePlans(ctx, db, captured, filepath.Join(runDir, file), path))
				report.PlanFiles = append(report.PlanFiles, file)
			}
			save()
		}
	}
	if i10ClaimSupplement(actions, manifest.Supplemental, "deep-page", runID) {
		must(i10WriteJSON(report.Manifest, manifest))
		path := i10SearchPath("交响", "tracks", 100)
		var response i10Response
		captured := tracer.collect(func() { response = request(path) })
		record(response.Err)
		record(i10ValidateDeepPage(response.Body))
		report.DeepPageMS = response.MS
		file := "explain-page-100.json"
		record(i10SavePlans(ctx, db, captured, filepath.Join(runDir, file), path))
		report.PlanFiles = append(report.PlanFiles, file)
		save()
	}
	for _, order := range []string{"album", "random"} {
		if !i10ClaimSupplement(actions, manifest.Supplemental, "queue-"+order, runID) {
			continue
		}
		must(i10WriteJSON(report.Manifest, manifest))
		path := "/api/v1/artists/" + scalePopularArtist + "/queue?order=" + order
		if order == "random" {
			path += "&seed=90000000-0000-4000-8000-000000000001"
		}
		var values []float64
		item := i10Queue{Order: order}
		var captured []scaleQuery
		for i := 0; i < 3; i++ {
			var response i10Response
			if i == 0 {
				captured = tracer.collect(func() { response = request(path) })
			} else {
				response = request(path)
			}
			record(response.Err)
			sample, err := i10InspectQueue(response.Body)
			record(err)
			item.Responses = append(item.Responses, sample)
			values = append(values, response.MS)
		}
		item.SamplesMS = values
		item.Latency = scaleStats(values[0], append([]float64(nil), values...), 0)
		report.Queues = append(report.Queues, item)
		file := "explain-queue-" + order + ".json"
		record(i10SavePlans(ctx, db, captured, filepath.Join(runDir, file), path))
		report.PlanFiles = append(report.PlanFiles, file)
		save()
	}
	if i10ClaimSupplement(actions, manifest.Supplemental, "queue-cancel", runID) {
		must(i10WriteJSON(report.Manifest, manifest))
		path := "/api/v1/artists/" + scalePopularArtist + "/queue?order=album"
		ms, status, err := scaleCancelArtistQueue(ctx, db, application.Handler(), token, path)
		report.QueueCancellation = &i10QueueCancellation{Order: "album", ElapsedMS: ms, HTTPStatus: status}
		if err != nil {
			report.QueueCancellation.Error = err.Error()
			record(fmt.Errorf("队列在途取消: %w", err))
		}
		save()
	}
	report.Complete = ctx.Err() == nil
	report.Pass = report.Complete && len(report.Errors) == 0
	for _, item := range append(append([]i10Case(nil), report.Cases...), report.Supplemental...) {
		report.Pass = report.Pass && item.Pass
	}
	if actions.Measure && len(report.Cases) != 2 {
		report.Pass = false
	}
	save()
	if !report.Pass {
		b.Errorf("I10 未通过，schema=%s 已保留；详见 %s。仅用 reuse 重测热门词，禁止重造夹具。", manifest.Schema, runDir)
	}
}

type i10Report struct {
	Mode, Schema, Manifest, TotalBudget   string
	CPUCount, PoolMax, ResultsConcurrency int
	RAMBytes                              uint64
	Counts                                map[string]int64
	PG                                    map[string]string
	Cases, Supplemental                   []i10Case
	DeepPageMS                            float64
	Queues                                []i10Queue
	QueueCancellation                     *i10QueueCancellation
	PlanFiles, Errors, Notes              []string
	Complete, Pass, Finalized             bool
}
type i10Queue struct {
	Order     string
	SamplesMS []float64
	Latency   scaleLatency
	Responses []i10QueueResponse
}
type i10QueueResponse struct {
	ResponseBytes, Returned, ItemCount, Limit int
	Total, MissingTotal                       int64
	Truncated                                 bool
}

// 取消只记录一次真实锁等待后的结果；不将耗时与任何新增性能门槛比较。
type i10QueueCancellation struct {
	Order      string
	ElapsedMS  float64
	HTTPStatus int
	Error      string
}
type i10Case struct {
	Query                    string
	Warmups, Measurements    int
	TargetMS                 float64
	Suggestions, Results     scaleLatency
	SuggestionsMS, ResultsMS []float64
	ColumnsMS, ColumnsDBMS   map[string][]float64
	SuggestionsDBMS          []float64
	Errors                   []string
	Pass                     bool
}
type i10Response struct {
	Body     []byte
	MS, DBMS float64
	Err      error
}
type i10Request func(string) i10Response

func i10HotQueries() []string { return []string{"周杰伦", "周杰倫"} }
func i10SearchPath(q, kind string, page int) string {
	if kind == "suggestions" {
		return "/api/v1/search/suggestions?limit=5&q=" + url.QueryEscape(q)
	}
	return fmt.Sprintf("/api/v1/search?type=%s&page=%d&page_size=50&q=%s", kind, page, url.QueryEscape(q))
}
func i10Requester(ctx context.Context, handler http.Handler, token string) i10Request {
	return func(path string) i10Response {
		requestCtx, cancel := context.WithTimeout(ctx, scaleRequestBudget)
		defer cancel()
		dbTime := &atomic.Int64{}
		requestCtx = context.WithValue(requestCtx, scaleRequestKey{}, dbTime)
		req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(requestCtx)
		req.Host = "roomusic.test"
		req.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: token})
		r := httptest.NewRecorder()
		start := time.Now()
		handler.ServeHTTP(r, req)
		result := i10Response{Body: r.Body.Bytes(), MS: float64(time.Since(start)) / float64(time.Millisecond), DBMS: float64(dbTime.Load()) / float64(time.Millisecond)}
		if r.Code != http.StatusOK {
			result.Err = fmt.Errorf("GET %s: %d %s", path, r.Code, r.Body)
		}
		return result
	}
}
func i10MeasureCase(q string, warmups, samples int, target float64, db *sql.DB, request i10Request) i10Case {
	item := i10Case{Query: q, Warmups: warmups, Measurements: samples, TargetMS: target, ColumnsMS: map[string][]float64{}, ColumnsDBMS: map[string][]float64{}}
	check := func(err error) {
		if err != nil {
			item.Errors = append(item.Errors, err.Error())
		}
	}
	var coldS, coldR float64
	// 新连接冷请求另记；预热、正式样本不混入冷值；只有三栏并发，建议独立。
	for i := -1 - warmups; i < samples; i++ {
		if i == -1-warmups {
			scaleColdConnections(db)
		}
		s := request(i10SearchPath(q, "suggestions", 1))
		check(s.Err)
		check(i10ValidateSuggestions(q, s.Body))
		if i == -1-warmups {
			scaleColdConnections(db)
		}
		type column struct {
			kind     string
			response i10Response
		}
		done := make(chan column, 3)
		start := time.Now()
		for _, kind := range []string{"albums", "tracks", "artists"} {
			go func(kind string) { done <- column{kind, request(i10SearchPath(q, kind, 1))} }(kind)
		}
		columns := make([]column, 0, 3)
		for range 3 {
			columns = append(columns, <-done)
		}
		ms := float64(time.Since(start)) / float64(time.Millisecond)
		for _, col := range columns {
			check(col.response.Err)
			check(i10ValidateSearch(q, col.kind, col.response.Body))
			if i >= 0 {
				item.ColumnsMS[col.kind] = append(item.ColumnsMS[col.kind], col.response.MS)
				item.ColumnsDBMS[col.kind] = append(item.ColumnsDBMS[col.kind], col.response.DBMS)
			}
		}
		if i == -1-warmups {
			coldS, coldR = s.MS, ms
		}
		if i >= 0 {
			item.SuggestionsMS = append(item.SuggestionsMS, s.MS)
			item.ResultsMS = append(item.ResultsMS, ms)
			item.SuggestionsDBMS = append(item.SuggestionsDBMS, s.DBMS)
		}
	}
	item.Suggestions = scaleStats(coldS, append([]float64(nil), item.SuggestionsMS...), target)
	item.Results = scaleStats(coldR, append([]float64(nil), item.ResultsMS...), target)
	item.Pass = item.Suggestions.Pass && item.Results.Pass && len(item.Errors) == 0
	return item
}

func i10FixtureFacts(ctx context.Context, db *sql.DB, report *i10Report) error {
	for _, table := range []string{"tracks", "releases", "release_groups", "artists", "catalog_search_values", "artist_credits"} {
		var count int64
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			return err
		}
		report.Counts[table] = count
	}
	for _, status := range []string{"present", "missing"} {
		var count int64
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM tracks WHERE source_status=$1", status).Scan(&count); err != nil {
			return err
		}
		report.Counts["tracks_"+status] = count
	}
	if err := i10ValidateFixtureCounts(report.Counts); err != nil {
		return err
	}
	if err := scaleAssertArtistFixture(ctx, db, &scaleReport{Counts: report.Counts}); err != nil {
		return err
	}
	for _, setting := range []string{"server_version", "shared_buffers", "work_mem", "jit", "max_connections", "effective_cache_size", "random_page_cost", "max_parallel_workers_per_gather"} {
		var value string
		if err := db.QueryRowContext(ctx, "SELECT current_setting($1)", setting).Scan(&value); err != nil {
			return err
		}
		report.PG[setting] = value
	}
	return nil
}

// 按实际执行语句标注阶段，保留全部捕获 SQL/参数/完整 JSON 计划；不手抄简化 SQL。
// 同一查询词按 endpoint 分文件，便于与旧 explain-03/04.json 的 SQL、参数、节点和 buffers 对照。
func i10PlanStage(query string) string {
	q := strings.ToLower(query)
	switch {
	case strings.Contains(q, "limit 1001") && strings.Contains(q, "count(*)"):
		return "count"
	case strings.Contains(q, "selected_tracks"), strings.Contains(q, "with ranked as"), strings.Contains(q, "=any($1::uuid[])") && !strings.Contains(q, "catalog_search_values"):
		return "completion"
	case strings.Contains(q, "offset "):
		return "page"
	default:
		return "support"
	}
}
func i10SavePlans(ctx context.Context, db *sql.DB, queries []scaleQuery, path, endpoint string) error {
	if len(queries) == 0 {
		return fmt.Errorf("未捕获生产 SQL: %s", endpoint)
	}
	plans := make([]map[string]any, 0, len(queries))
	var failures []string
	for _, query := range queries {
		start := time.Now()
		raw, err := scaleExplain(ctx, db, query)
		item := map[string]any{"endpoint": endpoint, "stage": i10PlanStage(query.SQL), "sql_sha256": i10Hash([]byte(query.SQL)), "query": query, "plan": raw, "wall_ms": float64(time.Since(start)) / float64(time.Millisecond)}
		if err != nil {
			item["error"] = err.Error()
			failures = append(failures, err.Error())
		}
		plans = append(plans, item)
		// 每条计划都持久化，后续超时/中断不丢失已完成的 count/page/completion。
		if err := i10WriteJSON(path, plans); err != nil {
			return err
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("EXPLAIN %s: %s", endpoint, strings.Join(failures, "; "))
	}
	return nil
}
func i10ValidateDeepPage(body []byte) error {
	var result struct {
		Items      []catalog.SearchTrackItem `json:"items"`
		Pagination catalog.SearchPagination  `json:"pagination"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return err
	}
	if result.Pagination.Page != 100 || result.Pagination.PageSize != 50 || result.Pagination.Total != 1000 || result.Pagination.TotalRelation != "gte" || !result.Pagination.HasMore || len(result.Items) != 50 {
		return fmt.Errorf("深页形状不符: %+v items=%d", result.Pagination, len(result.Items))
	}
	return nil
}
func i10InspectQueue(body []byte) (i10QueueResponse, error) {
	sample := i10QueueResponse{ResponseBytes: len(body)}
	var result catalog.ArtistQueue
	if err := json.Unmarshal(body, &result); err != nil {
		return sample, err
	}
	sample.Returned, sample.ItemCount, sample.Limit = result.Returned, len(result.Items), result.Limit
	sample.Total, sample.MissingTotal, sample.Truncated = result.Total, result.MissingTotal, result.Truncated
	if result.Returned != 10000 || len(result.Items) != 10000 || result.Limit != 10000 || result.Total != 50000 || !result.Truncated {
		return sample, fmt.Errorf("队列形状不符: returned=%d total=%d limit=%d", result.Returned, result.Total, result.Limit)
	}
	seen := map[string]bool{}
	for _, item := range result.Items {
		if !item.Present || seen[item.TrackID] {
			return sample, fmt.Errorf("队列出现重复或 missing 曲目")
		}
		seen[item.TrackID] = true
	}
	return sample, nil
}

func i10Finalize(report *i10Report, failed bool) {
	report.Finalized = true
	if failed || len(report.Errors) > 0 || !report.Complete {
		report.Pass = false
		if failed && len(report.Errors) == 0 {
			report.Errors = append(report.Errors, "测试或资源清理失败；详见执行日志")
		}
	}
}
func i10ValidateSearch(q, kind string, body []byte) error {
	if err := scaleValidateSearch(q, kind, body); err != nil {
		return err
	}
	if q != "周杰伦" && q != "周杰倫" {
		return nil
	}
	var result struct {
		Items      []json.RawMessage        `json:"items"`
		Pagination catalog.SearchPagination `json:"pagination"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return err
	}
	if kind == "artists" {
		if len(result.Items) != 1 || result.Pagination.Total != 1 || result.Pagination.TotalRelation != "eq" || result.Pagination.HasMore {
			return fmt.Errorf("热门艺人应只命中同一身份")
		}
		var artist catalog.SearchArtistItem
		if err := json.Unmarshal(result.Items[0], &artist); err != nil {
			return err
		}
		if artist.Artist.ID != scalePopularArtist {
			return fmt.Errorf("热门艺人身份不匹配")
		}
	} else if len(result.Items) != 50 || result.Pagination.Total != 1000 || result.Pagination.TotalRelation != "gte" || !result.Pagination.HasMore {
		return fmt.Errorf("热门词缺失完整首屏/1000+计数: %s", kind)
	}
	return nil
}

// 已完成的30个样本先入报告并持久化；后续昂贵 EXPLAIN 失败不能吞掉首次测量。
func i10CheckpointCase(report *i10Report, item i10Case, save func()) {
	report.Cases = append(report.Cases, item)
	save()
}
func i10ValidateFixtureHash(fixture []byte, expected string) error {
	if i10Hash(fixture) != expected {
		return fmt.Errorf("原规模夹具摘要发生变化")
	}
	return nil
}
func i10ValidateFixtureCounts(counts map[string]int64) error {
	if counts["tracks_present"] != 100000 || counts["tracks_missing"] != 10000 || counts["releases"] != 10000 || counts["release_groups"] != 8000 {
		return fmt.Errorf("I10 原规模夹具计数不匹配: %v", counts)
	}
	return nil
}
func i10ValidateSuggestions(q string, body []byte) error {
	if err := scaleValidateSuggestions(body); err != nil {
		return err
	}
	if q != "周杰伦" && q != "周杰倫" {
		return nil
	}
	var result catalog.SearchSuggestions
	if err := json.Unmarshal(body, &result); err != nil {
		return err
	}
	if len(result.Albums.Items) != 5 || !result.Albums.HasMore || len(result.Tracks.Items) != 5 || !result.Tracks.HasMore {
		return fmt.Errorf("热门建议应各含5张专辑和5首曲目且仍有更多：albums=%d/%v tracks=%d/%v", len(result.Albums.Items), result.Albums.HasMore, len(result.Tracks.Items), result.Tracks.HasMore)
	}
	if len(result.Artists.Items) != 1 || result.Artists.HasMore || result.Artists.Items[0].Artist.ID != scalePopularArtist {
		return fmt.Errorf("热门建议应恰好命中目标艺人且没有更多")
	}
	return nil
}
