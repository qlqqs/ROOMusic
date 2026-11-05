package app

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

func TestI10SelectionAndThresholds(t *testing.T) {
	if i10Warmups != 5 || i10Measurements != 30 || i10HotTargetMS != 1000 {
		t.Fatal("I10 正式样本或门槛漂移")
	}
	if !reflect.DeepEqual(i10HotQueries(), []string{"周杰伦", "周杰倫"}) {
		t.Fatal("热门词选择漂移")
	}
	for _, mode := range []string{"create", "reuse", "explain", "cleanup"} {
		actions, err := i10SelectActions(mode)
		if err != nil {
			t.Fatal(err)
		}
		if actions.Build != (mode == "create") || actions.Supplement != (mode == "create") || actions.Measure != (mode == "create" || mode == "reuse") || actions.Drop != (mode == "cleanup") {
			t.Fatalf("模式越权: %s %+v", mode, actions)
		}
	}
	for _, mode := range []string{"", "all", "legacy"} {
		if _, err := i10SelectActions(mode); err == nil {
			t.Fatalf("接受未授权模式 %q", mode)
		}
	}
	for _, test := range []struct {
		value, target float64
		pass          bool
	}{{1000, 1000, true}, {1000.01, 1000, false}, {21000, 0, true}} {
		stats := scaleStats(test.value, []float64{test.value}, test.target)
		if stats.Pass != test.pass {
			t.Fatalf("I10 边界错误: %+v", stats)
		}
	}
	for _, test := range []struct {
		input string
		want  time.Duration
		valid bool
	}{{"", 150 * time.Minute, true}, {"180m", 180 * time.Minute, true}, {"149m", 0, false}, {"invalid", 0, false}} {
		got, err := i10Budget(test.input)
		if got != test.want || (err == nil) != test.valid {
			t.Fatalf("budget %q = %s, %v", test.input, got, err)
		}
	}
}
func TestI10ReuseNeverBuildsOrRepeatsSupplement(t *testing.T) {
	for _, mode := range []string{"create", "reuse", "explain"} {
		actions, _ := i10SelectActions(mode)
		var calls []string
		err := i10BuildOnce(actions, func() error { calls = append(calls, "fixture"); return nil }, func() error { calls = append(calls, "worker"); return nil }, func() error { calls = append(calls, "ready"); return nil })
		want := []string{"ready"}
		if mode == "create" {
			want = []string{"fixture", "worker", "ready"}
		}
		if err != nil || !reflect.DeepEqual(calls, want) {
			t.Fatalf("%s: %v %v", mode, calls, err)
		}
		claimed := map[string]string{}
		first := i10ClaimSupplement(actions, claimed, "queue-album", "run-1")
		if first != (mode == "create") || i10ClaimSupplement(actions, claimed, "queue-album", "run-2") {
			t.Fatal("补测重复或复测模式执行补测")
		}
	}
	actions, _ := i10SelectActions("create")
	calls := 0
	err := i10BuildOnce(actions, func() error { return context.Canceled }, func() error { calls++; return nil }, func() error { calls++; return nil })
	if err != context.Canceled || calls != 0 {
		t.Fatal("夹具失败后仍回填")
	}
}
func TestI10ReuseSafety(t *testing.T) {
	m := i10Manifest{Version: 1, Schema: "i10_search_" + strings.Repeat("a", 32), Nonce: strings.Repeat("b", 32), DatabaseFingerprint: "test-db"}
	if err := i10ValidateManifest(m, m.Schema, "test-db"); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*i10Manifest){func(m *i10Manifest) { m.Schema = "public" }, func(m *i10Manifest) { m.Nonce = "bad'" }, func(m *i10Manifest) { m.Cleaned = true }, func(m *i10Manifest) { m.Version = 2 }} {
		bad := m
		mutate(&bad)
		if i10ValidateManifest(bad, bad.Schema, "test-db") == nil {
			t.Fatalf("危险 manifest 被接受: %+v", bad)
		}
	}
	if i10ValidateManifest(m, "", "test-db") == nil || i10ValidateManifest(m, m.Schema, "other-db") == nil {
		t.Fatal("缺显式 schema 或另一数据库被接受")
	}
	path := t.TempDir() + "/fixture.json"
	if err := i10WriteJSON(path, m); err != nil {
		t.Fatal(err)
	}
	// nil diagnostics 的真实 tracer 调用必须安全；不用启动任何数据库。
	var diagnostics *scaleDiagnostics
	if diagnostics.beginQuery(context.Background(), 0, 0, "SELECT 1", nil) != context.Background() {
		t.Fatal("nil diagnostics 未早退")
	}
}

func TestI10PlanStagesAndDeepPage(t *testing.T) {
	for _, item := range []struct{ sql, want string }{{"SELECT count(*) FROM (SELECT id FROM artists LIMIT 1001) counted", "count"}, {"WITH hits AS (...) SELECT id LIMIT $8 OFFSET $9", "page"}, {"WITH selected_tracks AS (...) SELECT * FROM selected_tracks", "completion"}, {"WITH ranked AS (...) SELECT * FROM release_effective", "completion"}} {
		if got := i10PlanStage(item.sql); got != item.want {
			t.Fatalf("stage=%s want=%s", got, item.want)
		}
	}
	body := map[string]any{"items": make([]map[string]any, 50), "pagination": map[string]any{"page": 100, "page_size": 50, "total": 1000, "total_relation": "gte", "has_more": true}}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := i10ValidateDeepPage(raw); err != nil {
		t.Fatal(err)
	}
	if i10ValidateDeepPage([]byte(`{"items":[],"pagination":{"page":1}}`)) == nil {
		t.Fatal("深页未断言实际页")
	}
}

func TestI10AttemptsAndCleanupFinalization(t *testing.T) {
	root := t.TempDir()
	one, err := i10AttemptDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	two, err := i10AttemptDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(one, "attempt-01") || !strings.HasSuffix(two, "attempt-02") {
		t.Fatalf("证据目录覆盖: %s %s", one, two)
	}
	for _, failed := range []bool{false, true} {
		report := i10Report{Complete: true, Pass: true}
		i10Finalize(&report, failed)
		if !report.Finalized || report.Pass == failed {
			t.Fatalf("执行与摘要不一致: %+v", report)
		}
	}
	report := i10Report{Complete: true, Pass: true, Errors: []string{"cleanup.database: close failed"}}
	i10Finalize(&report, true)
	if report.Pass || report.Errors[0] != "cleanup.database: close failed" {
		t.Fatal("清理错误被掩盖")
	}
}

func TestI10QueueMeasurementsAndSingleCancellation(t *testing.T) {
	result := catalog.ArtistQueue{Returned: 10000, Limit: 10000, Total: 50000, MissingTotal: 17, Truncated: true, Items: make([]catalog.TrackSummary, 10000)}
	for i := range result.Items {
		result.Items[i] = catalog.TrackSummary{TrackID: strconv.Itoa(i), Present: true}
	}
	body, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	sample, err := i10InspectQueue(body)
	if err != nil || sample.ResponseBytes != len(body) || sample.Returned != 10000 || sample.ItemCount != 10000 || sample.Limit != 10000 || sample.Total != 50000 || sample.MissingTotal != 17 || !sample.Truncated {
		t.Fatalf("队列大小/计数遗漏: %+v %v", sample, err)
	}
	result.Returned = 3
	body, err = json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	sample, err = i10InspectQueue(body)
	if err == nil || sample.Returned != 3 || sample.ResponseBytes != len(body) {
		t.Fatal("无效响应丢失实测事实")
	}
	bad := []byte(`{`)
	if sample, err := i10InspectQueue(bad); err == nil || sample.ResponseBytes != len(bad) {
		t.Fatal("解码失败丢失响应字节数")
	}
	claimed := map[string]string{}
	create, _ := i10SelectActions("create")
	if !i10ClaimSupplement(create, claimed, "queue-cancel", "attempt-01") || i10ClaimSupplement(create, claimed, "queue-cancel", "attempt-02") {
		t.Fatal("取消补测不是仅一次")
	}
	reuse, _ := i10SelectActions("reuse")
	if i10ClaimSupplement(reuse, map[string]string{}, "queue-cancel", "attempt-02") {
		t.Fatal("reuse 重做取消补测")
	}
}

func TestI10PopularSuggestionsRejectEmptyAndWrongIdentity(t *testing.T) {
	base := catalog.SearchSuggestions{
		CatalogState:   catalog.CatalogState{SearchMode: "normalized", ArtistsState: "ready"},
		AvailableTypes: []string{"albums", "tracks", "artists"},
		Albums:         catalog.SearchSuggestionSection[catalog.SearchAlbumItem]{Items: make([]catalog.SearchAlbumItem, 5), HasMore: true},
		Tracks:         catalog.SearchSuggestionSection[catalog.SearchTrackItem]{Items: make([]catalog.SearchTrackItem, 5), HasMore: true},
		Artists:        catalog.SearchSuggestionSection[catalog.SearchArtistItem]{Items: []catalog.SearchArtistItem{{Artist: catalog.ArtistSummary{ID: scalePopularArtist}}}},
	}
	for _, q := range i10HotQueries() {
		for _, tc := range []struct {
			name   string
			mutate func(*catalog.SearchSuggestions)
		}{
			{"正确", func(*catalog.SearchSuggestions) {}},
			{"空三栏", func(r *catalog.SearchSuggestions) {
				r.Albums.Items = []catalog.SearchAlbumItem{}
				r.Tracks.Items = []catalog.SearchTrackItem{}
				r.Artists.Items = []catalog.SearchArtistItem{}
			}},
			{"少专辑", func(r *catalog.SearchSuggestions) { r.Albums.Items = r.Albums.Items[:4] }},
			{"少曲目", func(r *catalog.SearchSuggestions) { r.Tracks.Items = r.Tracks.Items[:4] }},
			{"专辑无更多", func(r *catalog.SearchSuggestions) { r.Albums.HasMore = false }},
			{"曲目无更多", func(r *catalog.SearchSuggestions) { r.Tracks.HasMore = false }},
			{"错误艺人", func(r *catalog.SearchSuggestions) {
				r.Artists.Items = []catalog.SearchArtistItem{{Artist: catalog.ArtistSummary{ID: "wrong"}}}
			}},
			{"多艺人", func(r *catalog.SearchSuggestions) {
				r.Artists.Items = append(append([]catalog.SearchArtistItem{}, r.Artists.Items...), r.Artists.Items[0])
			}},
			{"艺人更多", func(r *catalog.SearchSuggestions) { r.Artists.HasMore = true }},
		} {
			r := base
			tc.mutate(&r)
			body, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			if err := i10ValidateSuggestions(q, body); (err == nil) != (tc.name == "正确") {
				t.Fatalf("%s/%s err=%v", q, tc.name, err)
			}
		}
	}
}
func TestI10CaseCheckpointPrecedesPlans(t *testing.T) {
	path := t.TempDir() + "/summary.json"
	report := i10Report{}
	item := i10Case{Query: "周杰伦", Measurements: 30, SuggestionsMS: make([]float64, 30), ResultsMS: make([]float64, 30), Pass: false, Errors: []string{"首次测量失败"}}
	i10CheckpointCase(&report, item, func() {
		if err := i10WriteJSON(path, report); err != nil {
			t.Fatal(err)
		}
	})
	// 计划阶段开始时即可读取完整失败样本；之后的取消不依赖最终 Cleanup 才保存。
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved i10Report
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Cases) != 1 || len(saved.Cases[0].SuggestionsMS) != 30 || len(saved.Cases[0].ResultsMS) != 30 || saved.Cases[0].Pass || saved.Cases[0].Errors[0] != "首次测量失败" {
		t.Fatalf("计划前检查点缺样本: %+v", saved)
	}
}
func TestI10ReadOnlyFixtureIdentityRejectsShrinking(t *testing.T) {
	fixture := []byte("原规模夹具")
	if err := i10ValidateFixtureHash(fixture, i10Hash(fixture)); err != nil {
		t.Fatal(err)
	}
	if i10ValidateFixtureHash([]byte("缩小夹具"), i10Hash(fixture)) == nil || i10ValidateFixtureHash(fixture, "") == nil {
		t.Fatal("夹具 SHA 不一致仍允许复用")
	}
	counts := map[string]int64{"tracks_present": 100000, "tracks_missing": 10000, "releases": 10000, "release_groups": 8000}
	if err := i10ValidateFixtureCounts(counts); err != nil {
		t.Fatal(err)
	}
	for key := range counts {
		counts[key]--
		if i10ValidateFixtureCounts(counts) == nil {
			t.Fatalf("复用容许规模缩小: %s", key)
		}
		counts[key]++
	}
	if i10ValidateFixtureCounts(nil) == nil {
		t.Fatal("复用容许缺失规模证据")
	}
}
