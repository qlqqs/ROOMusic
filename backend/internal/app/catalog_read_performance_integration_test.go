package app

import (
	"bytes"
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
)

const catalogHTTPPerfWarmups = 5
const catalogHTTPPerfSamples = 40

var catalogHTTPPerfCoreTables = []string{"library_roots", "release_groups", "releases", "media", "tracks", "users", "sessions"}
var catalogHTTPPerfAuxTables = []string{"release_corrections", "release_issue_reviews", "release_artworks", "release_field_decisions", "release_grouping_evidence"}

// TestPostgreSQLCatalogHTTPPerformance 是显式启用的顺序基准，不加入普通回归的计时竞争。
// 输出采用追加模式：失败的运行和原始样本不会被之后成功的运行覆盖。
func TestPostgreSQLCatalogHTTPPerformance(t *testing.T) {
	output := os.Getenv("ROOMUSIC_CATALOG_HTTP_PERF_OUTPUT")
	if output == "" {
		t.Skip("设置 ROOMUSIC_CATALOG_HTTP_PERF_OUTPUT 后运行完整 HTTP 性能验证")
	}
	if os.Getenv("ROOMUSIC_TEST_DATABASE_URL") == "" {
		t.Fatal("性能验证必须显式提供隔离测试库 ROOMUSIC_TEST_DATABASE_URL")
	}
	var report strings.Builder
	fmt.Fprintf(&report, "\n# Catalog 完整 HTTP 性能验证（%s）\n\n", time.Now().UTC().Format(time.RFC3339Nano))
	defer func() {
		status := "通过"
		if t.Failed() {
			status = "失败（保留已记录结果；不能视为性能达标）"
		}
		fmt.Fprintf(&report, "\n本轮结果：**%s**。\n", status)
		file, err := os.OpenFile(output, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			t.Errorf("打开性能报告：%v", err)
			return
		}
		_, writeErr := io.WriteString(file, report.String())
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			t.Errorf("保存性能报告：write=%v close=%v", writeErr, closeErr)
		}
	}()
	race := catalogHTTPPerfRaceEnabled
	fmt.Fprintf(&report, "- 构建：%s，%s/%s，CPU=%d，GOMAXPROCS=%d，race=%t。\n", runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.GOMAXPROCS(0), race)
	report.WriteString("- 数据：100,000 曲目、10,000 发行、10,000 介质、8,000 组；前 2,000 组双版本，每发行 10 曲；发行序号 n%40=0 的 250 发行整版 missing（2,500 曲），n%4=1 的 2,500 发行仅 p=1 missing（2,500 曲），共 5,000（5%），7,800 可见组。固定时间 2020-01-01 UTC+n 秒，year=1980+n%40。辅助表均为空。\n")
	report.WriteString("- 测量：真实 httptest TCP 服务，真实 HTTP 初始化管理员、密码登录、创建普通成员及普通成员登录；每次请求经过 App.Handler 的完整生产中间件、会话鉴权、事务/SQL、JSON 序列化与 ResponseWriter 写出。外层计时至 handler 返回并 Flush 完成；不含客户端 JSON 解码、TCP 建连和请求解析。客户端完整收包耗时单独记录，不冒充服务端耗时。日志使用现有集成测试的 io.Discard 输出，但生产日志中间件仍执行。\n")
	report.WriteString("- 每阶段、每端点先单独记录首读，再热身 5 次，再顺序实测 40 次，无并发、无样本删除、无异常值剔除。p50/p95 使用 nearest-rank：排序后第 ceil(p*N) 项，max 为最大值。首读与热身不计入分位数；首读不是冷盘保证，未清 OS/数据库缓存。两个阶段共用同一 fixture/服务，第二阶段只显式 ANALYZE 辅助空表，不重建数据。\n")
	report.WriteString("- 门槛只在正常构建断言：recent p95≤150ms，added_at_desc 首页 50 项 p95≤300ms；race 构建仅验证同规模正确性，时间不可作为性能验收。所有阶段执行前后核验 pg_class 与 pg_stat_all_tables，辅助表 autovacuum_enabled=false，拒绝 autoanalyze 或额外 ANALYZE 污染。\n")
	report.WriteString("- 重现：在 backend 目录，source /tmp/roomusic-m5f-env；设置 ROOMUSIC_CATALOG_HTTP_PERF_OUTPUT 为任务 verification/catalog-http-performance.md 的绝对路径；执行 `go test ./internal/app -run '^TestPostgreSQLCatalogHTTPPerformance$' -count=1 -parallel=1 -v`。只使用 roomusic-test-m5f 的显式测试 URL，每次新建独立 schema 并清理；不启动/停止容器，不改业务请求中的统计信息。\n")
	app := newIntegrationTestApplication(t)
	connection, err := app.database.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	catalogHTTPPerfSettings(t, connection, &report)
	tables := append(append([]string{}, catalogHTTPPerfCoreTables...), catalogHTTPPerfAuxTables...)
	for _, table := range tables {
		catalogHTTPPerfExec(t, connection, "ALTER TABLE "+table+" SET (autovacuum_enabled=false)")
	}
	catalogHTTPPerfSeed(t, connection, app.allowedRoot)
	client := newCatalogHTTPPerfClient(t, app.handler)
	client.login(t)
	postgrestest.AnalyzeSchema(t, context.Background(), connection, catalogHTTPPerfCoreTables...)
	catalogHTTPPerfExec(t, connection, "SELECT pg_stat_force_next_flush()")
	cases := []struct {
		name, path string
		size       int
		limit      time.Duration
	}{
		{"recent", "/api/v1/release-groups/recently-added", 8, 150 * time.Millisecond},
		{"added_at_desc", "/api/v1/release-groups?sort=added_at_desc&page=1&page_size=50", 50, 300 * time.Millisecond},
	}
	for phase, label := range []string{"辅助空表未 ANALYZE", "辅助空表已 ANALYZE"} {
		fmt.Fprintf(&report, "\n## %s\n", label)
		if phase == 1 {
			postgrestest.AnalyzeSchema(t, context.Background(), connection, catalogHTTPPerfAuxTables...)
			catalogHTTPPerfExec(t, connection, "SELECT pg_stat_force_next_flush()")
		}
		catalogHTTPPerfStats(t, connection, phase == 1, "请求前", &report)
		for _, test := range cases {
			// 未登录请求必须在同一真实服务上被拒绝，不能用绕过鉴权的 handler 或伪 session。
			response := client.request(t, http.MethodGet, test.path, nil, false)
			if response.status != http.StatusUnauthorized {
				t.Fatalf("未登录 %s: HTTP %d", test.path, response.status)
			}
			first := client.request(t, http.MethodGet, test.path, nil, true)
			catalogHTTPPerfCheckResponse(t, first, test.size)
			fmt.Fprintf(&report, "\n### %s\n\n首读服务端 %.3fms，客户端 %.3fms，响应 %d 字节。\n", test.name, catalogHTTPPerfMS(first.server), catalogHTTPPerfMS(first.client), len(first.body))
			for i := 0; i < catalogHTTPPerfWarmups; i++ {
				response := client.request(t, http.MethodGet, test.path, nil, true)
				catalogHTTPPerfCheckResponse(t, response, test.size)
			}
			serverTimes := make([]time.Duration, 0, catalogHTTPPerfSamples)
			clientTimes := make([]time.Duration, 0, catalogHTTPPerfSamples)
			for i := 0; i < catalogHTTPPerfSamples; i++ {
				response := client.request(t, http.MethodGet, test.path, nil, true)
				// 解码和语义校验在服务端时间之外，但每个实测响应都必须完整且正确。
				catalogHTTPPerfCheckResponse(t, response, test.size)
				serverTimes = append(serverTimes, response.server)
				clientTimes = append(clientTimes, response.client)
			}
			p50, p95, max := catalogHTTPPerfQuantiles(serverTimes)
			result := "通过"
			if race {
				result = "仅正确性（race）"
			} else if p95 > test.limit {
				result = "失败"
				t.Errorf("%s/%s p95=%s，门槛=%s", label, test.name, p95, test.limit)
			}
			t.Logf("%s/%s first=%.3fms n=%d p50=%.3fms p95=%.3fms max=%.3fms %s", label, test.name, catalogHTTPPerfMS(first.server), len(serverTimes), catalogHTTPPerfMS(p50), catalogHTTPPerfMS(p95), catalogHTTPPerfMS(max), result)
			fmt.Fprintf(&report, "\n| 范围 | 样本数 | p50 ms | p95 ms | max ms | p95 门槛 ms | 结果 |\n| --- | ---: | ---: | ---: | ---: | ---: | --- |\n| 完整服务端 | %d | %.3f | %.3f | %.3f | %.0f | %s |\n", len(serverTimes), catalogHTTPPerfMS(p50), catalogHTTPPerfMS(p95), catalogHTTPPerfMS(max), catalogHTTPPerfMS(test.limit), result)
			c50, c95, cmax := catalogHTTPPerfQuantiles(clientTimes)
			fmt.Fprintf(&report, "| 客户端完整收包（诊断） | %d | %.3f | %.3f | %.3f | 不适用 | 不设门槛 |\n", len(clientTimes), catalogHTTPPerfMS(c50), catalogHTTPPerfMS(c95), catalogHTTPPerfMS(cmax))
			fmt.Fprintf(&report, "\n服务端原始样本（采集顺序，ms）：`%s`。\n\n客户端原始样本（采集顺序，ms）：`%s`。\n", catalogHTTPPerfSamplesText(serverTimes), catalogHTTPPerfSamplesText(clientTimes))
		}
		catalogHTTPPerfStats(t, connection, phase == 1, "请求后", &report)
	}
}

func catalogHTTPPerfExec(t *testing.T, connection *sql.Conn, statement string, args ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := connection.ExecContext(ctx, statement, args...); err != nil {
		t.Fatalf("性能 fixture SQL: %v", err)
	}
}

func catalogHTTPPerfSeed(t *testing.T, connection *sql.Conn, root string) {
	t.Helper()
	catalogHTTPPerfExec(t, connection, `INSERT INTO library_roots(id,path,status,revision) VALUES(md5('http-perf-root')::uuid,$1,'active',1)`, root)
	catalogHTTPPerfExec(t, connection, `INSERT INTO release_groups(id) SELECT md5('http-perf-group-'||n)::uuid FROM generate_series(1,8000) n`)
	catalogHTTPPerfExec(t, connection, `INSERT INTO releases(id,group_id,title,artist,year,created_at,source_root_id,relative_directory,candidate_kind)
 SELECT md5('http-perf-release-'||n)::uuid,md5('http-perf-group-'||CASE WHEN n<=8000 THEN n ELSE n-8000 END)::uuid,
 '合成专辑 '||n,'合成艺人',1980+n%40,TIMESTAMPTZ '2020-01-01T00:00:00Z'+n*INTERVAL '1 second',
 md5('http-perf-root')::uuid,'synthetic/'||n,'ordinary_directory' FROM generate_series(1,10000) n`)
	catalogHTTPPerfExec(t, connection, `INSERT INTO media(id,release_id,position,title)
 SELECT md5('http-perf-medium-'||n)::uuid,md5('http-perf-release-'||n)::uuid,1,'合成碟' FROM generate_series(1,10000) n`)
	catalogHTTPPerfExec(t, connection, `INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,duration_seconds)
 SELECT md5('http-perf-track-'||n||'-'||p)::uuid,md5('http-perf-medium-'||n)::uuid,p,'合成曲目','合成艺人',1,
 md5('http-perf-root')::uuid,'synthetic/'||n||'/'||p||'.flac',CASE WHEN n%40=0 OR (n%4=1 AND p=1) THEN 'missing' ELSE 'present' END,
 TIMESTAMPTZ '2026-01-01T00:00:00Z','physical',180 FROM generate_series(1,10000) n CROSS JOIN generate_series(1,10) p`)
	var tracks, releases, media, groups, missing, visible int
	err := connection.QueryRowContext(context.Background(), `SELECT (SELECT COUNT(*) FROM tracks),(SELECT COUNT(*) FROM releases),
 (SELECT COUNT(*) FROM media),(SELECT COUNT(*) FROM release_groups),(SELECT COUNT(*) FROM tracks WHERE source_status='missing'),
 (SELECT COUNT(DISTINCT r.group_id) FROM releases r JOIN media m ON m.release_id=r.id JOIN tracks t ON t.medium_id=m.id WHERE t.source_status='present')`).Scan(&tracks, &releases, &media, &groups, &missing, &visible)
	if err != nil || tracks != 100000 || releases != 10000 || media != 10000 || groups != 8000 || missing != 5000 || visible != 7800 {
		t.Fatalf("fixture 规模错误：tracks=%d releases=%d media=%d groups=%d missing=%d visible=%d err=%v", tracks, releases, media, groups, missing, visible, err)
	}
}

func catalogHTTPPerfSettings(t *testing.T, connection *sql.Conn, report *strings.Builder) {
	t.Helper()
	report.WriteString("\n数据库环境（不记录连接 URL 或凭据）：\n\n")
	for _, key := range []string{"server_version", "jit", "jit_above_cost", "work_mem", "shared_buffers", "max_parallel_workers_per_gather", "default_statistics_target", "track_counts"} {
		var value string
		if err := connection.QueryRowContext(context.Background(), "SHOW "+key).Scan(&value); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(report, "- %s：`%s`。\n", key, value)
	}
}

func catalogHTTPPerfStats(t *testing.T, connection *sql.Conn, analyzed bool, moment string, report *strings.Builder) {
	t.Helper()
	catalogHTTPPerfExec(t, connection, "SELECT pg_stat_clear_snapshot()")
	fmt.Fprintf(report, "\n### 统计状态（%s）\n\n| 表 | reltuples | relpages | analyze_count | autoanalyze_count | last_analyze | last_autoanalyze | 禁用 autovacuum |\n| --- | ---: | ---: | ---: | ---: | --- | --- | --- |\n", moment)
	tables := append(append([]string{}, catalogHTTPPerfCoreTables...), catalogHTTPPerfAuxTables...)
	for i, table := range tables {
		var tuples float64
		var pages int
		var manual, automatic int64
		var lastManual, lastAuto sql.NullTime
		var disabled bool
		err := connection.QueryRowContext(context.Background(), `SELECT c.reltuples,c.relpages,s.analyze_count,s.autoanalyze_count,s.last_analyze,s.last_autoanalyze,
   COALESCE('autovacuum_enabled=false'=ANY(c.reloptions),false)
   FROM pg_class c JOIN pg_stat_all_tables s ON s.relid=c.oid WHERE c.relnamespace=current_schema()::regnamespace AND c.relname=$1`, table).Scan(&tuples, &pages, &manual, &automatic, &lastManual, &lastAuto, &disabled)
		if err != nil {
			t.Fatal(err)
		}
		timeText := func(value sql.NullTime) string {
			if !value.Valid {
				return "NULL"
			}
			return value.Time.UTC().Format(time.RFC3339Nano)
		}
		fmt.Fprintf(report, "| %s | %.0f | %d | %d | %d | %s | %s | %t |\n", table, tuples, pages, manual, automatic, timeText(lastManual), timeText(lastAuto), disabled)
		if automatic != 0 || lastAuto.Valid || !disabled {
			t.Fatalf("%s 的自动统计污染了基准：auto=%d last_auto=%v disabled=%t", table, automatic, lastAuto, disabled)
		}
		auxiliary := i >= len(catalogHTTPPerfCoreTables)
		if auxiliary {
			var count int
			if err := connection.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
				t.Fatalf("辅助表 %s 必须为空：count=%d err=%v", table, count, err)
			}
		}
		if auxiliary && !analyzed {
			if tuples != -1 || manual != 0 || lastManual.Valid {
				t.Fatalf("辅助表 %s 已有统计：reltuples=%v analyze_count=%d last_analyze=%v", table, tuples, manual, lastManual)
			}
		} else if manual != 1 || !lastManual.Valid || tuples < 0 || (auxiliary && tuples != 0) {
			t.Fatalf("%s 未保持恰好一次手动 ANALYZE：reltuples=%v analyze_count=%d last_analyze=%v", table, tuples, manual, lastManual)
		}
	}
}

type catalogHTTPPerfTiming struct {
	id      string
	elapsed time.Duration
	err     error
}
type catalogHTTPPerfResponse struct {
	status         int
	body           []byte
	server, client time.Duration
}
type catalogHTTPPerfClient struct {
	server    *httptest.Server
	client    *http.Client
	completed chan catalogHTTPPerfTiming
	sequence  int
}

func newCatalogHTTPPerfClient(t *testing.T, handler http.Handler) *catalogHTTPPerfClient {
	t.Helper()
	completed := make(chan catalogHTTPPerfTiming, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		handler.ServeHTTP(w, r)
		// 包含写至真实 TCP ResponseWriter 的 flush，不以 Store 返回或首字节作为结束。
		err := http.NewResponseController(w).Flush()
		completed <- catalogHTTPPerfTiming{id: r.Header.Get("X-Request-ID"), elapsed: time.Since(start), err: err}
	}))
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.Jar = jar
	client.Timeout = 30 * time.Second
	return &catalogHTTPPerfClient{server: server, client: client, completed: completed}
}

func (client *catalogHTTPPerfClient) request(t *testing.T, method, path string, body any, authenticated bool) catalogHTTPPerfResponse {
	t.Helper()
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, client.server.URL+path, payload)
	if err != nil {
		t.Fatal(err)
	}
	client.sequence++
	requestID := fmt.Sprintf("catalog-perf-%d", client.sequence)
	request.Header.Set("X-Request-ID", requestID)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		request.Header.Set("Origin", client.server.URL)
	}
	transport := *client.client
	if !authenticated {
		transport.Jar = nil
	}
	start := time.Now()
	response, err := transport.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	content, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	closeErr := response.Body.Close()
	elapsed := time.Since(start)
	if readErr != nil || closeErr != nil {
		t.Fatalf("读取 %s: read=%v close=%v", path, readErr, closeErr)
	}
	var timing catalogHTTPPerfTiming
	select {
	case timing = <-client.completed:
	case <-time.After(30 * time.Second):
		t.Fatal("未收到服务端完成计时")
	}
	if timing.id != requestID || timing.err != nil {
		t.Fatalf("服务端计时关联/flush 失败：id=%s want=%s err=%v", timing.id, requestID, timing.err)
	}
	if response.Header.Get("X-Request-ID") != requestID || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("请求未经过真实 JSON/关联中间件：%s", path)
	}
	return catalogHTTPPerfResponse{status: response.StatusCode, body: content, server: timing.elapsed, client: elapsed}
}

func (client *catalogHTTPPerfClient) login(t *testing.T) {
	t.Helper()
	steps := []struct {
		path, username string
		status         int
	}{
		{"/api/v1/setup/admin", "perf-admin", http.StatusCreated},
		{"/api/v1/auth/login", "perf-admin", http.StatusOK},
		{"/api/v1/users", "perf-member", http.StatusCreated},
		{"/api/v1/auth/login", "perf-member", http.StatusOK},
	}
	for _, step := range steps {
		response := client.request(t, http.MethodPost, step.path, accountBody(step.username, identityTestPassword), true)
		if response.status != step.status {
			t.Fatalf("真实登录准备 %s: HTTP %d %s", step.path, response.status, response.body)
		}
	}
	response := client.request(t, http.MethodGet, "/api/v1/auth/me", nil, true)
	var user struct{ Role, Username string }
	if err := json.Unmarshal(response.body, &user); err != nil || response.status != 200 || user.Role != "user" || user.Username != "perf-member" {
		t.Fatalf("性能请求必须使用真实登录的普通成员：%s", response.body)
	}
}

func catalogHTTPPerfCheckResponse(t *testing.T, response catalogHTTPPerfResponse, size int) {
	t.Helper()
	if response.status != http.StatusOK {
		t.Fatalf("目录读取 HTTP %d: %s", response.status, response.body)
	}
	var page releaseGroupListResponse
	if err := json.Unmarshal(response.body, &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != size {
		t.Fatalf("目录返回 %d 项，期望 %d", len(page.Items), size)
	}
	if size == 50 && (page.Pagination.Total != 7800 || page.Pagination.Page != 1 || page.Pagination.PageSize != 50) {
		t.Fatalf("目录分页错误：%+v", page.Pagination)
	}
	n := 8000
	for _, item := range page.Items {
		for n%40 == 0 {
			n--
		}
		expected := md5.Sum([]byte(fmt.Sprintf("http-perf-group-%d", n)))
		expectedID := fmt.Sprintf("%x-%x-%x-%x-%x", expected[0:4], expected[4:6], expected[6:8], expected[8:10], expected[10:16])
		added := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Second)
		missing := int64(0)
		if n%4 == 1 {
			missing = 1
		}
		if item.ID != expectedID || !item.AddedAt.Equal(added) || item.ReleaseCount != 1 || item.Representative.TrackCount != 10-missing || item.AttentionCount != missing || item.Representative.AttentionCount != missing {
			t.Fatalf("目录排序/摘要错误：n=%d item=%+v", n, item)
		}
		n--
	}
}

func catalogHTTPPerfMS(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
func catalogHTTPPerfQuantiles(samples []time.Duration) (time.Duration, time.Duration, time.Duration) {
	sorted := append([]time.Duration{}, samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[int(math.Ceil(.50*float64(len(sorted))))-1], sorted[int(math.Ceil(.95*float64(len(sorted))))-1], sorted[len(sorted)-1]
}
func catalogHTTPPerfSamplesText(samples []time.Duration) string {
	values := make([]string, len(samples))
	for i, sample := range samples {
		values[i] = fmt.Sprintf("%.3f", catalogHTTPPerfMS(sample))
	}
	return strings.Join(values, ", ")
}

func TestCatalogHTTPPerformanceQuantiles(t *testing.T) {
	// 40 个倒序样本：nearest-rank 的 p50 为第 20 项，p95 为第 38 项；不能把最大值冒充 p95。
	samples := make([]time.Duration, catalogHTTPPerfSamples)
	for i := range samples {
		samples[i] = time.Duration(len(samples)-i) * time.Millisecond
	}
	p50, p95, maximum := catalogHTTPPerfQuantiles(samples)
	if p50 != 20*time.Millisecond || p95 != 38*time.Millisecond || maximum != 40*time.Millisecond {
		t.Fatalf("nearest-rank 计算错误：p50=%s p95=%s max=%s", p50, p95, maximum)
	}
	if samples[0] != 40*time.Millisecond || samples[len(samples)-1] != time.Millisecond {
		t.Fatal("分位数计算不能改变原始采集顺序")
	}
}
