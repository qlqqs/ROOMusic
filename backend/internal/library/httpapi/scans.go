package httpapi

import (
	"errors"
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/library"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func (handlers *Handlers) startScan(responseWriter http.ResponseWriter, request *http.Request) {
	if !handlers.guard.RequireSameOrigin(responseWriter, request) {
		return
	}
	if _, ok := handlers.guard.RequireAdmin(responseWriter, request); !ok {
		return
	}
	result, err := handlers.service.StartScan(request.Context())
	switch {
	case errors.Is(err, library.ErrCoordinationUnavailable):
		httpserver.WriteError(responseWriter, request, 503, "scan_coordination_unavailable", "无法协调扫描任务")
		return
	case errors.Is(err, library.ErrStartFailed):
		httpserver.WriteError(responseWriter, request, 503, "database_unavailable", "无法启动扫描")
		return
	case err != nil:
		httpserver.WriteError(responseWriter, request, 503, "database_unavailable", "无法读取扫描状态")
		return
	}
	status := http.StatusOK
	if result.Started {
		status = http.StatusAccepted
	}
	httpserver.WriteJSON(responseWriter, status, presentScan(result.Scan, true))
}

// 扫描记录列表的页大小：默认 20、上限 100。
const (
	defaultScanPageSize = 20
	maxScanPageSize     = 100
)

// listScans 返回扫描记录（仅管理员）：条目与 GET /scans/{id} 相同，按开始时间倒序分页。
func (handlers *Handlers) listScans(responseWriter http.ResponseWriter, request *http.Request) {
	if _, ok := handlers.guard.RequireAdmin(responseWriter, request); !ok {
		return
	}
	page, pageSize, err := httpserver.ParsePagination(request, defaultScanPageSize, maxScanPageSize)
	if err != nil {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_pagination", "分页参数无效")
		return
	}
	result, err := handlers.service.ListScans(request.Context(), page, pageSize)
	if err != nil {
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "无法读取扫描记录")
		return
	}
	httpserver.WriteJSON(responseWriter, http.StatusOK, map[string]any{
		"items":      presentScans(result.Items, true),
		"pagination": map[string]any{"page": page, "page_size": pageSize, "total": result.Total},
	})
}

func (handlers *Handlers) scanStatus(responseWriter http.ResponseWriter, request *http.Request) {
	actor, authErr := handlers.guard.Authenticate(request)
	if authErr != nil {
		handlers.guard.WriteUnauthenticated(responseWriter, request)
		return
	}
	scanID := request.PathValue("id")
	if !uuid.Valid(scanID) {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_id", "扫描标识无效")
		return
	}
	scan, err := handlers.service.GetScan(request.Context(), scanID)
	if err != nil {
		if errors.Is(err, library.ErrScanNotFound) {
			httpserver.WriteError(responseWriter, request, http.StatusNotFound, "not_found", "扫描不存在")
		} else {
			httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "无法读取扫描状态")
		}
		return
	}
	httpserver.WriteJSON(responseWriter, http.StatusOK, presentScan(scan, actor.Role == "admin"))
}

func (handlers *Handlers) activeScan(responseWriter http.ResponseWriter, request *http.Request) {
	actor, authErr := handlers.guard.Authenticate(request)
	if authErr != nil {
		handlers.guard.WriteUnauthenticated(responseWriter, request)
		return
	}
	scan, err := handlers.service.ActiveScan(request.Context())
	if err != nil {
		httpserver.WriteError(responseWriter, request, 503, "database_unavailable", "无法读取扫描状态")
		return
	}
	var result any
	if scan != nil {
		result = presentScan(*scan, actor.Role == "admin")
	}
	httpserver.WriteJSON(responseWriter, http.StatusOK, map[string]any{"scan": result})
}

func (handlers *Handlers) cancelScan(responseWriter http.ResponseWriter, request *http.Request) {
	if !handlers.guard.RequireSameOrigin(responseWriter, request) {
		return
	}
	actor, ok := handlers.guard.RequireAdmin(responseWriter, request)
	if !ok {
		return
	}
	id := request.PathValue("id")
	if !uuid.Valid(id) {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_id", "扫描标识无效")
		return
	}
	result, err := handlers.service.CancelScan(request.Context(), id, actor.ID)
	switch {
	case errors.Is(err, library.ErrScanNotFound):
		httpserver.WriteError(responseWriter, request, 404, "not_found", "扫描不存在")
		return
	case errors.Is(err, library.ErrDiagnosticsUnavailable):
		httpserver.WriteError(responseWriter, request, 503, "database_unavailable", "无法读取扫描诊断")
		return
	case err != nil:
		httpserver.WriteError(responseWriter, request, 503, "database_unavailable", "无法请求取消扫描")
		return
	}
	status := http.StatusOK
	if result.Requested {
		status = http.StatusAccepted
	}
	httpserver.WriteJSON(responseWriter, status, presentScan(result.Scan, true))
}

func (handlers *Handlers) diagnostics(responseWriter http.ResponseWriter, request *http.Request) {
	if _, ok := handlers.guard.RequireAdmin(responseWriter, request); !ok {
		return
	}
	scanID := request.PathValue("id")
	if !uuid.Valid(scanID) {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_id", "扫描标识无效")
		return
	}
	page, err := handlers.service.Diagnostics(request.Context(), scanID)
	switch {
	case errors.Is(err, library.ErrScanNotFound):
		httpserver.WriteError(responseWriter, request, http.StatusNotFound, "not_found", "扫描不存在")
		return
	case errors.Is(err, library.ErrDiagnosticsUnavailable):
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "无法读取扫描诊断")
		return
	case errors.Is(err, library.ErrQuery):
		httpserver.WriteError(responseWriter, request, 503, "database_unavailable", "服务暂不可用")
		return
	case err != nil:
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_error", "无法读取扫描诊断")
		return
	}
	httpserver.WriteJSON(responseWriter, 200, map[string]any{"items": page.Items, "summary": page.Summary})
}

// 所有扫描响应共用角色 presenter；普通成员不获得根的身份或名称。
func presentScan(scan library.Scan, admin bool) any {
	var root any
	if scan.RootProgress != nil {
		r := scan.RootProgress
		value := map[string]any{"index": r.Index, "total": r.Total}
		if admin {
			value["id"] = r.ID
			value["name"] = r.Name
		}
		root = value
	}
	return struct {
		library.Scan
		Root any `json:"root_progress"`
	}{Scan: scan, Root: root}
}
func presentScans(scans []library.Scan, admin bool) []any {
	result := make([]any, 0, len(scans))
	for _, scan := range scans {
		result = append(result, presentScan(scan, admin))
	}
	return result
}
func (handlers *Handlers) scanReport(w http.ResponseWriter, r *http.Request) {
	if _, ok := handlers.guard.RequireAdmin(w, r); !ok {
		return
	}
	id := r.PathValue("id")
	if !uuid.Valid(id) {
		httpserver.WriteError(w, r, 400, "invalid_id", "扫描标识无效")
		return
	}
	page, pageSize, err := httpserver.ParsePagination(r, defaultScanPageSize, maxScanPageSize)
	if err != nil {
		httpserver.WriteError(w, r, 400, "invalid_pagination", "分页参数无效")
		return
	}
	report, err := handlers.service.GetScanReport(r.Context(), id, page, pageSize)
	switch {
	case errors.Is(err, library.ErrScanNotFound):
		httpserver.WriteError(w, r, 404, "not_found", "扫描不存在")
	case errors.Is(err, library.ErrReportLegacy):
		httpserver.WriteError(w, r, 404, "scan_report_not_available", "这次扫描没有报告")
	case errors.Is(err, library.ErrReportPending):
		httpserver.WriteError(w, r, 409, "scan_report_not_ready", "扫描报告尚未完成")
	case errors.Is(err, library.ErrReportUnavailable):
		httpserver.WriteError(w, r, 503, "scan_report_unavailable", "扫描结果已保留，报告未能生成")
	case err != nil:
		httpserver.WriteError(w, r, 503, "database_unavailable", "无法读取扫描报告")
	default:
		httpserver.WriteJSON(w, 200, report)
	}
}
