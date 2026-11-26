package app

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestPostgreSQL整理六类反例无错连错并错改(t *testing.T) {
	t.Setenv("ROOMUSIC_CURATION_METRICS_MODEL", "first_candidate")
	var samples []metricSample
	shapes := map[string]int{}
	for _, sample := range loadMetricSamples(t) {
		if sample.Negative {
			shapes[sample.Shape]++
		}
		if sample.Negative || sample.ID == "picard-01" || sample.ID == "picard-02" {
			samples = append(samples, sample)
		}
	}
	for _, shape := range []string{"negative_homonym", "negative_near_candidate", "negative_compilation", "negative_wrong_id", "negative_artist_merge", "negative_truncated"} {
		if shapes[shape] == 0 {
			t.Fatalf("回归曲库缺少反例 %s", shape)
		}
	}
	app, entries, endpoint := runMetricSamples(t, samples)
	artists := map[string]string{}
	for _, entry := range entries {
		artists[entry.sample.ID] = entry.artists[entry.sample.Local.Artist]
	}
	// 两组近名艺人分别跨两个样本，必须在同一曲库里同时保有独立的身份。
	for _, pair := range [][2]string{{"picard-01", "distinct-artist-01"}, {"picard-02", "distinct-artist-02"}} {
		a, b := artists[pair[0]], artists[pair[1]]
		if a == "" || b == "" || a == b {
			t.Fatalf("近名艺人 %v 扫描出的身份为 %q、%q", pair, a, b)
		}
		b3RequireCount(t, app, 2, `SELECT count(DISTINCT coalesce(o.redirect_artist_id,a.id))
			FROM artists a LEFT JOIN artist_overrides o ON o.artist_id=a.id WHERE a.id IN ($1,$2)`, a, b)
	}
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()
	for i, score := range metricScoreEntries(t, app, entries, endpoint.routes) {
		if entries[i].sample.Negative {
			t.Logf("%s %s：错连 %d，错并 %d，错改 %d", score.Shape, score.ID, score.WrongLinks, score.WrongMerges, score.WrongFields)
		}
		if score.WrongLinks != 0 || score.WrongMerges != 0 || score.WrongFields != 0 {
			t.Errorf("%s 的实际写入与标注冲突：%+v", score.ID, score.Reasons)
		}
	}
}

func TestPostgreSQL整理关闭总开关后双层停止出站(t *testing.T) {
	var s metricSample
	for _, sample := range loadMetricSamples(t) {
		if sample.ID == "stream-01" {
			s = sample
			break
		}
	}
	if s.ID == "" {
		t.Fatal("缺少流媒体专辑样本 stream-01")
	}
	clock, endpoint := newProgramClock(), newMetricsModel(t)
	clock.now.Store(time.Now().UTC().Add(time.Hour).UnixNano())
	app, mock, upstream := newCurationMockApplication(t, writeMetricCatalog(t, []metricSample{s}), clock, patrolModelOptions(clock))
	admin := app.createUser(t, "admin")
	b3Root(t, app)
	publishSearch(t, app)
	endpoint.configure(t, app, &admin, nil, upstream)
	b3Enable(t, app, admin)
	resp := app.request(t, http.MethodPut, curationBase+"/settings", map[string]bool{"enabled": false}, &admin, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("关闭总开关返回 %d：%s", resp.Code, resp.Body)
	}
	writeMetricSample(t, app.allowedRoot, s)
	scanAndConfirmCuration(t, app, &admin)
	entry := metricScannedEntry(t, app, s)
	targets := map[string]metricsModelTarget{entry.release: {SampleID: s.ID, ReleaseID: s.Expect.Release, GroupID: s.Expect.Group}}
	for name, artist := range entry.artists {
		targets[artist] = metricsModelTarget{SampleID: s.ID, ArtistID: s.Expect.Artists[name]}
	}
	endpoint.setTargets(targets)
	clock.advance(t, time.Minute)
	var progress metricProgress
	getJSON(t, app, &admin, curationBase+"/progress", &progress)
	if progress.State != "disabled" || progress.Model.State != "disabled" {
		t.Fatalf("关闭时程序状态 %q，模型状态 %q", progress.State, progress.Model.State)
	}
	if calls, usage := metricCounters(t, mock), endpoint.usage(); len(calls) != 0 || len(usage) != 0 {
		t.Fatalf("总开关关闭期间的来源请求 %v，模型请求 %v", calls, usage)
	}
	b3RequireCount(t, app, 0, `SELECT count(*) FROM steward_conversations WHERE kind='curation'`)
	b3Enable(t, app, admin)
	metricDrain(t, app, &admin, clock, endpoint, entry)
	if calls, patrol := metricCounters(t, mock), endpoint.usage()[s.ID]["patrol"]; len(calls) == 0 || patrol.Calls == 0 {
		t.Fatalf("重新开启后同一专辑应实际请求来源与巡检模型：来源 %v，巡检 %+v", calls, patrol)
	}
}

func TestPostgreSQL整理重扫仅厂牌变化后由队列作废自动值(t *testing.T) {
	clock := newProgramClock()
	app, _, _ := newCurationMockApplication(t, writeMetricCatalog(t, nil), clock)
	admin := app.createUser(t, "admin")
	b3Root(t, app)
	b3Enable(t, app, admin)
	path := filepath.Join(app.allowedRoot, "厂牌重扫", "01.flac")
	rescan := func(label string) {
		t.Helper()
		writeSyntheticTrack(t, path, 71, "ALBUM=厂牌重扫", "ARTIST=合成艺人", "TITLE=合成曲目", "LABEL="+label, "TRACKNUMBER=1")
		scanAndConfirmCuration(t, app, &admin)
		clock.advance(t, time.Minute)
	}
	rescan("首次扫描厂牌")
	release := programString(t, app, `SELECT id FROM releases WHERE relative_directory='厂牌重扫'`)
	check := func(want string) {
		t.Helper()
		var detail struct{ Label string }
		getJSON(t, app, &admin, "/api/v1/releases/"+release, &detail)
		if detail.Label != want {
			t.Fatalf("发行详情显示厂牌 %q，期望 %q", detail.Label, want)
		}
	}
	check("首次扫描厂牌")
	cMatchFixture(app, admin).apply(t, release, "fixture", "", map[string]any{"label": "外部厂牌"})
	check("外部厂牌")
	rescan("新的扫描厂牌")
	check("新的扫描厂牌")
	b3RequireCount(t, app, 0, `SELECT count(*) FROM release_auto_values WHERE release_id=$1 AND field='label'`, release)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM operations WHERE target_id=$1 AND origin->>'provider'='scan'`, release)
}
