package app

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/personal"
)

//go:embed testdata/personal-realistic-scale.sql
var personalRealisticScaleSQL string

// 默认也实际核对完整规模、两阶段统计与全部 HTTP 合同，不因未启用性能模式额外 Skip。
// 基线：ROOMUSIC_PERSONAL_REALISTIC_SAMPLES=2，少样本分位数只供诊断，不是 SLA 证据。
// 最终：ROOMUSIC_PERSONAL_REALISTIC_PERF=1，默认 20 个有效样本，拒绝少于 20 个。
// ROOMUSIC_PERSONAL_REALISTIC_DIR 指定 JSON 目录，ROOMUSIC_PERSONAL_REALISTIC_REPORT 指定文件名。
// ROOMUSIC_PERSONAL_REALISTIC_SLA=1 仅供显式非 race 性能门禁，普通/race 正确性门禁不判耗时。
// 测试仅消费启动器提供的 ROOMUSIC_TEST_DATABASE_URL，用既有随机 schema 工具隔离和清理。
func TestPostgreSQLPersonalRealisticHTTPPerformance(t *testing.T) {
	options := realisticOptions(t)
	app := newIntegrationTestApplication(t)
	user, other := app.createUser(t, "user"), app.createUser(t, "user")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	realisticSeed(t, ctx, app, user, other)
	report := realisticReport{
		StartedAt: time.Now().UTC(), Mode: "correctness", SamplesPerScenario: options.samples,
		SLAGate: options.gate, GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		NumCPU: runtime.NumCPU(), GOMAXPROCS: runtime.GOMAXPROCS(0), RaceBuild: options.race,
		Notes: []string{
			"真实 app、鉴权、数据库与 JSON 编码，经 httptest.NewServer 的本机 TCP；不经过 ResponseRecorder。",
			"TCP 耗时从 client.Do 前到客户端读完并关闭 body；handler 从进入完整 app 到 ServeHTTP 返回；Go 解码另计。",
			"详情组合按两个真实请求串行测量，总耗时至第二个 body 读完，解码与业务断言均不计入 TCP 时间。",
			"逐轮串行轮转场景，无并发压测、无强制 GC；会话/全局与 enable_* 参数未改，personal 只读事务执行 SET LOCAL jit = off；无系统缓存清除，首观测不等于冷数据库。",
			"50 项首屏 missing 精确按轨号分布断言（2 或 3 项），全库、收藏集合与每张歌单 missing 均为 5%。",
			"响应字节数是未压缩 body 字节数，不含 HTTP 头；不是浏览器 decoder/DOM 或音频实播性能。",
		},
	}
	if options.performance {
		report.Mode = "performance"
	}
	if options.samples < 20 {
		report.Notes = append(report.Notes, "少于 20 个样本的 p95 仅为探索性基线，不能用于 SLA 通过声明。")
	}
	defer func() {
		report.FinishedAt = time.Now().UTC()
		report.Valid = !t.Failed()
		realisticSaveReport(t, options, report)
	}()
	report.Fixture = realisticVerifyScale(t, ctx, app, user)
	if err := app.database.QueryRowContext(ctx, `SELECT version()`).Scan(&report.PostgreSQL); err != nil {
		t.Fatal(err)
	}
	report.Settings = realisticSettings(t, ctx, app)
	core := []string{"users", "sessions", "library_roots", "release_groups", "releases", "media", "tracks", "personal_favorite_state", "favorite_tracks", "favorite_albums", "personal_playlists", "personal_playlist_items", "personal_playlist_requests", "personal_playlist_undo"}
	auxiliary := []string{"release_corrections", "release_issue_reviews", "release_field_decisions", "release_grouping_evidence", "release_artworks"}
	realisticAnalyze(t, ctx, app, core)

	// 旁路仅采集 ServeHTTP 起止时间，不包裹 ResponseWriter，不改应用响应或生产设置。
	timings := make(chan realisticHandlerTiming, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		app.handler.ServeHTTP(w, r)
		timings <- realisticHandlerTiming{ID: r.Header.Get("X-Request-ID"), MS: realisticMilliseconds(time.Since(started))}
	}))
	defer server.Close()
	client := server.Client()
	client.Timeout = 2 * time.Minute
	client.Transport.(*http.Transport).DisableCompression = true
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	scenarios := realisticScenarios()
	for _, stageName := range []string{"core_analyzed_auxiliary_unanalyzed", "core_analyzed_auxiliary_analyzed"} {
		if stageName == "core_analyzed_auxiliary_analyzed" {
			realisticAnalyze(t, ctx, app, auxiliary)
		}
		report.Stages = append(report.Stages, realisticStage{Name: stageName})
		stage := &report.Stages[len(report.Stages)-1]
		stage.Before = realisticStatistics(t, ctx, app, core, stageName)
		stage.Measurements = make([]realisticMeasurement, len(scenarios))
		for i, scenario := range scenarios {
			stage.Measurements[i] = realisticMeasurement{Name: scenario.name, BudgetMS: scenario.budget, Samples: []realisticSample{}}
		}
		firstRound := 0
		if options.performance {
			firstRound = -1
		}
		for round := firstRound; round < options.samples; round++ {
			// 轮转起点，避免所有场景的缓存/GC 影响始终按同一顺序发生。
			for offset := range scenarios {
				index := (max(round, 0) + offset) % len(scenarios)
				scenario := scenarios[index]
				playlist := max(round, 0)%20 + 1
				sample := realisticMeasure(t, ctx, client, server.URL, user, timings, scenario, playlist, round)
				measurement := &stage.Measurements[index]
				if round < 0 {
					measurement.Warmup = &sample
				} else {
					measurement.Samples = append(measurement.Samples, sample)
				}
				// 每个完整、已验证的样本之后保存，长测中断不会把未完成报告冒充通过。
				if options.directory != "" {
					realisticSaveReport(t, options, report)
				}
			}
		}
		for i := range stage.Measurements {
			measurement := &stage.Measurements[i]
			measurement.summarize()
			t.Logf("%s/%s n=%d tcp p50=%.2f p95=%.2fms handler p95=%.2fms decode p95=%.2fms bytes=%d budget=%.0fms tcp_pass=%v handler_pass=%v pass=%v",
				stage.Name, measurement.Name, len(measurement.Samples), measurement.TCP.P50, measurement.TCP.P95,
				measurement.Handler.P95, measurement.Decode.P95, measurement.MaxBodyBytes, measurement.BudgetMS, measurement.TCPWithinBudget, measurement.HandlerWithinBudget, measurement.WithinBudget)
			if options.gate && !measurement.WithinBudget {
				t.Errorf("%s/%s: TCP p95=%.2fms (pass=%v), server handler p95=%.2fms (pass=%v), budget=%.0fms", stage.Name, measurement.Name, measurement.TCP.P95, measurement.TCPWithinBudget, measurement.Handler.P95, measurement.HandlerWithinBudget, measurement.BudgetMS)
			}
		}
		stage.After = realisticStatistics(t, ctx, app, core, stageName)
	}
	if got := realisticSettings(t, ctx, app); !equalRealisticSettings(report.Settings, got) {
		t.Fatal("性能测试期间 PostgreSQL 设置发生变化")
	}
}

type realisticRunOptions struct {
	samples                 int
	performance, gate, race bool
	directory, filename     string
}

func realisticOptions(t *testing.T) realisticRunOptions {
	t.Helper()
	options := realisticRunOptions{samples: 1, directory: os.Getenv("ROOMUSIC_PERSONAL_REALISTIC_DIR"), filename: os.Getenv("ROOMUSIC_PERSONAL_REALISTIC_REPORT")}
	options.performance = os.Getenv("ROOMUSIC_PERSONAL_REALISTIC_PERF") == "1"
	options.gate = os.Getenv("ROOMUSIC_PERSONAL_REALISTIC_SLA") == "1"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "-race" {
				options.race = setting.Value == "true"
			}
		}
	}
	if options.performance {
		options.samples = 20
	}
	if value := os.Getenv("ROOMUSIC_PERSONAL_REALISTIC_SAMPLES"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 100 {
			t.Fatal("ROOMUSIC_PERSONAL_REALISTIC_SAMPLES 必须为 1–100")
		}
		options.samples = n
	}
	if options.performance && (options.samples < 20 || options.race) {
		t.Fatal("性能模式要求非 race 且至少 20 个有效样本")
	}
	if options.gate && !options.performance {
		t.Fatal("SLA 门禁必须显式启用非 race 性能模式")
	}
	if options.directory != "" && !filepath.IsAbs(options.directory) {
		t.Fatal("ROOMUSIC_PERSONAL_REALISTIC_DIR 必须是绝对路径（go test 使用包目录作为工作目录）")
	}
	if options.filename == "" {
		options.filename = "personal-realistic-http-performance.json"
	}
	if filepath.Base(options.filename) != options.filename || filepath.Ext(options.filename) != ".json" {
		t.Fatal("报告名必须是目录内的 .json 文件名")
	}
	return options
}

func realisticSeed(t *testing.T, ctx context.Context, app *integrationTestApplication, user, other integrationTestUser) {
	t.Helper()
	tx, err := app.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO library_roots(id,path) VALUES('70000000-0000-4000-8000-000000000001',$1)`, app.allowedRoot); err != nil {
		t.Fatal(err)
	}
	for i, statement := range strings.Split(personalRealisticScaleSQL, ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		var args []any
		if strings.Contains(statement, "$1") {
			args = append(args, user.id)
		}
		if strings.Contains(statement, "$2") {
			args = append(args, other.id)
		}
		if _, err = tx.ExecContext(ctx, statement, args...); err != nil {
			t.Fatalf("合成夹具语句 %d: %v", i+1, err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func realisticID(kind, number int) string {
	return fmt.Sprintf("%d0000000-0000-4000-8000-%012d", kind, number)
}
func realisticTrack(sequence int) int { return (sequence%10000)*10 + sequence/10000 + 1 }
func realisticPlaylistTrack(playlist, index int) int {
	return realisticTrack(((playlist-1)*5000 + index) % 100000)
}
func realisticMissing(track int) bool { return ((track-1)/10+1+(track-1)%10+1)%20 == 0 }
func realisticGroup(track int) int    { return ((track-1)/10)%8000 + 1 }
func realisticMilliseconds(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}

// 每项来自实际 SQL 聚合，不拿生成器常量充当数据库规模证据。
func realisticVerifyScale(t *testing.T, ctx context.Context, app *integrationTestApplication, user integrationTestUser) map[string]int64 {
	t.Helper()
	checks := []struct {
		name, sql string
		want      int64
	}{
		{"catalog_tracks", `SELECT count(*) FROM tracks`, 100000},
		{"catalog_missing", `SELECT count(*) FROM tracks WHERE source_status='missing'`, 5000},
		{"catalog_releases", `SELECT count(*) FROM releases`, 10000},
		{"catalog_groups", `SELECT count(*) FROM release_groups`, 8000},
		{"catalog_media", `SELECT count(*) FROM media`, 10000},
		{"releases_not_ten_tracks", `SELECT count(*) FROM (SELECT r.id FROM releases r LEFT JOIN media m ON m.release_id=r.id LEFT JOIN tracks t ON t.medium_id=m.id GROUP BY r.id HAVING count(t.id)<>10) bad`, 0},
		{"owner_favorite_tracks", `SELECT count(*) FROM favorite_tracks WHERE user_id=$1`, 20000},
		{"owner_favorite_missing", `SELECT count(*) FROM favorite_tracks f JOIN tracks t ON t.id=f.track_id WHERE f.user_id=$1 AND t.source_status='missing'`, 1000},
		{"owner_favorite_releases", `SELECT count(DISTINCT m.release_id) FROM favorite_tracks f JOIN tracks t ON t.id=f.track_id JOIN media m ON m.id=t.medium_id WHERE f.user_id=$1`, 10000},
		{"owner_favorite_groups", `SELECT count(DISTINCT r.group_id) FROM favorite_tracks f JOIN tracks t ON t.id=f.track_id JOIN media m ON m.id=t.medium_id JOIN releases r ON r.id=m.release_id WHERE f.user_id=$1`, 8000},
		{"owner_favorite_albums", `SELECT count(*) FROM favorite_albums WHERE user_id=$1`, 1000},
		{"other_favorite_tracks", `SELECT count(*) FROM favorite_tracks WHERE user_id<>$1`, 100000},
		{"other_favorite_albums", `SELECT count(*) FROM favorite_albums WHERE user_id<>$1`, 8000},
		{"owner_playlists", `SELECT count(*) FROM personal_playlists WHERE user_id=$1`, 20},
		{"other_playlists", `SELECT count(*) FROM personal_playlists WHERE user_id<>$1`, 20},
		{"owner_playlist_items", `SELECT count(*) FROM personal_playlist_items WHERE user_id=$1`, 200000},
		{"playlist_union_tracks", `SELECT count(DISTINCT track_id) FROM personal_playlist_items WHERE user_id=$1`, 100000},
		{"playlist_union_releases", `SELECT count(DISTINCT m.release_id) FROM personal_playlist_items i JOIN tracks t ON t.id=i.track_id JOIN media m ON m.id=t.medium_id WHERE i.user_id=$1`, 10000},
		{"playlist_union_groups", `SELECT count(DISTINCT r.group_id) FROM personal_playlist_items i JOIN tracks t ON t.id=i.track_id JOIN media m ON m.id=t.medium_id JOIN releases r ON r.id=m.release_id WHERE i.user_id=$1`, 8000},
		{"tracks_not_in_two_playlists", `SELECT count(*) FROM (SELECT track_id FROM personal_playlist_items WHERE user_id=$1 GROUP BY track_id HAVING count(*)<>2) bad`, 0},
		{"adjacent_playlist_overlap", `SELECT count(*) FROM personal_playlist_items a JOIN personal_playlist_items b ON b.user_id=a.user_id AND b.track_id=a.track_id WHERE a.user_id=$1 AND a.playlist_id='50000000-0000-4000-8000-000000000001' AND b.playlist_id='50000000-0000-4000-8000-000000000002'`, 5000},
	}
	result := map[string]int64{}
	for _, check := range checks {
		var got int64
		var args []any
		if strings.Contains(check.sql, "$1") {
			args = []any{user.id}
		}
		if err := app.database.QueryRowContext(ctx, check.sql, args...).Scan(&got); err != nil {
			t.Fatalf("%s: %v", check.name, err)
		}
		if got != check.want {
			t.Fatalf("%s=%d，期望%d", check.name, got, check.want)
		}
		result[check.name] = got
	}
	rows, err := app.database.QueryContext(ctx, `SELECT p.id::text,count(i.id),count(DISTINCT m.release_id),count(DISTINCT r.group_id),count(*) FILTER (WHERE t.source_status='missing'),p.track_count
 FROM personal_playlists p LEFT JOIN personal_playlist_items i ON i.playlist_id=p.id AND i.user_id=p.user_id
 LEFT JOIN tracks t ON t.id=i.track_id LEFT JOIN media m ON m.id=t.medium_id LEFT JOIN releases r ON r.id=m.release_id
 WHERE p.user_id=$1 GROUP BY p.id ORDER BY p.id`, user.id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id string
		var items, releases, groups, missing, stored int64
		if err := rows.Scan(&id, &items, &releases, &groups, &missing, &stored); err != nil {
			t.Fatal(err)
		}
		count++
		if id != realisticID(5, count) || items != 10000 || releases != 10000 || groups != 8000 || missing != 500 || stored != items {
			t.Fatalf("歌单 %s: items=%d releases=%d groups=%d missing=%d stored=%d", id, items, releases, groups, missing, stored)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 20 {
		t.Fatalf("核对歌单数=%d", count)
	}
	result["each_playlist_tracks"], result["each_playlist_releases"], result["each_playlist_groups"], result["each_playlist_missing"] = 10000, 10000, 8000, 500
	return result
}

type realisticTableStatistics struct {
	Name            string     `json:"table"`
	Rows            int64      `json:"actual_rows"`
	Estimate        float64    `json:"reltuples"`
	Pages           int64      `json:"relpages"`
	LastAnalyze     *time.Time `json:"last_analyze"`
	LastAutoAnalyze *time.Time `json:"last_autoanalyze"`
	Core            bool       `json:"core"`
}

func realisticAnalyze(t *testing.T, ctx context.Context, app *integrationTestApplication, tables []string) {
	t.Helper()
	var schema string
	if err := app.database.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(schema, "roomusic_test_") {
		t.Fatal("拒绝对非测试 schema 执行 ANALYZE")
	}
	for _, table := range tables {
		if _, err := app.database.ExecContext(ctx, "ANALYZE "+pgx.Identifier{schema, table}.Sanitize()); err != nil {
			t.Fatal(err)
		}
	}
}

func realisticStatistics(t *testing.T, ctx context.Context, app *integrationTestApplication, core []string, stage string) []realisticTableStatistics {
	t.Helper()
	rows, err := app.database.QueryContext(ctx, `SELECT c.relname,c.reltuples,c.relpages,s.last_analyze,s.last_autoanalyze
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace LEFT JOIN pg_stat_all_tables s ON s.relid=c.oid
 WHERE n.nspname=current_schema() AND c.relkind='r' ORDER BY c.relname`)
	if err != nil {
		t.Fatal(err)
	}
	var result []realisticTableStatistics
	for rows.Next() {
		var row realisticTableStatistics
		if err := rows.Scan(&row.Name, &row.Estimate, &row.Pages, &row.LastAnalyze, &row.LastAutoAnalyze); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		row.Core = slices.Contains(core, row.Name)
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	for i := range result {
		row := &result[i]
		if err := app.database.QueryRowContext(ctx, "SELECT count(*) FROM "+pgx.Identifier{row.Name}.Sanitize()).Scan(&row.Rows); err != nil {
			t.Fatal(err)
		}
		if row.Core {
			if (row.Rows > 0 && (row.Estimate <= 0 || math.Abs(row.Estimate-float64(row.Rows)) > float64(row.Rows)*0.2)) || (row.Rows == 0 && (row.Estimate != 0 || row.LastAnalyze == nil)) {
				t.Fatalf("核心统计未建立: %+v", row)
			}
		}
	}
	for _, name := range slices.Clone(core) {
		if !slices.ContainsFunc(result, func(s realisticTableStatistics) bool { return s.Name == name }) {
			t.Fatalf("缺少核心统计核对表 %s", name)
		}
	}
	for _, name := range []string{"release_corrections", "release_issue_reviews", "release_field_decisions", "release_grouping_evidence", "release_artworks"} {
		index := slices.IndexFunc(result, func(s realisticTableStatistics) bool { return s.Name == name })
		if index < 0 {
			t.Fatalf("缺少辅助统计核对表 %s", name)
		}
		row := result[index]
		if row.Rows != 0 {
			t.Fatalf("辅助表必须为空: %+v", row)
		}
		if stage == "core_analyzed_auxiliary_unanalyzed" && (row.Estimate != -1 || row.LastAnalyze != nil || row.LastAutoAnalyze != nil) {
			t.Fatalf("阶段 A 辅助表已被分析: %+v", row)
		}
		if stage == "core_analyzed_auxiliary_analyzed" && (row.Estimate != 0 || row.LastAnalyze == nil) {
			t.Fatalf("阶段 B 辅助表未被显式 ANALYZE: %+v", row)
		}
	}
	return result
}

func realisticSettings(t *testing.T, ctx context.Context, app *integrationTestApplication) map[string]string {
	t.Helper()
	rows, err := app.database.QueryContext(ctx, `SELECT name,setting FROM pg_settings WHERE name LIKE 'enable_%' OR name LIKE 'jit%' OR name IN ('plan_cache_mode','work_mem','shared_buffers','effective_cache_size','random_page_cost','seq_page_cost','max_parallel_workers_per_gather','default_statistics_target','autovacuum') ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			t.Fatal(err)
		}
		result[name] = value
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}
func equalRealisticSettings(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

type realisticScenario struct {
	name     string
	budget   float64
	paths    func(int) []string
	validate func(*testing.T, int, [][]byte) float64
}

func realisticScenarios() []realisticScenario {
	single := func(path string) func(int) []string { return func(int) []string { return []string{path} } }
	playlist := func(suffix string) func(int) []string {
		return func(p int) []string { return []string{playlistsURL + "/" + realisticID(5, p) + suffix} }
	}
	return []realisticScenario{
		{"playlists_first_20x10000", 1000, single(playlistsURL + "?page=1&page_size=20"), realisticValidateCards},
		{"favorites_20000_queue_first_10000_present", 2000, single(favoritesURL + "/tracks/queue"), realisticValidateFavoriteQueue},
		{"playlist_10000_queue", 2000, playlist("/queue"), realisticValidatePlaylistQueue},
		{"favorite_tracks_first_50", 300, single(favoritesURL + "/tracks?page=1&page_size=50"), realisticValidateTracks},
		{"favorite_albums_first_50", 300, single(favoritesURL + "/albums?page=1&page_size=50"), realisticValidateAlbums},
		{"playlist_summary", 300, playlist(""), realisticValidateDetail},
		{"playlist_items_first_50", 300, playlist("/items?page=1&page_size=50"), realisticValidateItems},
		{"playlist_detail_summary_and_first_50", 300, func(p int) []string {
			return []string{playlistsURL + "/" + realisticID(5, p), playlistsURL + "/" + realisticID(5, p) + "/items?page=1&page_size=50"}
		}, func(t *testing.T, p int, b [][]byte) float64 {
			return realisticValidateDetail(t, p, b[:1]) + realisticValidateItems(t, p, b[1:])
		}},
	}
}

type realisticHandlerTiming struct {
	ID string
	MS float64
}
type realisticRequestTiming struct {
	Path      string  `json:"path"`
	TCPMS     float64 `json:"tcp_http_ms"`
	HandlerMS float64 `json:"server_handler_ms"`
	BodyBytes int     `json:"body_bytes"`
}
type realisticSample struct {
	Round     int                      `json:"round"`
	Playlist  int                      `json:"playlist_number"`
	TCPMS     float64                  `json:"tcp_http_ms"`
	HandlerMS float64                  `json:"server_handler_ms"`
	DecodeMS  float64                  `json:"go_decode_ms"`
	BodyBytes int                      `json:"body_bytes"`
	Requests  []realisticRequestTiming `json:"requests"`
}

func realisticMeasure(t *testing.T, ctx context.Context, client *http.Client, base string, user integrationTestUser, timings <-chan realisticHandlerTiming, scenario realisticScenario, playlist, round int) realisticSample {
	t.Helper()
	sample := realisticSample{Round: round + 1, Playlist: playlist}
	var bodies [][]byte
	var first time.Time
	for index, path := range scenario.paths(playlist) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		id := fmt.Sprintf("realistic-%s-%d-%d", scenario.name, round+1, index)
		request.Header.Set("X-Request-ID", id)
		request.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: user.token})
		started := time.Now()
		if index == 0 {
			first = started
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatalf("%s: %v", scenario.name, err)
		}
		body, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		completed := time.Now()
		if readErr != nil || closeErr != nil {
			t.Fatalf("%s: 读取 body=%v，关闭=%v", scenario.name, readErr, closeErr)
		}
		if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Content-Type") != "application/json" || response.Header.Get("X-Request-ID") != id {
			t.Fatalf("%s: HTTP=%d headers=%v", scenario.name, response.StatusCode, response.Header)
		}
		var handler realisticHandlerTiming
		select {
		case handler = <-timings:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		if handler.ID != id || handler.MS <= 0 {
			t.Fatalf("handler 样本关联错误: %+v", handler)
		}
		sample.Requests = append(sample.Requests, realisticRequestTiming{Path: path, TCPMS: realisticMilliseconds(completed.Sub(started)), HandlerMS: handler.MS, BodyBytes: len(body)})
		sample.TCPMS = realisticMilliseconds(completed.Sub(first))
		sample.HandlerMS += handler.MS
		sample.BodyBytes += len(body)
		bodies = append(bodies, body)
	}
	sample.DecodeMS = scenario.validate(t, playlist, bodies)
	return sample
}

func realisticDecode(t *testing.T, body []byte, target any) float64 {
	t.Helper()
	started := time.Now()
	err := json.Unmarshal(body, target)
	elapsed := realisticMilliseconds(time.Since(started))
	if err != nil {
		t.Fatal(err)
	}
	return elapsed
}
func realisticPagination(t *testing.T, page personal.Pagination, total int64, size int) {
	t.Helper()
	if page.Page != 1 || page.PageSize != size || page.Total != total {
		t.Fatalf("分页合同: %+v", page)
	}
}
func realisticState(t *testing.T, state personal.PlaylistState, p int) {
	t.Helper()
	if state.ID != realisticID(5, p) || state.Name != fmt.Sprintf("合成歌单 %d", p) || state.Revision != 1 || state.TrackCount != 10000 || state.TrackLimit != 10000 || state.OverLimitCount != 0 || state.CreatedAt.IsZero() || state.UpdatedAt.IsZero() {
		t.Fatalf("歌单状态: %+v", state)
	}
}
func realisticSummary(t *testing.T, summary personal.PlaylistSummary, p int) {
	t.Helper()
	realisticState(t, summary.PlaylistState, p)
	if summary.MissingCount != 500 || summary.DurationSeconds != 1200000 || summary.UnknownDurationCount != 0 || len(summary.CoverGroups) != 4 {
		t.Fatalf("歌单摘要: %+v", summary)
	}
	for i, cover := range summary.CoverGroups {
		if cover.GroupID != realisticID(3, realisticGroup(realisticPlaylistTrack(p, i))) || cover.Artwork != nil {
			t.Fatalf("歌单首四个不同组: %+v", summary.CoverGroups)
		}
	}
}
func realisticRelease(t *testing.T, summary catalog.ReleaseSummary, release int) {
	t.Helper()
	missing := int64(0)
	for p := 1; p <= 10; p++ {
		if realisticMissing((release-1)*10 + p) {
			missing++
		}
	}
	year := 1990 + (release-1)/8000
	if summary.ID != realisticID(2, release) || summary.Title != fmt.Sprintf("合成发行 %d", release) || summary.Artist != "合成艺人" || summary.AlbumArtist == nil || *summary.AlbumArtist != "合成艺人" || summary.Year == nil || *summary.Year != year || summary.TrackCount != 10-missing || summary.MediumCount != 1 || summary.AttentionCount != missing || summary.Artwork != nil {
		t.Fatalf("发行摘要 %d: %+v", release, summary)
	}
}
func realisticTrackSummary(t *testing.T, summary catalog.TrackSummary, track int) {
	t.Helper()
	if summary.TrackID != realisticID(1, track) || summary.Title != fmt.Sprintf("合成曲目 %d", track) || summary.Artist != "合成艺人" || summary.GroupID != realisticID(3, realisticGroup(track)) || summary.Present == realisticMissing(track) || summary.DurationSeconds == nil || *summary.DurationSeconds != 120 || summary.Source.SourceKind != "physical" || summary.Source.Codec == nil || *summary.Source.Codec != "flac" || summary.Source.BitDepth == nil || *summary.Source.BitDepth != 24 || summary.Source.SampleRate == nil || *summary.Source.SampleRate != 96000 || summary.Source.Channels == nil || *summary.Source.Channels != 2 {
		t.Fatalf("曲目摘要 %d: %+v", track, summary)
	}
	realisticRelease(t, summary.Release, (track-1)/10+1)
}
func realisticValidateCards(t *testing.T, _ int, b [][]byte) float64 {
	var page personal.PlaylistsPage
	elapsed := realisticDecode(t, b[0], &page)
	realisticPagination(t, page.Pagination, 20, 20)
	if len(page.Items) != 20 {
		t.Fatalf("歌单首屏长度=%d", len(page.Items))
	}
	for i, summary := range page.Items {
		realisticSummary(t, summary, i+1)
	}
	return elapsed
}
func realisticValidateTracks(t *testing.T, _ int, b [][]byte) float64 {
	var page personal.FavoriteTracksPage
	elapsed := realisticDecode(t, b[0], &page)
	realisticPagination(t, page.Pagination, 20000, 50)
	if len(page.Items) != 50 || page.Revision != 1 {
		t.Fatal("收藏曲目首屏长度/修订号")
	}
	missing := 0
	for i, item := range page.Items {
		realisticTrackSummary(t, item.Track, realisticTrack(i))
		if !item.Track.Present {
			missing++
		}
		if item.CreatedAt.IsZero() || (i > 0 && !item.CreatedAt.Before(page.Items[i-1].CreatedAt)) {
			t.Fatal("收藏时间排序")
		}
	}
	if missing != 2 {
		t.Fatalf("收藏首屏 missing=%d，预期2/50（全集1000/20000）", missing)
	}
	return elapsed
}
func realisticValidateAlbums(t *testing.T, _ int, b [][]byte) float64 {
	var page personal.FavoriteAlbumsPage
	elapsed := realisticDecode(t, b[0], &page)
	realisticPagination(t, page.Pagination, 1000, 50)
	if len(page.Items) != 50 || page.Revision != 1 {
		t.Fatal("收藏专辑首屏长度/修订号")
	}
	for i, item := range page.Items {
		album := item.Album
		if album.GroupID != realisticID(3, i+1) || !album.Available || album.ReleaseCount != 2 || album.Representative == nil || album.Title != fmt.Sprintf("合成发行 %d", i+1) || album.Artist != "合成艺人" {
			t.Fatalf("专辑摘要: %+v", album)
		}
		realisticRelease(t, *album.Representative, i+1)
		if item.CreatedAt.IsZero() || (i > 0 && !item.CreatedAt.Before(page.Items[i-1].CreatedAt)) {
			t.Fatal("专辑收藏时间排序")
		}
	}
	return elapsed
}
func realisticValidateDetail(t *testing.T, p int, b [][]byte) float64 {
	var detail personal.PlaylistDetail
	elapsed := realisticDecode(t, b[0], &detail)
	realisticSummary(t, detail.Playlist, p)
	return elapsed
}
func realisticValidateItems(t *testing.T, p int, b [][]byte) float64 {
	var page personal.PlaylistItemsPage
	elapsed := realisticDecode(t, b[0], &page)
	realisticState(t, page.Playlist, p)
	realisticPagination(t, page.Pagination, 10000, 50)
	if len(page.Items) != 50 || page.PageAfterItemID == nil || *page.PageAfterItemID != realisticID(6, (p-1)*10000+51) {
		t.Fatal("歌单首屏长度/下一项")
	}
	missing, wantMissing := 0, 0
	for i, item := range page.Items {
		track := realisticPlaylistTrack(p, i)
		realisticTrackSummary(t, item.Track, track)
		if item.ID != realisticID(6, (p-1)*10000+i+1) || item.Position != int64(i+1) || item.AddedAt.IsZero() {
			t.Fatal("歌单首屏身份/位置/时间")
		}
		if !item.Track.Present {
			missing++
		}
		if realisticMissing(track) {
			wantMissing++
		}
	}
	if missing != wantMissing || (missing != 2 && missing != 3) {
		t.Fatalf("歌单首屏 missing=%d，期望%d；完整歌单500/10000", missing, wantMissing)
	}
	return elapsed
}
func realisticQueue(t *testing.T, q personal.QueueTracks, total, missing, returned int64, trackAt func(int) int) {
	t.Helper()
	if q.Total != total || q.MissingCount != missing || q.EligibleCount != total-missing || q.ReturnedCount != returned || len(q.Items) != int(returned) || q.OmittedCount != total-missing-returned || q.Limit != 10000 || q.Truncated != (q.OmittedCount > 0) {
		t.Fatalf("队列计数: total=%d missing=%d eligible=%d returned=%d omitted=%d limit=%d truncated=%v", q.Total, q.MissingCount, q.EligibleCount, q.ReturnedCount, q.OmittedCount, q.Limit, q.Truncated)
	}
	index := 0
	for _, item := range q.Items {
		for realisticMissing(trackAt(index)) {
			index++
		}
		realisticTrackSummary(t, item, trackAt(index))
		if !item.Present {
			t.Fatal("队列包含 missing")
		}
		index++
	}
}
func realisticValidateFavoriteQueue(t *testing.T, _ int, b [][]byte) float64 {
	var queue personal.FavoriteQueue
	elapsed := realisticDecode(t, b[0], &queue)
	if queue.Revision != 1 {
		t.Fatal("收藏队列修订号")
	}
	realisticQueue(t, queue.QueueTracks, 20000, 1000, 10000, realisticTrack)
	return elapsed
}
func realisticValidatePlaylistQueue(t *testing.T, p int, b [][]byte) float64 {
	var queue personal.PlaylistQueue
	elapsed := realisticDecode(t, b[0], &queue)
	realisticState(t, queue.Playlist, p)
	realisticQueue(t, queue.QueueTracks, 10000, 500, 9500, func(i int) int { return realisticPlaylistTrack(p, i) })
	return elapsed
}

type realisticQuantiles struct {
	P50 float64 `json:"p50_ms"`
	P95 float64 `json:"p95_ms"`
	Max float64 `json:"max_ms"`
}

func realisticPercentiles(values []float64) realisticQuantiles {
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	if len(sorted) == 0 {
		return realisticQuantiles{}
	}
	rank := func(p float64) float64 { return sorted[int(math.Ceil(p*float64(len(sorted))))-1] }
	return realisticQuantiles{P50: rank(.50), P95: rank(.95), Max: sorted[len(sorted)-1]}
}

type realisticMeasurement struct {
	Name                string             `json:"name"`
	BudgetMS            float64            `json:"budget_ms"`
	TCPWithinBudget     bool               `json:"tcp_within_budget"`
	HandlerWithinBudget bool               `json:"handler_within_budget"`
	WithinBudget        bool               `json:"within_budget"`
	TCP                 realisticQuantiles `json:"tcp_http"`
	Handler             realisticQuantiles `json:"server_handler"`
	Decode              realisticQuantiles `json:"go_decode"`
	MaxBodyBytes        int                `json:"max_body_bytes"`
	Warmup              *realisticSample   `json:"warmup,omitempty"`
	Samples             []realisticSample  `json:"samples"`
}

func (m *realisticMeasurement) summarize() {
	tcp, handler, decode := []float64{}, []float64{}, []float64{}
	for _, s := range m.Samples {
		tcp = append(tcp, s.TCPMS)
		handler = append(handler, s.HandlerMS)
		decode = append(decode, s.DecodeMS)
		m.MaxBodyBytes = max(m.MaxBodyBytes, s.BodyBytes)
	}
	m.TCP, m.Handler, m.Decode = realisticPercentiles(tcp), realisticPercentiles(handler), realisticPercentiles(decode)
	m.TCPWithinBudget = len(m.Samples) > 0 && m.TCP.P95 <= m.BudgetMS
	m.HandlerWithinBudget = len(m.Samples) > 0 && m.Handler.P95 <= m.BudgetMS
	m.WithinBudget = m.TCPWithinBudget && m.HandlerWithinBudget
}

type realisticStage struct {
	Name         string                     `json:"name"`
	Before       []realisticTableStatistics `json:"statistics_before"`
	After        []realisticTableStatistics `json:"statistics_after"`
	Measurements []realisticMeasurement     `json:"measurements"`
}
type realisticReport struct {
	StartedAt          time.Time         `json:"started_at"`
	FinishedAt         time.Time         `json:"finished_at"`
	Valid              bool              `json:"valid"`
	Mode               string            `json:"mode"`
	SamplesPerScenario int               `json:"samples_per_scenario"`
	SLAGate            bool              `json:"sla_gate"`
	PostgreSQL         string            `json:"postgresql"`
	GoVersion          string            `json:"go_version"`
	GOOS               string            `json:"goos"`
	GOARCH             string            `json:"goarch"`
	NumCPU             int               `json:"num_cpu"`
	GOMAXPROCS         int               `json:"gomaxprocs"`
	RaceBuild          bool              `json:"race_build"`
	Settings           map[string]string `json:"postgresql_settings"`
	Fixture            map[string]int64  `json:"fixture"`
	Notes              []string          `json:"notes"`
	Stages             []realisticStage  `json:"stages"`
}

func realisticSaveReport(t *testing.T, options realisticRunOptions, report realisticReport) {
	t.Helper()
	if options.directory == "" {
		return
	}
	for si := range report.Stages {
		for mi := range report.Stages[si].Measurements {
			report.Stages[si].Measurements[mi].summarize()
		}
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(options.directory, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(options.directory, options.filename)
	// 先完整写临时文件再原子替换，读取者不会拿到半截 JSON。
	file, err := os.CreateTemp(options.directory, ".personal-realistic-*.json")
	if err != nil {
		t.Fatal(err)
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if _, err = file.Write(append(data, '\n')); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(temporary, path); err != nil {
		t.Fatal(err)
	}
}

func TestPersonalRealisticFixtureDistribution(t *testing.T) {
	union := make(map[int]int, 100000)
	for p := 1; p <= 20; p++ {
		tracks, releases, groups := map[int]bool{}, map[int]bool{}, map[int]bool{}
		missing := 0
		for i := 0; i < 10000; i++ {
			track := realisticPlaylistTrack(p, i)
			tracks[track] = true
			releases[(track-1)/10+1] = true
			groups[realisticGroup(track)] = true
			union[track]++
			if realisticMissing(track) {
				missing++
			}
		}
		if len(tracks) != 10000 || len(releases) != 10000 || len(groups) != 8000 || missing != 500 {
			t.Fatalf("歌单%d 分布不完整", p)
		}
	}
	if len(union) != 100000 {
		t.Fatalf("并集=%d", len(union))
	}
	for track, count := range union {
		if count != 2 {
			t.Fatalf("track=%d count=%d", track, count)
		}
	}
	missing := 0
	for i := 0; i < 20000; i++ {
		if realisticMissing(realisticTrack(i)) {
			missing++
		}
	}
	if missing != 1000 {
		t.Fatalf("收藏 missing=%d", missing)
	}
}
func TestPersonalRealisticPercentiles(t *testing.T) {
	values := make([]float64, 20)
	for i := range values {
		values[i] = float64(20 - i)
	}
	q := realisticPercentiles(values)
	if q.P50 != 10 || q.P95 != 19 || q.Max != 20 || values[0] != 20 {
		t.Fatalf("最近秩分位数或原数据被修改: %+v", q)
	}
	if q := realisticPercentiles(nil); q != (realisticQuantiles{}) {
		t.Fatal(q)
	}
}
