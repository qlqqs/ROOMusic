// Package testsupport 提供跨 catalog 能力测试共用的投影状态夹具。
package testsupport

import (
	"context"
	"database/sql"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/artistgraph"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/searchindex"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

type execContexter interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// SetArtistProjectionVersionCurrent 让低层测试夹具使用当前代码的艺人投影版本。
// 生产启动流程负责版本登记；测试直接调用存储/扫描器时不会经过该流程。
// 艺人刷新可能连带刷新曲目搜索，因此同时对齐搜索投影版本。
func SetArtistProjectionVersionCurrent(ctx context.Context, db execContexter) error {
	if _, err := db.ExecContext(ctx, `UPDATE catalog_projection_state SET
 textnorm_version=$1,search_projection_version=$2,
 published_textnorm_version=$1,published_search_projection_version=$2
 WHERE capability='artists'`, textnorm.Version, artistgraph.Version); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `UPDATE catalog_projection_state SET
 textnorm_version=$1,search_projection_version=$2,
 published_textnorm_version=$1,published_search_projection_version=$2
 WHERE capability='search'`, textnorm.Version, searchindex.ProjectionVersion)
	return err
}
