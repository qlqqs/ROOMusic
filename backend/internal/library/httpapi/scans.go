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
	httpserver.WriteJSON(responseWriter, status, result.Scan)
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
		"items":      result.Items,
		"pagination": map[string]any{"page": page, "page_size": pageSize, "total": result.Total},
	})
}

func (handlers *Handlers) scanStatus(responseWriter http.ResponseWriter, request *http.Request) {
	if _, err := handlers.guard.Authenticate(request); err != nil {
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
	httpserver.WriteJSON(responseWriter, http.StatusOK, scan)
}

func (handlers *Handlers) activeScan(responseWriter http.ResponseWriter, request *http.Request) {
	if _, err := handlers.guard.Authenticate(request); err != nil {
		handlers.guard.WriteUnauthenticated(responseWriter, request)
		return
	}
	scan, err := handlers.service.ActiveScan(request.Context())
	if err != nil {
		httpserver.WriteError(responseWriter, request, 503, "database_unavailable", "无法读取扫描状态")
		return
	}
	httpserver.WriteJSON(responseWriter, http.StatusOK, map[string]any{"scan": scan})
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
	httpserver.WriteJSON(responseWriter, status, result.Scan)
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
