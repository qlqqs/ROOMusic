package app

import (
	"encoding/json"
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
	record("curation/settings-paused", "PUT", curationBase+"/settings", map[string]any{"paused": true, "web_daily_token_limit": 240000})
	record("curation/progress", "GET", curationBase+"/progress", nil)
	samples.recordEmpty("curation/repatrol", f.application.request(t, http.MethodPost, curationBase+"/repatrol", nil, &f.admin, nil), http.StatusAccepted)

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
	makeException := func(after string) string {
		t.Helper()
		input, err := json.Marshal(map[string]any{"release_id": f.a, "expected_revision": 1, "basis": map[string]string{"note": "合成候选资料"}, "changes": map[string]any{"title": map[string]string{"mode": "set", "value": after}}})
		if err != nil {
			t.Fatal(err)
		}
		preview, err := json.Marshal(map[string]any{"changes": []map[string]string{{"field": "title", "before": "Album A", "after": after}}})
		if err != nil {
			t.Fatal(err)
		}
		risk := "medium"
		id, err := f.writer.CreateException(ctx, curation.ModelException{PendingItemID: itemID, PlanItemID: uuid.New(), ConversationID: &c.ID, Tool: "correct_release", Input: input, Preview: preview, Provider: "sample-model",
			Review: curation.ExceptionReview{ID: uuid.New(), Model: "sample-review", Decision: "needs_human_confirmation", Source: "model", Risk: &risk, Reasons: []string{"发行资料需要人工核对"}}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	first := makeException("合成标题甲")
	input, err := json.Marshal(schedulerCorrection(f.a, 1, "label", setChange("岸风唱片")))
	requireCurationOK(t, err)
	unverified, err := f.writer.RecordSuggestion(ctx, curation.ModelException{
		PendingItemID: itemID, ConversationID: &c.ID, Tool: "correct_release", Provider: "sample-model",
		Input:   input,
		Preview: []byte(`{"changes":[{"field":"label","before":null,"after":"岸风唱片"}]}`), ReasonCode: "unverified_fact",
		Reasons: []string{"开放库没有可核实的厂牌"}, Evidence: []byte(`{"field":"label","value":"岸风唱片"}`),
	})
	requireCurationOK(t, err)
	mustExec(t, f.application, `UPDATE curation_model_exceptions SET created_at=now()-interval '1 second' WHERE id=$1`, unverified)
	observationItem := uuid.New()
	const reason = "保留原有标点"
	mustExec(t, f.application, `INSERT INTO curation_pending_items(id,target_type,target_id,kind,status,rule_version,input_digest,evidence,conversation_id,claimed_at)
		VALUES($1,'release',$2,'verify_name','in_progress',1,'sample-style','{}',$3,now())`, observationItem, f.a, c.ID)
	input, err = json.Marshal(curation.VerificationInput{ItemID: observationItem, Verdict: "style", Reason: reason})
	requireCurationOK(t, err)
	observation, err := f.writer.RecordSuggestion(ctx, curation.ModelException{
		PendingItemID: observationItem, ConversationID: &c.ID, Tool: "observation", Provider: "sample-model",
		Input: input, Preview: []byte(`{"changes":[]}`), ReasonCode: "verification_declined",
		Reasons: []string{reason}, Evidence: []byte(`{"field":"title","before":"Album A","after":"Album: A"}`),
	})
	requireCurationOK(t, err)
	mustExec(t, f.application, `UPDATE curation_model_exceptions SET created_at=now()-interval '2 seconds' WHERE id=$1`, observation)
	record("curation/exceptions", "GET", curationBase+"/exceptions", nil)
	for _, id := range []string{unverified, observation} {
		r := f.application.request(t, http.MethodPost, curationBase+"/exceptions/"+id+"/decline", nil, &f.admin, nil)
		if r.Code != http.StatusOK {
			t.Fatalf("关闭建议 HTTP %d %s", r.Code, r.Body)
		}
	}
	second := makeException("合成标题乙")
	base := curationBase + "/exceptions/"
	record("curation/exception-accepted", "POST", base+first+"/accept", nil)
	errSample := func(name, path string) {
		samples.record(name, f.application.request(t, "POST", path, nil, &f.admin, map[string]string{"X-Request-ID": sampleRequestID}), 409)
	}
	errSample("errors/exception-stale", base+second+"/accept")
	record("curation/exception-declined", "POST", base+second+"/decline", nil)
	errSample("errors/exception-decided", base+second+"/decline")
	conversationPath := "/api/v1/me/steward/conversations/" + c.ID
	samples.record("errors/curation-conversation", f.application.request(t, http.MethodPatch, conversationPath, map[string]string{"mode": "operator"}, &f.admin, map[string]string{"X-Request-ID": sampleRequestID}), 409)
	plan, err := store.CreatePlan(ctx, f.admin.id, c.ID, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	errSample("errors/curation-plan", conversationPath+"/plans/"+plan.ID+"/review")

	var trackID, title string
	if err := f.application.database.QueryRowContext(ctx, `SELECT t.id,t.title FROM track_effective t JOIN media m ON m.id=t.medium_id WHERE m.release_id=$1 ORDER BY m.position,t.position LIMIT 1`, f.a).Scan(&trackID, &title); err != nil {
		t.Fatal(err)
	}
	applied, err := f.writer.ApplyTrack(ctx, curation.TrackRequest{TrackID: trackID, ReleaseID: f.a, Key: uuid.New(), Fields: map[string]catalog.CorrectionIntent{"title": autoIntent("合成曲目自动标题")}, Source: curation.Source{Kind: "source", Provider: "track-sample", Basis: "合成录音对应"}})
	if err != nil {
		t.Fatal(err)
	}
	record("operations/track-auto-list", "GET", "/api/v1/operations?provider=track-sample", nil)
	record("operations/track-auto-reverted", "POST", revertPath(applied.OperationID), nil)
	mustExec(t, f.application, `UPDATE curation_pending_items SET status='in_progress',conversation_id=$2,claimed_at=now() WHERE id=$1`, itemID, c.ID)
	input, err = json.Marshal(map[string]any{"release_id": f.a, "track_id": trackID, "expected_revision": 1, "changes": map[string]any{"title": setChange("待核对曲名")}, "basis": map[string]string{"source": "external", "note": "合成曲目资料"}})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := json.Marshal(map[string]any{"changes": []map[string]string{{"field": "title", "before": title, "after": "待核对曲名"}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.writer.CreateException(ctx, curation.ModelException{PendingItemID: itemID, PlanItemID: uuid.New(), ConversationID: &c.ID, Tool: "correct_track", Input: input, Preview: preview, Provider: "sample-model", Review: curation.ExceptionReview{ID: uuid.New(), Model: "sample-review", Decision: "needs_human_confirmation", Source: "model", Reasons: []string{"曲名需要人工核对"}}})
	if err != nil {
		t.Fatal(err)
	}
	record("curation/exceptions-track", "GET", curationBase+"/exceptions", nil)
	recordReleaseMergeCurationSamples(t, samples)
}
