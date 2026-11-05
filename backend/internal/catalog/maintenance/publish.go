package maintenance

import (
	"context"
	"database/sql"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/searchindex"
)

// gapPredicate 仅由内部固定 subject 类型构造；扫描包含 missing 和空字段对象。
func gapPredicate(kind string) string {
	return `NOT EXISTS (SELECT 1 FROM catalog_projection_subject_state s
  WHERE s.subject_kind='` + kind + `' AND s.subject_id=f.id AND s.search_applied_generation=$1
  AND s.search_applied_input_revision=s.input_revision)`
}

func publishOrRepair(ctx context.Context, conn *sql.Conn, state searchindex.State) (bool, error) {
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err = lockBuild(ctx, tx, state); err != nil {
		return false, err
	}
	var pending bool
	if err = tx.QueryRowContext(ctx, `SELECT NOT manifest_sealed OR EXISTS (
  SELECT 1 FROM catalog_projection_work WHERE build_id=$1::uuid AND NOT completed)
  FROM catalog_projection_state WHERE capability='search'`, state.BuildID).Scan(&pending); err != nil {
		return false, err
	}
	if pending {
		return false, nil
	}
	for _, kind := range []string{"release", "track"} {
		var gap bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+kind+`s f WHERE `+gapPredicate(kind)+`)`, state.TargetGeneration).Scan(&gap); err != nil {
			return false, err
		}
		if gap {
			// 缺口清单可能很大，先释放 gate，再在后台快照里登记修复阶段。
			if err = tx.Rollback(); err != nil {
				return false, err
			}
			return false, discoverRepair(ctx, conn, state, kind)
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE catalog_projection_state SET published_generation=target_generation,
  published_textnorm_version=textnorm_version,published_search_projection_version=search_projection_version,
  status='ready',catalog_epoch=catalog_epoch+1,updated_at=NOW() WHERE capability='search'`)
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func discoverRepair(ctx context.Context, conn *sql.Conn, state searchindex.State, kind string) error {
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := searchindex.LoadState(ctx, tx)
	if err != nil {
		return err
	}
	if current.BuildID != state.BuildID || current.TargetGeneration != state.TargetGeneration {
		return errBuildChanged
	}
	var suffix string
	if err = tx.QueryRowContext(ctx, `SELECT gen_random_uuid()::text`).Scan(&suffix); err != nil {
		return err
	}
	phase := "repair_" + kind + "s_" + suffix
	_, err = tx.ExecContext(ctx, `WITH inserted AS (
  INSERT INTO catalog_projection_work(build_id,phase,subject_kind,subject_id)
  SELECT $2::uuid,$3,$4,f.id FROM `+kind+`s f WHERE `+gapPredicate(kind)+` RETURNING subject_id)
  INSERT INTO catalog_projection_phases(build_id,phase,subject_kind,total)
  SELECT $2::uuid,$3,$4,count(*) FROM inserted`, state.TargetGeneration, state.BuildID, phase, kind)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE catalog_projection_state SET status='building',updated_at=NOW()
  WHERE capability='search' AND build_id=$1::uuid AND target_generation=$2`, state.BuildID, state.TargetGeneration)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errBuildChanged
	}
	return tx.Commit()
}
