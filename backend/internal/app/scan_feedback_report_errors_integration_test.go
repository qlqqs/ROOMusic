package app

import (
	"net/http"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLScanFeedbackReportDatabaseErrors(t *testing.T) {
	app := newIntegrationTestApplication(t)
	admin, member := app.createUser(t, "admin"), app.createUser(t, "user")
	id := uuid.New()
	mustExec(t, app, `INSERT INTO scan_runs(id,status,started_at,feedback_version,report_state) VALUES($1,'succeeded',NOW(),1,'ready')`, id)
	path := "/api/v1/scans/" + id + "/report"
	// 记账缺失和读取数据库故障属于不同错误；鉴权先于任何报告存在性查询。
	requireAPIError(t, app.request(t, http.MethodGet, path, nil, &admin, nil), 503, "scan_report_unavailable")
	mustExec(t, app, `ALTER TABLE scan_reports RENAME TO hidden_scan_reports`)
	requireAPIError(t, app.request(t, http.MethodGet, path, nil, &admin, nil), 503, "database_unavailable")
	requireAPIError(t, app.request(t, http.MethodGet, path, nil, &member, nil), 403, "forbidden")
	requireAPIError(t, app.request(t, http.MethodGet, path, nil, nil, nil), 401, "unauthorized")
}
