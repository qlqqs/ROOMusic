package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

var (
	// ErrScanNotFound 表示指定的扫描不存在。
	ErrScanNotFound = errors.New("scan not found")
	// ErrCoordinationUnavailable 表示无法取得或确认扫描互斥，因此没有创建扫描。
	ErrCoordinationUnavailable = errors.New("scan coordination unavailable")
	// ErrStartFailed 表示已取得扫描互斥，但创建扫描记录失败。
	ErrStartFailed = errors.New("scan start failed")
	// ErrDiagnosticsUnavailable 表示无法读取扫描的诊断汇总。
	ErrDiagnosticsUnavailable = errors.New("scan diagnostics unavailable")
	// ErrQuery 表示诊断明细查询本身没能执行；不带它的错误表示读取行失败。
	ErrQuery = errors.New("library query failed")
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

type Scan struct {
	ID                string             `json:"id"`
	ScanRunID         string             `json:"scan_run_id"`
	Status            string             `json:"status"`
	StartedAt         time.Time          `json:"started_at"`
	FinishedAt        *time.Time         `json:"finished_at"`
	CancelRequestedAt *time.Time         `json:"cancel_requested_at"`
	Diagnostics       DiagnosticsSummary `json:"diagnostics"`
}

type DiagnosticCount struct {
	Kind  string `json:"kind"`
	Count int64  `json:"count"`
}

type DiagnosticsSummary struct {
	Total  int64             `json:"total"`
	Counts []DiagnosticCount `json:"counts"`
}

type Diagnostic struct {
	ID      string  `json:"id"`
	Kind    string  `json:"kind"`
	Path    *string `json:"path"`
	Message string  `json:"message"`
}

func scanRow(row interface{ Scan(...any) error }) (Scan, error) {
	result := Scan{Diagnostics: DiagnosticsSummary{Counts: []DiagnosticCount{}}}
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

// GetScan 读取一次扫描及其诊断汇总；不存在时返回 ErrScanNotFound。
func (service *Service) GetScan(ctx context.Context, id string) (Scan, error) {
	result, err := scanRow(service.database.QueryRowContext(ctx, "SELECT "+scanSelectColumns+" FROM scan_runs WHERE id=$1::uuid", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Scan{}, ErrScanNotFound
	}
	if err != nil {
		return Scan{}, err
	}
	result.Diagnostics, err = loadScanDiagnosticsSummary(ctx, service.database, id)
	return result, err
}

// activeScan 读取最早开始的运行中扫描；没有时返回 sql.ErrNoRows。
func (service *Service) activeScan(ctx context.Context) (Scan, error) {
	result, err := scanRow(service.database.QueryRowContext(ctx, "SELECT "+scanSelectColumns+" FROM scan_runs WHERE status='running' ORDER BY started_at,id LIMIT 1"))
	if err != nil {
		return Scan{}, err
	}
	result.Diagnostics, err = loadScanDiagnosticsSummary(ctx, service.database, result.ID)
	return result, err
}

type scanDiagnosticsQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadScanDiagnosticsSummary(ctx context.Context, queryer scanDiagnosticsQuerier, scanID string) (DiagnosticsSummary, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT kind,COUNT(*) FROM scan_diagnostics WHERE scan_run_id=$1::uuid GROUP BY kind ORDER BY kind LIMIT 101`, scanID)
	if err != nil {
		return DiagnosticsSummary{}, err
	}
	defer rows.Close()
	result := DiagnosticsSummary{Counts: []DiagnosticCount{}}
	for rows.Next() {
		if len(result.Counts) == 100 {
			return DiagnosticsSummary{}, errors.New("scan diagnostic kinds exceed bound")
		}
		var item DiagnosticCount
		if err := rows.Scan(&item.Kind, &item.Count); err != nil {
			return DiagnosticsSummary{}, err
		}
		item.Kind = safeDiagnosticKind(item.Kind)
		result.Total += item.Count
		result.Counts = append(result.Counts, item)
	}
	if err := rows.Err(); err != nil {
		return DiagnosticsSummary{}, err
	}
	return result, nil
}

func safeDiagnosticKind(kind string) string {
	kind = strings.TrimSpace(kind)
	if kind == "" || len(kind) > 64 {
		return "unknown"
	}
	for _, character := range kind {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return "unknown"
		}
	}
	return kind
}

func safeDiagnosticMessage(kind, _ string) string {
	switch kind {
	case "root_unavailable":
		return "注册目录当前不可用"
	case "walk_error":
		return "目录中的部分内容无法读取"
	case "broken_or_escaped_link":
		return "文件链接目标不可安全读取"
	case "unsupported_cue":
		return "CUE 文件无法安全处理"
	case "parse_failure":
		return "音频文件解析失败"
	case "unsupported_format":
		return "音频格式暂不支持"
	case "staging_write_failure", "staging_read_failure":
		return "扫描观察暂存失败"
	case "non_regular_file":
		return "音频来源不是普通文件"
	case "catalog_write_failure":
		return "发行目录保存失败"
	case "artwork_failure":
		return "封面处理失败"
	case "rip_log_unreadable":
		return "抓轨日志无法读取"
	case "database_error":
		return "扫描数据库操作失败"
	default:
		return "扫描发现需要检查的问题"
	}
}

// StartResult 是 StartScan 的结果：Started 为 true 表示新建了扫描；false 表示已有扫描在运行。
type StartResult struct {
	Scan    Scan
	Started bool
}

// StartScan 启动一次扫描；已有扫描在运行（本进程或其他进程）时返回该扫描而不新建。
// 整个调用持有 scanMutex，所以同一进程内的并发启动请求只会创建一个扫描。
// 错误带 ErrCoordinationUnavailable（无法协调）或 ErrStartFailed（创建记录失败）；其余为读取扫描状态失败。
func (service *Service) StartScan(ctx context.Context) (StartResult, error) {
	service.scanMutex.Lock()
	defer service.scanMutex.Unlock()
	if service.runningScan != "" {
		scan, err := service.GetScan(ctx, service.runningScan)
		if err != nil {
			return StartResult{}, err
		}
		return StartResult{Scan: scan}, nil
	}
	execution, acquired, err := acquireScanExecution(ctx, service.database)
	if err != nil {
		return StartResult{}, fmt.Errorf("%w: %v", ErrCoordinationUnavailable, err)
	}
	if !acquired {
		scan, queryErr := service.activeScan(ctx)
		if queryErr == nil {
			return StartResult{Scan: scan}, nil
		}
		return StartResult{}, fmt.Errorf("%w: %v", ErrCoordinationUnavailable, queryErr)
	}
	transaction, err := execution.connection.BeginTx(ctx, nil)
	if err != nil {
		_ = execution.close(context.Background())
		return StartResult{}, fmt.Errorf("%w: %v", ErrStartFailed, err)
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, `UPDATE scan_runs SET status='incomplete',finished_at=NOW(),error_message='process_restarted' WHERE status='running'`); err != nil {
		_ = transaction.Rollback()
		_ = execution.close(context.Background())
		return StartResult{}, fmt.Errorf("%w: %v", ErrStartFailed, err)
	}
	scanID := uuid.New()
	if _, err := transaction.ExecContext(ctx, "INSERT INTO scan_runs(id,status,started_at) VALUES($1::uuid,'running',NOW())", scanID); err != nil {
		_ = transaction.Rollback()
		_ = execution.close(context.Background())
		return StartResult{}, fmt.Errorf("%w: %v", ErrStartFailed, err)
	}
	if err := transaction.Commit(); err != nil {
		_ = transaction.Rollback()
		_ = execution.close(context.Background())
		return StartResult{}, fmt.Errorf("%w: %v", ErrStartFailed, err)
	}
	service.runningScan = scanID
	service.scanWorkers.Add(1)
	go func() {
		defer service.scanWorkers.Done()
		service.runScan(scanID, execution)
	}()
	scan, err := service.GetScan(ctx, scanID)
	if err != nil {
		return StartResult{}, err
	}
	return StartResult{Scan: scan, Started: true}, nil
}

// ActiveScan 返回最早开始的运行中扫描；没有运行中的扫描时返回 nil。
func (service *Service) ActiveScan(ctx context.Context) (*Scan, error) {
	scan, err := service.activeScan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &scan, nil
}

// CancelResult 是 CancelScan 的结果：Requested 为 true 表示扫描仍在运行，取消请求已持久化（含重复请求）；
// false 表示扫描已处于终态，无需取消。
type CancelResult struct {
	Scan      Scan
	Requested bool
}

// CancelScan 持久化对运行中扫描的取消请求，重复请求不改变首次的取消时间。请求不等待扫描 worker 结束。
// 扫描不存在时返回 ErrScanNotFound；读取诊断汇总失败时错误带 ErrDiagnosticsUnavailable。
func (service *Service) CancelScan(ctx context.Context, id, actorID string) (CancelResult, error) {
	transaction, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return CancelResult{}, err
	}
	defer transaction.Rollback()
	scan, err := scanRow(transaction.QueryRowContext(ctx, "SELECT "+scanSelectColumns+" FROM scan_runs WHERE id=$1::uuid FOR UPDATE", id))
	if errors.Is(err, sql.ErrNoRows) {
		return CancelResult{}, ErrScanNotFound
	}
	if err != nil {
		return CancelResult{}, err
	}
	requested := false
	if scan.Status == "running" {
		scan, err = scanRow(transaction.QueryRowContext(ctx, "UPDATE scan_runs SET cancel_requested_at=COALESCE(cancel_requested_at,NOW()) WHERE id=$1::uuid RETURNING "+scanSelectColumns, id))
		if err != nil {
			return CancelResult{}, err
		}
		requested = true
	}
	scan.Diagnostics, err = loadScanDiagnosticsSummary(ctx, transaction, id)
	if err != nil {
		return CancelResult{}, fmt.Errorf("%w: %v", ErrDiagnosticsUnavailable, err)
	}
	if err := transaction.Commit(); err != nil {
		return CancelResult{}, err
	}
	service.logger.Info("scan cancellation requested", "event", "library.scan.cancel_requested", "scan_run_id", id, "actor_id", actorID)
	return CancelResult{Scan: scan, Requested: requested}, nil
}

// DiagnosticsPage 是扫描的诊断明细（最多 100 条）与汇总。
type DiagnosticsPage struct {
	Items   []Diagnostic
	Summary DiagnosticsSummary
}

// Diagnostics 读取扫描的有界诊断。路径只有通过安全相对引用校验才会返回，消息由诊断类别决定，
// 不回显存储的原文。扫描不存在时返回 ErrScanNotFound；读取扫描失败时错误带 ErrDiagnosticsUnavailable；
// 明细查询无法执行时错误带 ErrQuery，读取行失败时为原始错误。
func (service *Service) Diagnostics(ctx context.Context, scanID string) (DiagnosticsPage, error) {
	scan, err := service.GetScan(ctx, scanID)
	if errors.Is(err, ErrScanNotFound) {
		return DiagnosticsPage{}, ErrScanNotFound
	}
	if err != nil {
		return DiagnosticsPage{}, fmt.Errorf("%w: %v", ErrDiagnosticsUnavailable, err)
	}
	rows, err := service.database.QueryContext(ctx, "SELECT id::text,kind,COALESCE(relative_path,''),message FROM scan_diagnostics WHERE scan_run_id=$1::uuid ORDER BY id LIMIT 100", scanID)
	if err != nil {
		return DiagnosticsPage{}, fmt.Errorf("%w: %v", ErrQuery, err)
	}
	defer rows.Close()
	items := []Diagnostic{}
	for rows.Next() {
		var kind, relativePath, message string
		var id string
		if err := rows.Scan(&id, &kind, &relativePath, &message); err != nil {
			return DiagnosticsPage{}, err
		}
		item := Diagnostic{ID: id, Kind: safeDiagnosticKind(kind), Message: safeDiagnosticMessage(kind, message)}
		if safePath, safe := catalog.SafeRelativeSourceRef(relativePath); safe {
			item.Path = &safePath
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return DiagnosticsPage{}, err
	}
	return DiagnosticsPage{Items: items, Summary: scan.Diagnostics}, nil
}

// RecoverInterruptedScans 把上次进程遗留的运行中扫描标为 incomplete。仍有存活的扫描持有者时什么也不做。
func RecoverInterruptedScans(ctx context.Context, database *sql.DB) (err error) {
	holder, acquired, err := acquireScanExecution(ctx, database)
	if err != nil {
		return fmt.Errorf("acquire scan recovery lock: %w", err)
	}
	if !acquired {
		return nil
	}
	defer func() {
		if closeErr := holder.close(context.Background()); closeErr != nil && err == nil {
			err = fmt.Errorf("release scan recovery lock: %w", closeErr)
		}
	}()
	_, err = holder.connection.ExecContext(ctx, `UPDATE scan_runs
		SET status='incomplete', finished_at=NOW(), error_message='process_restarted'
		WHERE status='running'`)
	if err != nil {
		return fmt.Errorf("recover interrupted scans: %w", err)
	}
	return nil
}
