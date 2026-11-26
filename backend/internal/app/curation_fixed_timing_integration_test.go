package app

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime/trace"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/coverartarchive"
	"github.com/qlqq/roomusic/backend/internal/upstreammock"
)

type fixedEvent struct {
	Kind    string `json:"kind"`
	StartUS int64  `json:"start_us"`
	EndUS   int64  `json:"end_us"`
	Status  int    `json:"status,omitempty"`
}
type fixedMeter struct {
	gate   sync.RWMutex
	mu     sync.Mutex
	origin time.Time
	server *upstreammock.Server
	events []fixedEvent
}

func newFixedMeter(mock *upstreammock.Server) *fixedMeter {
	return &fixedMeter{origin: time.Now(), server: mock}
}
func (m *fixedMeter) record(kind string, start, end time.Time, status int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, fixedEvent{kind, start.Sub(m.origin).Microseconds(), end.Sub(m.origin).Microseconds(), status})
}

type fixedResponse struct {
	http.ResponseWriter
	status int
}

func (w *fixedResponse) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *fixedResponse) Flush() { w.ResponseWriter.(http.Flusher).Flush() }
func (m *fixedMeter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/_mock/") {
		m.server.ServeHTTP(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/openai/") {
		m.gate.RLock()
		defer m.gate.RUnlock()
	}
	endpoint := strings.Trim(r.URL.Path, "/")
	if rest, ok := strings.CutPrefix(endpoint, "musicbrainz/ws/2/"); ok {
		entity, _, lookup := strings.Cut(rest, "/")
		action := "browse"
		if lookup {
			action = "lookup"
		} else if r.URL.Query().Has("query") {
			action = "search"
		}
		endpoint = "musicbrainz/" + entity + "/" + action
	}
	start := time.Now()
	rw := fixedResponse{ResponseWriter: w, status: 200}
	trace.WithRegion(r.Context(), endpoint, func() { m.server.ServeHTTP(&rw, r) })
	m.record(endpoint, start, time.Now(), rw.status)
}
func (m *fixedMeter) snapshot() []fixedEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.events)
}
func (m *fixedMeter) counts(start time.Time) map[string]int {
	counts := map[string]int{}
	for _, event := range m.snapshot() {
		if event.Status != 0 && event.StartUS >= start.Sub(m.origin).Microseconds() {
			counts[event.Kind]++
		}
	}
	return counts
}
func (m *fixedMeter) timer(kind string) func(time.Duration) <-chan time.Time {
	return func(d time.Duration) <-chan time.Time {
		start := time.Now()
		m.record(kind, start, start.Add(d), 0)
		return time.After(d)
	}
}

func fixedIdle(t *testing.T, samples []fixedSample, corpus fixedCorpus) {
	out := os.Getenv("ROOMUSIC_CURATION_METRICS_OUT")
	if out == "" {
		t.Fatal("空等诊断需要 ROOMUSIC_CURATION_METRICS_OUT")
	}
	requireCurationOK(t, os.MkdirAll(filepath.Dir(out), 0755))
	mock, err := upstreammock.New(upstreammock.Options{DataDir: corpus.catalog})
	requireCurationOK(t, err)
	req := httptest.NewRequest("PUT", "/_mock/scenarios/musicbrainz", bytes.NewBufferString(`{"scenario":"slow","delay_ms":100}`))
	w := httptest.NewRecorder()
	mock.ServeHTTP(w, req)
	if w.Code != 200 && w.Code != 204 {
		t.Fatalf("固定延迟 %d", w.Code)
	}
	meter := newFixedMeter(mock)
	server := httptest.NewServer(meter)
	t.Cleanup(server.Close)
	app := newConfiguredIntegrationTestApplication(t, slog.Default(), config.Server{UpstreamMockURL: server.URL}, WithCurationProgram(true, program.Options{After: meter.timer("程序调度定时器"), PollInterval: 2 * time.Second, FullPassInterval: 30 * time.Second}), WithCurationSources(sources.Options{Limiter: sources.NewLimiter(time.Second, nil, meter.timer("来源间隔定时器"))}, coverartarchive.Options{}, sources.Options{}))
	admin := app.createUser(t, "admin")
	fixedConfigure(t, app, &admin, server.URL, false, false)
	for _, s := range samples {
		dir := s.Local.Directory
		requireCurationOK(t, os.CopyFS(filepath.Join(app.allowedRoot, dir), os.DirFS(filepath.Join(corpus.root, dir))))
	}
	file, err := os.Create(out + ".trace")
	requireCurationOK(t, err)
	requireCurationOK(t, trace.Start(file))
	defer func() { trace.Stop(); file.Close() }()
	start := time.Now()
	trace.Log(t.Context(), "采样", "首次扫描")
	runScan(t, app, &admin)
	b3Enable(t, app, admin)
	deadline := start.Add(4 * time.Minute)
	var observations []fixedEvent
	last := ""
	for {
		var p metricProgress
		getJSON(t, app, &admin, curationBase+"/progress", &p)
		q := p.Queues[0]
		state := fmt.Sprintf("程序 %s；识别 %s；可领取 %d；完成 %d/%d", p.State, q.State, q.Ready, p.Releases.Processed, p.Releases.Total)
		if state != last {
			us := time.Since(meter.origin).Microseconds()
			observations = append(observations, fixedEvent{Kind: state, StartUS: us, EndUS: us})
			trace.Log(context.Background(), "队列", state)
			last = state
		}
		if p.State == "idle" && p.Releases.Total == int64(len(samples)) && p.Releases.Processed == p.Releases.Total && len(meter.counts(start)) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("计时队列未完成 %+v", p)
		}
		time.Sleep(100 * time.Millisecond)
	}
	end := time.Now()
	var scans []map[string]any
	metricReadRows(t, app, `SELECT trigger_kind,status,started_at,finished_at FROM scan_runs ORDER BY started_at`, func(rows *sql.Rows) {
		var trigger, status string
		var from time.Time
		var to sql.NullTime
		requireCurationOK(t, rows.Scan(&trigger, &status, &from, &to))
		scans = append(scans, map[string]any{"trigger": trigger, "status": status, "start_us": from.Sub(meter.origin).Microseconds(), "end_us": to.Time.Sub(meter.origin).Microseconds()})
	})
	events := meter.snapshot()
	slices.SortFunc(events, func(a, b fixedEvent) int {
		if a.StartUS < b.StartUS {
			return -1
		}
		if a.StartUS > b.StartUS {
			return 1
		}
		return 0
	})
	fixedSave(t, out, map[string]any{"start_us": start.Sub(meter.origin).Microseconds(), "end_us": end.Sub(meter.origin).Microseconds(), "events": events, "observations": observations, "scans": scans})
	t.Logf("空等计时 %s，%d 个观测事件", end.Sub(start), len(events))
}
