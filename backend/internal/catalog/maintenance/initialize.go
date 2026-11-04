// Package maintenance 提供目录投影的短版本登记与可取消后台回填。
package maintenance

import (
	"context"
	"database/sql"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/searchindex"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/writegate"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

// Initialize 在正常写者启动前登记目标；只接触一行状态，不发现对象、不回填。
// 内置表坏掉时记 failed 并保留旧搜索，不能因此阻断普通 ready。
func Initialize(ctx context.Context, db *sql.DB) error {
	preflightErr := textnorm.LoadEmbedded()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = writegate.Lock(ctx, tx); err != nil {
		return err
	}
	state, err := searchindex.LoadState(ctx, tx)
	if err != nil {
		return err
	}
	if !state.CurrentVersion() {
		_, err = tx.ExecContext(ctx, `UPDATE catalog_projection_state SET target_generation=target_generation+1,
   textnorm_version=$1,search_projection_version=$2,build_id=gen_random_uuid(),status='preparing',
   catalog_epoch=catalog_epoch+1,manifest_sealed=FALSE,live_updated=0,started_at=NOW(),updated_at=NOW() WHERE capability='search'`,
			textnorm.Version, searchindex.ProjectionVersion)
		if err != nil {
			return err
		}
	}
	if preflightErr != nil {
		_, err = tx.ExecContext(ctx, `UPDATE catalog_projection_state SET status='failed',updated_at=NOW() WHERE capability='search'`)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
