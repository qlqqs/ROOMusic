package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// CatalogStats 是概览页展示的资料库规模；艺人整理尚未就绪时艺人计数为 nil。
type CatalogStats struct {
	Albums    int64  `json:"albums"`
	Tracks    int64  `json:"tracks"`
	Artists   *int64 `json:"artists"`
	Composers *int64 `json:"composers"`
}

// ReadCatalogStats 在一个只读可重复读事务中读取与列表总数一致的资料库计数。
func ReadCatalogStats(ctx context.Context, database ReadTransactionBeginner) (CatalogStats, error) {
	return readWithLocalJITOff(ctx, database, func(tx *sql.Tx) (CatalogStats, error) {
		store := NewStore(tx)
		stats := CatalogStats{}
		if err := tx.QueryRowContext(ctx, "WITH "+releaseGroupRankedCTE(false)+" SELECT COUNT(*) FROM ranked WHERE member_rank = 1").Scan(&stats.Albums); err != nil {
			return stats, fmt.Errorf("count catalog albums: %w", err)
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM tracks WHERE source_status = 'present'`).Scan(&stats.Tracks); err != nil {
			return stats, fmt.Errorf("count catalog tracks: %w", err)
		}
		if err := store.requireArtistsReady(ctx); err != nil {
			if errors.Is(err, ErrArtistCatalogPreparing) {
				return stats, nil
			}
			return stats, err
		}
		artists, err := store.artistListCount(ctx, "", "", "")
		if err != nil {
			return stats, fmt.Errorf("count catalog artists: %w", err)
		}
		composers, err := store.artistListCount(ctx, "", "", "composer")
		if err != nil {
			return stats, fmt.Errorf("count catalog composers: %w", err)
		}
		stats.Artists = &artists
		stats.Composers = &composers
		return stats, nil
	})
}
