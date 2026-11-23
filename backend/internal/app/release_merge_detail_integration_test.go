package app

import (
	"net/http"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLReleaseMergeDetailAPI(t *testing.T) {
	f := newGroupEnvironment(t)
	target, source := "/api/v1/releases/"+f.a, "/api/v1/releases/"+f.b
	get := func(path string, user *integrationTestUser) map[string]any {
		t.Helper()
		r := f.application.request(t, http.MethodGet, path, nil, user, nil)
		if r.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, r.Code, r.Body.String())
		}
		return decodeJSONMap(t, r)
	}
	if refs, ok := get(target, &f.admin)["merged_releases"].([]any); !ok || len(refs) != 0 {
		t.Fatalf("管理员的初始并入列表=%v", refs)
	}
	preview := get(target+"/merge-preview?source="+f.b, &f.admin)
	body := map[string]any{"source_release_id": f.b, "token": preview["token"]}
	requireAPIError(t, f.application.request(t, http.MethodGet, target+"/merge-preview?source="+f.b, nil, &f.ordinary, nil), 403, "forbidden")
	requireAPIError(t, f.application.request(t, http.MethodPost, target+"/merge", body, &f.ordinary, idempotencyHeader(uuid.New())), 403, "forbidden")
	headers := idempotencyHeader(uuid.New())
	headers["Origin"] = "http://other.example"
	requireAPIError(t, f.application.request(t, http.MethodPost, target+"/merge", body, &f.admin, headers), 403, "origin_forbidden")
	merged := f.application.request(t, http.MethodPost, target+"/merge", body, &f.admin, idempotencyHeader(uuid.New()))
	if merged.Code != 200 {
		t.Fatalf("合并=%d %s", merged.Code, merged.Body.String())
	}
	op := decodeJSONMap(t, merged)["operation"].(map[string]any)["id"]
	refs, ok := get(target, &f.admin)["merged_releases"].([]any)
	if !ok || len(refs) != 1 {
		t.Fatalf("管理员的并入列表=%v", refs)
	}
	ref := refs[0].(map[string]any)
	if ref["id"] != f.b || ref["operation_id"] != op || ref["title"] != "Album B" || ref["revertible"] != true {
		t.Fatalf("并入来源=%v", ref)
	}
	if _, present := get(target, &f.ordinary)["merged_releases"]; present {
		t.Fatal("普通成员收到了管理员并入列表")
	}
	for _, user := range []*integrationTestUser{&f.admin, &f.ordinary} {
		requireAPIError(t, f.application.request(t, http.MethodGet, source, nil, user, nil), 404, "not_found")
	}
}
