package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	curationtools "github.com/qlqq/roomusic/backend/internal/curation/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/coverartarchive"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
	"github.com/qlqq/roomusic/backend/internal/upstreammock"
)

const stewardMatchTool = "match_release_external"

type stewardMatchFixture struct {
	*curationFixture
	clock   *programClock
	matcher *program.ModelMatcher
}

func newStewardMatchFixture(t *testing.T, samples []metricSample, wrap func(http.Handler) http.Handler) *stewardMatchFixture {
	t.Helper()
	mock, err := upstreammock.New(upstreammock.Options{DataDir: writeMetricCatalog(t, samples)})
	requireCurationOK(t, err)
	var handler http.Handler = mock
	if wrap != nil {
		handler = wrap(handler)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	clock := newProgramClock()
	app := newConfiguredIntegrationTestApplication(t, slog.New(slog.NewTextHandler(io.Discard, nil)),
		config.Server{UpstreamMockURL: server.URL}, WithCurationProgram(true, clock.options()),
		WithCurationSources(sources.Options{Limiter: sources.NewLimiter(0, nil, nil)},
			coverartarchive.Options{Limiter: sources.NewLimiter(0, nil, nil), ManifestLimiter: sources.NewLimiter(0, nil, nil)},
			sources.Options{Limiter: sources.NewLimiter(0, nil, nil)}))
	clock.wait(t)
	admin := app.createUser(t, "admin")
	f := cMatchFixture(app, admin)
	client := curationMusicBrainz{musicbrainz.New(sources.Options{
		BaseURL: server.URL + "/musicbrainz/ws/2", NewClient: b3HTTPClient(server), Limiter: sources.NewLimiter(0, nil, nil),
	})}
	gate := curation.NewSources(app.database, curation.SourceRegistration{
		Declaration: musicbrainz.Declaration(), Releases: client, Recordings: client, Artists: client,
	})
	b3Root(t, app)
	publishSearch(t, app)
	for _, sample := range samples {
		writeMetricSample(t, app.allowedRoot, sample)
	}
	scanAndConfirmCuration(t, app, &admin)
	return &stewardMatchFixture{f, clock, program.NewModelMatcher(app.database, f.writer, gate)}
}

func stewardMatchAlbum(name string, tracks int) metricSample {
	artist := metricArtist{ID: uuid.New(), Name: name + "艺人"}
	sample := cMatchAlbum(name, artist.Name, tracks, artist.ID)
	sample.External.Artists = []metricArtist{artist}
	return sample
}

func stewardMatchLoose() metricSample {
	sample := stewardMatchAlbum("合成散曲", 1)
	sample.Local.Album, sample.Local.Directory = "", "."
	sample.Local.Tracks[0].File = "合成散曲.flac"
	sample.Local.Tracks[0].Tags = nil
	return sample
}

func (f *stewardMatchFixture) input(t *testing.T, sample metricSample) program.ExternalMatchInput {
	t.Helper()
	return program.ExternalMatchInput{ReleaseID: stewardMatchScan(t, f.application, sample).ids[0], Entity: "release", MBID: sample.External.Releases[0].ID}
}

func stewardMatchScan(t *testing.T, app *integrationTestApplication, sample metricSample) fixedPartition {
	t.Helper()
	part := fixedScanPartition(t, app, fixedSample{metricSample: sample})
	if len(part.ids) != 1 || slices.Contains(part.tracks, "") {
		t.Fatalf("扫描应将 %s 的全部曲目放入同一发行：%+v", sample.ID, part)
	}
	return part
}

func (f *stewardMatchFixture) selectExternal(ctx context.Context, in program.ExternalMatchInput, key string) (program.ExternalMatchResult, error) {
	return f.matcher.SelectExternal(ctx, in, f.admin.id, key, operations.Origin{Source: "chat", Mode: "operator"}, uuid.New())
}

// 比对完整行，确保拒绝和预览没有改写已有结论或待定项。
func (f *stewardMatchFixture) snapshot(t *testing.T) string {
	t.Helper()
	return programString(t, f.application, `SELECT jsonb_build_array(
 (SELECT jsonb_agg(to_jsonb(o) ORDER BY o.id) FROM operations o),
 (SELECT jsonb_agg(to_jsonb(r) ORDER BY r.id) FROM releases r),
 (SELECT jsonb_agg(to_jsonb(g) ORDER BY g.id) FROM release_groups g),
 (SELECT jsonb_agg(to_jsonb(l) ORDER BY l.id) FROM external_links l),
 (SELECT jsonb_agg(to_jsonb(v) ORDER BY v.release_id,v.field) FROM release_auto_values v),
 (SELECT jsonb_agg(to_jsonb(v) ORDER BY v.track_id,v.field) FROM track_auto_values v),
 (SELECT jsonb_agg(to_jsonb(r) ORDER BY r.release_id,r.source) FROM curation_release_match_results r),
 (SELECT jsonb_agg(to_jsonb(a) ORDER BY a.release_id) FROM release_external_artworks a),
 (SELECT jsonb_agg(to_jsonb(s) ORDER BY s.subject,s.target_id) FROM curation_work_states s),
 (SELECT jsonb_agg(to_jsonb(p) ORDER BY p.id) FROM curation_pending_items p),
 (SELECT jsonb_agg(to_jsonb(s) ORDER BY s.id) FROM curation_suppressions s)
 )::text`)
}

func TestPostgreSQLStewardMatchExternalAlbum(t *testing.T) {
	sample := stewardMatchAlbum("合成完整专辑", 2)
	sample.External.Releases[0].Image = "cover.png"
	remote := sample.External.Releases[0]
	coverStarted, coverResponse := make(chan struct{}, 1), make(chan struct{}, 1)
	var coverIntercepted atomic.Bool
	defer close(coverResponse)
	f := newStewardMatchFixture(t, []metricSample{sample}, func(mock http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.TrimSuffix(r.URL.Path, "/") == "/coverartarchive/release/"+remote.ID && coverIntercepted.CompareAndSwap(false, true) {
				coverStarted <- struct{}{}
				select {
				case <-coverResponse:
				case <-r.Context().Done():
					return
				}
			}
			mock.ServeHTTP(w, r)
		})
	})
	ctx, app := t.Context(), f.application
	in := f.input(t, sample)
	entry := stewardMatchScan(t, app, sample)
	evidence, err := f.writer.LoadCurationEvidence(ctx, []string{in.ReleaseID})
	requireCurationOK(t, err)
	e := evidence[in.ReleaseID]
	previous := curation.WorkState{
		Subject: "release", TargetID: in.ReleaseID, Status: "unmatched", Generation: 7,
		RuleVersion: program.MatchRuleVersion - 1, MatchRuleVersion: program.MatchRuleVersion - 1,
		InputDigest: program.ReleaseDigest(e), IdentityDigest: program.IdentityDigest(e),
		GapDigest: program.GapDigest(e), FileDigest: e.FileDigest, LocalRuleVersion: program.LocalRuleVersion,
	}
	requireCurationOK(t, curation.SaveWorkState(ctx, app.database, previous))
	requireCurationOK(t, curation.SaveReleaseMatchResult(ctx, app.database, in.ReleaseID,
		curation.ReleaseMatchResult{Source: "musicbrainz", Status: "unmatched", Reason: "无候选"}))
	for _, kind := range []string{"release_match", "recording_match"} {
		requireCurationOK(t, curation.UpsertPendingItem(ctx, app.database, curation.PendingItem{
			TargetID: in.ReleaseID, Kind: kind, RuleVersion: previous.RuleVersion, InputDigest: previous.InputDigest, Evidence: json.RawMessage(`{}`),
		}))
	}
	f.enable(t)
	key := uuid.New()
	out, err := f.selectExternal(ctx, in, key)
	requireCurationOK(t, err)
	if out.Level != "release" || out.MatchedMBID != remote.ID || !uuid.Valid(out.OperationID) ||
		len(out.OperationIDs) < 2 || out.OperationIDs[0] != out.OperationID {
		t.Fatalf("版本对应收据错误：%+v", out)
	}
	b3RequireCount(t, app, 1, `SELECT count(*) FROM external_links WHERE source='musicbrainz' AND object_type='release'
 AND object_id=$1 AND external_id=$2 AND operation_id=$3`, in.ReleaseID, remote.ID, out.OperationID)
	for i, track := range entry.tracks {
		b3RequireCount(t, app, 1, `SELECT count(*) FROM external_links WHERE source='musicbrainz' AND object_type='track'
 AND object_id=$1 AND external_id=$2 AND operation_id=$3`, track, remote.Tracks[i].ID, out.OperationID)
	}
	b3RequireCount(t, app, 1, `SELECT count(*) FROM release_auto_values WHERE release_id=$1 AND field='label'
 AND text_value=$2 AND writer_level=$3 AND operation_id=ANY($4::uuid[])`, in.ReleaseID, remote.Label, catalog.LevelProgramExternal, out.OperationIDs)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM release_auto_values WHERE release_id=$1 AND field='year'
 AND year_value=2019 AND operation_id=ANY($2::uuid[])`, in.ReleaseID, out.OperationIDs)
	b3RequireCount(t, app, 0, `SELECT count(*) FROM release_auto_values WHERE release_id=$1 AND field='title'`, in.ReleaseID)
	b3RequireCount(t, app, len(out.OperationIDs), `SELECT count(*) FROM operations WHERE id=ANY($1::uuid[])
 AND batch_id IS NULL AND actor_id=$2 AND origin->>'source'='chat' AND origin->>'provider_kind'='source'
 AND origin->>'provider'='steward'`, out.OperationIDs, f.admin.id)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_release_match_results WHERE release_id=$1 AND source='musicbrainz'
 AND status='matched' AND checked_at IS NOT NULL AND selection IS NULL`, in.ReleaseID)
	state := b3WorkState(t, app, "release", in.ReleaseID)
	if state.Status != "matched" || state.Generation != previous.Generation+1 || state.MatchRuleVersion != program.MatchRuleVersion {
		t.Fatalf("对应后工作状态错误：%+v", state)
	}
	b3RequireCount(t, app, 2, `SELECT count(*) FROM curation_pending_items WHERE target_id=$1
 AND kind IN ('release_match','recording_match') AND status='resolved' AND resolution='user_selected' AND resolved_at IS NOT NULL`, in.ReleaseID)
	before := f.snapshot(t)
	replay, err := f.selectExternal(ctx, in, key)
	requireCurationOK(t, err)
	if replay.OperationID != out.OperationID || !slices.Equal(replay.OperationIDs, out.OperationIDs) {
		t.Fatalf("同键回放收据不同：首次 %+v，回放 %+v", out, replay)
	}
	other := in
	other.MBID = uuid.New()
	_, err = f.selectExternal(ctx, other, key)
	curationCRequireProblem(t, err, http.StatusConflict, "idempotency_conflict")
	_, err = f.selectExternal(ctx, in, uuid.New())
	curationCRequireProblem(t, err, http.StatusConflict, "already_matched")
	if got := f.snapshot(t); got != before {
		t.Fatalf("回放或拒绝改动了已匹配的数据：%s", got)
	}

	artist := sample.External.Artists[0].ID
	b3RequireCount(t, app, 0, `SELECT count(*) FROM external_links WHERE object_type='artist' AND external_id=$1`, artist)
	b3RequireCount(t, app, 0, `SELECT count(*) FROM release_external_artworks WHERE release_id=$1`, in.ReleaseID)
	f.clock.tick <- time.Unix(0, f.clock.now.Add(int64(time.Minute)))
	wait, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	awaitExternalResult(t, wait, coverStarted)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_work_states WHERE subject='artwork' AND target_id=$1 AND queue_release_id=$1 AND needs_work`, in.ReleaseID)
	coverResponse <- struct{}{}
	f.clock.wait(t)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM external_links WHERE source='musicbrainz' AND object_type='artist' AND external_id=$1`, artist)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM release_external_artworks WHERE release_id=$1 AND source='coverartarchive'`, in.ReleaseID)
}

func TestPostgreSQLStewardMatchExternalRejections(t *testing.T) {
	album, loose := stewardMatchAlbum("合成核对专辑", 2), stewardMatchLoose()
	// 三曲候选不满足两曲本地专辑的组级覆盖下限。
	countMismatch := cMatchAlbum(album.Local.Album, album.Local.Artist, 3, album.External.Artists[0].ID).External.Releases[0]
	durationMismatch := cMatchAlbum(album.Local.Album, album.Local.Artist, 2, album.External.Artists[0].ID).External.Releases[0]
	durationMismatch.Tracks[0].Length += 40000
	album.External.Releases = append(album.External.Releases, countMismatch, durationMismatch)
	f := newStewardMatchFixture(t, []metricSample{album, loose}, nil)
	in, single := f.input(t, album), f.input(t, loose)
	track, _ := externalTrack(t, f.application, in.ReleaseID)
	for _, tc := range []struct {
		name, code, reason string
		input              program.ExternalMatchInput
		status             int
	}{
		{"曲数不同", "insufficient_corroboration", "track_count", program.ExternalMatchInput{ReleaseID: in.ReleaseID, Entity: "release", MBID: countMismatch.ID}, 409},
		{"时长超差", "insufficient_corroboration", "duration_mismatch", program.ExternalMatchInput{ReleaseID: in.ReleaseID, Entity: "release", MBID: durationMismatch.ID}, 409},
		{"发行不存在", "not_found", "", program.ExternalMatchInput{ReleaseID: uuid.New(), Entity: "release", MBID: in.MBID}, 404},
		{"有声书不适用", "not_eligible", "", in, 409},
		{"散曲使用版本", "invalid_input", "", single, 400},
		{"成张使用录音", "invalid_input", "", program.ExternalMatchInput{ReleaseID: in.ReleaseID, Entity: "recording", MBID: album.External.Releases[0].Tracks[0].ID}, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.code == "not_eligible" {
				mustExec(t, f.application, `UPDATE tracks SET scan_tags=COALESCE(scan_tags,'{}') || '{"audiobook":true}' WHERE id=$1`, track)
				t.Cleanup(func() {
					mustExec(t, f.application, `UPDATE tracks SET scan_tags=scan_tags - 'audiobook' WHERE id=$1`, track)
				})
			}
			before := f.snapshot(t)
			_, err := f.selectExternal(t.Context(), tc.input, uuid.New())
			curationCRequireProblem(t, err, tc.status, tc.code)
			if _, _, message := operations.Problem(err); !strings.Contains(message, tc.reason) {
				t.Errorf("错误消息 %q 应含 %q", message, tc.reason)
			}
			if got := f.snapshot(t); got != before {
				t.Fatalf("拒绝后写入了数据：%s", got)
			}
		})
	}
}

func TestPostgreSQLStewardMatchExternalGroupAndRecording(t *testing.T) {
	partial, loose := stewardMatchAlbum("合成缺曲专辑", 6), stewardMatchLoose()
	partial.Local.Tracks = partial.Local.Tracks[:4]
	f := newStewardMatchFixture(t, []metricSample{partial, loose}, nil)
	app := f.application
	for _, tc := range []struct {
		sample              metricSample
		entity, level, link string
		mbid, matched       string
	}{
		{partial, "release", "release_group", "release_group_match", partial.External.Releases[0].ID, partial.External.Releases[0].Group},
		{loose, "recording", "recording", "track", loose.External.Releases[0].Tracks[0].ID, loose.External.Releases[0].Tracks[0].ID},
	} {
		t.Run(tc.level, func(t *testing.T) {
			entry := stewardMatchScan(t, app, tc.sample)
			in := program.ExternalMatchInput{ReleaseID: entry.ids[0], Entity: tc.entity, MBID: tc.mbid}
			out, err := f.selectExternal(t.Context(), in, uuid.New())
			requireCurationOK(t, err)
			if out.Level != tc.level || out.MatchedMBID != tc.matched {
				t.Fatalf("对应层级或编号错误：%+v", out)
			}
			target := in.ReleaseID
			if tc.entity == "recording" {
				b3RequireCount(t, app, 1, `SELECT count(*) FROM releases WHERE id=$1 AND candidate_kind='loose_unknown'`, in.ReleaseID)
				target = entry.tracks[0]
			}
			b3RequireCount(t, app, 1, `SELECT count(*) FROM external_links WHERE object_type=$1 AND object_id=$2
 AND source='musicbrainz' AND external_id=$3 AND operation_id=$4`, tc.link, target, tc.matched, out.OperationID)
			b3RequireCount(t, app, 0, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1`, in.ReleaseID)
			if tc.level == "release_group" {
				b3RequireCount(t, app, 0, `SELECT count(*) FROM external_links WHERE object_type='track' AND object_id=ANY($1::uuid[])`, entry.tracks)
			}
			b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_release_match_results WHERE release_id=$1
 AND source='musicbrainz' AND status='matched'`, in.ReleaseID)
		})
	}
}

func TestPostgreSQLStewardMatchExternalRevertAndSourceGate(t *testing.T) {
	sample := stewardMatchAlbum("合成撤销专辑", 2)
	f := newStewardMatchFixture(t, []metricSample{sample}, nil)
	ctx, app := t.Context(), f.application
	in := f.input(t, sample)
	beforeLabel := programString(t, app, `SELECT COALESCE(label,'') FROM release_effective WHERE id=$1`, in.ReleaseID)
	settings, err := f.writer.Settings(ctx)
	requireCurationOK(t, err)
	if settings.Enabled {
		t.Fatal("测试需要在后台整理总开关关闭时写入")
	}
	fixedSource(t, app, &f.admin, "musicbrainz", false)
	before := f.snapshot(t)
	_, err = f.selectExternal(ctx, in, uuid.New())
	curationCRequireProblem(t, err, http.StatusConflict, "source_disabled")
	if got := f.snapshot(t); got != before {
		t.Fatalf("源关闭后写入了数据：%s", got)
	}
	fixedSource(t, app, &f.admin, "musicbrainz", true)
	out, err := f.selectExternal(ctx, in, uuid.New())
	requireCurationOK(t, err)
	if got := programString(t, app, `SELECT label FROM release_effective WHERE id=$1`, in.ReleaseID); got != sample.External.Releases[0].Label {
		t.Fatalf("撤销前应显示外部厂牌，实际 %q", got)
	}
	for _, id := range slices.Backward(out.OperationIDs) {
		f.undo(t, id)
	}
	b3RequireCount(t, app, 0, `SELECT count(*) FROM external_links WHERE operation_id=ANY($1::uuid[])`, out.OperationIDs)
	b3RequireCount(t, app, 0, `SELECT count(*) FROM release_auto_values WHERE release_id=$1`, in.ReleaseID)
	b3RequireCount(t, app, 0, `SELECT count(*) FROM track_auto_values WHERE operation_id=ANY($1::uuid[])`, out.OperationIDs)
	if got := programString(t, app, `SELECT COALESCE(label,'') FROM release_effective WHERE id=$1`, in.ReleaseID); got != beforeLabel {
		t.Fatalf("撤销后厂牌 %q，期望 %q", got, beforeLabel)
	}
	suppressed, err := curation.LinkSuppressed(ctx, app.database, "release", in.ReleaseID, "musicbrainz", in.MBID)
	requireCurationOK(t, err)
	if !suppressed {
		t.Fatal("撤销的版本编号应进入不再提出")
	}
	before = f.snapshot(t)
	_, err = f.selectExternal(ctx, in, uuid.New())
	curationCRequireProblem(t, err, http.StatusConflict, "suppressed")
	if got := f.snapshot(t); got != before {
		t.Fatalf("被抑制的编号改动了数据：%s", got)
	}
}

func TestPostgreSQLStewardMatchExternalTool(t *testing.T) {
	sample := stewardMatchAlbum("合成预览专辑", 2)
	f := newStewardMatchFixture(t, []metricSample{sample}, nil)
	ctx := t.Context()
	in := f.input(t, sample)
	registry := tool.NewRegistry()
	requireCurationOK(t, registry.Register(curationtools.MatchReleaseExternal(f.matcher)))
	raw, err := json.Marshal(in)
	requireCurationOK(t, err)
	before := f.snapshot(t)
	member := f.application.createUser(t, "user")
	caller := tool.Caller{User: identity.User{ID: member.id, Role: "user"}, Source: "chat", SharedExecution: true, IdempotencyKey: uuid.New()}
	_, invokeErr := registry.Invoke(ctx, caller, stewardMatchTool, raw)
	_, previewErr := registry.PreviewCall(ctx, caller, stewardMatchTool, raw)
	for _, err := range []error{invokeErr, previewErr} {
		var failure *tool.Error
		if !errors.As(err, &failure) || failure.Code != tool.CodePermissionDenied {
			t.Errorf("非管理员应被拒绝，实际 %v", err)
		}
	}
	caller.User = identity.User{ID: f.admin.id, Role: "admin"}
	preview, err := registry.PreviewCall(ctx, caller, stewardMatchTool, raw)
	requireCurationOK(t, err)
	edition := curation.ExternalSummary{Title: sample.Local.Album, Artist: sample.Local.Artist, Date: "2019", DiscCount: 1, TrackCount: 2}
	for _, want := range []tool.Change{
		{Field: "musicbrainz", After: edition}, {Field: "level", After: "release"},
		{Field: "matched_mbid", After: in.MBID}, {Field: "label", After: sample.External.Releases[0].Label},
	} {
		if !slices.ContainsFunc(preview.Changes, func(got tool.Change) bool {
			return got.Field == want.Field && got.After == want.After
		}) {
			t.Errorf("预览缺少 %+v，实际 %+v", want, preview)
		}
	}
	if preview.Target != sample.Local.Album || f.snapshot(t) != before {
		t.Fatalf("预览目标错误或写入了数据库：%+v", preview)
	}

	caller.ConversationID, caller.PlanID, caller.RequestID = uuid.New(), uuid.New(), uuid.New()
	result, err := registry.Invoke(ctx, caller, stewardMatchTool, raw)
	requireCurationOK(t, err)
	var out program.ExternalMatchResult
	requireCurationOK(t, json.Unmarshal(result.Output, &out))
	if !result.Untrusted || out.Level != "release" || out.MatchedMBID != in.MBID || !uuid.Valid(out.OperationID) {
		t.Fatalf("工具返回无效收据：%+v", result)
	}
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM operations WHERE id=$1 AND actor_id=$2
 AND origin->>'conversation_id'=$3 AND origin->>'plan_id'=$4 AND request_id=$5`,
		out.OperationID, caller.User.ID, caller.ConversationID, caller.PlanID, caller.RequestID)
}

func TestPostgreSQLStewardMatchExternalRegistration(t *testing.T) {
	f := newControlledArtistWebSearch(t, nil)
	has := func(q schedulerRequest, name string) bool {
		for _, d := range q.Tools {
			if d.Function.Name == name {
				return true
			}
		}
		return false
	}
	chat := f.startChat(t)
	first := f.next(t, false)
	if !has(first, stewardMatchTool) {
		t.Errorf("管理员聊天工具缺少 %s", stewardMatchTool)
	}
	first.complete(t)
	finishSchedulerChat(t, chat)

	artist := f.addArtist(t, "合成联网艺人")
	f.markArtistReleasesRecognized(t, artist)
	f.startArtist(t, artist)
	background := f.next(t, false)
	if !has(background, "resolve_artist_identity") || has(background, stewardMatchTool) {
		t.Errorf("后台整理工具注册错误：%+v", background.Tools)
	}
	// 强制请求该名称，区分工具确实未注册与仅按后台策略隐藏。
	background.tool(t, stewardMatchTool, program.ExternalMatchInput{ReleaseID: uuid.New(), Entity: "release", MBID: uuid.New()})
	f.next(t, false).complete(t)
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM steward_tool_calls WHERE tool_name=$1
 AND status='failed' AND error_code=$2`, stewardMatchTool, tool.CodeNotFound)
}

func TestPostgreSQLStewardMatchExternalConcurrent(t *testing.T) {
	sample := stewardMatchAlbum("合成并发专辑", 2)
	remote := sample.External.Releases[0]
	sample.Local.Tags = map[string]string{"MUSICBRAINZ_ALBUMID": remote.ID}
	entered, releaseResponse := make(chan struct{}), make(chan struct{})
	var intercepted atomic.Bool
	f := newStewardMatchFixture(t, []metricSample{sample}, func(mock http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/musicbrainz/ws/2/release/"+remote.ID && intercepted.CompareAndSwap(false, true) {
				close(entered)
				select {
				case <-releaseResponse:
				case <-r.Context().Done():
					return
				}
			}
			mock.ServeHTTP(w, r)
		})
	})
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	app := f.application
	in := f.input(t, sample)
	f.enable(t)
	key := uuid.New()
	done := make(chan error, 1)
	go func() {
		_, err := f.selectExternal(ctx, in, key)
		done <- err
	}()
	// 管家已读取本地证据，等程序完成整轮并保存结论后才放行上游响应。
	awaitExternalResult(t, ctx, entered)
	f.clock.advance(t, time.Minute)
	state := b3WorkState(t, app, "release", in.ReleaseID)
	if state.Status != "matched" {
		t.Fatalf("程序应先保存匹配结论：%+v", state)
	}
	before := f.snapshot(t)
	close(releaseResponse)
	curationCRequireProblem(t, awaitExternalResult(t, ctx, done), http.StatusConflict, "item_stale")
	b3RequireCount(t, app, 1, `SELECT count(*) FROM external_links l JOIN operations o ON o.id=l.operation_id
 WHERE l.object_type='release' AND l.object_id=$1 AND l.external_id=$2 AND o.origin->>'source'='curation'`, in.ReleaseID, remote.ID)
	b3RequireCount(t, app, 0, `SELECT count(*) FROM operations WHERE idempotency_key=$1 OR origin->>'provider'='steward'`, key)
	if got := f.snapshot(t); got != before {
		t.Fatalf("管家竞争失败后应保留程序结论：%s", got)
	}
}
