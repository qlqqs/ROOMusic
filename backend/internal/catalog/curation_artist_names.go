package catalog

import (
	"context"
	"database/sql"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/artistgraph"
)

// RefreshMergedArtistProfiles 在调用者持有目录写锁的事务中重算外部对应变更影响的合并身份。
func RefreshMergedArtistProfiles(ctx context.Context, tx *sql.Tx, members []string) error {
	if len(members) == 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `WITH roots AS (
 SELECT DISTINCT COALESCE(o.redirect_artist_id,a.id) AS id FROM artists a
 LEFT JOIN artist_overrides o ON o.artist_id=a.id WHERE a.id=ANY($1::uuid[])
 ) SELECT id FROM roots WHERE EXISTS(SELECT FROM artist_overrides o WHERE o.redirect_artist_id=roots.id)`, members)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if err = rows.Err(); err != nil {
		return err
	}
	return artistgraph.RefreshProfiles(ctx, tx, ids)
}
