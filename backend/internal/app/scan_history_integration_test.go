package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type scanListResponse struct {
	Items      []map[string]any `json:"items"`
	Pagination struct {
		Page     int   `json:"page"`
		PageSize int   `json:"page_size"`
		Total    int64 `json:"total"`
	} `json:"pagination"`
}

func listScans(t *testing.T, application *integrationTestApplication, query string, user *integrationTestUser) scanListResponse {
	t.Helper()
	response := application.request(t, http.MethodGet, "/api/v1/scans"+query, nil, user, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("list scans%s: HTTP %d: %s", query, response.Code, response.Body.String())
	}
	var result scanListResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode scan list: %v: %s", err, response.Body.String())
	}
	return result
}

func scanIDs(items []map[string]any) []string {
	ids := make([]string, len(items))
	for index, item := range items {
		ids[index], _ = item["id"].(string)
	}
	return ids
}

func TestPostgreSQLScanListIsAdminOnlyAndValidatesPagination(t *testing.T) {
	application := newIntegrationTestApplication(t)
	admin := application.createUser(t, "admin")
	ordinary := application.createUser(t, "user")

	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/scans", nil, nil, nil), http.StatusUnauthorized, "unauthorized")
	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/scans", nil, &ordinary, nil), http.StatusForbidden, "forbidden")
	// 普通用户仍可读取单个扫描与活动扫描：列表只是管理员的“找回记录”入口。
	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/scans/"+uuid.New(), nil, &ordinary, nil), http.StatusNotFound, "not_found")
	if response := application.request(t, http.MethodGet, "/api/v1/scans/active", nil, &ordinary, nil); response.Code != http.StatusOK {
		t.Fatalf("ordinary user active scan: HTTP %d: %s", response.Code, response.Body.String())
	}

	for _, query := range []string{
		"?page=0", "?page=-1", "?page=abc", "?page_size=0", "?page_size=101", "?page_size=many",
		"?page=" + strconv.Itoa(int(^uint(0)>>1)) + "&page_size=100",
	} {
		requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/scans"+query, nil, &admin, nil), http.StatusBadRequest, "invalid_pagination")
	}

	empty := listScans(t, application, "", &admin)
	if len(empty.Items) != 0 || empty.Pagination.Page != 1 || empty.Pagination.PageSize != 20 || empty.Pagination.Total != 0 {
		t.Fatalf("unexpected empty scan list: %+v", empty)
	}
	// 空列表的 items 是 []，不是 null。
	raw := application.request(t, http.MethodGet, "/api/v1/scans", nil, &admin, nil).Body.String()
	var rawBody map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &rawBody); err != nil || string(rawBody["items"]) != "[]" {
		t.Fatalf("empty list must encode items as []: %s (%v)", raw, err)
	}
	if got := listScans(t, application, "?page_size=100", &admin).Pagination.PageSize; got != 100 {
		t.Fatalf("page_size=100 should be accepted, got %d", got)
	}
}

func TestPostgreSQLScanListOrdersPagesAndMatchesScanDetails(t *testing.T) {
	application := newIntegrationTestApplication(t)
	admin := application.createUser(t, "admin")

	// 五次扫描：两次开始时间相同（同时间按 ID 倒序），状态与取消、结束时间各异。
	type fixture struct {
		id, status, startedAt string
		finished, cancel      bool
	}
	scans := []fixture{
		{id: "00000000-0000-4000-8000-0000000000a1", status: "succeeded", startedAt: "2026-03-01T10:00:00Z", finished: true},
		{id: "00000000-0000-4000-8000-0000000000b2", status: "failed", startedAt: "2026-03-02T10:00:00Z", finished: true},
		{id: "00000000-0000-4000-8000-0000000000c3", status: "incomplete", startedAt: "2026-03-02T10:00:00Z", finished: true},
		{id: "00000000-0000-4000-8000-0000000000d4", status: "canceled", startedAt: "2026-03-03T10:00:00Z", finished: true, cancel: true},
		{id: "00000000-0000-4000-8000-0000000000e5", status: "running", startedAt: "2026-03-04T10:00:00Z", cancel: true},
	}
	for _, scan := range scans {
		finished, cancel := "NULL", "NULL"
		if scan.finished {
			finished = "'" + scan.startedAt + "'::timestamptz + INTERVAL '1 minute'"
		}
		if scan.cancel {
			cancel = "'" + scan.startedAt + "'::timestamptz + INTERVAL '30 seconds'"
		}
		mustExec(t, application, fmt.Sprintf(`INSERT INTO scan_runs(id,status,started_at,finished_at,cancel_requested_at) VALUES($1::uuid,$2,$3::timestamptz,%s,%s)`, finished, cancel), scan.id, scan.status, scan.startedAt)
	}
	// 诊断：失败的扫描有多种类别，其中一个类别名不安全（规范化为 unknown）；不完整的扫描有一条；其余没有。
	for _, diagnostic := range []struct {
		scanID, kind string
		count        int
	}{
		{"00000000-0000-4000-8000-0000000000b2", "parse_failure", 3},
		{"00000000-0000-4000-8000-0000000000b2", "walk_error", 1},
		{"00000000-0000-4000-8000-0000000000b2", "Not Safe!", 2},
		{"00000000-0000-4000-8000-0000000000c3", "parse_failure", 1},
	} {
		for index := 0; index < diagnostic.count; index++ {
			mustExec(t, application, `INSERT INTO scan_diagnostics(scan_run_id,kind,message) VALUES($1::uuid,$2,'diagnostic')`, diagnostic.scanID, diagnostic.kind)
		}
	}

	// 开始时间倒序；相同的开始时间按 ID 倒序（c3 在 b2 之前）。
	wantOrder := []string{scans[4].id, scans[3].id, scans[2].id, scans[1].id, scans[0].id}
	all := listScans(t, application, "", &admin)
	if got := scanIDs(all.Items); !reflect.DeepEqual(got, wantOrder) {
		t.Fatalf("scan order = %v, want %v", got, wantOrder)
	}
	if all.Pagination.Total != 5 || all.Pagination.Page != 1 || all.Pagination.PageSize != 20 {
		t.Fatalf("unexpected pagination: %+v", all.Pagination)
	}

	// 逐页读取：每页大小、总数不变，拼起来就是完整顺序；超出末页为空而总数照常。
	var paged []string
	for page, wantSize := range map[int]int{1: 2, 2: 2, 3: 1} {
		result := listScans(t, application, fmt.Sprintf("?page=%d&page_size=2", page), &admin)
		if len(result.Items) != wantSize || result.Pagination.Total != 5 || result.Pagination.Page != page || result.Pagination.PageSize != 2 {
			t.Fatalf("page %d: %d items, pagination %+v", page, len(result.Items), result.Pagination)
		}
	}
	for page := 1; page <= 3; page++ {
		paged = append(paged, scanIDs(listScans(t, application, fmt.Sprintf("?page=%d&page_size=2", page), &admin).Items)...)
	}
	if !reflect.DeepEqual(paged, wantOrder) {
		t.Fatalf("concatenated pages = %v, want %v", paged, wantOrder)
	}
	beyond := listScans(t, application, "?page=4&page_size=2", &admin)
	if len(beyond.Items) != 0 || beyond.Pagination.Total != 5 || beyond.Pagination.Page != 4 {
		t.Fatalf("page beyond the end: %+v", beyond)
	}

	// 每一项与 GET /scans/{id} 完全相同（含诊断计数与 null 字段）。
	for _, item := range all.Items {
		id, _ := item["id"].(string)
		detail := application.request(t, http.MethodGet, "/api/v1/scans/"+id, nil, &admin, nil)
		if detail.Code != http.StatusOK {
			t.Fatalf("scan detail %s: HTTP %d: %s", id, detail.Code, detail.Body.String())
		}
		var want map[string]any
		if err := json.Unmarshal(detail.Body.Bytes(), &want); err != nil {
			t.Fatalf("decode scan detail: %v", err)
		}
		if !reflect.DeepEqual(item, want) {
			t.Fatalf("list item differs from GET /scans/%s:\nlist:   %v\ndetail: %v", id, item, want)
		}
	}
	failed := all.Items[3]
	diagnostics, _ := failed["diagnostics"].(map[string]any)
	if diagnostics["total"] != float64(6) {
		t.Fatalf("failed scan diagnostics total = %v, want 6: %v", diagnostics["total"], failed)
	}
	wantCounts := []any{
		map[string]any{"kind": "unknown", "count": float64(2)},
		map[string]any{"kind": "parse_failure", "count": float64(3)},
		map[string]any{"kind": "walk_error", "count": float64(1)},
	}
	if !reflect.DeepEqual(diagnostics["counts"], wantCounts) {
		t.Fatalf("failed scan diagnostic counts = %v, want %v", diagnostics["counts"], wantCounts)
	}
	if want := map[string]any{"total": float64(0), "counts": []any{}}; !reflect.DeepEqual(all.Items[0]["diagnostics"], want) {
		t.Fatalf("scan without diagnostics must report an empty summary, got %v", all.Items[0]["diagnostics"])
	}
	if all.Items[0]["finished_at"] != nil || all.Items[0]["cancel_requested_at"] == nil {
		t.Fatalf("running scan with a cancel request should have finished_at null and cancel_requested_at set: %v", all.Items[0])
	}
}

func TestPostgreSQLScanListReportsDatabaseFailures(t *testing.T) {
	for _, table := range []string{"scan_runs", "scan_diagnostics"} {
		t.Run("unreadable "+table, func(t *testing.T) {
			application := newIntegrationTestApplication(t)
			admin := application.createUser(t, "admin")
			mustExec(t, application, `INSERT INTO scan_runs(id,status,started_at) VALUES($1::uuid,'succeeded',NOW())`, uuid.New())
			mustExec(t, application, fmt.Sprintf(`ALTER TABLE %s RENAME TO %s_hidden`, table, table))
			response := application.request(t, http.MethodGet, "/api/v1/scans", nil, &admin, nil)
			requireAPIError(t, response, http.StatusServiceUnavailable, "database_unavailable")
			if message := apiErrorMessage(t, response); message != "无法读取扫描记录" {
				t.Fatalf("unexpected message %q", message)
			}
		})
	}
}
