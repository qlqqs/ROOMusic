package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/model"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/coverartarchive"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
	"github.com/qlqq/roomusic/backend/internal/upstreammock"
)

func cMatchAlbum(name, artist string, tracks int, mbid string) metricSample {
	s := metricSample{ID: name, Local: metricLocal{Directory: name, Format: "flac", Album: name, Artist: artist}}
	r := metricRelease{ID: uuid.New(), Group: uuid.New(), GroupTitle: name, Title: name, Artist: mbid, Label: "外部厂牌", Date: "2019"}
	for i := 1; i <= tracks; i++ {
		title := fmt.Sprintf("合成曲目 %d", i)
		s.Local.Tracks = append(s.Local.Tracks, metricLocalTrack{File: fmt.Sprintf("%02d.flac", i), Title: title, Seconds: 100 + i, Tags: map[string]string{"TRACKNUMBER": fmt.Sprint(i)}})
		r.Tracks = append(r.Tracks, metricRecording{ID: uuid.New(), TrackID: uuid.New(), Title: title, Artist: mbid, Length: (100 + i) * 1000})
	}
	s.External.Releases = []metricRelease{r}
	return s
}

func cMatchFixture(app *integrationTestApplication, admin integrationTestUser) *curationFixture {
	return &curationFixture{
		operationsEnvironment: &operationsEnvironment{groupEnvironment: &groupEnvironment{application: app, admin: admin}},
		writer:                curation.NewWriter(app.database, app.dataDirectory, nil),
	}
}

type cMatchRequest struct {
	Service, Path string
	Query         url.Values
}

func cMatchRequests(t *testing.T, mock *upstreammock.Server) []cMatchRequest {
	t.Helper()
	out := httptest.NewRecorder()
	mock.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/_mock/requests", nil))
	var requests []cMatchRequest
	requireCurationOK(t, json.Unmarshal(out.Body.Bytes(), &requests))
	slices.Reverse(requests)
	return requests
}

func TestPostgreSQL整理发行组对应的封面归组和撤销(t *testing.T) {
	artist := metricArtist{ID: uuid.New(), Name: "合成发行组艺人"}
	a := cMatchAlbum("合成组第一版", artist.Name, 5, artist.ID)
	b := cMatchAlbum("合成组第二版", artist.Name, 5, artist.ID)
	a.External.Artists = []metricArtist{artist}
	group := a.External.Releases[0].Group
	a.External.Releases[0].Image = "cover.png"
	b.External.Releases[0].Group = group
	clock := newProgramClock()
	app, mock, _ := newCurationMockApplication(t, writeMetricCatalog(t, []metricSample{a, b}), clock)
	admin := app.createUser(t, "admin")
	b3Root(t, app)
	b3Enable(t, app, admin)
	var ids []string
	for _, s := range []metricSample{a, b} {
		startRequests := len(cMatchRequests(t, mock))
		writeMetricSample(t, app.allowedRoot, s)
		scanAndConfirmCuration(t, app, &admin)
		publishSearch(t, app)
		e := metricScannedEntry(t, app, s)
		ids = append(ids, e.release)
		clock.advance(t, time.Minute)
		state := b3WorkState(t, app, "release", e.release)
		var result struct{ Level, Rule string }
		requireCurationOK(t, json.Unmarshal(state.Result, &result))
		if state.Status != "matched" || result.Level != "release_group" || result.Rule != "unique_group" {
			t.Fatalf("%s 的匹配状态 %+v，结果 %+v", s.ID, state, result)
		}
		b3RequireCount(t, app, 1, `SELECT count(*) FROM external_links WHERE object_type='release_group_match' AND object_id=$1 AND external_id=$2`, e.release, group)
		b3RequireCount(t, app, 0, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1`, e.release)
		b3RequireCount(t, app, 0, `SELECT count(*) FROM external_links WHERE object_type='track' AND object_id=ANY($1::uuid[])`, e.tracks)
		b3RequireCount(t, app, 0, `SELECT count(*) FROM release_auto_values WHERE release_id=$1`, e.release)
		b3RequireCount(t, app, 0, `SELECT count(*) FROM curation_pending_items WHERE target_id=$1 AND kind='release_match'`, e.release)
		b3RequireCount(t, app, 1, `SELECT count(*) FROM release_external_artworks WHERE release_id=$1`, e.release)
		mb, detailReads, artistReads, groupCover := 0, 0, 0, 0
		for _, req := range cMatchRequests(t, mock)[startRequests:] {
			if req.Service == "musicbrainz" {
				if req.Path == "/musicbrainz/ws/2/artist/"+artist.ID {
					artistReads++
					continue
				}
				if req.Path == "/musicbrainz/ws/2/release/"+s.External.Releases[0].ID {
					detailReads++
					continue
				}
				mb++
				if req.Path != "/musicbrainz/ws/2/release" || req.Query.Get("limit") != "100" {
					t.Errorf("组级识别请求 %+v", req)
				}
			}
			if req.Service == "coverartarchive" && strings.Contains(req.Path, "/release-group/"+group) {
				groupCover++
			}
		}
		// 第二张复用封面清单缓存，第一张必须向独立封面服务取组清单。
		if mb != 1 || detailReads != 1 || artistReads > 1 || len(ids) == 1 && groupCover != 1 {
			t.Errorf("%s 的列表请求为 %d，版本详情 %d，艺人读取 %d，组封面清单 %d", s.ID, mb, detailReads, artistReads, groupCover)
		}
	}
	aID, bID := ids[0], ids[1]
	b3RequireCount(t, app, 1, `SELECT count(DISTINCT group_id) FROM releases WHERE id IN ($1,$2)`, aID, bID)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM operations WHERE target_id=$1 AND operation_type='release_group_change' AND origin->>'source'='curation'`, bID)
	f := cMatchFixture(app, admin)
	requireCurationOK(t, f.writer.DiscoverReleaseMerges(t.Context(), group, program.StoredRuleVersion))
	b3RequireCount(t, app, 0, `SELECT count(*) FROM curation_pending_items WHERE kind='release_merge'`)
	b3RequireCount(t, app, 0, `SELECT count(*) FROM release_merges`)

	// 按逆序撤销第二张的归组和本发行组对应。
	f.undo(t, programString(t, app, `SELECT id FROM operations WHERE target_id=$1 AND operation_type='release_group_change'`, bID))
	f.undo(t, programString(t, app, `SELECT operation_id FROM external_links WHERE object_type='release_group_match' AND object_id=$1`, bID))
	b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_suppressions WHERE target_type='release_group_match' AND target_id=$1 AND subject='external_link'`, bID)
	clock.advance(t, time.Minute)
	b3RequireCount(t, app, 0, `SELECT count(*) FROM external_links WHERE object_type='release_group_match' AND object_id=$1`, bID)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM external_links WHERE object_type='release_group_match' AND object_id=$1`, aID)
	b3RequireCount(t, app, 2, `SELECT count(DISTINCT group_id) FROM releases WHERE id IN ($1,$2)`, aID, bID)
}

func TestPostgreSQL整理散放单曲的写入粒度(t *testing.T) {
	artist := metricArtist{ID: uuid.New(), Name: "合成单曲艺人"}
	unique := cMatchAlbum("散放唯一录音", artist.Name, 1, artist.ID)
	common := cMatchAlbum("散放共同艺人", artist.Name, 1, artist.ID)
	for _, s := range []*metricSample{&unique, &common} {
		s.Local.Directory, s.Local.Album = ".", ""
		s.Local.Tracks[0].File, s.Local.Tracks[0].Title = s.ID+".flac", s.ID
		s.Local.Tracks[0].Tags = nil
		s.External.Releases[0].Tracks[0].Title = s.ID
	}
	unique.External.Artists = []metricArtist{artist}
	second := cMatchAlbum("共同艺人另一录音", artist.Name, 1, artist.ID).External.Releases[0]
	second.Tracks[0].Title = common.ID
	common.External.Releases = append(common.External.Releases, second)
	clock := newProgramClock()
	app, _, _ := newCurationMockApplication(t, writeMetricCatalog(t, []metricSample{unique, common}), clock)
	admin := app.createUser(t, "admin")
	b3Root(t, app)
	b3Enable(t, app, admin)
	// 先运行共同艺人的样本，以确认它独立建立艺人对应。
	for _, tc := range []struct {
		sample metricSample
		level  string
	}{{common, "artist"}, {unique, "recording"}} {
		s := tc.sample
		writeMetricSample(t, app.allowedRoot, s)
		scanAndConfirmCuration(t, app, &admin)
		publishSearch(t, app)
		track := programString(t, app, `SELECT id FROM tracks WHERE relative_path=$1 AND source_status='present'`, s.Local.Tracks[0].File)
		release := programString(t, app, `SELECT m.release_id FROM media m JOIN tracks t ON t.medium_id=m.id WHERE t.id=$1`, track)
		clock.advance(t, time.Minute)
		state := b3WorkState(t, app, "release", release)
		var result struct{ Level string }
		requireCurationOK(t, json.Unmarshal(state.Result, &result))
		if state.Status != "matched" || result.Level != tc.level {
			t.Fatalf("%s 状态 %+v，粒度 %q", s.ID, state, result.Level)
		}
		b3RequireCount(t, app, 0, `SELECT count(*) FROM external_links WHERE object_type IN ('release','release_group_match') AND object_id=$1`, release)
		b3RequireCount(t, app, 0, `SELECT count(*) FROM curation_pending_items WHERE target_id=$1 AND kind='recording_match'`, release)
		if tc.level == "recording" {
			b3RequireCount(t, app, 1, `SELECT count(*) FROM external_links WHERE object_type='track' AND object_id=$1 AND external_id=$2`, track, s.External.Releases[0].Tracks[0].ID)
		} else {
			b3RequireCount(t, app, 0, `SELECT count(*) FROM external_links WHERE object_type='track' AND object_id=$1`, track)
			b3RequireCount(t, app, 1, `SELECT count(*) FROM external_links WHERE object_type='artist' AND external_id=$1`, artist.ID)
		}
	}
}

func TestPostgreSQL整理两边专辑匹配后用别名合并艺人(t *testing.T) {
	a := metricArtist{ID: uuid.New(), Name: "周杰倫", Aliases: []string{"Jay Chou"}}
	primary := cMatchAlbum("合成主名专辑", "周杰伦", 3, a.ID)
	alias := cMatchAlbum("合成别名专辑", "Jay Chou", 3, a.ID)
	primary.External.Artists = []metricArtist{a}
	// 两边都以版本结构作证，让别名检索独立覆盖 arid 重查和同位署名。
	clock := newProgramClock()
	app, mock, _ := newCurationMockApplication(t, writeMetricCatalog(t, []metricSample{primary, alias}), clock)
	admin := app.createUser(t, "admin")
	b3Root(t, app)
	// 0026 把两个名字预路由给同一身份，此夹具需要两个独立的初始身份。
	aliasID := uuid.New()
	mustExec(t, app, `INSERT INTO artists(id,initial_name) VALUES($1,'Jay Chou')`, aliasID)
	mustExec(t, app, `UPDATE artist_name_keys SET initial_artist_id=$1 WHERE normalized_key='jay chou'`, aliasID)
	b3Enable(t, app, admin)
	var artists []string
	for _, s := range []metricSample{primary, alias} {
		before := len(cMatchRequests(t, mock))
		writeMetricSample(t, app.allowedRoot, s)
		scanAndConfirmCuration(t, app, &admin)
		publishSearch(t, app)
		e := metricScannedEntry(t, app, s)
		artists = append(artists, e.artists[s.Local.Artist])
		clock.advance(t, time.Minute)
		if countRows(t, app, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1 AND external_id=$2`, e.release, s.External.Releases[0].ID) != 1 {
			state := b3WorkState(t, app, "release", e.release)
			t.Fatalf("%s 未定到版本：状态 %s，结果 %s，出站 %v", s.ID, state.Status, state.Result, cMatchRequests(t, mock)[before:])
		}
		b3RequireCount(t, app, 0, `SELECT count(*) FROM curation_pending_items WHERE target_id=$1 AND kind='release_match'`, e.release)
		if s.ID == primary.ID {
			var releaseRequests int
			for _, req := range cMatchRequests(t, mock)[before:] {
				if strings.HasPrefix(req.Path, "/musicbrainz/ws/2/release") {
					releaseRequests++
				}
			}
			if releaseRequests != 2 {
				t.Errorf("普通版本应有一次搜索和一次详情，发行请求 %d", releaseRequests)
			}
		}
	}
	if artists[0] == "" || artists[1] == "" || artists[0] == artists[1] {
		t.Fatalf("扫描应先得到两个本地艺人：%v", artists)
	}
	b3RequireCount(t, app, 2, `SELECT count(*) FROM external_links WHERE object_type='artist' AND object_id IN ($1,$2) AND external_id=$3`, artists[0], artists[1], a.ID)
	merge := programString(t, app, `SELECT id FROM operations WHERE operation_type='artist_merge' AND origin->>'provider_kind'='source' AND origin->>'provider'='musicbrainz'`)
	b3RequireCount(t, app, 0, `SELECT count(*) FROM operations WHERE origin->>'provider_kind'='model'`)
	var aliasQuery, artistRead int
	for _, req := range cMatchRequests(t, mock) {
		if req.Path == "/musicbrainz/ws/2/artist" && req.Query.Get("query") == `"Jay Chou"` {
			aliasQuery++
		}
		if req.Path == "/musicbrainz/ws/2/artist/"+a.ID {
			artistRead++
		}
	}
	if aliasQuery != 1 || artistRead > 1 {
		t.Errorf("别名搜索 %d 次，艺人详情 %d 次", aliasQuery, artistRead)
	}

	cMatchFixture(app, admin).undo(t, merge)
	clock.advance(t, time.Minute)
	b3RequireCount(t, app, 0, `SELECT count(*) FROM artist_overrides WHERE artist_id IN ($1,$2) AND redirect_artist_id IS NOT NULL`, artists[0], artists[1])
	b3RequireCount(t, app, 1, `SELECT count(*) FROM operations WHERE operation_type='artist_merge'`)
}

func TestPostgreSQL整理零候选按来源和模型分流(t *testing.T) {
	known := metricArtist{ID: uuid.New(), Name: "外部库有此艺人"}
	relaxed := cMatchAlbum("可放宽的专辑", known.Name, 5, known.ID)
	relaxed.Local.Album += " (Deluxe Edition)"
	relaxed.External.Artists = []metricArtist{known}
	clock := newProgramClock()
	app, mock, upstream := newCurationMockApplication(t, writeMetricCatalog(t, []metricSample{relaxed}), clock)
	admin := app.createUser(t, "admin")
	b3Root(t, app)
	b3Enable(t, app, admin)
	add := func(t *testing.T, s metricSample) (string, []cMatchRequest) {
		t.Helper()
		before := len(cMatchRequests(t, mock))
		writeMetricSample(t, app.allowedRoot, s)
		scanAndConfirmCuration(t, app, &admin)
		publishSearch(t, app)
		id := metricScannedEntry(t, app, s).release
		clock.advance(t, time.Minute)
		return id, cMatchRequests(t, mock)[before:]
	}

	t.Run("先放宽再核对结构", func(t *testing.T) {
		id, requests := add(t, relaxed)
		var releaseQueries, artistQueries []string
		for _, req := range requests {
			if req.Path == "/musicbrainz/ws/2/release" {
				releaseQueries = append(releaseQueries, req.Query.Get("query"))
			}
			if req.Path == "/musicbrainz/ws/2/artist" {
				artistQueries = append(artistQueries, req.Query.Get("query"))
			}
		}
		state := b3WorkState(t, app, "release", id)
		if len(releaseQueries) < 2 || !strings.Contains(releaseQueries[0], "Deluxe Edition") || strings.Contains(releaseQueries[1], "Deluxe Edition") || state.Status != "matched" {
			t.Fatalf("按原题和放宽题查询后应由程序定案：查询 %v，状态 %+v", releaseQueries, state)
		}
		b3RequireCount(t, app, 1, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1 AND external_id=$2`, id, relaxed.External.Releases[0].ID)
		b3RequireCount(t, app, 0, `SELECT count(*) FROM curation_pending_items WHERE target_id=$1 AND kind='release_match'`, id)
		t.Run("已可定案时停止扩查", func(t *testing.T) {
			if len(releaseQueries) != 2 || len(artistQueries) != 0 {
				t.Errorf("放宽已得到结构吻合的唯一版本，应直接核对详情：发行搜索 %v，艺人存在性搜索 %v", releaseQueries, artistQueries)
			}
		})
	})
	t.Run("艺人在库但无模型", func(t *testing.T) {
		id, _ := add(t, cMatchAlbum("未登记专辑一", known.Name, 3, known.ID))
		state := b3WorkState(t, app, "release", id)
		if state.Status != "unlisted" || state.NextAttemptAt == nil {
			t.Fatalf("无模型应进未收录，状态 %+v", state)
		}
		b3RequireCount(t, app, 0, `SELECT count(*) FROM curation_pending_items WHERE target_id=$1`, id)
	})

	endpoint := newMetricsModel(t)
	endpoint.configure(t, app, &admin, nil, upstream)
	resp := app.request(t, http.MethodPut, curationBase+"/sources/web_search", map[string]bool{"enabled": false}, &admin, nil)
	if resp.Code != http.StatusOK {
		t.Fatal(resp.Code, resp.Body.String())
	}
	t.Run("艺人不在库且联网关闭", func(t *testing.T) {
		id, requests := add(t, cMatchAlbum("未登记专辑二", "外部库没有的艺人一", 3, uuid.New()))
		state := b3WorkState(t, app, "release", id)
		if state.Status != "unlisted" || !strings.Contains(string(state.Result), "web_unavailable") {
			t.Fatalf("无联网应进未收录，状态 %+v，结果 %s", state, state.Result)
		}
		var paths []string
		for _, req := range requests {
			if req.Service == "musicbrainz" {
				paths = append(paths, req.Path)
			}
		}
		if len(paths) != 3 || paths[0] != "/musicbrainz/ws/2/release" || paths[1] != paths[0] || paths[2] != "/musicbrainz/ws/2/artist" {
			t.Errorf("无结果时应先放宽并查艺人，请求 %v", paths)
		}
		b3RequireCount(t, app, 0, `SELECT count(*) FROM curation_pending_items WHERE target_id=$1`, id)
	})

	t.Run("艺人在库且巡检和程序高优先项完成后领取第二档", func(t *testing.T) {
		id, _ := add(t, cMatchAlbum("未登记专辑三", known.Name, 3, known.ID))
		f := cMatchFixture(app, admin)
		pending := programString(t, app, `SELECT id FROM curation_pending_items WHERE target_id=$1 AND status='waiting_route' AND route='lookup' AND candidates='[]'`, id)
		conversation := modelConversation(t, f)
		state := b3WorkState(t, app, "release", id)
		if state.Status != "uncertain" || state.IdentityDigest == "" {
			t.Fatalf("第二档工作状态 %+v", state)
		}

		ctx := t.Context()
		gateClock := newProgramClock()
		gateClock.now.Store(clock.now.Load())
		gateOptions := gateClock.options()
		sourceGate := curation.NewSources(app.database, curation.SourceRegistration{Declaration: musicbrainz.Declaration()})
		worker := program.NewWorker(app.database, f.writer, sourceGate, func(context.Context) (bool, error) { return false, nil }, nil, gateOptions)
		// 应用内的工作者已完成本轮。用同一隔离库启动可直接检查就绪状态的工作者，并让它停在轮询点。
		workerCtx, stopWorker := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			worker.Run(workerCtx)
		}()
		defer func() {
			stopWorker()
			<-done
		}()
		gateClock.wait(t)

		claimWhenReady := func(wantReady bool) []curation.PendingItem {
			t.Helper()
			now := gateOptions.Now()
			ready, err := worker.LookupTierReady(ctx, id, now)
			requireCurationOK(t, err)
			if ready != wantReady {
				t.Fatalf("第二档就绪状态为 %t，期望 %t", ready, wantReady)
			}
			if !ready {
				b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_pending_items
					WHERE id=$1 AND status='waiting_route' AND conversation_id IS NULL`, pending)
				return nil
			}
			items, err := f.writer.ClaimModelKind(ctx, conversation, now, 1, []string{"release"}, string(model.LookupCandidate), id)
			requireCurationOK(t, err)
			return items
		}
		claimWhenReady(false)
		mustExec(t, app, `UPDATE curation_patrol_names SET needs_patrol=false,model='合成巡检模型',prompt_version=$2,patrolled_at=now()
			WHERE id IN (SELECT name_id FROM curation_patrol_inputs WHERE release_id=$1)`, id, program.PatrolPromptVersion)
		mustExec(t, app, `UPDATE curation_work_states SET needs_work=true,manual_requested_at=$2
			WHERE subject='release' AND target_id=$1`, id, gateOptions.Now())
		claimWhenReady(false)
		mustExec(t, app, `UPDATE curation_work_states SET needs_work=false,manual_requested_at=NULL
			WHERE subject='release' AND target_id=$1`, id)
		items := claimWhenReady(true)
		if len(items) != 1 || items[0].ID != pending || items[0].Route != "lookup" ||
			items[0].Status != "in_progress" || items[0].ConversationID != conversation {
			t.Fatalf("第二档应领取指定的零候选项：%+v", items)
		}
		err := f.writer.FinishUnlisted(t.Context(), pending, "过期摘要", conversation, "仍未找到")
		curationCRequireProblem(t, err, http.StatusConflict, "item_stale")
		b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_pending_items WHERE id=$1 AND status='in_progress'`, pending)
		requireCurationOK(t, f.writer.FinishUnlisted(t.Context(), pending, state.IdentityDigest, conversation, "仍未找到"))
		b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_pending_items WHERE id=$1 AND status='resolved' AND resolution='unlisted'`, pending)
		b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_work_states WHERE target_id=$1 AND status='unlisted' AND next_attempt_at=updated_at+interval '90 days'`, id)
	})
	t.Run("艺人不在库时直接登记联网路线", func(t *testing.T) {
		response := app.request(t, http.MethodPut, curationBase+"/sources/web_search", map[string]bool{"enabled": true}, &admin, nil)
		if response.Code != http.StatusOK {
			t.Fatal(response.Code, response.Body.String())
		}
		id, _ := add(t, cMatchAlbum("未登记专辑四", "外部库没有的艺人二", 3, uuid.New()))
		b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_pending_items WHERE target_id=$1 AND status='waiting_route' AND route='web' AND candidates='[]'`, id)
	})
}

func TestPostgreSQL整理未收录九十天与管理员重查(t *testing.T) {
	s := cMatchAlbum("长期未收录", "未收录的合成艺人", 3, uuid.New())
	clock := newProgramClock()
	clock.now.Store(time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond).UnixNano())
	immediate := func() *sources.Limiter { return sources.NewLimiter(0, nil, nil) }
	app, mock, upstream := newCurationMockApplication(t, writeMetricCatalog(t, nil), clock,
		WithCurationSources(sources.Options{Limiter: immediate(), Now: clock.options().Now},
			coverartarchive.Options{Limiter: immediate(), ManifestLimiter: immediate()}, sources.Options{Limiter: immediate()}))
	admin := app.createUser(t, "admin")
	member := app.createUser(t, "user")
	root := b3Root(t, app)
	b3Enable(t, app, admin)
	writeMetricSample(t, app.allowedRoot, s)
	scanAndConfirmCuration(t, app, &admin)
	publishSearch(t, app)
	id := metricScannedEntry(t, app, s).release
	clock.advance(t, time.Minute)
	begin := clock.options().Now()
	first := b3WorkState(t, app, "release", id)
	if first.Status != "unlisted" || first.NextAttemptAt == nil || first.NextAttemptAt.Sub(begin) != 90*24*time.Hour {
		t.Fatalf("首次未收录应登记九十天到期，状态 %+v，当前时间 %v", first, begin)
	}

	endpoint := newMetricsModel(t)
	endpoint.configure(t, app, &admin, nil, upstream)
	before := len(cMatchRequests(t, mock))
	clock.advance(t, 90*24*time.Hour-time.Minute)
	if n := len(cMatchRequests(t, mock)); n != before {
		t.Fatalf("未到期时新增了 %d 个外部请求", n-before)
	}
	clock.advance(t, time.Minute)
	second := b3WorkState(t, app, "release", id)
	got := cMatchRequests(t, mock)
	searches := 0
	for _, req := range got[before:] {
		if req.Path == "/musicbrainz/ws/2/release" {
			searches++
		}
		if req.Service == "acoustid" || req.Service == "searxng" || req.Service == "openai" {
			t.Errorf("程序重查发出了非程序来源请求：%+v", req)
		}
	}
	if searches != 2 || second.Status != "unlisted" || second.NextAttemptAt != nil || second.Generation != first.Generation+1 ||
		!strings.Contains(string(second.Result), "program_recheck_inconclusive") {
		t.Fatalf("到期应仅用程序重查一次：搜索 %d，之前 %+v，之后 %+v，结果 %s", searches, first, second, second.Result)
	}
	clock.advance(t, 90*24*time.Hour)
	if n := len(cMatchRequests(t, mock)); n != len(got) {
		t.Fatalf("下一次九十天不应再自动重查，新增 %d 个请求", n-len(got))
	}

	check := func(release string, user *integrationTestUser, want int) {
		t.Helper()
		response := app.request(t, http.MethodPost, curationBase+"/releases/"+release+"/recheck", nil, user, nil)
		if response.Code != want {
			t.Fatalf("重查接口返回 %d，期望 %d：%s", response.Code, want, response.Body.String())
		}
	}
	check(id, &member, http.StatusForbidden)
	check(uuid.New(), &admin, http.StatusNotFound)
	neverProcessed, _ := insertGroupedRelease(t, app, root, groupedRelease{title: "尚未处理", artist: "合成艺人", tracks: []string{"present"}})
	check(neverProcessed, &admin, http.StatusConflict)
	mustExec(t, app, `UPDATE tracks SET source_status='missing' WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1)`, neverProcessed)
	check(id, &admin, http.StatusAccepted)
	check(id, &admin, http.StatusAccepted)
	clock.advance(t, time.Minute)
	after := b3WorkState(t, app, "release", id)
	if after.Generation != second.Generation+1 || after.Status != "unlisted" || after.NextAttemptAt != nil {
		t.Fatalf("两次手动登记应合并为一次程序重查，状态 %+v -> %+v", second, after)
	}
	b3RequireCount(t, app, 0, `SELECT count(*) FROM curation_pending_items WHERE target_id=$1`, id)
	b3RequireCount(t, app, 0, `SELECT count(*) FROM curation_artist_credit_proposals WHERE release_id=$1`, id)
}
