package ingest

import (
	"context"
	"database/sql"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/searchindex"
)

// MarkMissingSourcesWithFeedback 保持全根一个终态事务；核心更新和变更记账各按 256 行处理。
// 所有既有发行锁均先于组锁；报告锁失败会释放该尝试的锁并降级到原有核心路径。
func MarkMissingSourcesWithFeedback(ctx context.Context, tx *sql.Tx, scanID string, rootIDs []string, feedback *ScanFeedback) error {
	if err := LockProjectionWrites(ctx, tx); err != nil {
		return err
	}
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
		// 键集推进避免从头跳过已处理曲目；每批至多保留 256 个 ID 以刷新搜索可见性。
		rows, err := tx.QueryContext(ctx, `WITH batch AS (
   SELECT id FROM tracks WHERE source_status='present' AND source_root_id=ANY($1::uuid[])
   AND observed_at<(SELECT started_at FROM scan_runs WHERE id=$2::uuid)
   AND ($3::uuid IS NULL OR id>$3::uuid) ORDER BY id LIMIT 256
  ), changed AS (
   UPDATE tracks SET source_status='missing' WHERE id IN (SELECT id FROM batch) RETURNING id
  ) SELECT id::text FROM changed ORDER BY id`, rootIDs, scanID, nullableString(after))
		if err != nil {
			return err
		}
		ids := make([]string, 0, 256)
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			break
		}
		// 搜索属于核心目录事务，不放入可降级的反馈保存点。
		if err := searchindex.RefreshTrackVisibility(ctx, tx, ids); err != nil {
			return err
		}
		after = ids[len(ids)-1]
	}
	return feedback.attempt(ctx, tx, func() error {
		if err := snapshotFeedbackGraph(ctx, tx, true); err != nil {
			return err
		}
		return feedback.recordChanges(ctx, tx)
	})
}
