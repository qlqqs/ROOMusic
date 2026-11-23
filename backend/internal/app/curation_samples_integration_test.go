package app

import (
	"net/http"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
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
	record("curation/settings-paused", "PUT", curationBase+"/settings", map[string]bool{"paused": true})
	record("curation/progress", "GET", curationBase+"/progress", nil)
}
