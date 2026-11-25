package personal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type FavoriteResult struct {
	TargetKind string     `json:"target_kind"`
	TargetID   string     `json:"target_id"`
	Favorited  bool       `json:"favorited"`
	CreatedAt  *time.Time `json:"created_at"`
	Revision   int64      `json:"revision"`
}

type favoriteRelation struct{ table, column string }

func relationForFavorite(kind string) (favoriteRelation, error) {
	switch kind {
	case "track":
		return favoriteRelation{"favorite_tracks", "track_id"}, nil
	case "album":
		return favoriteRelation{"favorite_albums", "group_id"}, nil
	default:
		return favoriteRelation{}, ErrInvalidInput
	}
}

// SetFavorite 在本人集合锁内执行幂等切换；重复收藏不改变时间或集合版本。
func (s *Service) SetFavorite(ctx context.Context, userID, kind, targetID string, favorited bool) (FavoriteResult, error) {
	if kind == "artist" {
		return s.setArtistFavorite(ctx, userID, targetID, favorited)
	}
	relation, err := relationForFavorite(kind)
	if err != nil {
		return FavoriteResult{}, err
	}
	ids, err := normalizeIDs([]string{targetID}, 1)
	if err != nil {
		return FavoriteResult{}, err
	}
	targetID = ids[0]
	tx, err := s.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return FavoriteResult{}, fmt.Errorf("begin favorite change: %w", err)
	}
	defer tx.Rollback()
	revision, err := lockFavoriteState(ctx, tx, userID)
	if err != nil {
		return FavoriteResult{}, err
	}
	result := FavoriteResult{TargetKind: kind, TargetID: targetID, Favorited: favorited, Revision: revision}
	var created time.Time
	err = tx.QueryRowContext(ctx, `SELECT created_at FROM `+relation.table+` WHERE user_id=$1::uuid AND `+relation.column+`=$2::uuid`, userID, targetID).Scan(&created)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return FavoriteResult{}, fmt.Errorf("read favorite: %w", err)
	}
	if exists != favorited {
		if result.Revision >= MaxRevision {
			return FavoriteResult{}, fmt.Errorf("favorite revision exhausted")
		}
		if favorited {
			created, err = insertFavorite(ctx, tx, s.catalogFor(tx), userID, kind, targetID)
		} else {
			_, err = tx.ExecContext(ctx, `DELETE FROM `+relation.table+` WHERE user_id=$1::uuid AND `+relation.column+`=$2::uuid`, userID, targetID)
		}
		if err != nil {
			return FavoriteResult{}, fmt.Errorf("change favorite: %w", err)
		}
		if err := tx.QueryRowContext(ctx, `UPDATE personal_favorite_state SET revision=revision+1 WHERE user_id=$1::uuid RETURNING revision`, userID).Scan(&result.Revision); err != nil {
			return FavoriteResult{}, fmt.Errorf("advance favorite state: %w", err)
		}
	}
	if favorited {
		created = created.UTC()
		result.CreatedAt = &created
	}
	if err := tx.Commit(); err != nil {
		return FavoriteResult{}, fmt.Errorf("commit favorite change: %w", err)
	}
	return result, nil
}

func lockFavoriteState(ctx context.Context, tx *sql.Tx, userID string) (int64, error) {
	if _, err := tx.ExecContext(ctx, `INSERT INTO personal_favorite_state(user_id) VALUES($1::uuid) ON CONFLICT(user_id) DO NOTHING`, userID); err != nil {
		return 0, fmt.Errorf("initialize favorite state: %w", err)
	}
	var revision int64
	err := tx.QueryRowContext(ctx, `SELECT revision FROM personal_favorite_state WHERE user_id=$1::uuid FOR UPDATE`, userID).Scan(&revision)
	return revision, err
}

func insertFavorite(ctx context.Context, tx *sql.Tx, source Catalog, userID, kind, targetID string) (time.Time, error) {
	var created time.Time
	if kind == "track" {
		if err := requireTrack(ctx, source, targetID); err != nil {
			return created, err
		}
		err := tx.QueryRowContext(ctx, `INSERT INTO favorite_tracks(user_id,track_id,created_at) VALUES($1::uuid,$2::uuid,clock_timestamp()) RETURNING created_at`, userID, targetID).Scan(&created)
		return created, err
	}
	snapshot, err := loadAlbumSnapshot(ctx, source, targetID)
	if err != nil {
		return created, err
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO favorite_albums(user_id,group_id,created_at,saved_title,saved_artist) VALUES($1::uuid,$2::uuid,clock_timestamp(),$3,$4) RETURNING created_at`, userID, targetID, snapshot.Title, snapshot.Artist).Scan(&created)
	return created, err
}

func requireTrack(ctx context.Context, source Catalog, id string) error {
	facts, err := source.LoadTrackFacts(ctx, []string{id})
	if err != nil {
		return err
	}
	if _, exists := facts[id]; !exists {
		return ErrNotFound
	}
	return nil
}

type albumSnapshot struct {
	Title, Artist string
	Available     bool
}

func loadAlbumSnapshot(ctx context.Context, source Catalog, id string) (albumSnapshot, error) {
	groups, err := source.LoadReleaseGroupSummaries(ctx, []string{id})
	if err != nil {
		return albumSnapshot{}, err
	}
	group, exists := groups[id]
	if !exists {
		return albumSnapshot{}, ErrNotFound
	}
	if !group.Available {
		return albumSnapshot{}, nil
	}
	if group.Representative == nil {
		return albumSnapshot{}, fmt.Errorf("available album has no representative")
	}
	return albumSnapshot{group.Representative.Title, group.Representative.Artist, true}, nil
}

func favoriteRevision(ctx context.Context, tx *sql.Tx, userID string, expected *int64) (int64, error) {
	var revision int64
	err := tx.QueryRowContext(ctx, `SELECT revision FROM personal_favorite_state WHERE user_id=$1::uuid`, userID).Scan(&revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("read favorite revision: %w", err)
	}
	if expected != nil && *expected != revision {
		return 0, ErrFavoritesChanged
	}
	return revision, nil
}
