package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
)

const scanAdvisoryLockKey int64 = 0x5343414e

type scanExecution struct {
	connection *sql.Conn
}

func acquireScanExecution(ctx context.Context, database *sql.DB) (*scanExecution, bool, error) {
	if database.Stats().MaxOpenConnections == 1 {
		return nil, false, errors.New("scan coordination requires a connection for control requests")
	}
	connection, err := database.Conn(ctx)
	if err != nil {
		return nil, false, err
	}
	var acquired bool
	if err := connection.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1::bigint)", scanAdvisoryLockKey).Scan(&acquired); err != nil {
		_ = connection.Close()
		return nil, false, err
	}
	if !acquired {
		_ = connection.Close()
		return nil, false, nil
	}
	return &scanExecution{connection: connection}, true, nil
}

func (execution *scanExecution) close(ctx context.Context) error {
	if execution == nil || execution.connection == nil {
		return nil
	}
	var unlocked bool
	err := execution.connection.QueryRowContext(ctx, "SELECT pg_advisory_unlock($1::bigint)", scanAdvisoryLockKey).Scan(&unlocked)
	closeErr := execution.connection.Close()
	if err != nil {
		return err
	}
	if !unlocked {
		return errors.New("scan advisory lock was not held")
	}
	return closeErr
}

type scanDTO struct {
	ID                string     `json:"id"`
	ScanRunID         string     `json:"scan_run_id"`
	Status            string     `json:"status"`
	StartedAt         time.Time  `json:"started_at"`
	FinishedAt        *time.Time `json:"finished_at"`
	CancelRequestedAt *time.Time `json:"cancel_requested_at"`
}

func scanRow(row interface{ Scan(...any) error }) (scanDTO, error) {
	var result scanDTO
	var finishedAt, cancelRequestedAt sql.NullTime
	err := row.Scan(&result.ID, &result.Status, &result.StartedAt, &finishedAt, &cancelRequestedAt)
	result.ScanRunID = result.ID
	if finishedAt.Valid {
		value := finishedAt.Time
		result.FinishedAt = &value
	}
	if cancelRequestedAt.Valid {
		value := cancelRequestedAt.Time
		result.CancelRequestedAt = &value
	}
	return result, err
}

const scanSelectColumns = "id::text,status,started_at,finished_at,cancel_requested_at"

func (application *roomusicApplication) startScan(responseWriter http.ResponseWriter, request *http.Request) {
	if !application.requireSameOrigin(responseWriter, request) {
		return
	}
	if _, ok := application.requireAdmin(responseWriter, request); !ok {
		return
	}
	application.scanMutex.Lock()
	defer application.scanMutex.Unlock()
	if application.runningScan != "" {
		scan, err := application.getScan(request.Context(), application.runningScan)
		if err != nil {
			writeAPIError(responseWriter, request, 503, "database_unavailable", "无法读取扫描状态")
			return
		}
		writeJSON(responseWriter, http.StatusOK, scan)
		return
	}
	execution, acquired, err := acquireScanExecution(request.Context(), application.database.connection)
	if err != nil {
		writeAPIError(responseWriter, request, 503, "scan_coordination_unavailable", "无法协调扫描任务")
		return
	}
	if !acquired {
		scan, queryErr := application.getActiveScan(request.Context())
		if queryErr == nil {
			writeJSON(responseWriter, http.StatusOK, scan)
			return
		}
		writeAPIError(responseWriter, request, 503, "scan_coordination_unavailable", "无法协调扫描任务")
		return
	}
	transaction, err := execution.connection.BeginTx(request.Context(), nil)
	if err != nil {
		_ = execution.close(context.Background())
		writeAPIError(responseWriter, request, 503, "database_unavailable", "无法启动扫描")
		return
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(request.Context(), `UPDATE scan_runs SET status='incomplete',finished_at=NOW(),error_message='process_restarted' WHERE status='running'`); err != nil {
		_ = transaction.Rollback()
		_ = execution.close(context.Background())
		writeAPIError(responseWriter, request, 503, "database_unavailable", "无法启动扫描")
		return
	}
	scanID := createIdentifier()
	if _, err := transaction.ExecContext(request.Context(), "INSERT INTO scan_runs(id,status,started_at) VALUES($1::uuid,'running',NOW())", scanID); err != nil {
		_ = transaction.Rollback()
		_ = execution.close(context.Background())
		writeAPIError(responseWriter, request, 503, "database_unavailable", "无法启动扫描")
		return
	}
	if err := transaction.Commit(); err != nil {
		_ = transaction.Rollback()
		_ = execution.close(context.Background())
		writeAPIError(responseWriter, request, 503, "database_unavailable", "无法启动扫描")
		return
	}
	application.runningScan = scanID
	application.scanWorkers.Add(1)
	go func() {
		defer application.scanWorkers.Done()
		application.runScan(scanID, execution)
	}()
	scan, err := application.getScan(request.Context(), scanID)
	if err != nil {
		writeAPIError(responseWriter, request, 503, "database_unavailable", "无法读取扫描状态")
		return
	}
	writeJSON(responseWriter, http.StatusAccepted, scan)
}

func (application *roomusicApplication) scanStatus(responseWriter http.ResponseWriter, request *http.Request) {
	if _, err := application.authenticatedUser(request); err != nil {
		application.writeAuthenticationError(responseWriter, request)
		return
	}
	scan, err := application.getScan(request.Context(), request.PathValue("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeAPIError(responseWriter, request, http.StatusNotFound, "not_found", "扫描不存在")
		} else {
			writeAPIError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "无法读取扫描状态")
		}
		return
	}
	writeJSON(responseWriter, http.StatusOK, scan)
}

func (application *roomusicApplication) getScan(ctx context.Context, id string) (scanDTO, error) {
	return scanRow(application.database.connection.QueryRowContext(ctx, "SELECT "+scanSelectColumns+" FROM scan_runs WHERE id=$1::uuid", id))
}

func (application *roomusicApplication) getActiveScan(ctx context.Context) (scanDTO, error) {
	return scanRow(application.database.connection.QueryRowContext(ctx, "SELECT "+scanSelectColumns+" FROM scan_runs WHERE status='running' ORDER BY started_at,id LIMIT 1"))
}

func (application *roomusicApplication) activeScan(responseWriter http.ResponseWriter, request *http.Request) {
	if _, err := application.authenticatedUser(request); err != nil {
		application.writeAuthenticationError(responseWriter, request)
		return
	}
	scan, err := application.getActiveScan(request.Context())
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(responseWriter, http.StatusOK, map[string]any{"scan": nil})
		return
	}
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
	transaction, err := application.database.connection.BeginTx(request.Context(), nil)
	if err != nil {
		writeAPIError(responseWriter, request, 503, "database_unavailable", "无法请求取消扫描")
		return
	}
	defer transaction.Rollback()
	id := request.PathValue("id")
	scan, err := scanRow(transaction.QueryRowContext(request.Context(), "SELECT "+scanSelectColumns+" FROM scan_runs WHERE id=$1::uuid FOR UPDATE", id))
	if errors.Is(err, sql.ErrNoRows) {
		writeAPIError(responseWriter, request, 404, "not_found", "扫描不存在")
		return
	}
	if err != nil {
		writeAPIError(responseWriter, request, 503, "database_unavailable", "无法请求取消扫描")
		return
	}
	status := http.StatusOK
	if scan.Status == "running" {
		scan, err = scanRow(transaction.QueryRowContext(request.Context(), "UPDATE scan_runs SET cancel_requested_at=COALESCE(cancel_requested_at,NOW()) WHERE id=$1::uuid RETURNING "+scanSelectColumns, id))
		if err != nil {
			writeAPIError(responseWriter, request, 503, "database_unavailable", "无法请求取消扫描")
			return
		}
		status = http.StatusAccepted
	}
	if err := transaction.Commit(); err != nil {
		writeAPIError(responseWriter, request, 503, "database_unavailable", "无法请求取消扫描")
		return
	}
	application.logger.Info("scan cancellation requested", "event", "library.scan.cancel_requested", "scan_run_id", id, "actor_id", actor.ID)
	writeJSON(responseWriter, status, scan)
}

func (application *roomusicApplication) diagnostics(responseWriter http.ResponseWriter, request *http.Request) {
	if _, ok := application.requireAdmin(responseWriter, request); !ok {
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
