package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
)

// A-V2：用独立发行与固定用户名录制，保留写入者、撤销关联和批次计数。
func recordCurationSamples(t *testing.T, samples *apiSampleRecorder) {
	t.Helper()
	f := newCurationFixture(t)
	mustExec(t, f.application, "UPDATE users SET username='curator' WHERE id=$1", f.admin.id)
	samples.forbid(f.admin.token)
	samples.forbid(f.application.allowedRoot)
	samples.forbid(f.application.dataDirectory)
	record := func(name, method, path string, body any) {
		t.Helper()
		r := f.application.request(t, method, path, body, &f.admin, idempotencyHeader(uuid.New()))
		samples.record(name, r, 200)
	}
	record("curation/settings-disabled", "GET", curationBase+"/settings", nil)
	record("curation/sources", "GET", curationBase+"/sources", nil)
	record("curation/settings-enabled", "PUT", curationBase+"/settings", map[string]bool{"enabled": true})
	record("curation/source-updated", "PUT", curationBase+"/sources/musicbrainz", map[string]bool{"enabled": false})
	const sampleKey = "synthetic-application-key-4321"
	samples.forbid(sampleKey)
	keyPath := curationBase + "/sources/acoustid/keys/application"
	record("curation/source-key-set", http.MethodPut, keyPath, map[string]string{"key": sampleKey})
	record("curation/source-key-cleared", http.MethodDelete, keyPath, nil)
	samples.record("errors/curation-key-not-supported", f.application.request(t, http.MethodPut,
		curationBase+"/sources/musicbrainz/keys/application", map[string]string{"key": sampleKey}, &f.admin, map[string]string{"X-Request-ID": sampleRequestID}), http.StatusBadRequest)
	batch := f.batch(t, "program")
	op := f.apply(t, f.a, "fixture-catalog", batch, map[string]any{"title": "合成自动标题", "year": nil})
	record("corrections/get-auto", "GET", "/api/v1/releases/"+f.a+"/corrections", nil)
	record("releases/get-auto", "GET", "/api/v1/releases/"+f.a, nil)
	record("operations/auto-reverted", "POST", revertPath(op), nil)
	record("operations/auto-list", "GET", "/api/v1/operations?release_id="+f.a, nil)
	// 单个标记生成列表，避免来自同一事务的多个标记按随机 UUID 排序。
	f.apply(t, f.b, "fixture-catalog", batch, map[string]any{"edition": "合成版本"})
	if err := f.writer.CloseBatch(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	record("curation/scope-reverted", "POST", curationBase+"/reverts", map[string]any{"scope": curation.Scope{Kind: "batch", ID: batch}})
	record("curation/suppressions", "GET", curationBase+"/suppressions?release_id="+f.b, nil)
	record("curation/batches", "GET", curationBase+"/batches", nil)
	record("curation/batch", "GET", curationBase+"/batches/"+batch, nil)
	record("curation/settings-paused", "PUT", curationBase+"/settings", map[string]any{"paused": true, "max_request_tokens": 240000})
	record("curation/progress", "GET", curationBase+"/progress", nil)

	record("curation/settings-model", "PUT", curationBase+"/settings", map[string]any{"model": "sample-model", "daily_token_limit": nil})
	ctx := t.Context()
	store := runtime.NewStore(f.application.database)
	c, err := store.CreateCurationConversation(ctx, f.admin.id, "自动整理合成样例", "sample-model")
	if err != nil {
		t.Fatal(err)
	}
	itemID := uuid.New()
	mustExec(t, f.application, `INSERT INTO curation_pending_items(id,target_type,target_id,kind,status,rule_version,input_digest,evidence,conversation_id,claimed_at) VALUES($1,'release',$2,'release_match','in_progress',1,'sample','{}',$3,now())`, itemID, f.a, c.ID)
	record("curation/conversations", "GET", curationBase+"/conversations", nil)
	errSample := func(name, path string) {
		samples.record(name, f.application.request(t, "POST", path, nil, &f.admin, map[string]string{"X-Request-ID": sampleRequestID}), 409)
	}
	conversationPath := "/api/v1/me/steward/conversations/" + c.ID
	samples.record("errors/curation-conversation", f.application.request(t, http.MethodPatch, conversationPath, map[string]string{"mode": "operator"}, &f.admin, map[string]string{"X-Request-ID": sampleRequestID}), 409)
	plan, err := store.CreatePlan(ctx, f.admin.id, c.ID, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	errSample("errors/curation-plan", conversationPath+"/plans/"+plan.ID+"/review")

	// 独立设置这条曲目操作的展示标题，原样本标题曾来自已经下线的建议操作。
	mustExec(t, f.application, `UPDATE releases SET title='合成标题甲' WHERE id=$1`, f.a)
	var trackID string
	if err := f.application.database.QueryRowContext(ctx, `SELECT t.id FROM track_effective t JOIN media m ON m.id=t.medium_id WHERE m.release_id=$1 ORDER BY m.position,t.position LIMIT 1`, f.a).Scan(&trackID); err != nil {
		t.Fatal(err)
	}
	applied, err := f.writer.ApplyTrack(ctx, curation.TrackRequest{TrackID: trackID, ReleaseID: f.a, Key: uuid.New(), Fields: map[string]catalog.CorrectionIntent{"title": autoIntent("合成曲目自动标题")}, Source: curation.Source{Kind: "source", Provider: "track-sample", Basis: "合成录音对应"}})
	if err != nil {
		t.Fatal(err)
	}
	record("operations/track-auto-list", "GET", "/api/v1/operations?provider=track-sample", nil)
	record("operations/track-auto-reverted", "POST", revertPath(applied.OperationID), nil)
	recordReleaseMergeCurationSamples(t, samples)
}

func recordProgramMatchSamples(t *testing.T, samples *apiSampleRecorder) {
	t.Helper()
	f := newProgramFixture(t)
	app := f.application
	samples.forbid(f.admin.token)
	track, _ := f.tagged(t)
	mustExec(t, app, `UPDATE tracks SET duration_seconds=100 WHERE id=$1`, track)

	single, _ := insertGroupedRelease(t, app, f.rootID, groupedRelease{
		title: "合成冲突单曲", artist: "合成单曲艺人", tracks: []string{"present"},
	})
	singleTrack, _ := externalTrack(t, app, single)
	mustExec(t, app, `UPDATE releases SET candidate_kind='loose_unknown' WHERE id=$1`, single)
	mustExec(t, app, `UPDATE tracks SET duration_seconds=100,title='合成单曲录音' WHERE id=$1`, singleTrack)
	const isrc = "USABC2600001"
	programTag(t, app, singleTrack, "isrc", isrc)
	length := int64(100000)
	artist := []sources.Credit{{Name: "合成单曲艺人"}}
	// 候选按固定录音编号排序。两种写法归一后都能通过曲名核对。
	recordings := []sources.Recording{
		{ID: "6b8baa35-eb21-4e5b-9c65-1bad3c6f3142", Title: "合成单曲录音", Artists: artist, LengthMS: &length, ISRCs: []string{isrc}},
		{ID: "6b8baa35-eb21-4e5b-9c65-1bad3c6f3143", Title: "合成单曲・录音", Artists: artist, LengthMS: &length, ISRCs: []string{isrc}},
	}
	f.source.isrc[isrc] = recordings
	for _, recording := range recordings {
		f.source.recordings[recording.ID] = recording
	}
	f.sourceEnabled(t, "lrclib", false)
	f.enable(t)
	f.run(t)
	f.assertRelease(t, f.a, "matched", "tag_id")
	f.assertRelease(t, single, "conflict", "isrc")

	matched := app.request(t, http.MethodGet, curationBase+"/releases/"+f.a+"/match-results", nil, &f.admin, nil)
	conflict := app.request(t, http.MethodGet, curationBase+"/releases/"+single+"/match-results", nil, &f.admin, nil)
	list := app.request(t, http.MethodGet, curationBase+"/conflicts", nil, &f.admin, nil)
	samples.record("curation/match-results-matched", matched, http.StatusOK)
	samples.record("curation/match-results-recording-conflict", conflict, http.StatusOK)
	samples.record("curation/conflicts-recording", list, http.StatusOK)

	ctx := t.Context()
	rows, err := f.writer.ReleaseMatchResults(ctx, single)
	requireCurationOK(t, err)
	if len(rows) != 1 || !uuid.Valid(rows[0].SnapshotID) {
		t.Fatalf("单曲冲突没有候选快照：%+v", rows)
	}
	choice := curation.MatchChoice{Source: "musicbrainz", SnapshotID: rows[0].SnapshotID, ExternalID: recordings[1].ID}
	matcher := program.NewModelMatcher(app.database, f.writer, f.gate)
	selected, err := matcher.SelectMatch(ctx, single, f.admin.id, uuid.New(), choice)
	requireCurationOK(t, err)
	if selected.Status != "matched" || selected.Selection == nil || selected.Selection.ExternalID != choice.ExternalID {
		t.Fatalf("没有保存所选录音：%+v", selected)
	}
}

func recordAlbumRetryProgressSample(t *testing.T, samples *apiSampleRecorder) {
	t.Helper()
	clock := newProgramClock()
	external := uuid.New()
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mb/release/"+external {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":`)
			return
		}
		http.NotFound(w, r)
	})
	f := newSchedulerFixtureWithSources(t, nil, upstream, WithCurationProgram(true, clock.options()))
	clock.wait(t)
	app := f.application
	samples.forbid(f.admin.token)
	mustExec(t, app, `UPDATE tracks SET source_status='missing' WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1)`, f.b)
	track, _ := externalTrack(t, app, f.a)
	programTag(t, app, track, "mb_release", external)
	f.enable(t)
	clock.advance(t, time.Minute)

	response := app.request(t, http.MethodGet, curationBase+"/progress", nil, &f.admin, nil)
	samples.record("curation/progress-album-retry", response, http.StatusOK)
	var progress curation.Progress
	requireCurationOK(t, json.Unmarshal(response.Body.Bytes(), &progress))
	queue := progress.Queues[0]
	if queue.ID != "release" || queue.State != "paused" || queue.PauseReason == nil || *queue.PauseReason != "invalid_response" ||
		queue.Source == nil || *queue.Source != "musicbrainz" || queue.RetryAt == nil || !queue.RetryAt.After(clock.options().Now()) {
		t.Fatalf("识别队列没有进入专辑专属重试：%+v，响应 %s", queue, response.Body)
	}
}

func recordModelQuotaProgressSample(t *testing.T, samples *apiSampleRecorder) {
	t.Helper()
	f := newSchedulerFixture(t)
	samples.forbid(f.admin.token)
	f.configure(t, &f.admin)
	f.pending(t, f.a)
	f.enable(t)
	f.next(t, false).fail <- schedulerHTTPFailure{status: http.StatusPaymentRequired, code: "insufficient_balance", message: "余额不足"}
	retry := f.clock.options().Now().Add(time.Hour)
	model := requireCurationRulePause(t, f, "provider_quota", &retry)
	if model.State != "unavailable" || model.UnavailableReason == nil || *model.UnavailableReason != "provider_quota" {
		t.Fatalf("模型没有返回上游额度不足：%+v", model)
	}
	samples.record("curation/progress-model-provider-quota", f.application.request(t, http.MethodGet, curationBase+"/progress", nil, &f.admin, nil), http.StatusOK)
}
