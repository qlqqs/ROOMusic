package app

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
	"github.com/qlqq/roomusic/backend/internal/upstreammock"
)

func TestFixedMeterBusinessRequests(t *testing.T) {
	sample := fixedSamples(t, "picard-01")[0]
	mock, err := upstreammock.New(upstreammock.Options{DataDir: writeMetricCatalog(t, []metricSample{sample.metricSample})})
	requireCurationOK(t, err)
	meter := newFixedMeter(mock)
	server := httptest.NewServer(meter)
	t.Cleanup(server.Close)

	setup := httptest.NewRecorder()
	meter.ServeHTTP(setup, httptest.NewRequest(http.MethodGet, "/openai/chat/v1/models", nil))
	if setup.Code != http.StatusOK {
		t.Fatalf("模型配置探测返回 %d", setup.Code)
	}
	start := time.Now()
	setScenario := func(scenario string) {
		t.Helper()
		w := httptest.NewRecorder()
		meter.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/_mock/scenarios/musicbrainz",
			strings.NewReader(`{"scenario":"`+scenario+`","retry_after":1}`)))
		if w.Code != http.StatusOK {
			t.Fatalf("设置情景 %s 返回 %d：%s", scenario, w.Code, w.Body.String())
		}
	}
	setScenario("unavailable")
	client := musicbrainz.New(sources.Options{
		BaseURL: server.URL + "/musicbrainz/ws/2", DataDirectory: t.TempDir(),
		NewClient: b3HTTPClient(server), Limiter: sources.NewLimiter(0, nil, nil),
		After: func(d time.Duration) <-chan time.Time {
			setScenario("ok")
			ready := make(chan time.Time, 1)
			ready <- time.Now().Add(d)
			return ready
		},
	})
	for range 2 {
		release, err := client.Lookup(sources.WithBackground(t.Context()), "release", sample.Expect.Release, nil)
		requireCurationOK(t, err)
		if release.ID != sample.Expect.Release {
			t.Fatalf("读取发行 %q，期望 %q", release.ID, sample.Expect.Release)
		}
	}
	want := map[string]int{"musicbrainz/release/lookup": 2}
	if got := meter.counts(start); !maps.Equal(got, want) {
		t.Errorf("业务请求 %v，期望首次失败与实际重试各一次", got)
	}
	var statuses []int
	for _, event := range meter.snapshot() {
		if event.Kind == "musicbrainz/release/lookup" {
			statuses = append(statuses, event.Status)
		}
	}
	if !slices.Equal(statuses, []int{503, 200}) {
		t.Errorf("出站响应 %v，期望 [503 200]，第二次逻辑读取应命中缓存", statuses)
	}
	var mockRequests int
	for key, n := range metricCounters(t, mock) {
		if strings.HasPrefix(key, "musicbrainz/") {
			mockRequests += n
		}
	}
	if mockRequests != meter.counts(start)["musicbrainz/release/lookup"] {
		t.Errorf("mock 实收 %d 次，与业务窗口 %v 不一致", mockRequests, meter.counts(start))
	}
}

func TestFixedMeterTimeline(t *testing.T) {
	origin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	meter := fixedMeter{origin: origin}
	at := func(ms int) time.Time { return origin.Add(time.Duration(ms) * time.Millisecond) }
	// HTTP 事件按响应完成顺序记录，其中前两次请求并行且结束顺序与发送顺序相反。
	meter.record("程序调度定时器", at(0), at(10), 0)
	meter.record("musicbrainz/release/lookup", at(15), at(22), http.StatusOK)
	meter.record("musicbrainz/release/search", at(10), at(25), http.StatusOK)
	meter.record("来源间隔定时器", at(25), at(30), 0)
	meter.record("musicbrainz/artist/search", at(30), at(40), http.StatusOK)
	events := meter.snapshot()
	want := []fixedEvent{
		{Kind: "程序调度定时器", StartUS: 0, EndUS: 10000},
		{Kind: "musicbrainz/release/lookup", StartUS: 15000, EndUS: 22000, Status: 200},
		{Kind: "musicbrainz/release/search", StartUS: 10000, EndUS: 25000, Status: 200},
		{Kind: "来源间隔定时器", StartUS: 25000, EndUS: 30000},
		{Kind: "musicbrainz/artist/search", StartUS: 30000, EndUS: 40000, Status: 200},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("请求与定时器时间线 %+v，期望 %+v", events, want)
	}
	lookup, search, artist := events[1], events[2], events[4]
	overlap := min(lookup.EndUS, search.EndUS) - max(lookup.StartUS, search.StartUS)
	busy := lookup.EndUS - lookup.StartUS + search.EndUS - search.StartUS + artist.EndUS - artist.StartUS - overlap
	firstWait := search.StartUS
	between := artist.StartUS - max(lookup.EndUS, search.EndUS)
	wall := at(48).Sub(origin).Microseconds()
	lastWait := wall - artist.EndUS
	if overlap != 7000 || busy != 25000 || firstWait != 10000 || between != 5000 || lastWait != 8000 ||
		busy+firstWait+between+lastWait != wall {
		t.Errorf("墙钟 %d µs，请求占用 %d（重叠 %d），首段 %d、段间 %d、末段 %d", wall, busy, overlap, firstWait, between, lastWait)
	}
	wantCounts := map[string]int{"musicbrainz/release/search": 1, "musicbrainz/release/lookup": 1, "musicbrainz/artist/search": 1}
	if got := meter.counts(origin); !maps.Equal(got, wantCounts) {
		t.Errorf("请求计数 %v，期望 %v", got, wantCounts)
	}
}
