package maintenance

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/searchindex"
)

func processBatch(ctx context.Context, conn *sql.Conn, state searchindex.State, releaseLimit, trackLimit int) (batchResult, error) {
	started := time.Now()
	result := batchResult{}
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if err = lockBuild(ctx, tx, state); err != nil {
		return result, err
	}
	err = tx.QueryRowContext(ctx, `SELECT phase,subject_kind,total,completed FROM catalog_projection_phases
  WHERE build_id=$1::uuid AND completed<total ORDER BY phase LIMIT 1`, state.BuildID).
		Scan(&result.Phase, &result.Kind, &result.Total, &result.Completed)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	limit := releaseLimit
	if result.Kind == "track" {
		limit = trackLimit
	}
	rows, err := tx.QueryContext(ctx, `SELECT subject_id::text FROM catalog_projection_work
  WHERE build_id=$1::uuid AND phase=$2 AND NOT completed ORDER BY subject_id LIMIT $3`, state.BuildID, result.Phase, limit)
	if err != nil {
		return result, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return result, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	for _, id := range ids {
		written, err := searchindex.Apply(ctx, tx, state, result.Kind, id)
		if err != nil {
			return result, err
		}
		if written {
			result.Written++
		}
	}
	if len(ids) == 0 {
		return result, errors.New("catalog projection phase has inconsistent progress")
	}
	_, err = tx.ExecContext(ctx, `UPDATE catalog_projection_work w SET completed=TRUE,completed_input_revision=s.input_revision
  FROM catalog_projection_subject_state s WHERE w.build_id=$1::uuid AND w.phase=$2 AND w.subject_id=ANY($3::uuid[])
  AND NOT w.completed AND s.subject_kind=w.subject_kind AND s.subject_id=w.subject_id`, state.BuildID, result.Phase, ids)
	if err != nil {
		return result, err
	}
	result.BatchSize = len(ids)
	result.Completed += int64(len(ids))
	_, err = tx.ExecContext(ctx, `UPDATE catalog_projection_phases SET completed=$3 WHERE build_id=$1::uuid AND phase=$2`, state.BuildID, result.Phase, result.Completed)
	if err != nil {
		return result, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE catalog_projection_state SET status='building',updated_at=NOW() WHERE capability='search'`)
	if err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return batchResult{}, err
	}
	result.Duration = time.Since(started)
	return result, nil
}
