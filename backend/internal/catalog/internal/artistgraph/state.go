// Package artistgraph 管理艺人扫描事实与派生关系；人工意图只读，事务由调用方持有。
package artistgraph

import (
	"context"
	"database/sql"
	"errors"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

const Version = 1

var ErrVersion = errors.New("artist projection version mismatch")

type Queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
type RowsQueryer interface {
	Queryer
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func Ready(ctx context.Context, q Queryer) (bool, error) {
	var ready bool
	err := q.QueryRowContext(ctx, `SELECT status='ready' AND manifest_sealed AND target_generation=published_generation
 AND textnorm_version=$1 AND published_textnorm_version=$1 AND search_projection_version=$2
 AND published_search_projection_version=$2 FROM catalog_projection_state WHERE capability='artists'`, textnorm.Version, Version).Scan(&ready)
	return ready, err
}
func generation(ctx context.Context, q Queryer) (int64, error) {
	var gen, tn, pv int64
	err := q.QueryRowContext(ctx, `SELECT target_generation,textnorm_version,search_projection_version FROM catalog_projection_state WHERE capability='artists'`).Scan(&gen, &tn, &pv)
	if err == nil && (tn != textnorm.Version || pv != Version) {
		err = ErrVersion
	}
	return gen, err
}

// Mappings 返回完整锚点解析；收藏owner在同一治理或发布事务内刷新缓存。
func Mappings(ctx context.Context, q RowsQueryer) (map[string]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT a.id::text,COALESCE(o.redirect_artist_id,a.id)::text FROM artists a LEFT JOIN artist_overrides o ON o.artist_id=a.id ORDER BY a.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, target string
		if err = rows.Scan(&id, &target); err != nil {
			return nil, err
		}
		out[id] = target
	}
	return out, rows.Err()
}
