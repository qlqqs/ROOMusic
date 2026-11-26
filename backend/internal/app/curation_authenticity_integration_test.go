package app

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/model"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/curation/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/coverartarchive"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
	"github.com/qlqq/roomusic/backend/internal/upstreammock"
)

func newCurationMockApplication(t *testing.T, dataDir string, clock *programClock, applied ...Option) (*integrationTestApplication, *upstreammock.Server, string) {
	t.Helper()
	mock, err := upstreammock.New(upstreammock.Options{DataDir: dataDir})
	requireCurationOK(t, err)
	upstream := httptest.NewServer(mock)
	t.Cleanup(upstream.Close)
	immediate := func() *sources.Limiter { return sources.NewLimiter(0, nil, nil) }
	opts := clock.options()
	opts.FullPassInterval = time.Minute
	applied = append([]Option{WithCurationProgram(true, opts),
		WithCurationSources(sources.Options{Limiter: immediate()}, coverartarchive.Options{Limiter: immediate(), ManifestLimiter: immediate()}, sources.Options{Limiter: immediate()})}, applied...)
	app := newConfiguredIntegrationTestApplication(t, slog.New(slog.NewTextHandler(io.Discard, nil)), config.Server{UpstreamMockURL: upstream.URL}, applied...)
	clock.wait(t)
	return app, mock, upstream.URL
}

func TestPostgreSQLCurationLocalRuleUpgradeQuiet(t *testing.T) {
	var sample metricSample
	for _, s := range loadMetricSamples(t) {
		if s.ID == "stream-01" {
			sample = s
			break
		}
	}
	if sample.ID == "" {
		t.Fatal("缺少度量夹具 stream-01")
	}
	endpoint := newMetricsModel(t)
	clock := newProgramClock()
	app, mock, upstream := newCurationMockApplication(t, writeMetricCatalog(t, []metricSample{sample}), clock,
		WithCurationModel(true, model.Options{Now: clock.options().Now, PollInterval: 10 * time.Millisecond, BatchSize: 1}))
	admin := app.createUser(t, "admin")
	b3Root(t, app)
	publishSearch(t, app)
	writeMetricSample(t, app.allowedRoot, sample)
	scanAndConfirmCuration(t, app, &admin)
	entry := metricScannedEntry(t, app, sample)
	targets := map[string]metricsModelTarget{entry.release: {
		SampleID: sample.ID, ReleaseID: sample.Expect.Release, GroupID: sample.Expect.Group, Fields: sample.Expect.Fields,
	}}
	for name, id := range entry.artists {
		targets[id] = metricsModelTarget{SampleID: sample.ID, ArtistID: sample.Expect.Artists[name]}
	}
	endpoint.configure(t, app, &admin, targets, upstream)
	b3Enable(t, app, admin)
	metricDrain(t, app, &admin, clock, endpoint, entry)
	before := b3WorkState(t, app, "release", entry.release)
	sourceCalls, modelCalls := metricCounters(t, mock), endpoint.usage()
	if before.Status != "matched" || len(sourceCalls) == 0 || len(modelCalls) == 0 {
		t.Fatalf("应先完成发行匹配并实际调用来源和模型，状态 %+v，来源 %v，模型 %v", before, sourceCalls, modelCalls)
	}

	mustExec(t, app, `UPDATE curation_work_states SET local_rule_version=$2 WHERE target_id=$1 AND subject='release'`, entry.release, program.LocalRuleVersion-1)
	for round := 1; round <= 2; round++ {
		clock.advance(t, time.Minute)
		got := b3WorkState(t, app, "release", entry.release)
		if got.LocalRuleVersion != program.LocalRuleVersion || got.MatchRuleVersion != before.MatchRuleVersion ||
			got.Generation != before.Generation || got.Attempts != before.Attempts {
			t.Errorf("第 %d 轮只应升级本地版本，之前 %+v，之后 %+v", round, before, got)
		}
		var progress metricProgress
		getJSON(t, app, &admin, curationBase+"/progress", &progress)
		if progress.Model.State != "idle" || progress.Model.Items.WaitingModel != 0 || progress.Model.Items.InProgress != 0 ||
			!reflect.DeepEqual(metricCounters(t, mock), sourceCalls) || !reflect.DeepEqual(endpoint.usage(), modelCalls) {
			t.Errorf("第 %d 轮产生外部工作，来源 %v -> %v，模型 %v -> %v，进度 %+v",
				round, sourceCalls, metricCounters(t, mock), modelCalls, endpoint.usage(), progress.Model)
		}
	}
}

func TestPostgreSQLCurationAuthenticityFromScan(t *testing.T) {
	clock := newProgramClock()
	real := metricSample{ID: "真实标签", Local: metricLocal{
		Directory: "真实标签目录", Format: "flac", Album: "合成标签专辑", Artist: "合成标签艺人",
		Tracks: []metricLocalTrack{{File: "tagged.flac", Title: "合成标签曲名", Seconds: 100, Tags: map[string]string{"TRACKNUMBER": "1"}}},
	}}
	remote := programAlbum("人工发行标题", "合成标签艺人")
	remoteTrack := remote.Media[0].Tracks[0]
	taggedArtist, trackArtist := uuid.New(), uuid.New()
	real.Local.Tags = map[string]string{"MUSICBRAINZ_ALBUMID": remote.ID}
	real.External.Artists = []metricArtist{{ID: taggedArtist, Name: "合成标签艺人"}, {ID: trackArtist, Name: "外部曲艺人"}}
	real.External.Releases = []metricRelease{{
		ID: remote.ID, Group: remote.Group.ID, Title: remote.Title, Artist: taggedArtist, Date: remote.Date,
		Tracks: []metricRecording{{TrackID: remoteTrack.ID, ID: remoteTrack.RecordingID,
			Title: "合成标签曲名", Artist: trackArtist, Length: 100000}},
	}}
	inferred := metricSample{ID: "推断文字", Local: metricLocal{
		Directory: "合成目录专辑", Format: "flac", Artist: "同名候选艺人",
		Tracks: []metricLocalTrack{{File: "文件名曲子.flac", Seconds: 110}},
	}}
	fallback := metricSample{ID: "无署名", Local: metricLocal{
		Directory: "无署名目录", Format: "flac",
		Tracks: []metricLocalTrack{{File: "01.flac", Seconds: 120}},
	}}
	loose := metricSample{ID: "散放无专辑", Local: metricLocal{
		Directory: ".", Format: "flac", Artist: "散放曲艺人",
		Tracks: []metricLocalTrack{{File: "loose.flac", Title: "散放曲标题", Seconds: 130}},
	}}
	compilation := metricSample{ID: "合辑", Local: metricLocal{
		Directory: "合辑目录", Format: "flac", Album: "合成合辑",
		Tags: map[string]string{"COMPILATION": "1"}, Tracks: []metricLocalTrack{{File: "compilation.flac", Title: "合辑真名", Artist: "合辑曲艺人", Seconds: 140}},
	}}
	releaseID, groupID, recordingID, inferredArtist := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	inferred.External.Artists = []metricArtist{{ID: inferredArtist, Name: "同名候选艺人"}}
	inferred.External.Releases = []metricRelease{{
		ID: releaseID, Group: groupID, Title: "合成目录专辑", Artist: inferredArtist,
		Tracks: []metricRecording{{TrackID: uuid.New(), ID: recordingID, Title: "候选曲", Artist: inferredArtist, Length: 114000}},
	}}
	app, mock, _ := newCurationMockApplication(t, writeMetricCatalog(t, []metricSample{real, inferred}), clock)
	admin := app.createUser(t, "admin")
	b3Root(t, app)
	for _, sample := range []metricSample{real, inferred, fallback, loose, compilation} {
		writeMetricSample(t, app.allowedRoot, sample)
	}
	scanAndConfirmCuration(t, app, &admin)

	read := func(t *testing.T, path string) catalog.CurationEvidence {
		t.Helper()
		id := programString(t, app, `SELECT m.release_id FROM media m JOIN tracks t ON t.medium_id=m.id WHERE t.relative_path=$1 AND t.source_status='present'`, path)
		evidence, err := catalog.LoadCurationEvidence(t.Context(), app.database, []string{id})
		requireCurationOK(t, err)
		e := evidence[id]
		if len(e.Tracks) != 1 {
			t.Fatalf("%s 应有一首可见曲目，得到 %+v", path, e)
		}
		return e
	}
	tagged := read(t, "真实标签目录/tagged.flac")
	tagTrack := tagged.Tracks[0]
	if tagged.Title != "合成标签专辑" || tagged.Authenticity["title"] != metadata.Real ||
		tagTrack.Title != "合成标签曲名" || tagTrack.Artist != "合成标签艺人" ||
		tagTrack.Authenticity["title"] != metadata.Real || tagTrack.Authenticity["artist"] != metadata.Real || tagTrack.Authenticity["position"] != metadata.Real {
		t.Errorf("标签真名和曲号应为真实，得到 %+v，曲目 %+v", tagged.Authenticity, tagTrack)
	}

	folder := read(t, "合成目录专辑/文件名曲子.flac")
	folderTrack := folder.Tracks[0]
	if folder.Title != "合成目录专辑" || folder.Authenticity["title"] != metadata.Inferred ||
		folderTrack.Title != "文件名曲子" || folderTrack.TitleObservation.Source != "filename_fallback" ||
		folderTrack.Authenticity["title"] != metadata.Inferred {
		t.Errorf("目录名与文件名应为推断，发行 %q/%+v，曲目 %+v", folder.Title, folder.Authenticity, folderTrack)
	}
	if folderTrack.Authenticity["disc"] != metadata.Placeholder || folderTrack.DiscNumber != 1 {
		t.Errorf("默认碟号应为占位，得到 %+v", folderTrack)
	}

	display := read(t, "无署名目录/01.flac")
	displayTrack := display.Tracks[0]
	if displayTrack.Artist != "未知艺术家" || displayTrack.ArtistObservation.Source != "display_fallback" ||
		displayTrack.Authenticity["artist"] != metadata.Placeholder || !displayTrack.ArtistPlaceholder {
		t.Errorf("无标签艺人的展示值应为占位，得到 %+v", displayTrack)
	}
	single := read(t, "loose.flac")
	if single.Title != "未知专辑" || single.Authenticity["title"] != metadata.Placeholder {
		t.Errorf("散放曲的展示专辑应为占位，得到 %q/%+v", single.Title, single.Authenticity)
	}
	various := read(t, "合辑目录/compilation.flac")
	if various.Artist != "群星" || various.FieldSources["artist"] != "compilation" ||
		various.SelectedArtistAuth() != metadata.Placeholder || various.Tracks[0].Authenticity["artist"] != metadata.Real {
		t.Errorf("合辑展示署名应为占位，曲艺人应为真实，得到 %q/%+v，曲目 %+v",
			various.Artist, various.Authenticity, various.Tracks[0])
	}

	// 用治理接口设值、设空，并给该发行一个带年份的可配对外部结果。
	writer := curation.NewWriter(app.database, app.dataDirectory, nil)
	f := &curationFixture{operationsEnvironment: &operationsEnvironment{groupEnvironment: &groupEnvironment{application: app, admin: admin}}, writer: writer}
	f.correct(t, tagged.ID, 1, map[string]any{"title": setChange(remote.Title), "year": nullChange})
	correctTrackForCuration(t, f, tagTrack.ID, 1, map[string]any{"artist": setChange("人工曲艺人")})
	manual := read(t, "真实标签目录/tagged.flac")
	if manual.Title != remote.Title || !manual.Corrected["title"] || manual.Authenticity["title"] != metadata.Real ||
		manual.Year != 0 || !manual.Corrected["year"] || manual.Authenticity["year"] != metadata.Placeholder ||
		!manual.Tracks[0].ArtistCorrected || manual.Tracks[0].EffectiveArtist != "人工曲艺人" ||
		manual.Tracks[0].Authenticity["artist"] != metadata.Real {
		t.Errorf("人工设值应为真实，人工设空应保留缺口及修正标志，得到 %+v", manual)
	}
	// 自带有效外部编号的发行仍可补其它空字段。
	f.enable(t)
	clock.advance(t, time.Minute)
	if state := b3WorkState(t, app, "release", tagged.ID); state.Status != "matched" {
		t.Errorf("对照发行应完成标签匹配并有回填机会，得到 %+v", state)
	}
	b3RequireCount(t, app, 0, `SELECT count(*) FROM release_auto_values WHERE release_id=$1 AND field='year'`, tagged.ID)
	b3RequireCount(t, app, 0, `SELECT count(*) FROM track_auto_values WHERE track_id=$1 AND field='artist'`, tagTrack.ID)
	requireCurationAuthenticitySearchEvidence(t, app, mock, f, folder.ID, releaseID)
}

func requireCurationAuthenticitySearchEvidence(t *testing.T, app *integrationTestApplication, mock *upstreammock.Server, f *curationFixture, release, candidate string) {
	t.Helper()
	log := httptest.NewRecorder()
	mock.ServeHTTP(log, httptest.NewRequest(http.MethodGet, "/_mock/requests", nil))
	var requests []struct {
		Service string
		Query   url.Values
	}
	requireCurationOK(t, json.Unmarshal(log.Body.Bytes(), &requests))
	sawInferred, sawLooseLyrics := false, false
	for _, r := range requests {
		for _, values := range r.Query {
			for _, value := range values {
				if strings.Contains(value, "未知专辑") || strings.Contains(value, "未知艺术家") {
					t.Errorf("受控来源 %s 的请求含占位词，查询 %v", r.Service, r.Query)
				}
				sawInferred = sawInferred || r.Service == "musicbrainz" && strings.Contains(value, "合成目录专辑")
				sawLooseLyrics = sawLooseLyrics || r.Service == "lrclib" && strings.Contains(value, "散放曲标题")
			}
		}
	}
	if !sawInferred || !sawLooseLyrics {
		t.Errorf("应实际发出推断专辑搜索和散放曲歌词查询，记录 %+v", requests)
	}
	id := programString(t, app, `SELECT id FROM curation_pending_items
 WHERE target_id=$1 AND kind='release_match' AND status='waiting_model' AND conflicts ? 'duration_mismatch'`, release)
	want, err := f.writer.PendingItem(t.Context(), id)
	requireCurationOK(t, err)
	var candidates []program.Candidate
	requireCurationOK(t, json.Unmarshal(want.Candidates, &candidates))
	if len(candidates) != 1 || candidates[0].ExternalID != candidate {
		t.Fatalf("推断名称的搜索应留下一项候选，得到 %+v", candidates)
	}

	conversation := modelConversation(t, f)
	_, err = f.writer.ClaimPendingItems(t.Context(), conversation, time.Now(), 20)
	requireCurationOK(t, err)
	caller := tool.Caller{Source: tool.SourceCuration, ConversationID: conversation, User: identity.User{ID: f.admin.id, Role: "admin"}}
	ctx := curation.WithModelRun(t.Context(), &curation.ModelRun{ConversationID: conversation, Model: "合成模型"})
	for _, definition := range stewardtools.StewardTools(f.writer, program.NewModelMatcher(app.database, f.writer, nil)) {
		if definition.Name != "read_pending_items" {
			continue
		}
		assertEvidence := func(t *testing.T) {
			t.Helper()
			result, err := definition.Handler(ctx, caller, modelToolInput(t, map[string]any{"item_ids": []string{want.ID}}))
			requireCurationOK(t, err)
			var items []struct {
				ItemID   string `json:"item_id"`
				Evidence struct {
					Authenticity map[string]metadata.Authenticity
					UsesInferred bool `json:"uses_inferred"`
					Tracks       []struct {
						Authenticity map[string]metadata.Authenticity
					}
				}
			}
			requireCurationOK(t, json.Unmarshal(result, &items))
			if len(items) != 1 || items[0].ItemID != want.ID {
				t.Fatalf("模型应读到领取的待定项，得到 %s", result)
			}
			e := items[0].Evidence
			if !e.UsesInferred || e.Authenticity["title"] != metadata.Inferred || e.Authenticity["artist"] != metadata.Real ||
				len(e.Tracks) != 1 || e.Tracks[0].Authenticity["title"] != metadata.Inferred {
				t.Errorf("模型证据应标记推断搜索及发行和曲目各字段的真实性，得到 %s", result)
			}
		}
		t.Run("模型读取新待定项的真实性", assertEvidence)
		// 模拟整份指纹仍有效但尚未写入真实性的历史项。
		mustExec(t, app, `UPDATE curation_pending_items
 SET evidence=(evidence-'authenticity'-'uses_inferred') #- '{tracks,0,authenticity}',
 match_rule_version=NULL, identity_digest=NULL WHERE id=$1`, want.ID)
		t.Run("模型读取旧待定项时补上真实性", assertEvidence)
		return
	}
	t.Fatal("缺少模型待定项读取工具")
}

func TestPostgreSQLCurationClassifications(t *testing.T) {
	app := newIntegrationTestApplication(t)
	admin, member := app.createUser(t, "admin"), app.createUser(t, "user")
	path := curationBase + "/classifications"
	requireAPIError(t, app.request(t, http.MethodGet, path, nil, &member, nil), http.StatusForbidden, "forbidden")
	requireAPIError(t, app.request(t, http.MethodGet, path, nil, nil, nil), http.StatusUnauthorized, "unauthorized")
	var empty struct {
		Releases map[string]int `json:"releases"`
	}
	getJSON(t, app, &admin, path, &empty)
	wantEmpty := map[string]int{"total": 0, "tagged_id": 0, "has_gaps": 0, "local_complete": 0, "insufficient": 0}
	if !reflect.DeepEqual(empty.Releases, wantEmpty) {
		t.Fatalf("空库应返回五个零，得到 %v", empty.Releases)
	}

	root := b3Root(t, app)
	insert := func(title, artist, status string) string {
		t.Helper()
		id, _ := insertGroupedRelease(t, app, root, groupedRelease{title: title, artist: artist, tracks: []string{status}})
		return id
	}
	tagged := insert("未知专辑", "未知艺术家", "present")
	track, _ := externalTrack(t, app, tagged)
	programTag(t, app, track, "mb_release", uuid.New())
	insert("有缺口的发行", "合成艺人", "present")
	complete := insert("本地完整发行", "标签艺人", "present")
	mustExec(t, app, `UPDATE releases SET year=2024 WHERE id=$1`, complete)
	mustExec(t, app, `INSERT INTO release_artworks(release_id,content_hash,mime_type,width,height,storage_key,source_type)
 VALUES($1,$2,'image/png',10,10,'local-cover.png','embedded')`, complete, strings.Repeat("b", 64))
	insert("未知专辑", "未知艺术家", "present")
	insert("不可见发行", "合成艺人", "missing")

	var got curation.Classifications
	getJSON(t, app, &admin, path, &got)
	want := curation.Classifications{
		Versions: curation.RuleVersions{LocalRule: program.LocalRuleVersion, ExternalMatchRule: program.MatchRuleVersion, PatrolPrompt: program.PatrolPromptVersion},
		Releases: catalog.CurationClassCounts{Total: 4, TaggedID: 1, HasGaps: 1, LocalComplete: 1, Insufficient: 1},
	}
	if got != want || got.Releases.Total != got.Releases.TaggedID+got.Releases.HasGaps+got.Releases.LocalComplete+got.Releases.Insufficient {
		t.Errorf("四类计数应互斥且只含可见发行，得到 %+v，期望 %+v", got, want)
	}
}
