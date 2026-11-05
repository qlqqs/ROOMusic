package personal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

// ArtistCatalog 只暴露艺人解析和安全摘要；实例与个人事务共用快照。
// ResolveArtists 即使输入为空也检查艺人准备状态。
type ArtistCatalog interface {
	ResolveArtists(context.Context, []string) (map[string]string, error)
	LoadArtistSummaries(context.Context, []string) (map[string]catalog.ArtistSummary, error)
}

func artistCatalogError(err error, write bool) error {
	if errors.Is(err, catalog.ErrArtistCatalogPreparing) {
		if write {
			return ErrArtistCatalogNotReady
		}
		return ErrArtistCatalogPreparing
	}
	return err
}

func (s *Service) setArtistFavorite(ctx context.Context, userID, targetID string, favorited bool) (FavoriteResult, error) {
	ids, err := normalizeIDs([]string{targetID}, 1)
	if err != nil {
		return FavoriteResult{}, err
	}
	targetID = ids[0]
	tx, err := s.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return FavoriteResult{}, fmt.Errorf("begin artist favorite: %w", err)
	}
	defer tx.Rollback()
	// 与治理统一先 CATP 后本人 state，不能在持有本人锁时等待目录 gate。
	if err := catalog.AcquireArtistWriteGate(ctx, tx); err != nil {
		return FavoriteResult{}, err
	}
	resolved, err := s.artistCatalogFor(tx).ResolveArtists(ctx, ids)
	if err != nil {
		return FavoriteResult{}, artistCatalogError(err, true)
	}
	canonical, exists := resolved[targetID]
	if !exists && favorited {
		return FavoriteResult{}, ErrNotFound
	}
	if exists {
		targetID = canonical
	}
	result := FavoriteResult{TargetKind: "artist", TargetID: targetID, Favorited: favorited}
	if _, err := tx.ExecContext(ctx, `INSERT INTO personal_favorite_state(user_id) VALUES($1::uuid) ON CONFLICT(user_id) DO NOTHING`, userID); err != nil {
		return FavoriteResult{}, fmt.Errorf("initialize artist favorite state: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM personal_favorite_state WHERE user_id=$1::uuid FOR UPDATE`, userID).Scan(&result.Revision); err != nil {
		return FavoriteResult{}, fmt.Errorf("lock artist favorite state: %w", err)
	}
	var created sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT MAX(created_at) FROM artist_favorites WHERE user_id=$1::uuid AND resolved_artist_id=$2::uuid`, userID, targetID).Scan(&created); err != nil {
		return FavoriteResult{}, fmt.Errorf("read artist favorite: %w", err)
	}
	if created.Valid != favorited {
		if result.Revision >= MaxRevision {
			return FavoriteResult{}, fmt.Errorf("favorite revision exhausted")
		}
		if favorited {
			err = tx.QueryRowContext(ctx, `INSERT INTO artist_favorites(user_id,artist_id,resolved_artist_id,created_at) VALUES($1::uuid,$2::uuid,$2::uuid,clock_timestamp()) RETURNING created_at`, userID, targetID).Scan(&created)
		} else {
			_, err = tx.ExecContext(ctx, `DELETE FROM artist_favorites WHERE user_id=$1::uuid AND resolved_artist_id=$2::uuid`, userID, targetID)
		}
		if err != nil {
			return FavoriteResult{}, fmt.Errorf("change artist favorite: %w", err)
		}
		if err := tx.QueryRowContext(ctx, `UPDATE personal_favorite_state SET revision=revision+1 WHERE user_id=$1::uuid RETURNING revision`, userID).Scan(&result.Revision); err != nil {
			return FavoriteResult{}, fmt.Errorf("advance artist favorite state: %w", err)
		}
	}
	if favorited {
		timestamp := created.Time.UTC()
		result.CreatedAt = &timestamp
	}
	if err := tx.Commit(); err != nil {
		return FavoriteResult{}, fmt.Errorf("commit artist favorite: %w", err)
	}
	return result, nil
}
