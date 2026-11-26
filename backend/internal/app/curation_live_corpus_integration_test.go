package app

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/model"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/coverartarchive"
)

type liveAlbum struct {
	Expect                         metricExpect `json:"expect"`
	ID, Group, Artist, Title, Tags string
	LocalTitle                     string `json:"local_title"`
	Directory                      string
	ReleaseIDs                     []string
}

type liveRequest struct {
	At         time.Time `json:"started_at"`
	End        time.Time `json:"finished_at"`
	Endpoint   string    `json:"endpoint"`
	Status     int       `json:"status"`
	RetryAfter string    `json:"retry_after,omitempty"`
}

type liveTransport struct {
	mu       sync.Mutex
	requests []liveRequest
}

func (r *liveTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != "GET" || req.URL.Scheme != "https" || req.URL.Host != "musicbrainz.org" || !strings.HasPrefix(req.URL.Path, "/ws/2/") || req.Header.Get("User-Agent") != sources.UserAgent() {
		return nil, fmt.Errorf("真实试用只接受带程序标识的公开 MusicBrainz 读取")
	}
	path := strings.Trim(strings.TrimPrefix(req.URL.Path, "/ws/2/"), "/")
	entity, _, lookup := strings.Cut(path, "/")
	endpoint := "musicbrainz/" + entity + "/search"
	if lookup {
		endpoint = "musicbrainz/" + entity + "/lookup"
	}
	row := liveRequest{At: time.Now().UTC(), Endpoint: endpoint}
	resp, err := http.DefaultTransport.RoundTrip(req)
	row.End = time.Now().UTC()
	if resp != nil {
		row.Status, row.RetryAfter = resp.StatusCode, resp.Header.Get("Retry-After")
	}
	r.mu.Lock()
	r.requests = append(r.requests, row)
	r.mu.Unlock()
	return resp, err
}

func (r *liveTransport) client(opts egress.Options) (*http.Client, error) {
	return &http.Client{Transport: r, Timeout: opts.Timeout}, nil
}

func (r *liveTransport) snapshot() ([]liveRequest, map[string]int, map[int]int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	endpoints, statuses := map[string]int{}, map[int]int{}
	for _, req := range r.requests {
		endpoints[req.Endpoint]++
		statuses[req.Status]++
	}
	return slices.Clone(r.requests), endpoints, statuses
}

func liveScan(t *testing.T, app *integrationTestApplication, admin *integrationTestUser) []liveAlbum {
	t.Helper()
	var corpus struct{ Releases []liveAlbum }
	raw, err := os.ReadFile("../../cmd/roomusic-curation-bench/corpus.json")
	requireCurationOK(t, err)
	requireCurationOK(t, json.Unmarshal(raw, &corpus))
	const root = "../../../temp/real-corpus"
	dirs, err := os.ReadDir(root)
	requireCurationOK(t, err)
	if len(dirs) != len(corpus.Releases) {
		t.Fatalf("生成的专辑目录数 %d，清单 %d", len(dirs), len(corpus.Releases))
	}
	for i, dir := range dirs {
		if !dir.IsDir() || !strings.HasPrefix(dir.Name(), fmt.Sprintf("%02d - ", i+1)) {
			t.Fatalf("曲库目录无法对应清单：%s", dir.Name())
		}
		corpus.Releases[i].Directory = dir.Name()
	}
	requireCurationOK(t, os.CopyFS(app.allowedRoot, os.DirFS(root)))
	b3Root(t, app)
	publishSearch(t, app)
	scanAndConfirmCuration(t, app, admin)
	for i := range corpus.Releases {
		a := &corpus.Releases[i]
		rows, err := app.database.QueryContext(t.Context(), `SELECT DISTINCT m.release_id FROM tracks t JOIN media m ON m.id=t.medium_id WHERE t.source_status='present' AND starts_with(t.relative_path,$1) ORDER BY 1`, a.Directory+"/")
		requireCurationOK(t, err)
		for rows.Next() {
			var id string
			requireCurationOK(t, rows.Scan(&id))
			a.ReleaseIDs = append(a.ReleaseIDs, id)
		}
		requireCurationOK(t, rows.Err())
		rows.Close()
		if len(a.ReleaseIDs) == 0 {
			t.Fatalf("%s 未扫出发行", a.Directory)
		}
	}
	return corpus.Releases
}

func liveOracle(t *testing.T, app *integrationTestApplication, admin *integrationTestUser, albums []liveAlbum, search string) *metricsModel {
	t.Helper()
	t.Setenv("ROOMUSIC_CURATION_METRICS_MODEL", os.Getenv("ROOMUSIC_LIVE_MODE"))
	m := newMetricsModel(t)
	targets := map[string]metricsModelTarget{}
	known := map[string]liveAlbum{}
	for _, a := range albums {
		m.canonical[a.Artist] = a.Artist + " | "
		if a.LocalTitle != "" {
			m.typos[a.LocalTitle] = a.Title
		}
		for _, id := range a.ReleaseIDs {
			targets[id] = metricsModelTarget{SampleID: a.ID, ReleaseID: a.ID, GroupID: a.Group, SearchTitle: a.Title, Fields: map[string]string{"title": a.Title}}
			known[id] = a
		}
	}
	// 清单未标注艺人编号，身份题一律放弃。程序或本地整理新建的身份也按此处理。
	m.manifestOracle = true
	metricReadRows(t, app, `SELECT id FROM artists`, func(rows *sql.Rows) {
		var id string
		requireCurationOK(t, rows.Scan(&id))
		targets[id] = metricsModelTarget{SampleID: "未标注艺人"}
	})
	m.localReply = func(item metricsPending) curation.LocalInput {
		a := known[item.ReleaseID]
		return curation.LocalInput{Title: a.Title, Artist: a.Artist}
	}
	m.configure(t, app, admin, targets, search)
	return m
}

func liveSearch(t *testing.T, searches *atomic.Int64) string {
	t.Helper()
	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/searxng/search" {
			http.NotFound(w, req)
			return
		}
		searches.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"results":[]}`)
	}))
	t.Cleanup(search.Close)
	return search.URL
}

func liveUsage(m *metricsModel) (map[string]metricsModelUsage, map[string]map[string]map[string]int, map[string][]string) {
	total := map[string]metricsModelUsage{}
	items := map[string]map[string]map[string]int{}
	routes := map[string][]string{}
	if m != nil {
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, counts := range m.counts {
			for stage, v := range counts {
				s := total[stage]
				s.Calls += v.Calls
				s.InputTokens += v.InputTokens
				s.OutputTokens += v.OutputTokens
				total[stage] = s
			}
		}
		for k, v := range m.itemUsage {
			if items[k.stage] == nil {
				items[k.stage] = map[string]map[string]int{}
			}
			items[k.stage][k.item] = map[string]int{"tool_calls": v.calls, "tokens": v.tokens}
		}
		for k, v := range m.routes {
			routes[k] = slices.Clone(v)
		}
	}
	return total, items, routes
}

// 两次运行使用独立数据库，保留真实限速与退避时钟；只缩短空闲轮询间隔。
func Test真实曲库队列试用(t *testing.T) {
	mode := os.Getenv("ROOMUSIC_LIVE_MODE")
	if mode == "" {
		t.Skip("仅在显式开启真实来源试用时运行")
	}
	if mode != "program" && mode != "oracle" && mode != "oracle_open" && mode != "real" {
		t.Fatal("ROOMUSIC_LIVE_MODE 只接受 program、oracle、oracle_open 或 real")
	}
	out := os.Getenv("ROOMUSIC_LIVE_OUT")
	if out == "" {
		t.Fatal("请指定原始 JSON 的 ROOMUSIC_LIVE_OUT")
	}
	transport := new(liveTransport)
	var logOutput io.Writer = os.Stderr
	if mode == "real" {
		logOutput = io.Discard
	}
	app := newIntegrationTestApplicationWithLogger(t, slog.New(slog.NewTextHandler(logOutput, &slog.HandlerOptions{Level: slog.LevelError})),
		WithCurationProgram(true, program.Options{PollInterval: 2 * time.Second, FullPassInterval: 30 * time.Second}),
		WithCurationModel(true, model.Options{PollInterval: 250 * time.Millisecond, BatchSize: 1}),
		WithCurationSources(sources.Options{NewClient: transport.client}, coverartarchive.Options{}, sources.Options{}))
	admin := app.createUser(t, "admin")
	albums := liveScan(t, app, &admin)
	var all struct{ Items []curation.SourceSettings }
	getJSON(t, app, &admin, curationBase+"/sources", &all)
	for _, s := range all.Items {
		want := s.ID == "musicbrainz" || (mode != "program" && s.ID == "web_search")
		if want == s.Enabled {
			continue
		}
		resp := app.request(t, "PUT", curationBase+"/sources/"+s.ID, map[string]bool{"enabled": want}, &admin, nil)
		if resp.Code != 200 {
			t.Fatal(s.ID, resp.Code, resp.Body.String())
		}
	}
	var oracle *metricsModel
	var searches atomic.Int64
	var key string
	var meter *liveResponseMeter
	limit := 22 * time.Minute
	switch mode {
	case "oracle", "oracle_open":
		oracle = liveOracle(t, app, &admin, albums, liveSearch(t, &searches))
	case "real":
		meter = new(liveResponseMeter)
		key = liveRealProvider(t, app, &admin, liveSearch(t, &searches), meter)
		liveStartItemUsage(t, app)
		limit = 2 * time.Hour
	}
	start := time.Now().UTC()
	b3Enable(t, app, admin)
	var p metricProgress
	type observedPause struct {
		FirstObserved         time.Time
		Queue, Source, Reason string
		RetryAt               time.Time
	}
	pauses := map[string]observedPause{}
	idleSince := time.Time{}
	checkpoint := ""
	drained := false
	nextLog := start
	for time.Since(start) < limit {
		// 重置指针字段以免沿用上次快照中已消失的暂停原因。
		p = metricProgress{}
		getJSON(t, app, &admin, curationBase+"/progress", &p)
		idle := p.State == "idle" && p.LastPassFinishedAt != nil && !p.LastPassFinishedAt.Before(start) && p.Releases.Processed == p.Releases.Total
		for _, q := range p.Queues {
			if q.RetryAt != nil && q.Source != nil && q.PauseReason != nil {
				key := q.ID + "/" + q.RetryAt.Format(time.RFC3339Nano)
				if _, ok := pauses[key]; !ok {
					pauses[key] = observedPause{time.Now().UTC(), q.ID, *q.Source, *q.PauseReason, *q.RetryAt}
				}
			}
			if q.Ready > 0 || q.RetryAt != nil || q.State == "running" {
				idle = false
			}
		}
		if mode != "program" {
			idle = idle && p.Model.Items.WaitingModel == 0 && p.Model.Items.InProgress == 0 && p.Model.State == "idle"
		}
		if idle {
			now := metricCheckpoint(t, app)
			if idleSince.IsZero() || checkpoint != now {
				idleSince = time.Now()
				checkpoint = now
			}
			if time.Since(idleSince) >= 4*time.Second {
				drained = true
				break
			}
		} else {
			idleSince = time.Time{}
		}
		if time.Now().After(nextLog) {
			requests, _, statuses := transport.snapshot()
			t.Logf("%s 耗时 %.0fs，已处理 %d/%d，出站 %d，HTTP %v，待模型 %d，运行中 %d", mode, time.Since(start).Seconds(), p.Releases.Processed, p.Releases.Total, len(requests), statuses, p.Model.Items.WaitingModel, p.Model.Items.InProgress)
			nextLog = time.Now().Add(20 * time.Second)
		}
		time.Sleep(500 * time.Millisecond)
	}
	end := time.Now().UTC()
	events, endpoints, statuses := transport.snapshot()
	usage, items, routes := liveUsage(oracle)
	var conversations, empty int
	requireCurationOK(t, app.database.QueryRowContext(t.Context(), `SELECT count(*),count(*) FILTER (
		WHERE NOT EXISTS (SELECT FROM steward_messages m WHERE m.conversation_id=c.id)
	) FROM steward_conversations c`).Scan(&conversations, &empty))
	result := map[string]any{
		"mode": mode, "started_at": start, "finished_at": end,
		"elapsed_seconds": end.Sub(start).Seconds(), "drained": drained, "progress": p,
		"public_sources": []string{"musicbrainz"}, "user_agent": sources.UserAgent(),
		"http_count": len(events), "http_503": statuses[503], "http_statuses": statuses,
		"endpoints": endpoints, "requests": events, "queue_backoffs": pauses,
		"loopback_search_requests": searches.Load(),
		"conversations":            conversations, "empty_conversations": empty,
		"model_by_stage": usage, "item_usage": items, "model_routes": routes,
		"token_estimate": "请求与 SSE 响应分别按字节数除以 4 向下取整",
	}
	if mode == "real" {
		delete(result, "token_estimate")
		routes = liveRealMetrics(t, app, result, meter)
	}
	write := func() {
		b, err := json.MarshalIndent(result, "", "  ")
		requireCurationOK(t, err)
		if key != "" && bytes.Contains(b, []byte(key)) {
			t.Fatal("已阻止写出包含凭据的试用结果")
		}
		requireCurationOK(t, os.MkdirAll(filepath.Dir(out), 0755))
		requireCurationOK(t, os.WriteFile(out, append(b, '\n'), 0644))
	}
	write()
	score := liveCorpusScore(t, app, albums, routes)
	result["score"] = score
	write()
	if mode == "real" && score.Attribution["info_missing"] != 0 {
		t.Errorf("真实试用中 %d 个未完整达标样本缺少决策信息，见逐样本归因", score.Attribution["info_missing"])
	}
	if !drained {
		t.Fatalf("试用超时，已处理 %d/%d，待模型 %d，运行中 %d", p.Releases.Processed, p.Releases.Total, p.Model.Items.WaitingModel, p.Model.Items.InProgress)
	}
}
