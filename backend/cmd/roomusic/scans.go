package main

import (
	"database/sql"
	"net/http"
	"strings"
	"time"
)

func (application *roomusicApplication) startScan(responseWriter http.ResponseWriter, request *http.Request) {
	if !application.requireSameOrigin(responseWriter, request) {
		return
	}
	if _, err := application.authenticatedUser(request); err != nil {
		application.writeAuthenticationError(responseWriter, request)
		return
	}
	application.scanMutex.Lock()
	defer application.scanMutex.Unlock()
	if application.runningScan != "" {
		writeJSON(responseWriter, http.StatusOK, map[string]string{"id": application.runningScan, "scan_run_id": application.runningScan, "status": "running"})
		return
	}
	scanID := createIdentifier()
	if _, err := application.database.connection.ExecContext(request.Context(), "INSERT INTO scan_runs(id,status,started_at) VALUES($1::uuid,'running',NOW())", scanID); err != nil {
		writeAPIError(responseWriter, request, 503, "database_unavailable", "无法启动扫描")
		return
	}
	application.runningScan = scanID
	go application.runScan(scanID)
	writeJSON(responseWriter, http.StatusAccepted, map[string]string{"id": scanID, "scan_run_id": scanID, "status": "running"})
}

func (application *roomusicApplication) scanStatus(responseWriter http.ResponseWriter, request *http.Request) {
	if _, err := application.authenticatedUser(request); err != nil {
		application.writeAuthenticationError(responseWriter, request)
		return
	}
	var status string
	var started time.Time
	var finished sql.NullTime
	err := application.database.connection.QueryRowContext(request.Context(), "SELECT status,started_at,finished_at FROM scan_runs WHERE id=$1::uuid", request.PathValue("id")).Scan(&status, &started, &finished)
	if err != nil {
		writeAPIError(responseWriter, request, http.StatusNotFound, "not_found", "扫描不存在")
		return
	}
	var finishedAt any
	if finished.Valid {
		finishedAt = finished.Time
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"id": request.PathValue("id"), "status": status, "started_at": started, "finished_at": finishedAt})
}

func (application *roomusicApplication) diagnostics(responseWriter http.ResponseWriter, request *http.Request) {
	if _, err := application.authenticatedUser(request); err != nil {
		application.writeAuthenticationError(responseWriter, request)
		return
	}
	rows, err := application.database.connection.QueryContext(request.Context(), "SELECT kind,COALESCE(relative_path,''),message FROM scan_diagnostics WHERE scan_run_id=$1::uuid ORDER BY id LIMIT 100", request.PathValue("id"))
	if err != nil {
		writeAPIError(responseWriter, request, 503, "database_unavailable", "服务暂不可用")
		return
	}
	defer rows.Close()
	items := []map[string]string{}
	for rows.Next() {
		var kind, relativePath, message string
		_ = rows.Scan(&kind, &relativePath, &message)
		items = append(items, map[string]string{"kind": kind, "path": strings.TrimSpace(relativePath), "message": message})
	}
	writeJSON(responseWriter, 200, map[string]any{"items": items})
}
