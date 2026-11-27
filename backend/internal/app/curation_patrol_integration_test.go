package app

import (
	"net/http"

	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/model"
)

func patrolModelOptions(clock *programClock) Option {
	return WithCurationModel(true, model.Options{Now: clock.options().Now, PollInterval: 10 * time.Millisecond, BatchSize: 1})
}

func setPatrolTestSource(t *testing.T, app *integrationTestApplication, admin *integrationTestUser, source string, enabled bool) {
	t.Helper()
	resp := app.request(t, http.MethodPut, curationBase+"/sources/"+source, map[string]bool{"enabled": enabled}, admin, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("设置来源 %s 返回 %d：%s", source, resp.Code, resp.Body)
	}
}

func TestPostgreSQL整理无标签文件名直接识别(t *testing.T) {
	var s metricSample
	for _, sample := range loadMetricSamples(t) {
		if sample.ID == "poor-01" {
			s = sample
			break
		}
	}
	if s.ID == "" {
		t.Fatal("缺少无标签夹具 poor-01")
	}
	clock, endpoint := newProgramClock(), newMetricsModel(t)
	app, mock, upstream := newCurationMockApplication(t, writeMetricCatalog(t, []metricSample{s}), clock, patrolModelOptions(clock))
	admin := app.createUser(t, "admin")
	b3Root(t, app)
	publishSearch(t, app)
	writeMetricSample(t, app.allowedRoot, s)
	scanAndConfirmCuration(t, app, &admin)
	entry := metricScannedEntry(t, app, s)
	evidence, err := catalog.LoadCurationEvidence(t.Context(), app.database, []string{entry.release})
	requireCurationOK(t, err)
	first := evidence[entry.release].Tracks[0]
	if !first.ArtistPlaceholder || first.Title != "灯塔合奏 - 旧灯之晨" || first.Authenticity["title"] != metadata.Inferred ||
		first.TitleObservation.Source != "filename_fallback" || first.ArtistObservation.Source != "display_fallback" {
		t.Fatalf("无标签扫描应保留原文件名片段和占位艺人：%+v", first)
	}
	endpoint.localReply = func(item metricsPending) curation.LocalInput {
		input := metricLocalAnswer(item, "旧灯", "灯塔合奏")
		for _, track := range evidence[entry.release].Tracks {
			name := strings.TrimPrefix(track.Title, "灯塔合奏 - ")
			input.Texts = append(input.Texts, curation.LocalText{Target: track.ID, Field: "title", Value: name}, curation.LocalText{Target: track.ID, Field: "artist", Value: "灯塔合奏"})
		}
		return input
	}
	endpoint.configure(t, app, &admin, map[string]metricsModelTarget{entry.release: {SampleID: s.ID}}, upstream)
	for name, id := range entry.artists {
		endpoint.setTargets(map[string]metricsModelTarget{id: {SampleID: s.ID, ArtistID: s.Expect.Artists[name]}})
	}
	b3Enable(t, app, admin)
	metricDrain(t, app, &admin, clock, endpoint, entry)
	usage := endpoint.usage()[s.ID]
	if usage["local_cleanup"].Calls != 1 || usage["select"].Calls+usage["lookup"].Calls+usage["web"].Calls != 0 {
		t.Errorf("本地片段还原后应由程序核实发行：%v", usage)
	}
	b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_pending_items WHERE kind='local_cleanup' AND target_id=$1 AND resolution='local_cleanup'`, entry.release)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM release_auto_values WHERE release_id=$1 AND field='label' AND text_value=$2`, entry.release, s.Expect.Fields["label"])
	b3RequireCount(t, app, len(entry.tracks), `SELECT count(*) FROM external_links WHERE object_type='track' AND object_id=ANY($1::uuid[])`, entry.tracks)
	b3RequireReleaseResult(t, app, entry.release, "matched", "search", "unavailable", "")
	var found bool
	for _, req := range cMatchRequests(t, mock) {
		q := req.Query.Get("query")
		found = found || req.Path == "/musicbrainz/ws/2/release" && strings.Contains(q, "旧灯") && strings.Contains(q, "灯塔合奏")
	}
	if !found {
		t.Error("缺少使用扫描推断文字的发行搜索")
	}
}

func TestPostgreSQL整理本地模型只写本地片段并保留碟号曲序(t *testing.T) {
	s := metricSample{ID: "本地文字", Local: metricLocal{
		Directory: "海港剪报 [FLAC]", Format: "flac",
		Tracks: []metricLocalTrack{
			{File: "01_[纸灯].flac", Seconds: 100, Tags: map[string]string{"TRACKNUMBER": "3", "DISCNUMBER": "2"}},
			{File: "02_[潮讯].flac", Seconds: 110, Tags: map[string]string{"TRACKNUMBER": "8", "DISCNUMBER": "2"}},
		},
	}}
	clock, endpoint := newProgramClock(), newMetricsModel(t)
	app, _, upstream := newCurationMockApplication(t, writeMetricCatalog(t, nil), clock, patrolModelOptions(clock))
	admin := app.createUser(t, "admin")
	b3Root(t, app)
	publishSearch(t, app)
	writeMetricSample(t, app.allowedRoot, s)
	scanAndConfirmCuration(t, app, &admin)
	entry := metricScannedEntry(t, app, s)
	order := func() string {
		return programString(t, app, `SELECT jsonb_agg(jsonb_build_array(t.id,m.position,t.position,t.disc_number) ORDER BY t.id)::text
			FROM tracks t JOIN media m ON m.id=t.medium_id WHERE t.id=ANY($1::uuid[])`, entry.tracks)
	}
	before := order()
	scanned, err := catalog.LoadCurationEvidence(t.Context(), app.database, []string{entry.release})
	requireCurationOK(t, err)
	if e := scanned[entry.release]; e.Title != "海港剪报" || e.Authenticity["title"] != metadata.Inferred {
		t.Fatalf("扫描应从目录推断去噪后的专辑名，实际为 %q、%v", e.Title, e.Authenticity["title"])
	}
	input := curation.LocalInput{Decision: "apply", Texts: []curation.LocalText{
		{Target: entry.release, Field: "title", Value: "海港剪报"}, {Target: entry.release, Field: "album_artist", Value: "凭空编造艺人"},
		{Target: entry.tracks[0], Field: "title", Value: "纸灯"}, {Target: entry.tracks[1], Field: "title", Value: "凭空编造曲名"},
	}}
	endpoint.localReply = func(metricsPending) curation.LocalInput { return input }
	endpoint.configure(t, app, &admin, map[string]metricsModelTarget{entry.release: {SampleID: s.ID}}, upstream)
	setPatrolTestSource(t, app, &admin, "musicbrainz", false)
	setPatrolTestSource(t, app, &admin, "web_search", false)
	b3Enable(t, app, admin)
	metricDrain(t, app, &admin, clock, endpoint, entry)
	if got := order(); got != before {
		t.Errorf("整理改变了曲序或碟号：%s -> %s", before, got)
	}
	if use := endpoint.usage()[s.ID]["local_cleanup"]; use.Calls != 1 || use.InputTokens == 0 || use.OutputTokens == 0 {
		t.Errorf("应调用一次本地整理：%+v", use)
	}
	b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_pending_items WHERE target_id=$1 AND kind='local_cleanup' AND resolution='local_cleanup'`, entry.release)
	b3RequireCount(t, app, 0, `SELECT count(*) FROM release_auto_values WHERE release_id=$1`, entry.release)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM track_auto_values v JOIN operations o ON o.id=v.operation_id
		WHERE v.track_id=$1 AND v.field='title' AND v.text_value='纸灯' AND v.writer_level=1 AND o.origin->>'mode'='local_cleanup'`, entry.tracks[0])
	b3RequireCount(t, app, 0, `SELECT count(*) FROM track_auto_values WHERE track_id=$1`, entry.tracks[1])
	evidence, err := catalog.LoadCurationEvidence(t.Context(), app.database, []string{entry.release})
	requireCurationOK(t, err)
	search := curation.LocalSearchEvidence(evidence[entry.release])
	if search.Title != "海港剪报" || search.Authenticity["title"] != metadata.Inferred ||
		search.Tracks[0].Title != "纸灯" || search.Tracks[0].TitleObservation.Source != "model_inferred" {
		t.Errorf("本地整理文字进入搜索时应标为推断：专辑 %q，曲目 %+v", search.Title, search.Tracks[0])
	}
}
