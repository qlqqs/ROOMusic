package app

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/model"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/playback/fingerprint"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/coverartarchive"
	"github.com/qlqq/roomusic/backend/internal/upstreammock"
)

const fixedData = "testdata/curation_regression"

type fixedSample struct {
	metricSample
	Source               string `json:"source_sample"`
	Program, Script      string
	OptionalFields       map[string]string   `json:"optional_fields"`
	OptionalScriptFields map[string]string   `json:"optional_script_fields"`
	ScriptTrackFields    []map[string]string `json:"script_track_fields"`
	ExpectedTitles       []string            `json:"expected_titles"`
	NoDetail             bool                `json:"no_detail"`
}

func fixedSamples(t *testing.T, selected string) []fixedSample {
	raw, err := os.ReadFile(filepath.Join(fixedData, "fixed.json"))
	requireCurationOK(t, err)
	var rows []json.RawMessage
	requireCurationOK(t, json.Unmarshal(raw, &rows))
	originals := map[string]metricSample{}
	for _, s := range loadMetricSamples(t) {
		originals[s.ID] = s
	}
	ids := strings.Split(selected, ",")
	var result []fixedSample
	for _, r := range rows {
		var s fixedSample
		requireCurationOK(t, json.Unmarshal(r, &s))
		if s.Source != "" {
			base, ok := originals[s.Source]
			if !ok {
				t.Fatalf("未知旧样本 %s", s.Source)
			}
			// 反序列化覆盖同目录的旧输入，字段答案和扫描约束按整项替换。
			if s.Expect.Fields != nil {
				base.Expect.Fields = nil
			}
			if s.Expect.Scan != nil {
				base.Expect.Scan = nil
			}
			s.metricSample = base
			requireCurationOK(t, json.Unmarshal(r, &s))
		}
		if selected == "" || slices.Contains(ids, s.ID) {
			result = append(result, s)
			ids = slices.DeleteFunc(ids, func(id string) bool { return id == s.ID })
		}
	}
	if len(result) == 0 || selected != "" && len(ids) > 0 {
		t.Fatalf("样本筛选无效：%s", selected)
	}
	return result
}

type fixedCorpus struct {
	root, catalog string
	tool          *fingerprint.Fingerprinter
}

func prepareFixedCorpus(t *testing.T, samples []fixedSample) fixedCorpus {
	f := fixedCorpus{root: t.TempDir()}
	var catalog []metricSample
	for _, s := range samples {
		writeMetricSample(t, f.root, s.metricSample)
		if s.Local.Format == "fingerprint" {
			f.tool = b3RealFingerprinter(t)
			s.External.Fingerprints = map[string][]string{}
			for i, tr := range s.Local.Tracks {
				file, err := os.Open(filepath.Join(f.root, s.Local.Directory, tr.File))
				requireCurationOK(t, err)
				key, err := f.tool.File(t.Context(), file, nil, nil)
				file.Close()
				requireCurationOK(t, err)
				if _, ok := s.External.Fingerprints[key]; ok {
					t.Fatalf("%s 第 %d 首声纹与另一首相同", s.ID, i+1)
				}
				s.External.Fingerprints[key] = []string{s.Expect.Recordings[i]}
			}
		}
		catalog = append(catalog, s.metricSample)
	}
	f.catalog = writeMetricCatalog(t, catalog)
	return f
}

func fixedSource(t *testing.T, app *integrationTestApplication, admin *integrationTestUser, id string, enabled bool) {
	t.Helper()
	resp := app.request(t, "PUT", curationBase+"/sources/"+id, map[string]bool{"enabled": enabled}, admin, nil)
	if resp.Code != 200 {
		t.Fatalf("设置来源 %s HTTP %d %s", id, resp.Code, resp.Body)
	}
}
func fixedConfigure(t *testing.T, app *integrationTestApplication, admin *integrationTestUser, base string, script, prints bool) {
	b3Root(t, app)
	publishSearch(t, app)
	if prints {
		b3SetHTTPKey(t, app, admin, "application", b3AppKey)
		b3SetHTTPKey(t, app, admin, "user", b3UserKey)
	}
	var list struct{ Items []curation.SourceSettings }
	getJSON(t, app, admin, curationBase+"/sources", &list)
	for _, v := range list.Items {
		enabled := v.ID == "musicbrainz" || prints && v.ID == "acoustid" || script && v.ID == "web_search"
		if v.Enabled != enabled {
			fixedSource(t, app, admin, v.ID, enabled)
		}
	}
	if script {
		response := app.request(t, "PUT", curationBase+"/settings", map[string]int{"model_concurrency": 1}, admin, nil)
		if response.Code != 200 {
			t.Fatalf("设置单项脚本并发 %d %s", response.Code, response.Body)
		}
		resp := app.request(t, "PUT", stewardBase+"/settings", map[string]string{
			"endpoint": base + "/openai/chat/v1", "protocol": "openai", "model": "mock-model", "review_model": "mock-reviewer", "search_source": "searxng", "searxng_url": base + "/searxng",
		}, admin, nil)
		if resp.Code != 200 {
			t.Fatalf("配置脚本模型 %d %s", resp.Code, resp.Body)
		}
	}
}

type fixedPartition struct {
	ids     []string
	tracks  []string
	members map[string][]string
}

func fixedScanPartition(t *testing.T, app *integrationTestApplication, s fixedSample) fixedPartition {
	part := fixedPartition{tracks: make([]string, len(s.Local.Tracks)), members: map[string][]string{}}
	paths := map[string]int{}
	for i, tr := range s.Local.Tracks {
		paths[path.Join(s.Local.Directory, tr.File)] = i
	}
	cueIndex := 0
	metricReadRows(t, app, `SELECT m.release_id,t.id,t.relative_path FROM tracks t JOIN media m ON m.id=t.medium_id
 WHERE t.source_status='present' ORDER BY t.relative_path,m.position,t.position`, func(rows *sql.Rows) {
		var release, track, file string
		requireCurationOK(t, rows.Scan(&release, &track, &file))
		part.members[release] = append(part.members[release], file)
		i, own := paths[file]
		if s.Local.Cue && strings.HasPrefix(file, s.Local.Directory+"/") {
			i, own = cueIndex, true
			cueIndex++
		}
		if own && i < len(part.tracks) {
			part.tracks[i] = track
			if !slices.Contains(part.ids, release) {
				part.ids = append(part.ids, release)
			}
		}
	})
	return part
}

func fixedSuite(t *testing.T, suite string) {
	selection := os.Getenv("ROOMUSIC_CURATION_METRICS_SAMPLES")
	if suite == "idle" {
		selection = "editions-model-quartet,picard-01,unlisted-01"
	}
	samples := fixedSamples(t, selection)
	corpus := prepareFixedCorpus(t, samples)
	if suite == "idle" {
		fixedIdle(t, samples, corpus)
		return
	}
	result := fixedReport{Totals: map[string]fixedTotal{}}
	for _, mode := range []string{"program", "script"} {
		started := time.Now()
		for i := 0; i < len(samples); {
			end := i + 1
			top, _, _ := strings.Cut(samples[i].Local.Directory, "/")
			for end < len(samples) {
				dir, _, _ := strings.Cut(samples[end].Local.Directory, "/")
				if dir != top {
					break
				}
				end++
			}
			batch := samples[i:end]
			if !t.Run(mode+"/"+batch[0].ID, func(t *testing.T) { fixedBatch(t, mode, batch, corpus, &result) }) {
				t.Fatalf("未完成 %s/%s", mode, batch[0].ID)
			}
			i = end
		}
		result.summarize(mode, time.Since(started))
	}
	result.finish(t, suite)
}

func fixedBatch(t *testing.T, mode string, batch []fixedSample, corpus fixedCorpus, report *fixedReport) {
	mock, err := upstreammock.New(upstreammock.Options{DataDir: corpus.catalog, ScriptFile: path.Join(fixedData, "fixed-script.json")})
	requireCurationOK(t, err)
	meter := newFixedMeter(mock)
	server := httptest.NewServer(meter)
	t.Cleanup(server.Close)
	clock := newProgramClock()
	clock.now.Store(time.Now().UTC().Add(time.Hour).UnixNano())
	opts := clock.options()
	opts.FullPassInterval = time.Minute
	script := mode == "script"
	prints := batch[0].Local.Format == "fingerprint"
	immediate := func() *sources.Limiter { return sources.NewLimiter(0, nil, nil) }
	app := newConfiguredIntegrationTestApplication(t, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})), config.Server{UpstreamMockURL: server.URL}, WithCurationProgram(true, opts),
		WithCurationModel(script, model.Options{Now: opts.Now, PollInterval: 10 * time.Millisecond, BatchSize: 1}), WithCurationSources(sources.Options{Limiter: immediate()}, coverartarchive.Options{Limiter: immediate(), ManifestLimiter: immediate()}, sources.Options{Limiter: immediate()}), WithFingerprinter(corpus.tool), WithAcoustID(b3AcoustOptions(server)))
	clock.wait(t)
	admin := app.createUser(t, "admin")
	fixedConfigure(t, app, &admin, server.URL, script, prints)
	top, _, _ := strings.Cut(batch[0].Local.Directory, "/")
	requireCurationOK(t, os.CopyFS(filepath.Join(app.allowedRoot, top), os.DirFS(filepath.Join(corpus.root, top))))
	runScan(t, app, &admin)
	mustExec(t, app, `UPDATE releases SET curation_changed_at=now()+interval '1 day'`)
	var begin time.Time
	owners := map[string]string{}
	b3Enable(t, app, admin)
	// 目录级本地辅助要求完整目录成员先进入稳定集。固定样本按样本记账，
	// 但同一批次的成员必须在第一张开始排空前一起确认，否则本地项会被
	// 稳定集门槛暂存到同目录的下一张样本。
	parts := make([]fixedPartition, len(batch))
	for i, s := range batch {
		parts[i] = fixedScanPartition(t, app, s)
		for _, id := range parts[i].ids {
			mustExec(t, app, `UPDATE releases SET curation_changed_at=now()-interval '60 seconds' WHERE id=$1`, id)
		}
	}
	runScan(t, app, &admin)
	for i, s := range batch {
		part := parts[i]
		row := newFixedRow(s, mode)
		start := time.Now()
		begin = start
		countBefore := metricCounters(t, mock)
		shared := false
		for _, id := range part.ids {
			if owner, ok := owners[id]; ok {
				row.Issues = append(row.Issues, "请求归入 "+owner)
				shared = true
				continue
			}
			owners[id] = s.ID
			// 交替推进真实程序轮和模型回包。HTTP 闸门只在固定的快速计数中使用。
			func() {
				meter.gate.Lock()
				defer meter.gate.Unlock()
				clock.advance(t, time.Minute)
			}()
			elapsed := fixedDrain(t, app, &admin, clock, meter, script, id, start)
			row.ElapsedMS = elapsed.Milliseconds()
		}
		if shared && len(part.ids) == 1 {
			row.ElapsedMS = 0
		}
		row.Requests = meter.counts(begin)
		countAfter := metricCounters(t, mock)
		total := 0
		for key, v := range countAfter {
			total += v - countBefore[key]
		}
		if total != row.requestCount() {
			t.Fatalf("样本 %s 出站未对账：mock=%d，明细=%d", s.ID, total, row.requestCount())
		}
		queries := metricQueriesSince(t, mock, begin)
		if s.ID == "one-char" || s.ID == "romanized" {
			assertFixedJudgeQueries(t, s, mode, queries)
		}
		if s.ID == "embedded-artist" {
			assertFixedLocalTitleConclusion(t, app, mode, part)
		}
		row.score(t, app, s, part, queries)
		report.Rows = append(report.Rows, row)
	}
}

// 记录发行与必要字段首次提交的墙钟时间，收齐本窗口的附带调用后再交给下一张。
func fixedDrain(t *testing.T, app *integrationTestApplication, admin *integrationTestUser, c *programClock, meter *fixedMeter, script bool, id string, start time.Time) time.Duration {
	var matched time.Duration
	var p metricProgress
	for round := 0; round < 12; round++ {
		before := metricCheckpoint(t, app)
		func() { meter.gate.Lock(); defer meter.gate.Unlock(); c.advance(t, time.Minute) }()
		deadline := time.Now().Add(15 * time.Second)
		idleSince := time.Time{}
		for {
			p = metricProgress{}
			getJSON(t, app, admin, curationBase+"/progress", &p)
			if matched == 0 && countRows(t, app, `SELECT count(*) FROM external_links WHERE object_id=$1 AND object_type IN ('release','release_group_match')`, id) > 0 {
				matched = time.Since(start)
			}
			if p.Model.Running == 0 && p.Model.Items.InProgress == 0 {
				if idleSince.IsZero() {
					idleSince = time.Now()
				}
			} else {
				idleSince = time.Time{}
			}
			if !script || !idleSince.IsZero() && time.Since(idleSince) >= 30*time.Millisecond && (p.Model.Items.WaitingModel == 0 || p.Queues[0].Ready > 0 || time.Since(idleSince) >= 500*time.Millisecond) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("模型未结束：%+v", p.Model)
			}
			time.Sleep(15 * time.Millisecond)
		}
		if script && p.Model.UnavailableReason != nil {
			t.Fatalf("脚本模型不可用：%s", *p.Model.UnavailableReason)
		}
		// 同一目录的本地辅助项可能覆盖多张发行。只有当前样本已经离开
		// pending，才能把这一张视为排空，避免把目录级辅助留给下一张样本。
		state := b3WorkState(t, app, "release", id)
		if metricCheckpoint(t, app) == before && state.Status != "pending" && p.Queues[0].Ready == 0 && p.Model.Items.InProgress == 0 {
			if matched > 0 {
				return matched
			}
			return time.Since(start)
		}
	}
	t.Fatalf("程序未结束：%+v", p)
	return 0
}

func fixedSave(t *testing.T, file string, v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	requireCurationOK(t, err)
	requireCurationOK(t, os.MkdirAll(filepath.Dir(file), 0755))
	requireCurationOK(t, os.WriteFile(file, append(data, '\n'), 0644))
}
