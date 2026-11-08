package library

import (
	"context"
	"database/sql"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
)

// AlbumFavoriteMover 在调用方事务内转移专辑收藏；由 app 注入，nil 时不转移。
type AlbumFavoriteMover func(ctx context.Context, tx *sql.Tx, moves map[string]string) (int, error)

// SetAlbumFavoriteMover 在启动扫描前注入收藏转移能力。
func (service *Service) SetAlbumFavoriteMover(mover AlbumFavoriteMover) {
	service.albumFavoriteMover = mover
}

// transferEmptiedAlbumFavorites 使用扫描持有的连接，在独立短事务内按 CATP → 收藏版本行的顺序转移。
func (service *Service) transferEmptiedAlbumFavorites(ctx context.Context, executor scanExecutor, scanID, rootID string) error {
	if service.albumFavoriteMover == nil {
		return nil
	}
	tx, err := executor.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := ingest.LockProjectionWrites(ctx, tx); err != nil {
		return err
	}
	moves, err := ingest.EmptiedGroupMoves(ctx, tx, scanID, rootID)
	if err != nil {
		return err
	}
	if len(moves) > 0 {
		if _, err := service.albumFavoriteMover(ctx, tx, moves); err != nil {
			return err
		}
	}
	return tx.Commit()
}
