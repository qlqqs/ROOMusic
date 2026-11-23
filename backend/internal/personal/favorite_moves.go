package personal

import (
	"context"
	"database/sql"
	"github.com/qlqq/roomusic/backend/internal/personal/albumfavorites"
)

// MoveAlbumFavorites 在扫描事务中转移已腾空专辑组的收藏。
func (s *Service) MoveAlbumFavorites(ctx context.Context, tx *sql.Tx, moves map[string]string) (int, error) {
	return albumfavorites.MoveAlbumFavorites(ctx, tx, moves)
}
