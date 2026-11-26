package app

import (
	"net/http"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
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
