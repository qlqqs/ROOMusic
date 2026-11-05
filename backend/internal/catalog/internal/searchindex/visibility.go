package searchindex

import (
	"context"
	"database/sql"

	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

// RefreshTrackVisibility 对纯可见性变化沿用同代文本投影，只推进输入 revision。
// 尚未回填的对象仍在本事务补齐；不把一条旧完成标记无条件升级到新代。
func RefreshTrackVisibility(ctx context.Context, tx *sql.Tx, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if err := textnorm.LoadEmbedded(); err != nil {
		return err
	}
	state, err := LoadState(ctx, tx)
	if err != nil {
		return err
	}
	if !state.CurrentVersion() {
		return ErrVersionMismatch
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO catalog_projection_subject_state(subject_kind,subject_id,input_revision)
  SELECT 'track',id,1 FROM unnest($1::uuid[]) id
  ON CONFLICT(subject_kind,subject_id) DO UPDATE SET input_revision=catalog_projection_subject_state.input_revision+1,
   search_applied_input_revision=CASE WHEN catalog_projection_subject_state.search_applied_generation=$2
    AND catalog_projection_subject_state.search_applied_input_revision=catalog_projection_subject_state.input_revision
    THEN catalog_projection_subject_state.input_revision+1 ELSE catalog_projection_subject_state.search_applied_input_revision END`, ids, state.TargetGeneration)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT subject_id::text FROM catalog_projection_subject_state
  WHERE subject_kind='track' AND subject_id=ANY($1::uuid[]) AND
   (search_applied_generation<>$2 OR search_applied_input_revision<>input_revision)`, ids, state.TargetGeneration)
	if err != nil {
		return err
	}
	var pending []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range pending {
		if _, err = Apply(ctx, tx, state, "track", id); err != nil {
			return err
		}
	}
	return nil
}
