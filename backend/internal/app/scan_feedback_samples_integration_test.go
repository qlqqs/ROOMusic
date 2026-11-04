package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// 阶段快照由隔离库构造，响应必须经过真实鉴权、查询和 presenter；不手写 wire JSON。
func TestPostgreSQLScanFeedbackAPIResponseSamples(t *testing.T) {
	app := newIntegrationTestApplication(t)
	admin, member := app.createUser(t, "admin"), app.createUser(t, "user")
	samples := newAPISampleRecorder(t, app)
	samples.scope = "scan-feedback"
	call := func(method, path string, body any, user *integrationTestUser) *httptest.ResponseRecorder {
		return app.request(t, method, path, body, user, map[string]string{"X-Request-ID": sampleRequestID})
	}
	record := func(name, path string, user *integrationTestUser, status int) *httptest.ResponseRecorder {
		r := call(http.MethodGet, path, nil, user)
		samples.record("scan-feedback/"+name, r, status)
		return r
	}
	record("active-none", "/api/v1/scans/active", &member, 200)
	record("recent-empty", "/api/v1/release-groups/recently-added", &member, 200)
	id, rootID := uuid.New(), uuid.New()
	mustExec(t, app, `INSERT INTO scan_runs(id,status,started_at,feedback_version,phase,progress_bp,phase_completed,phase_total,progress_updated_at,progress_seq,files_counted,files_total,files_processed,count_complete,files_changed,roots_total,current_root_index,current_root_id,current_root_name,report_state)
 VALUES($1,'running','2026-11-04T10:00:00Z',1,'counting',0,0,2,'2026-11-04T10:01:00Z',1,50,NULL,0,false,false,2,1,$2,'测试音乐甲','pending')`, id, rootID)
	path := "/api/v1/scans/" + id
	for _, phase := range []struct {
		name                       string
		bp, done, total, processed int
	}{
		{"counting", 0, 0, 2, 0}, {"reading_tags", 1205, 50, 100, 50}, {"organizing", 1910, 0, 1, 100}, {"saving", 2380, 0, 2, 100},
	} {
		mustExec(t, app, `UPDATE scan_runs SET phase=$2,progress_bp=$3,phase_completed=$4,phase_total=$5,files_processed=$6,files_counted=CASE WHEN $2='counting' THEN 50 ELSE 400 END,files_total=CASE WHEN $2='counting' THEN NULL ELSE 400 END,count_complete=($2<>'counting') WHERE id=$1`, id, phase.name, phase.bp, phase.done, phase.total, phase.processed)
		record(phase.name+"-admin", path, &admin, 200)
		response := record(phase.name+"-member", path, &member, 200)
		root := decodeJSONMap(t, response)["root_progress"].(map[string]any)
		if _, exists := root["id"]; exists {
			t.Fatal("member response leaks root id")
		}
		if _, exists := root["name"]; exists {
			t.Fatal("member response leaks root name")
		}
	}
	mustExec(t, app, `UPDATE scan_runs SET phase_completed=2,progress_bp=2850 WHERE id=$1`, id)
	record("first-root-saved", path, &admin, 200)
	mustExec(t, app, `UPDATE scan_runs SET phase='reading_tags',phase_completed=0,phase_total=300,current_root_index=2,current_root_id=$2,current_root_name='测试音乐乙' WHERE id=$1`, id, uuid.New())
	record("second-root-started", path, &admin, 200)
	mustExec(t, app, `UPDATE scan_runs SET phase_completed=150,files_processed=250,progress_bp=4965 WHERE id=$1`, id)
	record("second-root-reading", path, &member, 200)
	record("active-member", "/api/v1/scans/active", &member, 200)
	record("list-admin", "/api/v1/scans", &admin, 200)
	record("list-forbidden", "/api/v1/scans", &member, 403)
	record("report-pending", path+"/report", &admin, 409)
	record("report-unauthorized", path+"/report", nil, 401)
	record("report-forbidden", path+"/report", &member, 403)
	record("report-invalid-id", "/api/v1/scans/invalid/report", &admin, 400)
	record("report-not-found", "/api/v1/scans/"+uuid.New()+"/report", &admin, 404)
	for _, query := range []struct{ name, value string }{{"page", "page=0"}, {"page-size", "page_size=101"}, {"overflow", "page=9223372036854775807&page_size=100"}} {
		record("report-invalid-"+query.name, path+"/report?"+query.value, &admin, 400)
	}
	for _, status := range []string{"failed", "canceled", "incomplete", "succeeded"} {
		bp := 4965
		if status == "succeeded" {
			bp = 10000
		}
		mustExec(t, app, `UPDATE scan_runs SET status=$2,progress_bp=$3,finished_at='2026-11-04T10:02:00Z',report_state='unavailable' WHERE id=$1`, id, status, bp)
		record("terminal-"+status, path, &member, 200)
	}
	record("report-unavailable", path+"/report", &admin, 503)
	mustExec(t, app, `UPDATE scan_runs SET status='incomplete',progress_bp=4965,report_state='pending' WHERE id=$1`, id)
	record("recovered-unavailable", path, &admin, 200)
	legacy := uuid.New()
	mustExec(t, app, `INSERT INTO scan_runs(id,status,started_at,finished_at) VALUES($1,'succeeded',NOW(),NOW())`, legacy)
	record("legacy", "/api/v1/scans/"+legacy, &admin, 200)
	record("report-legacy", "/api/v1/scans/"+legacy+"/report", &admin, 404)
	for _, tc := range []struct {
		name              string
		roots             int
		counted           int
		total             any
		complete, changed bool
	}{
		{"no-roots", 0, 0, 0, true, false}, {"zero-files", 2, 0, 0, true, false}, {"count-incomplete", 2, 3, 3, false, false}, {"files-drift", 2, 3, 3, true, true},
	} {
		mustExec(t, app, `UPDATE scan_runs SET roots_total=$2,current_root_index=NULL,current_root_id=NULL,current_root_name=NULL,files_counted=$3,files_total=$4,count_complete=$5,files_changed=$6 WHERE id=$1`, id, tc.roots, tc.counted, tc.total, tc.complete, tc.changed)
		record(tc.name, path, &admin, 200)
	}
	// 启动/复用/取消使用真实 worker；锁住目录读取，保证响应停留在 running。
	holder, err := app.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	if _, err = holder.Exec(`LOCK TABLE library_roots IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	started := call(http.MethodPost, "/api/v1/scans", map[string]any{}, &admin)
	samples.record("scan-feedback/start-created", started, 202)
	scanID := decodeJSONMap(t, started)["id"].(string)
	waitForLockWaiters(t, app.database, 1)
	samples.record("scan-feedback/start-existing", call(http.MethodPost, "/api/v1/scans", map[string]any{}, &admin), 200)
	samples.record("scan-feedback/cancel-requested", call(http.MethodPost, "/api/v1/scans/"+scanID+"/cancel", nil, &admin), 202)
	if err = holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	if status := waitForScanToFinish(t, app, scanID); status != "canceled" {
		t.Fatalf("cancel result=%s", status)
	}
	samples.record("scan-feedback/cancel-terminal", call(http.MethodPost, "/api/v1/scans/"+scanID+"/cancel", nil, &admin), 200)
	record("report-partial-empty", "/api/v1/scans/"+scanID+"/report", &admin, 200)
	recordScanFeedbackCatalogSamples(t, samples)
	samples.finish()
}

func recordScanFeedbackCatalogSamples(t *testing.T, samples *apiSampleRecorder) {
	env := newClaimRescanEnvironment(t, "replaced", "updated", "missing")
	app := env.application
	member := app.createUser(t, "user")
	samples.forbid(app.allowedRoot)
	call := func(name, path string, user *integrationTestUser, status int) *httptest.ResponseRecorder {
		r := app.request(t, http.MethodGet, path, nil, user, map[string]string{"X-Request-ID": sampleRequestID})
		samples.record("scan-feedback/"+name, r, status)
		return r
	}
	old, _ := env.releaseIDs(t, "replaced")
	env.correct(t, old, "我保留的专辑标题")
	updated, _ := env.releaseIDs(t, "updated")
	updatedGroup, _ := scanFeedbackReleaseState(t, app, updated)
	prepared := app.request(t, http.MethodPost, "/api/v1/releases/"+old+"/group", joinBody(2, updatedGroup), &env.admin, idempotencyHeader(uuid.New()))
	if prepared.Code != 200 {
		t.Fatalf("prepare claim album: %s", prepared.Body.String())
	}
	for n := 1; n <= 2; n++ {
		env.write(t, "replaced", n, byte(100+n), "New")
	}
	env.write(t, "updated", 1, 21, "Updated")
	// missing 根内文件只在测试临时目录删除。
	removeScanFeedbackAlbum(t, env, "missing")
	env.write(t, "new", 1, 111, "Added")
	scanID := startScanFeedback(t, app, &env.admin)
	if status := waitForScanToFinish(t, app, scanID); status != "succeeded" {
		t.Fatalf("scan=%s", status)
	}
	reportPath := "/api/v1/scans/" + scanID + "/report"
	ready := call("report-ready", reportPath, &env.admin, 200)
	assertScanFeedbackCounts(t, decodeJSONMap(t, ready), [3]int{2, 1, 1}, [3]int{3, 1, 4})
	call("report-ready-page-one", reportPath+"?page_size=1", &env.admin, 200)
	call("report-ready-page-two", reportPath+"?page_size=1&page=2", &env.admin, 200)
	call("report-ready-page-empty", reportPath+"?page=99", &env.admin, 200)
	noChanges := startScanFeedback(t, app, &env.admin)
	assertScanFeedbackCounts(t, scanFeedbackReport(t, app, &env.admin, noChanges), [3]int{}, [3]int{})
	call("report-ready-empty", "/api/v1/scans/"+noChanges+"/report", &env.admin, 200)
	mustExec(t, app, `UPDATE scan_reports SET diagnostics_recorded=100,diagnostics_may_be_truncated=true WHERE scan_run_id=$1`, scanID)
	call("report-issues-truncated", reportPath, &env.admin, 200)
	mustExec(t, app, `UPDATE scan_reports SET diagnostics_recorded=2,diagnostics_may_be_truncated=false WHERE scan_run_id=$1`, scanID)
	target, _ := env.releaseIDs(t, "replaced")
	preview := decodeJSONMap(t, app.request(t, http.MethodGet, claimPreviewPath(target, old), nil, &env.admin, nil))
	response := app.request(t, http.MethodPost, claimPath(target), claimBody(int64(preview["revision"].(float64)), int64(preview["source_revision"].(float64)), old, preview["token"].(string)), &env.admin, idempotencyHeader(uuid.New()))
	if response.Code != 200 {
		t.Fatalf("claim: %s", response.Body.String())
	}
	call("report-after-claim", reportPath, &env.admin, 200)
	newAlbum, _ := env.releaseIDs(t, "new")
	newGroup, _ := scanFeedbackReleaseState(t, app, newAlbum)
	_, targetRevision := scanFeedbackReleaseState(t, app, target)
	joined := app.request(t, http.MethodPost, "/api/v1/releases/"+target+"/group", joinBody(targetRevision, newGroup), &env.admin, idempotencyHeader(uuid.New()))
	if joined.Code != 200 {
		t.Fatalf("report regroup: %s", joined.Body.String())
	}
	call("report-current-group-changed", reportPath, &env.admin, 200)
	mustExec(t, app, `UPDATE tracks SET source_status='missing' WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1)`, target)
	call("report-historical-invisible", reportPath, &env.admin, 200)
	// 非成功报告的形状与已提交变更相同，但不声称完成全根 missing 对账。
	mustExec(t, app, `UPDATE scan_runs SET status='incomplete',progress_bp=9000,report_state='partial' WHERE id=$1`, scanID)
	mustExec(t, app, `UPDATE scan_reports SET missing_reconciled=false WHERE scan_run_id=$1`, scanID)
	call("report-partial", reportPath, &env.admin, 200)
	// 控制可见数、创建时间与默认代表，保证最近新增的样本稳定且不等同代表版本的时间。
	mustExec(t, app, `UPDATE tracks SET source_status='missing'`)
	rootID := uuid.New()
	mustExec(t, app, `INSERT INTO library_roots(id,path,status,revision) VALUES($1,$2,'active',1)`, rootID, app.allowedRoot)
	for i := 1; i <= 9; i++ {
		release, groupID := insertGroupedRelease(t, app, rootID, groupedRelease{title: fmt.Sprintf("最近专辑 %02d", i), artist: "合成艺人", year: 2000 + i, tracks: []string{"present"}})
		mustExec(t, app, `UPDATE releases SET created_at='2026-01-01T00:00:00Z'::timestamptz+$2*interval '1 day' WHERE id=$1`, release, i)
		if i == 1 {
			_, _ = insertGroupedRelease(t, app, rootID, groupedRelease{groupID: groupID, title: "最近专辑 01 的后加版本", artist: "合成艺人", year: 1900, createdAt: "2027-01-01T00:00:00Z", tracks: []string{"present"}})
			call("recent-one", "/api/v1/release-groups/recently-added", &member, 200)
		}
	}
	call("recent-eight", "/api/v1/release-groups/recently-added", &member, 200)
	call("sort-title", "/api/v1/release-groups?sort=title", &member, 200)
	call("sort-added", "/api/v1/release-groups?sort=added_at_desc", &member, 200)
	call("sort-invalid", "/api/v1/release-groups?sort=bogus", &member, 400)
	call("sort-duplicate", "/api/v1/release-groups?sort=title&sort=added_at_desc", &member, 400)
	call("recent-invalid-query", "/api/v1/release-groups/recently-added?limit=9", &member, 400)
	call("recent-unauthorized", "/api/v1/release-groups/recently-added", nil, 401)
}
