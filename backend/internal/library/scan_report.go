package library

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
)

var (
	ErrReportLegacy      = errors.New("scan report not available")
	ErrReportPending     = errors.New("scan report not ready")
	ErrReportUnavailable = errors.New("scan report unavailable")
	ErrReportReadFailed  = errors.New("scan report read failed")
)

type ScanReportCounts struct {
	Added   int64 `json:"added"`
	Updated int64 `json:"updated"`
	Missing int64 `json:"missing"`
}
type ScanReportFiles struct {
	Counted       int64  `json:"counted"`
	Total         *int64 `json:"total"`
	Processed     int64  `json:"processed"`
	CountComplete bool   `json:"count_complete"`
	Changed       bool   `json:"changed"`
}
type ScanReportIssues struct {
	Recorded       int64 `json:"recorded"`
	MayBeTruncated bool  `json:"may_be_truncated"`
}
type ScanAddedAlbum struct {
	catalog.ScanAddedAlbum
	ClaimHints []governance.ClaimHint `json:"claim_hints"`
}
type ScanReportPagination struct {
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
	Total    int64 `json:"total"`
}
type ScanReportAddedAlbums struct {
	Items      []ScanAddedAlbum     `json:"items"`
	Pagination ScanReportPagination `json:"pagination"`
}

// ScanReport 的计数已冻结，新增列表中的当前摘要与认领提示在同一读取快照中补齐。
type ScanReport struct {
	ScanRunID         string                `json:"scan_run_id"`
	SchemaVersion     int                   `json:"schema_version"`
	Status            string                `json:"status"`
	ScanStatus        string                `json:"scan_status"`
	FinalizedAt       time.Time             `json:"finalized_at"`
	MissingReconciled bool                  `json:"missing_reconciled"`
	Files             ScanReportFiles       `json:"files"`
	Albums            ScanReportCounts      `json:"albums"`
	Tracks            ScanReportCounts      `json:"tracks"`
	Issues            ScanReportIssues      `json:"issues"`
	AddedAlbums       ScanReportAddedAlbums `json:"added_albums"`
}

func (service *Service) GetScanReport(ctx context.Context, scanID string, page, pageSize int) (ScanReport, error) {
	tx, err := service.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return ScanReport{}, errors.Join(ErrReportReadFailed, err)
	}
	defer tx.Rollback()
	scan, err := scanRow(tx.QueryRowContext(ctx, "SELECT "+scanSelectColumns+" FROM scan_runs WHERE id=$1::uuid", scanID))
	if errors.Is(err, sql.ErrNoRows) {
		return ScanReport{}, ErrScanNotFound
	}
	if err != nil {
		return ScanReport{}, errors.Join(ErrReportReadFailed, err)
	}
	switch scan.ReportStatus {
	case "legacy":
		return ScanReport{}, ErrReportLegacy
	case "pending":
		return ScanReport{}, ErrReportPending
	case "ready", "partial":
	default:
		return ScanReport{}, ErrReportUnavailable
	}
	result := ScanReport{ScanRunID: scanID, Status: scan.ReportStatus, ScanStatus: scan.Status}
	var total sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT schema_version,finalized_at,missing_reconciled,files_counted,files_total,files_processed,count_complete,files_changed,
 albums_added,albums_updated,albums_missing,tracks_added,tracks_updated,tracks_missing,diagnostics_recorded,diagnostics_may_be_truncated
 FROM scan_reports WHERE scan_run_id=$1::uuid`, scanID).Scan(&result.SchemaVersion, &result.FinalizedAt, &result.MissingReconciled,
		&result.Files.Counted, &total, &result.Files.Processed, &result.Files.CountComplete, &result.Files.Changed,
		&result.Albums.Added, &result.Albums.Updated, &result.Albums.Missing, &result.Tracks.Added, &result.Tracks.Updated, &result.Tracks.Missing, &result.Issues.Recorded, &result.Issues.MayBeTruncated)
	if errors.Is(err, sql.ErrNoRows) {
		return ScanReport{}, ErrReportUnavailable
	}
	if err != nil {
		return ScanReport{}, errors.Join(ErrReportReadFailed, err)
	}
	if total.Valid {
		result.Files.Total = &total.Int64
	}
	albums, err := catalog.NewStore(tx).ListScanAddedAlbums(ctx, scanID, page, pageSize)
	if err != nil {
		return ScanReport{}, errors.Join(ErrReportReadFailed, err)
	}
	ids := make([]string, 0, len(albums.Items))
	for _, album := range albums.Items {
		ids = append(ids, album.ReleaseID)
	}
	hints, err := governance.NewStore(tx).LoadClaimHintsBatch(ctx, ids)
	if err != nil {
		return ScanReport{}, errors.Join(ErrReportReadFailed, err)
	}
	result.AddedAlbums = ScanReportAddedAlbums{Items: make([]ScanAddedAlbum, 0, len(albums.Items)), Pagination: ScanReportPagination{Page: page, PageSize: pageSize, Total: albums.Total}}
	for _, album := range albums.Items {
		result.AddedAlbums.Items = append(result.AddedAlbums.Items, ScanAddedAlbum{ScanAddedAlbum: album, ClaimHints: hints[album.ReleaseID]})
	}
	if err = tx.Commit(); err != nil {
		return ScanReport{}, errors.Join(ErrReportReadFailed, err)
	}
	return result, nil
}

// reportAttempt 的保存点只包含反馈 SQL；核心变化绝不包在可降级回滚的范围里。
func reportAttempt(ctx context.Context, tx *sql.Tx, work func() error) error {
	if _, err := tx.ExecContext(ctx, "SAVEPOINT scan_report_attempt"); err != nil {
		return err
	}
	var oldTimeout string
	err := tx.QueryRowContext(ctx, "SHOW statement_timeout").Scan(&oldTimeout)
	if err == nil {
		_, err = tx.ExecContext(ctx, "SET LOCAL statement_timeout='500ms'")
	}
	if err == nil {
		err = work()
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, "SELECT set_config('statement_timeout',$1,true)", oldTimeout)
	}
	if err != nil {
		if _, rollbackErr := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT scan_report_attempt"); rollbackErr != nil {
			return errors.Join(err, rollbackErr)
		}
	}
	_, releaseErr := tx.ExecContext(ctx, "RELEASE SAVEPOINT scan_report_attempt")
	return errors.Join(err, releaseErr)
}

func finalizeScanFeedback(ctx context.Context, tx *sql.Tx, scanID, status string, errorMessage any, f *scanFeedback) error {
	var skipped any
	if f != nil {
		skipped = f.skipped
	}
	core := func() error {
		_, err := tx.ExecContext(ctx, "UPDATE scan_runs SET status=$1,finished_at=NOW(),error_message=$2,skipped_files=COALESCE($4::bigint,skipped_files) WHERE id=$3::uuid AND status='running'", status, errorMessage, scanID, skipped)
		return err
	}
	if f == nil {
		return core()
	}
	state := "unavailable"
	if !f.changes.Broken() && !f.invalid {
		err := reportAttempt(ctx, tx, func() error {
			summary, err := ingest.SummarizeScanChanges(ctx, tx, scanID)
			if err != nil {
				return err
			}
			var diagnostics int64
			if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM scan_diagnostics WHERE scan_run_id=$1::uuid", scanID).Scan(&diagnostics); err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO scan_reports(scan_run_id,schema_version,finalized_at,files_counted,files_total,files_processed,count_complete,files_changed,
 albums_added,albums_updated,albums_missing,tracks_added,tracks_updated,tracks_missing,diagnostics_recorded,diagnostics_may_be_truncated,missing_reconciled)
 VALUES($1::uuid,1,NOW(),$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, scanID, f.progress.FilesCounted, f.progress.FilesTotal, f.progress.FilesProcessed,
				f.progress.CountComplete, f.progress.FilesChanged, summary.Albums.Added, summary.Albums.Updated, summary.Albums.Missing, summary.Tracks.Added, summary.Tracks.Updated, summary.Tracks.Missing,
				diagnostics, diagnostics >= 100, status == "succeeded")
			return err
		})
		if err == nil {
			state = "partial"
			if status == "succeeded" {
				state = "ready"
			}
		} else {
			f.changes.Break()
		}
	}
	err := reportAttempt(ctx, tx, func() error {
		_, err := tx.ExecContext(ctx, `UPDATE scan_runs SET status=$1,finished_at=NOW(),error_message=$2,skipped_files=$5,report_state=$4,
 progress_bp=CASE WHEN $1='succeeded' THEN 10000 ELSE progress_bp END,
 phase=CASE WHEN $1='succeeded' THEN 'saving' ELSE phase END,
 progress_updated_at=CASE WHEN $1='succeeded' THEN NOW() ELSE progress_updated_at END,
 current_root_index=CASE WHEN $1='succeeded' THEN NULL ELSE current_root_index END,
 current_root_id=CASE WHEN $1='succeeded' THEN NULL ELSE current_root_id END,
 current_root_name=CASE WHEN $1='succeeded' THEN NULL ELSE current_root_name END
 WHERE id=$3::uuid AND status='running'`, status, errorMessage, scanID, state, f.skipped)
		return err
	})
	if err != nil {
		return core()
	}
	return nil
}
