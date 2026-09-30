package main

import (
	"errors"
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/library"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func (application *roomusicApplication) startScan(responseWriter http.ResponseWriter, request *http.Request) {
	if !application.requireSameOrigin(responseWriter, request) {
		return
	}
	if _, ok := application.requireAdmin(responseWriter, request); !ok {
		return
	}
	result, err := application.library.StartScan(request.Context())
	switch {
	case errors.Is(err, library.ErrCoordinationUnavailable):
		writeAPIError(responseWriter, request, 503, "scan_coordination_unavailable", "无法协调扫描任务")
		return
	case errors.Is(err, library.ErrStartFailed):
		writeAPIError(responseWriter, request, 503, "database_unavailable", "无法启动扫描")
		return
	case err != nil:
		writeAPIError(responseWriter, request, 503, "database_unavailable", "无法读取扫描状态")
		return
	}
	status := http.StatusOK
	if result.Started {
		status = http.StatusAccepted
	}
	writeJSON(responseWriter, status, result.Scan)
}

func (application *roomusicApplication) scanStatus(responseWriter http.ResponseWriter, request *http.Request) {
	if _, err := application.authenticatedUser(request); err != nil {
		application.writeAuthenticationError(responseWriter, request)
		return
	}
	scanID := request.PathValue("id")
	if !uuid.Valid(scanID) {
		writeAPIError(responseWriter, request, http.StatusBadRequest, "invalid_id", "扫描标识无效")
		return
	}
	scan, err := application.library.GetScan(request.Context(), scanID)
	if err != nil {
		if errors.Is(err, library.ErrScanNotFound) {
			writeAPIError(responseWriter, request, http.StatusNotFound, "not_found", "扫描不存在")
		} else {
			writeAPIError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "无法读取扫描状态")
		}
		return
	}
	writeJSON(responseWriter, http.StatusOK, scan)
}

func (application *roomusicApplication) activeScan(responseWriter http.ResponseWriter, request *http.Request) {
	if _, err := application.authenticatedUser(request); err != nil {
		application.writeAuthenticationError(responseWriter, request)
		return
	}
	scan, err := application.library.ActiveScan(request.Context())
	if err != nil {
		writeAPIError(responseWriter, request, 503, "database_unavailable", "无法读取扫描状态")
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"scan": scan})
}

func (application *roomusicApplication) cancelScan(responseWriter http.ResponseWriter, request *http.Request) {
	if !application.requireSameOrigin(responseWriter, request) {
		return
	}
	actor, ok := application.requireAdmin(responseWriter, request)
	if !ok {
		return
	}
	id := request.PathValue("id")
	if !uuid.Valid(id) {
		writeAPIError(responseWriter, request, http.StatusBadRequest, "invalid_id", "扫描标识无效")
		return
	}
	result, err := application.library.CancelScan(request.Context(), id, actor.ID)
	switch {
	case errors.Is(err, library.ErrScanNotFound):
		writeAPIError(responseWriter, request, 404, "not_found", "扫描不存在")
		return
	case errors.Is(err, library.ErrDiagnosticsUnavailable):
		writeAPIError(responseWriter, request, 503, "database_unavailable", "无法读取扫描诊断")
		return
	case err != nil:
		writeAPIError(responseWriter, request, 503, "database_unavailable", "无法请求取消扫描")
		return
	}
	status := http.StatusOK
	if result.Requested {
		status = http.StatusAccepted
	}
	writeJSON(responseWriter, status, result.Scan)
}

func (application *roomusicApplication) diagnostics(responseWriter http.ResponseWriter, request *http.Request) {
	if _, ok := application.requireAdmin(responseWriter, request); !ok {
		return
	}
	scanID := request.PathValue("id")
	if !uuid.Valid(scanID) {
		writeAPIError(responseWriter, request, http.StatusBadRequest, "invalid_id", "扫描标识无效")
		return
	}
	page, err := application.library.Diagnostics(request.Context(), scanID)
	switch {
	case errors.Is(err, library.ErrScanNotFound):
		writeAPIError(responseWriter, request, http.StatusNotFound, "not_found", "扫描不存在")
		return
	case errors.Is(err, library.ErrDiagnosticsUnavailable):
		writeAPIError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "无法读取扫描诊断")
		return
	case errors.Is(err, library.ErrQuery):
		writeAPIError(responseWriter, request, 503, "database_unavailable", "服务暂不可用")
		return
	case err != nil:
		writeAPIError(responseWriter, request, http.StatusServiceUnavailable, "database_error", "无法读取扫描诊断")
		return
	}
	writeJSON(responseWriter, 200, map[string]any{"items": page.Items, "summary": page.Summary})
}
