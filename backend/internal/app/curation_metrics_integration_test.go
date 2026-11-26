package app

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"math"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/model"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/coverartarchive"
	"github.com/qlqq/roomusic/backend/internal/upstreammock"
)

type metricEntry struct {
	sample   metricSample
	release  string
	tracks   []string
	artists  map[string]string // 扫描署名到本地艺人编号
	merges   []string
	requests map[string]int
}

func TestPostgreSQLCurationMetrics(t *testing.T) {
	samples := loadMetricSamples(t)
	mock, err := upstreammock.New(upstreammock.Options{DataDir: writeMetricCatalog(t, samples)})
	requireCurationOK(t, err)
	upstream := httptest.NewServer(mock)
	t.Cleanup(upstream.Close)
	endpoint := newMetricsModel(t)
	clock := newProgramClock()
	// 巡检唤醒写入使用数据库时间，程序和模型的测试时钟须能领取已唤醒的项。
	clock.now.Store(time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond).UnixNano())
	opts := clock.options()
	opts.FullPassInterval = time.Minute
	endpoint.setPatrolSamples(samples)
	immediate := func() *sources.Limiter { return sources.NewLimiter(0, nil, nil) }
	app := newConfiguredIntegrationTestApplication(t, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})), config.Server{UpstreamMockURL: upstream.URL},
		WithCurationProgram(true, opts), WithCurationModel(true, model.Options{Now: opts.Now, PollInterval: 10 * time.Millisecond, BatchSize: 1}),
		WithCurationSources(sources.Options{Limiter: immediate()}, coverartarchive.Options{Limiter: immediate(), ManifestLimiter: immediate()}, sources.Options{Limiter: immediate()}))
	clock.wait(t)
	admin := app.createUser(t, "admin")
	b3Root(t, app)
	publishSearch(t, app)
	resp := app.request(t, "PUT", curationBase+"/sources/acoustid", map[string]bool{"enabled": false}, &admin, nil)
	if resp.Code != 200 {
		t.Fatal(resp.Code, resp.Body.String())
	}
	endpoint.configure(t, app, &admin, nil, upstream.URL)
	b3Enable(t, app, admin)
	var entries []metricEntry
	targets := map[string]metricsModelTarget{}
	previous := metricCounters(t, mock)
	seenMerge := map[string]bool{}
	// 固定入库顺序，保留前面样本的对应与缓存。一次增量的所有出站（包括引起的复查）归本次样本。
	for i, s := range samples {
		writeMetricSample(t, app.allowedRoot, s)
		scanAndConfirmCuration(t, app, &admin)
		e := metricScannedEntry(t, app, s)
		target := metricsModelTarget{SampleID: s.ID, ReleaseID: s.Expect.Release, GroupID: s.Expect.Group, Fields: s.Expect.Fields}
		if len(s.Expect.Recordings) == 1 {
			target.RecordingID = s.Expect.Recordings[0]
		}
		for _, release := range s.External.Releases {
			if release.ID == target.ReleaseID {
				target.SearchTitle = release.Title
			}
			for _, track := range release.Tracks {
				if track.ID == target.RecordingID {
					target.SearchTitle = track.Title
				}
			}
		}
		targets[e.release] = target
		for name, id := range e.artists {
			if _, known := targets[id]; !known {
				targets[id] = metricsModelTarget{SampleID: s.ID, ArtistID: s.Expect.Artists[name]}
			}
		}
		endpoint.setTargets(targets)
		metricDrain(t, app, &admin, clock, endpoint, e)
		// 每次增量已完全排空，由新增的操作确定本次样本触发了哪些艺人合并。
		metricReadRows(t, app, `SELECT id FROM operations WHERE operation_type='artist_merge'`, func(rows *sql.Rows) {
			var id string
			requireCurationOK(t, rows.Scan(&id))
			if !seenMerge[id] {
				e.merges = append(e.merges, id)
				seenMerge[id] = true
			}
		})
		next := metricCounters(t, mock)
		e.requests = map[string]int{}
		for key, n := range next {
			if delta := n - previous[key]; delta > 0 {
				e.requests[key] = delta
			}
		}
		previous = next
		entries = append(entries, e)
		if (i+1)%10 == 0 {
			t.Logf("已扫描并排空 %d/%d", i+1, len(samples))
		}
	}
	metricReport(t, app, entries, endpoint)
}

func scanAndConfirmCuration(t *testing.T, app *integrationTestApplication, admin *integrationTestUser) {
	t.Helper()
	runScan(t, app, admin)
	// 模拟观测静默期已过，用第二次正常扫描确认成员签名。
	mustExec(t, app, `UPDATE releases SET curation_changed_at=now()-interval '60 seconds'
		WHERE NOT curation_confirmed AND curation_changed_at IS NOT NULL`)
	runScan(t, app, admin)
}

func metricScannedEntry(t *testing.T, app *integrationTestApplication, s metricSample) metricEntry {
	t.Helper()
	e := metricEntry{sample: s, artists: map[string]string{}}
	rows, err := app.database.Query(`SELECT m.release_id,t.id,t.duration_seconds
 FROM tracks t JOIN media m ON m.id=t.medium_id WHERE t.source_status='present' AND starts_with(t.relative_path,$1)
 ORDER BY m.position,t.position,t.relative_path`, s.Local.Directory+"/")
	requireCurationOK(t, err)
	defer rows.Close()
	for rows.Next() {
		var release, id string
		var seconds float64
		requireCurationOK(t, rows.Scan(&release, &id, &seconds))
		if e.release != "" && release != e.release {
			t.Fatalf("%s 的文件被扫描到多个发行", s.ID)
		}
		i := len(e.tracks)
		if i >= len(s.Local.Tracks) || seconds <= 0 || math.Abs(seconds-float64(s.Local.Tracks[i].Seconds)) > 0.5 {
			t.Fatalf("%s 第 %d 首扫描时长 %.3f 与样本不符", s.ID, i+1, seconds)
		}
		e.release = release
		e.tracks = append(e.tracks, id)
	}
	requireCurationOK(t, rows.Err())
	if len(e.tracks) != len(s.Local.Tracks) || len(e.tracks) == 0 {
		t.Fatalf("%s 扫描曲数 %d，清单 %d", s.ID, len(e.tracks), len(s.Local.Tracks))
	}
	credits, err := catalog.LoadCurationArtistCredits(t.Context(), app.database, e.release)
	requireCurationOK(t, err)
	add := func(group []catalog.CurationArtistCredit) {
		for _, credit := range group {
			e.artists[credit.Name] = credit.ArtistID
		}
	}
	add(credits.ReleaseArtists)
	add(credits.AlbumArtists)
	for _, group := range credits.Tracks {
		add(group)
	}
	return e
}

func metricCounters(t *testing.T, mock *upstreammock.Server) map[string]int {
	t.Helper()
	w := httptest.NewRecorder()
	mock.ServeHTTP(w, httptest.NewRequest("GET", "/_mock/counters", nil))
	var counters map[string]map[string]int
	requireCurationOK(t, json.Unmarshal(w.Body.Bytes(), &counters))
	flat := map[string]int{}
	for svc, kinds := range counters {
		for kind, n := range kinds {
			flat[svc+"/"+kind] = n
		}
	}
	return flat
}

type metricProgress struct {
	curation.Progress
	Model curation.ModelProgress `json:"model"`
}

func metricDrain(t *testing.T, app *integrationTestApplication, admin *integrationTestUser, clock *programClock, endpoint *metricsModel, entry metricEntry) {
	t.Helper()
	sample := entry.sample.ID
	writer := curation.NewWriter(app.database, app.dataDirectory, nil)
	var p metricProgress
	for round := 1; round <= 12; round++ {
		before := metricCheckpoint(t, app)
		tick := time.Unix(0, clock.now.Add(int64(time.Minute))).UTC()
		// 所有程序队列结束这一轮后才让模型返回，保留程序和模型的原有归因边界。
		func() {
			endpoint.gate.Lock()
			defer endpoint.gate.Unlock()
			clock.tick <- tick
			select {
			case <-clock.waiting:
				endpoint.registerEffectiveArtists(t, app, entry)
			case <-time.After(60 * time.Second):
				t.Fatalf("%s 第 %d 轮程序未进入等待", sample, round)
			}
		}()
		end := time.Now().Add(45 * time.Second)
		for {
			getJSON(t, app, admin, curationBase+"/progress", &p)
			if p.Model.State == "idle" && p.Model.Items.InProgress == 0 {
				// 巡检可能唤醒程序识别，等识别结果的艺人项要到下一轮才可领取。
				if p.Model.Items.WaitingModel == 0 || p.Queues[0].ID == "release" && p.Queues[0].Ready > 0 {
					break
				}
			}
			if time.Now().After(end) {
				t.Fatalf("%s 第 %d 轮模型未排空：%+v", sample, round, p.Model)
			}
			time.Sleep(15 * time.Millisecond)
		}
		programDrained := p.State == "idle"
		recognitionSettled := p.Releases.Total == p.Releases.Processed
		for _, q := range p.Queues {
			programDrained = programDrained && q.Ready == 0 && q.State != "running"
			if q.ID == "release" && q.PauseReason != nil && *q.PauseReason == "source_disabled" {
				recognitionSettled = true
			}
		}
		patrolled, err := writer.NamesPatrolled(t.Context(), entry.release)
		requireCurationOK(t, err)
		if patrolled && programDrained && p.Model.Items.WaitingModel == 0 && p.LastPassFinishedAt != nil && p.LastPassFinishedAt.Equal(tick) &&
			recognitionSettled && metricCheckpoint(t, app) == before {
			return
		}
	}
	t.Fatalf("%s 超过 12 个完整轮次：%+v", sample, p)
}

func metricCheckpoint(t *testing.T, app *integrationTestApplication) string {
	t.Helper()
	// 同时观察业务写入、程序证据更新、待定项生命周期与例外。只读轮不会改变这些值。
	return programString(t, app, `SELECT concat_ws('/',(SELECT count(*) FROM operations),
 (SELECT max(updated_at) FROM curation_work_states),(SELECT max(updated_at) FROM curation_pending_items),
 (SELECT count(*) FROM curation_model_exceptions))`)
}
