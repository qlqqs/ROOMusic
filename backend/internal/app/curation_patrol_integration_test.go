package app

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/model"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
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

func TestPostgreSQL整理增量巡检与管理员重巡(t *testing.T) {
	const primary, alias, uncertain = "晨窗", "Morning Casement", "相似但不确定"
	samples := []metricSample{
		cMatchAlbum("晨光集", primary, 2, uuid.New()),
		cMatchAlbum("潮汛集", alias, 2, uuid.New()),
		cMatchAlbum("晚霞集", uncertain, 2, uuid.New()),
	}
	clock, endpoint := newProgramClock(), newMetricsModel(t)
	endpoint.canonical[primary] = primary + " | Morning Casement"
	endpoint.canonical[alias] = "晨窗乐团 | Morning Casement"
	endpoint.canonical[uncertain] = endpoint.canonical[primary]
	endpoint.lowNames[uncertain] = true
	endpoint.typos[samples[0].Local.Album] = "晨晖集"
	endpoint.rejectMerge = true
	app, _, upstream := newCurationMockApplication(t, writeMetricCatalog(t, nil), clock, patrolModelOptions(clock))
	admin, member := app.createUser(t, "admin"), app.createUser(t, "user")
	b3Root(t, app)
	publishSearch(t, app)
	endpoint.configure(t, app, &admin, nil, upstream)
	setPatrolTestSource(t, app, &admin, "musicbrainz", false)
	setPatrolTestSource(t, app, &admin, "web_search", false)
	b3Enable(t, app, admin)
	add := func(sample metricSample) metricEntry {
		t.Helper()
		writeMetricSample(t, app.allowedRoot, sample)
		scanAndConfirmCuration(t, app, &admin)
		entry := metricScannedEntry(t, app, sample)
		endpoint.setTargets(map[string]metricsModelTarget{entry.release: {SampleID: sample.ID}})
		metricDrain(t, app, &admin, clock, endpoint, entry)
		return entry
	}
	writer := curation.NewWriter(app.database, app.dataDirectory, nil)
	first := add(samples[0])
	b3RequireCount(t, app, 2, `SELECT count(*) FROM curation_patrol_names WHERE model='metrics-main' AND prompt_version=$1 AND NOT needs_patrol`, program.PatrolPromptVersion)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_patrol_names WHERE kind='artist' AND name=$1 AND original_name=$1 AND latin_name=$2 AND confidence='high'`, primary, alias)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_patrol_names WHERE kind='album' AND name=$1 AND issues @> '[{"kind":"typo","suggested":"晨晖集"}]'`, samples[0].Local.Album)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_pending_items WHERE kind='verify_name' AND target_id=$1 AND evidence->>'after'='晨晖集'`, first.release)
	firstTokens := endpoint.usage()[first.sample.ID]["patrol"]
	if firstTokens.Calls != 2 || firstTokens.InputTokens == 0 || firstTokens.OutputTokens == 0 {
		t.Fatalf("首轮应实际巡检艺人与专辑，得到 %+v", firstTokens)
	}
	runScan(t, app, &admin)
	metricDrain(t, app, &admin, clock, endpoint, first)
	if got := endpoint.usage()[first.sample.ID]["patrol"]; got != firstTokens {
		t.Errorf("原样重扫的巡检用量有增量：%+v -> %+v", firstTokens, got)
	}

	r := app.request(t, http.MethodPut, stewardBase+"/settings", map[string]string{
		"endpoint": endpoint.server.URL, "protocol": "openai", "model": "metrics-main-v2", "review_model": "metrics-review",
	}, &admin, nil)
	if r.Code != http.StatusOK {
		t.Fatalf("换模型返回 %d：%s", r.Code, r.Body)
	}
	metricDrain(t, app, &admin, clock, endpoint, first)
	requireCurationOK(t, writer.MaterializePatrol(t.Context(), program.PatrolPromptVersion+1))
	b3RequireCount(t, app, 0, `SELECT count(*) FROM curation_pending_items WHERE kind='name_patrol' AND status<>'resolved'`)
	if got := endpoint.usage()[first.sample.ID]["patrol"]; got != firstTokens {
		t.Errorf("换模型或提升提示词版本触发了重巡：%+v -> %+v", firstTokens, got)
	}
	b3RequireCount(t, app, 2, `SELECT count(*) FROM curation_patrol_names WHERE model='metrics-main' AND prompt_version=$1`, program.PatrolPromptVersion)

	second := add(samples[1])
	third := add(samples[2])
	b3RequireCount(t, app, 2, `SELECT count(*) FROM curation_patrol_names WHERE model='metrics-main'`)
	b3RequireCount(t, app, 4, `SELECT count(*) FROM curation_patrol_names WHERE model='metrics-main-v2' AND prompt_version=$1`, program.PatrolPromptVersion)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_patrol_names WHERE name=$1 AND confidence='low' AND original_name=$1 AND latin_name=''`, uncertain)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_pending_items WHERE kind='verify_artist_merge'`)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_pending_items WHERE kind='verify_artist_merge'
		AND evidence->>'artist_a' IN ($1,$2) AND evidence->>'artist_b' IN ($1,$2)`, first.artists[primary], second.artists[alias])

	oldTitle := third.sample.Local.Album
	changed := third.sample
	changed.Local.Album = "晚霞与星光集"
	thirdBefore := endpoint.usage()[third.sample.ID]["patrol"].Calls
	third = add(changed)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_patrol_names WHERE name=$1 AND patrolled_at IS NOT NULL`, changed.Local.Album)
	b3RequireCount(t, app, 0, `SELECT count(*) FROM curation_patrol_inputs i JOIN curation_patrol_names n ON i.name_id=n.id WHERE n.name=$1`, oldTitle)
	if got := endpoint.usage()[third.sample.ID]["patrol"].Calls; got != thirdBefore+1 {
		t.Errorf("改名只应新增专辑批：%d -> %d", thirdBefore, got)
	}

	path := curationBase + "/repatrol"
	requireAPIError(t, app.request(t, http.MethodPost, path, nil, &member, nil), http.StatusForbidden, "forbidden")
	requireAPIError(t, app.request(t, http.MethodPost, path, nil, &admin, map[string]string{"Origin": "http://untrusted.example"}), http.StatusForbidden, "origin_forbidden")
	// 令已成功持久化的结果代表升级前的版本，重巡后应由真实运行以当前版本覆盖。
	mustExec(t, app, `UPDATE curation_patrol_names SET prompt_version=$1`, program.PatrolPromptVersion-1)
	r = app.request(t, http.MethodPost, path, nil, &admin, nil)
	if r.Code != http.StatusAccepted || r.Body.Len() != 0 {
		t.Fatalf("重巡返回 %d：%s", r.Code, r.Body)
	}
	for _, entry := range []metricEntry{first, second, third} {
		metricDrain(t, app, &admin, clock, endpoint, entry)
	}
	b3RequireCount(t, app, 6, `SELECT count(DISTINCT n.id) FROM curation_patrol_names n JOIN curation_patrol_inputs i ON i.name_id=n.id
		WHERE n.model='metrics-main-v2' AND n.prompt_version=$1 AND NOT n.needs_patrol`, program.PatrolPromptVersion)
	b3RequireCount(t, app, 0, `SELECT count(*) FROM operations WHERE origin->>'source'='curation'`)
	b3RequireCount(t, app, 0, `SELECT count(*) FROM artist_overrides WHERE redirect_artist_id IS NOT NULL`)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM releases WHERE id=$1 AND title=$2`, first.release, samples[0].Local.Album)
}

func TestPostgreSQL整理规范名搜索并合并艺人(t *testing.T) {
	const primary, alias, external = "周杰伦", "Jay Chou", "周杰倫"
	artist := metricArtist{ID: uuid.New(), Name: external, Aliases: []string{alias}}
	a := cMatchAlbum("虚构的时光线", primary, 5, artist.ID)
	a.Local.Tags = map[string]string{"MUSICBRAINZ_ALBUMID": a.External.Releases[0].ID, "ALBUMARTIST": primary}
	b := cMatchAlbum("虚构的雨夜集", alias, 5, artist.ID)
	b.Local.Tags = map[string]string{"ALBUMARTIST": alias}
	b.Local.Album = "虚构的雨页集"
	a.External.Artists = []metricArtist{artist}
	samples := []metricSample{a, b}
	clock, endpoint := newProgramClock(), newMetricsModel(t)
	clock.now.Store(time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond).UnixNano())
	endpoint.setPatrolSamples(samples)
	endpoint.typos[b.Local.Album] = b.External.Releases[0].Title
	endpoint.rejectMerge = true
	app, mock, upstream := newCurationMockApplication(t, writeMetricCatalog(t, samples), clock, patrolModelOptions(clock))
	admin := app.createUser(t, "admin")
	b3Root(t, app)
	publishSearch(t, app)
	aliasID := uuid.New()
	mustExec(t, app, `INSERT INTO artists(id,initial_name) VALUES($1,'Jay Chou')`, aliasID)
	mustExec(t, app, `UPDATE artist_name_keys SET initial_artist_id=$1 WHERE normalized_key='jay chou'`, aliasID)
	b3Enable(t, app, admin)
	var entries []metricEntry
	for _, s := range samples {
		writeMetricSample(t, app.allowedRoot, s)
		scanAndConfirmCuration(t, app, &admin)
		entry := metricScannedEntry(t, app, s)
		targets := map[string]metricsModelTarget{entry.release: {SampleID: s.ID, ReleaseID: s.External.Releases[0].ID}}
		for _, id := range entry.artists {
			targets[id] = metricsModelTarget{SampleID: s.ID, ArtistID: artist.ID}
		}
		endpoint.setTargets(targets)
		entries = append(entries, entry)
		clock.advance(t, time.Minute)
	}
	left, right := entries[0], entries[1]
	b3RequireCount(t, app, 1, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1 AND external_id=$2`, left.release, a.External.Releases[0].ID)
	b3RequireCount(t, app, 0, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1`, right.release)
	requestStart := len(cMatchRequests(t, mock))
	endpoint.configure(t, app, &admin, nil, upstream)
	setPatrolTestSource(t, app, &admin, "web_search", false)
	metricDrain(t, app, &admin, clock, endpoint, right)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1 AND external_id=$2`, right.release, b.External.Releases[0].ID)
	var searchedCanonical bool
	for _, request := range cMatchRequests(t, mock)[requestStart:] {
		q := request.Query.Get("query")
		searchedCanonical = searchedCanonical || request.Path == "/musicbrainz/ws/2/release" && strings.Contains(q, b.External.Releases[0].Title)
	}
	if !searchedCanonical {
		t.Error("巡检结束后没有用规范名查询该发行")
	}
	b3RequireCount(t, app, 0, `SELECT count(*) FROM curation_pending_items WHERE target_id=$1 AND kind='release_match'
		AND resolution='model_matched'`, right.release)
	b3RequireCount(t, app, 0, `SELECT count(*) FROM operations WHERE operation_type='artist_merge' AND origin->>'provider_kind'='model'`)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM operations WHERE operation_type='artist_merge' AND origin->>'provider_kind'='source'`)
	merged := programString(t, app, `SELECT COALESCE(redirect_artist_id,artist_id) FROM artist_overrides WHERE artist_id IN ($1,$2) AND redirect_artist_id IS NOT NULL`, left.artists[primary], right.artists[alias])
	b3RequireCount(t, app, 2, `SELECT count(*) FROM (
		SELECT name_id FROM artist_effective_credits WHERE artist_id=$1 GROUP BY name_id
		HAVING count(DISTINCT track_id)+count(DISTINCT release_id)=6
	) v JOIN artist_names n ON n.id=v.name_id WHERE n.spelling IN ($2,$3)`, merged, primary, alias)
	assertNames := func(display string, names ...string) {
		t.Helper()
		for _, name := range names {
			var list catalog.ArtistPage[catalog.ArtistSummary]
			getJSON(t, app, &admin, "/api/v1/artists?q="+url.QueryEscape(name), &list)
			if len(list.Items) != 1 || list.Items[0].ID != merged || list.Items[0].Name != display {
				t.Errorf("用 %q 搜索应得合并后的 %q，实际 %+v", name, display, list.Items)
			}
		}
	}
	assertNames(external, primary, alias, external)
	// 曲库增加主名写法的署名后，库内最高票应优先。
	majority := cMatchAlbum("额外的主名专辑", primary, 2, artist.ID)
	writeMetricSample(t, app.allowedRoot, majority)
	runScan(t, app, &admin)
	assertNames(primary, primary, alias)
	detail := readArtistMedia(t, app, &admin, merged)
	r := app.request(t, http.MethodPatch, "/api/v1/artists/"+merged+"/name", map[string]any{
		"expected_revision": detail.Revision, "mode": "set", "name": "人工主名称",
	}, &admin, idempotencyHeader(uuid.New()))
	if r.Code != http.StatusOK {
		t.Fatalf("人工命名返回 %d：%s", r.Code, r.Body)
	}
	assertNames("人工主名称", primary, alias, "人工主名称")
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
	if first.Artist != "灯塔合奏" || first.Title != "旧灯之晨" || first.Authenticity["title"] != metadata.Inferred ||
		first.TitleObservation.Source != "filename_fallback" || first.ArtistObservation.Source != "filename_fallback" {
		t.Fatalf("应在模型配置前从文件名解析署名与标题：%+v", first)
	}
	endpoint.configure(t, app, &admin, map[string]metricsModelTarget{entry.release: {SampleID: s.ID}}, upstream)
	for name, id := range entry.artists {
		endpoint.setTargets(map[string]metricsModelTarget{id: {SampleID: s.ID, ArtistID: s.Expect.Artists[name]}})
	}
	b3Enable(t, app, admin)
	metricDrain(t, app, &admin, clock, endpoint, entry)
	usage := endpoint.usage()[s.ID]
	if usage["local_cleanup"].Calls != 0 || usage["select"].Calls+usage["lookup"].Calls+usage["web"].Calls != 0 {
		t.Errorf("扫描可解析、时长相符的发行应由程序定案：%v", usage)
	}
	b3RequireCount(t, app, 0, `SELECT count(*) FROM curation_pending_items WHERE kind='local_cleanup' AND target_id=$1`, entry.release)
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
	var input curation.LocalInput
	raw, err := json.Marshal(map[string]any{"title": "海港剪报", "artist": "凭空编造艺人", "tracks": []map[string]string{
		{"track_id": entry.tracks[0], "title": "纸灯"},
		{"track_id": entry.tracks[1], "title": "凭空编造曲名"},
	}})
	requireCurationOK(t, err)
	requireCurationOK(t, json.Unmarshal(raw, &input))
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
