package app

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/acoustid"
	"github.com/qlqq/roomusic/backend/internal/sources/coverartarchive"
)

func TestPostgreSQLProgramMatchResultsAPI(t *testing.T) {
	f := newProgramFixture(t)
	app := f.application
	path := func(id string) string { return curationBase + "/releases/" + id + "/match-results" }
	read := func(id string) []curation.ReleaseMatchResult {
		t.Helper()
		var response struct{ Results []curation.ReleaseMatchResult }
		getJSON(t, app, &f.admin, path(id), &response)
		if response.Results == nil {
			t.Fatal("来源结论应为数组")
		}
		return response.Results
	}
	requireAPIError(t, app.request(t, http.MethodGet, path(f.a), nil, &f.ordinary, nil), 403, "forbidden")
	requireAPIError(t, app.request(t, http.MethodGet, path(f.hidden), nil, &f.admin, nil), 404, "not_found")
	if rows := read(f.a); len(rows) != 0 {
		t.Fatalf("尚未查询的发行应返回空数组，实际 %+v", rows)
	}
	at := time.Date(2026, 1, 3, 11, 0, 0, 0, time.FixedZone("UTC+8", 8*3600))
	save := func(row curation.ReleaseMatchResult) {
		t.Helper()
		requireCurationOK(t, curation.SaveReleaseMatchResult(t.Context(), app.database, f.a, row))
	}
	save(curation.ReleaseMatchResult{Source: "synthetic", Status: "conflict", Conflicts: []string{"track_count"}})
	save(curation.ReleaseMatchResult{Source: "musicbrainz", Status: "unmatched", CheckedAt: &at, Reason: "candidates_revoked"})
	save(curation.ReleaseMatchResult{Source: "musicbrainz", Status: "matched"})
	rows := read(f.a)
	if len(rows) != 2 {
		t.Fatalf("应独立返回两个来源，实际 %+v", rows)
	}
	if mb := rows[0]; mb.Source != "musicbrainz" || mb.Status != "matched" || mb.Reason != "" || mb.CheckedAt == nil ||
		mb.CheckedAt.Location() != time.UTC || !mb.CheckedAt.Equal(at) || len(mb.Conflicts) != 0 {
		t.Fatalf("MusicBrainz 结论与 UTC 时间错误：%+v", mb)
	}
	if other := rows[1]; other.Source != "synthetic" || other.Status != "conflict" || other.CheckedAt != nil ||
		!slices.Equal(other.Conflicts, []string{"track_count"}) {
		t.Fatalf("另一来源结论或历史未知时间被覆盖：%+v", other)
	}
}

func TestPostgreSQLProgramMatchFailedLookupKeepsLastSuccess(t *testing.T) {
	f := newProgramFixture(t)
	f.enable(t)
	var failure error = sources.ErrUnavailable
	f.source.hook = func(method string) error {
		if method == "SearchReleases" {
			return failure
		}
		return nil
	}
	f.run(t)
	if n := f.source.callCount("SearchReleases"); n == 0 {
		t.Fatal("未实际查询")
	}
	ctx := t.Context()
	read := func() []curation.ReleaseMatchResult {
		t.Helper()
		rows, err := f.writer.ReleaseMatchResults(ctx, f.a)
		requireCurationOK(t, err)
		return rows
	}
	if rows := read(); len(rows) != 0 {
		t.Fatalf("初次查询出错不应留下平台结论：%+v", rows)
	}
	failure = nil
	f.clock.advance(t, time.Hour)
	success := read()
	if len(success) != 1 || success[0].Status != "unmatched" || success[0].CheckedAt == nil {
		t.Fatalf("来源恢复后的空结果：%+v", success)
	}
	searches := f.source.callCount("SearchReleases")
	failure = sources.ErrUnavailable
	response := f.application.request(t, http.MethodPost, curationBase+"/releases/"+f.a+"/recheck", nil, &f.admin, nil)
	if response.Code != http.StatusAccepted {
		t.Fatalf("重查请求：%d %s", response.Code, response.Body)
	}
	f.clock.advance(t, time.Hour)
	if n := f.source.callCount("SearchReleases"); n != searches+1 {
		t.Fatalf("重查应实际出站一次，之前 %d 次，之后 %d 次", searches, n)
	}
	if got := read(); !reflect.DeepEqual(got, success) {
		t.Fatalf("失败重查改了前次成功的来源结论：之前 %+v，之后 %+v", success, got)
	}
}

func TestPostgreSQLProgramModelMatchSameKeyKeepsNewerConclusion(t *testing.T) {
	f := newProgramFixture(t)
	f.enable(t)
	ctx := t.Context()
	db := f.application.database
	_, remote := f.tagged(t)
	item := modelClaim(t, f.curationFixture, modelConversation(t, f.curationFixture),
		modelPending(t, f.curationFixture, f.a, "release_match", modelDigest(t, f)))
	matcher := program.NewModelMatcher(db, f.writer, f.gate)
	plan, err := matcher.Plan(ctx, item, remote.ID)
	requireCurationOK(t, err)
	checked := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	requireCurationOK(t, curation.SaveReleaseMatchResult(ctx, db, f.a, curation.ReleaseMatchResult{
		Source: "musicbrainz", Status: "conflict", CheckedAt: &checked, Conflicts: []string{"track_count"},
	}))
	opt := program.ApplyOptions{Key: uuid.New(), Batch: f.batch(t, "model"), PendingItemID: item.ID, ApprovedReviewID: uuid.New(),
		Source: curation.Source{Kind: "model", Provider: "synthetic", Basis: "合成匹配"}}
	first, err := matcher.Apply(ctx, plan, opt)
	requireCurationOK(t, err)
	if first.OperationID == "" {
		t.Fatal("模型匹配没有保存操作")
	}
	f.assertRelease(t, f.a, "matched", "model")
	rows, err := f.writer.ReleaseMatchResults(ctx, f.a)
	requireCurationOK(t, err)
	if len(rows) != 1 || rows[0].Status != "matched" || rows[0].CheckedAt == nil || !rows[0].CheckedAt.Equal(checked) {
		t.Fatalf("模型写入后的来源结论 %+v", rows)
	}
	newer := checked.Add(time.Hour)
	next := curation.ReleaseMatchResult{Source: "musicbrainz", Status: "conflict", CheckedAt: &newer, Conflicts: []string{"duration_mismatch"}}
	requireCurationOK(t, curation.SaveReleaseMatchResult(ctx, db, f.a, next))
	mustExec(t, f.application, `UPDATE curation_work_states SET generation=generation+1,status='conflict' WHERE subject='release' AND target_id=$1`, f.a)
	state := f.stateFor(t, "release", f.a)
	replay, err := matcher.Apply(ctx, plan, opt)
	requireCurationOK(t, err)
	rows, err = f.writer.ReleaseMatchResults(ctx, f.a)
	requireCurationOK(t, err)
	if !reflect.DeepEqual(replay, first) || !reflect.DeepEqual(rows, []curation.ReleaseMatchResult{next}) ||
		!reflect.DeepEqual(state, f.stateFor(t, "release", f.a)) {
		t.Fatalf("同键应回放原收据并保留新结论，首次 %+v、回放 %+v、来源结论 %+v", first, replay, rows)
	}
	otherKey := opt
	otherKey.Key = uuid.New()
	_, err = matcher.Apply(ctx, plan, otherKey)
	curationCRequireProblem(t, err, http.StatusConflict, "item_stale")
	f.assertCount(t, len(first.OperationIDs), `SELECT count(*) FROM operations WHERE batch_id=$1`, opt.Batch)
}

func TestPostgreSQLProgramMergeSameEdition(t *testing.T) {
	for _, scenario := range []string{"complementary", "duplicate", "missing-slot", "manual-group", "suppressed"} {
		t.Run(scenario, func(t *testing.T) {
			f := newCurationFixture(t)
			f.enable(t)
			app := f.application
			remote := programAlbum("合成同版", "合成艺人")
			second := remote.Media[0].Tracks[0]
			second.ID, second.RecordingID = uuid.New(), uuid.New()
			remote.Complete = true
			remote.Media = append(remote.Media, sources.Medium{Position: 2, Tracks: []sources.Track{second}})
			aTrack, _ := externalTrack(t, app, f.a)
			bTrack, _ := externalTrack(t, app, f.b)
			mustExec(t, app, `UPDATE tracks SET disc_number=2 WHERE id=$1`, bTrack)
			mustExec(t, app, `UPDATE media SET position=2 WHERE release_id=$1`, f.b)
			bRecording := second.RecordingID
			if scenario == "missing-slot" {
				bRecording = uuid.New()
			}
			if scenario == "duplicate" {
				bRecording = remote.Media[0].Tracks[0].RecordingID
			}
			for _, item := range []struct{ release, group, track, recording string }{
				{f.a, f.groupA, aTrack, remote.Media[0].Tracks[0].RecordingID}, {f.b, f.groupB, bTrack, bRecording},
			} {
				links := []curation.LinkIntent{curationCLink("release", item.release, remote.ID), curationCLink("track", item.track, item.recording)}
				if item.release == f.a {
					links = append(links, curationCLink("release_group", item.group, remote.Group.ID))
				}
				req := curationCLinkRequest(item.release, "", links...)
				req.Links[0].ExternalGroupID = remote.Group.ID
				req.External.DiscCount, req.External.TrackCount = 2, 2
				curationCApplyLinks(t, f, req)
			}
			switch scenario {
			case "manual-group":
				_, other := insertGroupedRelease(t, app, f.rootID, groupedRelease{title: "人工组目标", artist: "合成艺人", tracks: []string{"present"}})
				_, revision, _ := f.state(t, f.b)
				f.join(t, f.b, int(revision), other)
			case "suppressed":
				low, high := min(f.a, f.b), max(f.a, f.b)
				hash := fmt.Sprintf("%x", sha256.Sum256([]byte(high)))
				mustExec(t, app, `INSERT INTO curation_suppressions(target_type,target_id,subject,value_hash,value,created_by)
 VALUES ('release',$1,'release_merge',$2,jsonb_build_object('release_id',$3::text),$4)`, low, hash, high, f.admin.id)
			}
			action, err := f.writer.ProgramMergePreview(t.Context(), remote)
			requireCurationOK(t, err)
			switch scenario {
			case "complementary":
				if action == nil || action.Merge == nil || action.Merge.Target.ID != f.a || action.Merge.Source.ID != f.b {
					t.Fatalf("两份互补整碟应直接合并：%+v", action)
				}
				batch, key := f.batch(t, "program"), uuid.New()
				source := curation.Source{Kind: "source", Provider: "musicbrainz", WriterLevel: catalog.LevelProgramExternal, Basis: "合成整碟"}
				start := make(chan struct{})
				results := make(chan error, 2)
				for range 2 {
					go func() {
						<-start
						results <- f.writer.MergeProgramEdition(t.Context(), remote, *action.Merge, source, batch, key)
					}()
				}
				close(start)
				for range 2 {
					requireCurationOK(t, <-results)
				}
				requireCurationOK(t, f.writer.MergeProgramEdition(t.Context(), remote, *action.Merge, source, batch, key))
				b3RequireCount(t, app, 1, `SELECT count(*) FROM release_merges`)
				b3RequireCount(t, app, 2, `SELECT count(*) FROM media WHERE release_id=$1`, f.a)
				op := programString(t, app, `SELECT id FROM operations WHERE idempotency_key=$1`, key)
				f.undo(t, op)
				b3RequireCount(t, app, 1, `SELECT count(*) FROM media WHERE release_id=$1`, f.b)
			case "duplicate":
				if action == nil || action.Merge != nil || action.JoinRelease != f.b || action.JoinGroup != f.groupA {
					t.Fatalf("重复录音应保留两版并归入同组：%+v", action)
				}
			default:
				if action != nil {
					t.Fatalf("%s 不应生成程序合并或归组动作：%+v", scenario, action)
				}
			}
		})
	}
}

func TestPostgreSQLProgramGroupFirstReleaseYear(t *testing.T) {
	for _, tc := range []struct {
		name, first, tag string
		want             int
	}{
		{"earlier-group", "1987-05-06", "", 1987},
		{"missing-group", "", "", 0},
		{"existing-year", "1987-05-06", "2004", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			artist := metricArtist{ID: uuid.New(), Name: "合成分组艺人"}
			sample := cMatchAlbum("合成专辑四版", artist.Name, 5, artist.ID)
			sample.External.Artists = []metricArtist{artist}
			if tc.tag != "" {
				sample.Local.Tags = map[string]string{"DATE": tc.tag}
			}
			group := sample.External.Releases[0].Group
			for range 3 {
				edition := cMatchAlbum(sample.Local.Album, artist.Name, 5, artist.ID).External.Releases[0]
				edition.Group = group
				sample.External.Releases = append(sample.External.Releases, edition)
			}
			dir := writeMetricCatalog(t, []metricSample{sample})
			path := filepath.Join(dir, "catalog.json")
			raw, err := os.ReadFile(path)
			requireCurationOK(t, err)
			var data map[string]json.RawMessage
			requireCurationOK(t, json.Unmarshal(raw, &data))
			var groups []map[string]any
			requireCurationOK(t, json.Unmarshal(data["Groups"], &groups))
			if len(groups) != 1 {
				t.Fatalf("合成发行组数量 %d", len(groups))
			}
			groups[0]["first-release-date"] = tc.first
			data["Groups"], err = json.Marshal(groups)
			requireCurationOK(t, err)
			raw, err = json.Marshal(data)
			requireCurationOK(t, err)
			requireCurationOK(t, os.WriteFile(path, raw, 0600))
			clock := newProgramClock()
			app, mock, _ := newCurationMockApplication(t, dir, clock)
			admin := app.createUser(t, "admin")
			b3Root(t, app)
			writeMetricSample(t, app.allowedRoot, sample)
			scanAndConfirmCuration(t, app, &admin)
			publishSearch(t, app)
			release := metricScannedEntry(t, app, sample).release
			b3Enable(t, app, admin)
			clock.advance(t, time.Minute)
			b3RequireCount(t, app, 1, `SELECT count(*) FROM external_links WHERE object_type='release_group_match' AND object_id=$1 AND external_id=$2`, release, group)
			b3RequireCount(t, app, 0, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1`, release)
			if tc.want == 0 {
				b3RequireCount(t, app, 0, `SELECT count(*) FROM release_auto_values WHERE release_id=$1`, release)
			} else {
				b3RequireCount(t, app, 1, `SELECT count(*) FROM release_auto_values WHERE release_id=$1 AND field='year' AND year_value=$2`, release, tc.want)
				b3RequireCount(t, app, 0, `SELECT count(*) FROM release_auto_values WHERE release_id=$1 AND field<>'year'`, release)
			}
			for _, req := range cMatchRequests(t, mock) {
				if strings.HasPrefix(req.Path, "/musicbrainz/ws/2/release/") {
					t.Errorf("组级结论读了版本详情：%s", req.Path)
				}
			}
		})
	}
}

func TestPostgreSQLProgramPeriodicRecheckCallsAcoustID(t *testing.T) {
	clock := newProgramClock()
	clock.now.Store(time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond).UnixNano())
	immediate := func() *sources.Limiter { return sources.NewLimiter(0, nil, nil) }
	app, mock, upstream := newCurationMockApplication(t, writeMetricCatalog(t, nil), clock,
		WithFingerprinter(b3RealFingerprinter(t)), WithAcoustID(acoustid.Options{Now: clock.options().Now, Limiter: immediate()}),
		WithCurationSources(sources.Options{Limiter: immediate(), Now: clock.options().Now},
			coverartarchive.Options{Limiter: immediate(), ManifestLimiter: immediate()}, sources.Options{Limiter: immediate()}))
	hits := func() int {
		n := 0
		for _, req := range cMatchRequests(t, mock) {
			if req.Service == "acoustid" && req.Path == "/acoustid/v2/lookup" {
				n++
			}
		}
		return n
	}
	admin := app.createUser(t, "admin")
	b3Root(t, app)
	s := cMatchAlbum("声纹周期重查合成专辑", "无对应合成艺人", 2, uuid.New())
	s.Local.Format = "fingerprint"
	for i := range s.Local.Tracks {
		s.Local.Tracks[i].Seconds = 12 + 2*i
	}
	writeMetricSample(t, app.allowedRoot, s)
	scanAndConfirmCuration(t, app, &admin)
	publishSearch(t, app)
	id := metricScannedEntry(t, app, s).release
	b3SetHTTPKey(t, app, &admin, "application", b3AppKey)
	b3SetHTTPKey(t, app, &admin, "user", b3UserKey)
	b3Enable(t, app, admin)
	clock.advance(t, time.Minute)
	first := b3WorkState(t, app, "release", id)
	if first.Status != "unmatched" || first.NextAttemptAt == nil || hits() != 2 {
		t.Fatalf("初轮应查两首声纹并记为未匹配，实际请求 %d 次，状态 %q，到期 %v", hits(), first.Status, first.NextAttemptAt)
	}
	newMetricsModel(t).configure(t, app, &admin, nil, upstream)
	clock.advance(t, 89*24*time.Hour)
	if hits() != 2 {
		t.Fatalf("第 89 天不应出站，请求总数 %d", hits())
	}
	for day := range 2 {
		if day == 0 {
			clock.advance(t, 24*time.Hour)
		} else {
			clock.advance(t, 90*24*time.Hour)
		}
		state := b3WorkState(t, app, "release", id)
		if state.Status != "unmatched" || hits() != 4+2*day || state.Generation != first.Generation+day+1 {
			t.Fatalf("周期轮 %d 应再次查声纹，实际请求 %d 次，状态 %q，代次 %d", day+1, hits(), state.Status, state.Generation)
		}
		b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_pending_items
 WHERE target_id=$1 AND kind='release_match' AND status='waiting_route' AND route='lookup'`, id)
	}
	response := app.request(t, http.MethodPost, curationBase+"/releases/"+id+"/recheck", nil, &admin, nil)
	if response.Code != http.StatusAccepted {
		t.Fatalf("手动重查返回 %d %s", response.Code, response.Body)
	}
	clock.advance(t, time.Minute)
	b3RequireCount(t, app, 0, `SELECT count(*) FROM curation_pending_items WHERE target_id=$1 AND kind='release_match' AND status<>'resolved'`, id)
}

func TestPostgreSQLProgramAdmissionPreservesLyrics(t *testing.T) {
	f := newProgramFixture(t)
	f.enable(t)
	app := f.application
	track, _ := externalTrack(t, app, f.a)
	mustExec(t, app, `UPDATE releases SET title='未知专辑' WHERE id=$1`, f.a)
	mustExec(t, app, `UPDATE tracks SET duration_seconds=100,title='合成有名曲目',artist='合成曲目艺人' WHERE id=$1`, track)
	f.run(t)
	if searches := f.source.callCount("SearchReleases"); searches != 0 {
		t.Errorf("缺半边名称的专辑识别出站 %d 次", searches)
	}
	if lyrics := f.source.callCount("LyricsFor"); lyrics != 1 {
		t.Errorf("合成曲目歌词请求 %d 次，期望 1 次", lyrics)
	}
	b3RequireCount(t, app, 0, `SELECT count(*) FROM curation_release_match_results WHERE release_id=$1`, f.a)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM track_external_lyrics WHERE track_id=$1`, track)
}

func TestPostgreSQLProgramOccupiedCandidateUsesRevokedReason(t *testing.T) {
	f := newProgramFixture(t)
	f.enable(t)
	ctx := t.Context()
	track, remote := f.tagged(t)
	existing := uuid.New()
	curationCApplyLinks(t, f.curationFixture, curationCLinkRequest(f.a, "", curationCLink("track", track, existing)))
	f.run(t)
	state := f.assertRelease(t, f.a, "unmatched", "tag_id")
	if state.NextAttemptAt != nil {
		t.Errorf("占用的候选仍安排周期重查：%s", state.NextAttemptAt)
	}
	rows, err := f.writer.ReleaseMatchResults(ctx, f.a)
	requireCurationOK(t, err)
	if len(rows) != 1 || rows[0].Source != "musicbrainz" || rows[0].Status != "unmatched" || rows[0].Reason != "candidates_revoked" {
		t.Fatalf("录音位置已有对应 %s，候选为 %s，期望按候选已撤销登记，实际 %+v", existing, remote.Media[0].Tracks[0].RecordingID, rows)
	}
	f.clock.advance(t, 180*24*time.Hour)
	if n := f.source.callCount("ReleaseByID"); n != 1 {
		t.Errorf("占用的候选重复查询，累计 %d 次", n)
	}
}
