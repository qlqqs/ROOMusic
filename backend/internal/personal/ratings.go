package personal

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

type RatingResult struct {
	TargetKind       string     `json:"target_kind"`
	TargetID         string     `json:"target_id"`
	ResolvedArtistID *string    `json:"resolved_artist_id,omitempty"`
	Rating           *int       `json:"rating"`
	RatedAt          *time.Time `json:"rated_at"`
}

func (s *Service) SetRating(ctx context.Context, userID, kind, targetID string, rating int) (RatingResult, error) {
	if kind != "track" && kind != "album" && kind != "artist" {
		return RatingResult{}, ErrInvalidInput
	}
	return s.setRating(ctx, userID, kind, targetID, rating)
}

// SetRatingByID 接受客户端的无类型身份；包括清除在内，均须先找到目录目标。
func (s *Service) SetRatingByID(ctx context.Context, userID, targetID string, rating int) (RatingResult, error) {
	return s.setRating(ctx, userID, "", targetID, rating)
}

func (s *Service) setRating(ctx context.Context, userID, kind, targetID string, rating int) (RatingResult, error) {
	ids, err := normalizeIDs([]string{targetID}, 1)
	if err != nil || rating < 0 || rating > 5 {
		return RatingResult{}, ErrInvalidInput
	}
	targetID = ids[0]
	tx, err := s.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return RatingResult{}, err
	}
	defer tx.Rollback()
	if kind == "" {
		kind, err = catalog.NewStore(tx).PersonalTargetKind(ctx, targetID)
		if err != nil {
			return RatingResult{}, err
		}
		if kind == "" {
			return RatingResult{}, ErrNotFound
		}
	}
	result := RatingResult{TargetID: targetID, TargetKind: kind}
	if kind == "artist" {
		if err := catalog.AcquireArtistWriteGate(ctx, tx); err != nil {
			return RatingResult{}, err
		}
		resolved, err := s.artistCatalogFor(tx).ResolveArtists(ctx, ids)
		if err != nil {
			return RatingResult{}, artistCatalogError(err, true)
		}
		if canonical, ok := resolved[targetID]; ok {
			result.ResolvedArtistID = &canonical
		} else if rating != 0 {
			return RatingResult{}, ErrNotFound
		}
	}
	if _, err := lockFavoriteState(ctx, tx, userID); err != nil {
		return RatingResult{}, err
	}
	var ratedAt time.Time
	switch kind {
	case "artist":
		if result.ResolvedArtistID != nil {
			ratedAt, err = writeArtistRating(ctx, tx, userID, targetID, *result.ResolvedArtistID, rating)
		}
	case "track", "album":
		ratedAt, err = writeMediaRating(ctx, tx, s.catalogFor(tx), userID, kind, targetID, rating)
	}
	if err != nil {
		return RatingResult{}, err
	}
	if rating > 0 {
		ratedAt = ratedAt.UTC()
		result.Rating, result.RatedAt = &rating, &ratedAt
	}
	if err := tx.Commit(); err != nil {
		return RatingResult{}, err
	}
	return result, nil
}

func writeMediaRating(ctx context.Context, tx *sql.Tx, source Catalog, user, kind, id string, rating int) (time.Time, error) {
	table, column := "rating_tracks", "track_id"
	if kind == "album" {
		table, column = "rating_albums", "group_id"
	}
	var at time.Time
	where := " WHERE user_id=$1 AND " + column + "=$2"
	if rating == 0 {
		_, err := tx.ExecContext(ctx, "DELETE FROM "+table+where, user, id)
		return at, err
	}
	err := tx.QueryRowContext(ctx, "SELECT rated_at FROM "+table+where+" AND rating=$3", user, id, rating).Scan(&at)
	if !errors.Is(err, sql.ErrNoRows) {
		return at, err
	}
	if kind == "track" {
		if err := requireTrack(ctx, source, id); err != nil {
			return at, err
		}
		err = tx.QueryRowContext(ctx, `INSERT INTO rating_tracks(user_id,track_id,rating) VALUES ($1,$2,$3)
			ON CONFLICT (user_id,track_id) DO UPDATE SET rating=excluded.rating,rated_at=excluded.rated_at
			RETURNING rated_at`, user, id, rating).Scan(&at)
		return at, err
	}
	snapshot, err := loadAlbumSnapshot(ctx, source, id)
	if err != nil {
		return at, err
	}
	// 专辑暂不可用时，保留最近一次可用的标题和艺人快照。
	err = tx.QueryRowContext(ctx, `INSERT INTO rating_albums(user_id,group_id,rating,saved_title,saved_artist) VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (user_id,group_id) DO UPDATE SET rating=excluded.rating,rated_at=excluded.rated_at,
				saved_title=CASE WHEN $6 THEN excluded.saved_title ELSE rating_albums.saved_title END,
				saved_artist=CASE WHEN $6 THEN excluded.saved_artist ELSE rating_albums.saved_artist END
			RETURNING rated_at`, user, id, rating, snapshot.Title, snapshot.Artist, snapshot.Available).Scan(&at)
	return at, err
}

func writeArtistRating(ctx context.Context, tx *sql.Tx, user, anchor, canonical string, rating int) (time.Time, error) {
	var at time.Time
	if rating == 0 {
		_, err := tx.ExecContext(ctx, `DELETE FROM rating_artists WHERE user_id=$1 AND resolved_artist_id=$2`, user, canonical)
		return at, err
	}
	// 全部锚点已是请求值且原锚点存在时，重放保留显示的评分时间。
	err := tx.QueryRowContext(ctx, `SELECT MAX(rated_at) FROM rating_artists WHERE user_id=$1 AND resolved_artist_id=$2
		HAVING bool_and(rating=$3) AND bool_or(artist_id=$4)`, user, canonical, rating, anchor).Scan(&at)
	if !errors.Is(err, sql.ErrNoRows) {
		return at, err
	}
	err = tx.QueryRowContext(ctx, `WITH siblings AS (
		UPDATE rating_artists SET rating=$4,rated_at=statement_timestamp()
		WHERE user_id=$1 AND resolved_artist_id=$3 AND artist_id<>$2
	)
	INSERT INTO rating_artists(user_id,artist_id,resolved_artist_id,rating,rated_at)
	VALUES ($1,$2,$3,$4,statement_timestamp())
	ON CONFLICT(user_id,artist_id) DO UPDATE SET rating=excluded.rating,rated_at=excluded.rated_at
	RETURNING rated_at`, user, anchor, canonical, rating).Scan(&at)
	return at, err
}
