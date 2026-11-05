package maintenance

import (
	"context"
	"database/sql"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/searchindex"
)

// discover 只在后台的同一快照中登记两个清单和固定分母；不占用 CATP。
// 新建对象由实时写者负责；发布前另做完整覆盖检查，所以 UUID 游标没有遗漏窗口。
func discover(ctx context.Context, conn *sql.Conn, state searchindex.State) error {
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
	if current.ManifestSealed {
		return nil
	}
	for _, kind := range []string{"release", "track"} {
		table, phase := kind+"s", kind+"s"
		_, err = tx.ExecContext(ctx, `WITH inserted AS (
   INSERT INTO catalog_projection_work(build_id,phase,subject_kind,subject_id)
   SELECT $1::uuid,$2,$3,id FROM `+table+` RETURNING subject_id)
   INSERT INTO catalog_projection_phases(build_id,phase,subject_kind,total)
   SELECT $1::uuid,$2,$3,count(*) FROM inserted`, state.BuildID, phase, kind)
		if err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE catalog_projection_state SET manifest_sealed=TRUE,status='building',updated_at=NOW()
  WHERE capability='search' AND build_id=$1::uuid AND target_generation=$2`, state.BuildID, state.TargetGeneration)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errBuildChanged
	}
	return tx.Commit()
}

type batchResult struct {
	Phase, Kind        string
	Total, Completed   int64
	BatchSize, Written int
	Duration           time.Duration
}
