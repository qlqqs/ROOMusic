package ingest

import (
	"context"
	"database/sql"
)

// MarkMissingSourcesWithFeedback 保持全根一个终态事务；核心更新和变更记账各按 256 行处理。
// 所有既有发行锁均先于组锁；报告锁失败会释放该尝试的锁并降级到原有核心路径。
func MarkMissingSourcesWithFeedback(ctx context.Context, tx *sql.Tx, scanID string, rootIDs []string, feedback *ScanFeedback) error {
	if err := feedback.attempt(ctx, tx, func() error {
		if err := createFeedbackTables(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO scan_feedback_tracks(id) SELECT id FROM tracks
   WHERE source_status='present' AND source_root_id=ANY($1::uuid[])
   AND observed_at<(SELECT started_at FROM scan_runs WHERE id=$2::uuid)`, rootIDs, scanID); err != nil {
			return err
		}
		if err := collectFeedbackReleases(ctx, tx, ""); err != nil {
			return err
		}
		if err := lockFeedbackGraph(ctx, tx); err != nil {
			return err
		}
		return snapshotFeedbackGraph(ctx, tx, false)
	}); err != nil {
		return err
	}
	var after string
	for {
		// 键集推进避免每一批从头跳过已经变成 missing 的曲目；结果只返回计数与游标。
		var count int64
		var last string
		err := tx.QueryRowContext(ctx, `WITH batch AS (
   SELECT id FROM tracks WHERE source_status='present' AND source_root_id=ANY($1::uuid[])
   AND observed_at<(SELECT started_at FROM scan_runs WHERE id=$2::uuid)
   AND ($3::uuid IS NULL OR id>$3::uuid) ORDER BY id LIMIT 256
  ), changed AS (
   UPDATE tracks SET source_status='missing' WHERE id IN (SELECT id FROM batch) RETURNING id
  ) SELECT COUNT(*),COALESCE(MAX(id::text),'') FROM changed`, rootIDs, scanID, nullableString(after)).Scan(&count, &last)
		if err != nil {
			return err
		}
		if count == 0 {
			break
		}
		after = last
	}
	return feedback.attempt(ctx, tx, func() error {
		if err := snapshotFeedbackGraph(ctx, tx, true); err != nil {
			return err
		}
		return feedback.recordChanges(ctx, tx)
	})
}
