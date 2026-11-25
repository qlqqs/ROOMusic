package albumfavorites

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// MergedAlbumFavorite 记录并入时实际转移的收藏及目标当时的状态。
type MergedAlbumFavorite struct {
	UserID        string    `json:"user_id"`
	GroupID       string    `json:"group_id"`
	CreatedAt     time.Time `json:"created_at"`
	SavedTitle    string    `json:"saved_title"`
	SavedArtist   string    `json:"saved_artist"`
	TargetExisted bool      `json:"target_existed"`
}

func CountAlbumFavorites(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, groupID string) (int, error) {
	var count int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM favorite_albums WHERE group_id=$1::uuid`, groupID).Scan(&count)
	return count, err
}

// MoveMergedAlbumState 按收藏与评分的用户并集一次加锁，随后读取前态并转移。
func MoveMergedAlbumState(ctx context.Context, tx *sql.Tx, source, target string) ([]MergedAlbumFavorite, []MergedAlbumRating, error) {
	from, to := []string{source}, []string{target}
	users, err := lockAlbumSources(ctx, tx, from)
	if err != nil || len(users) == 0 {
		return nil, nil, err
	}
	favorites, err := mergedFavoriteSnapshot(ctx, tx, source, target, users)
	if err != nil {
		return nil, nil, err
	}
	ratings, err := mergedRatingSnapshot(ctx, tx, source, target, users)
	if err != nil {
		return nil, nil, err
	}
	if _, err := moveLockedAlbumFavorites(ctx, tx, from, to, users); err != nil {
		return nil, nil, err
	}
	if err := moveLockedAlbumRatings(ctx, tx, from, to, users); err != nil {
		return nil, nil, err
	}
	return favorites, ratings, nil
}

func mergedFavoriteSnapshot(ctx context.Context, tx *sql.Tx, source, target string, users []string) ([]MergedAlbumFavorite, error) {
	rows, err := tx.QueryContext(ctx, `SELECT f.user_id::text, f.group_id::text, f.created_at, f.saved_title, f.saved_artist,
		EXISTS(SELECT 1 FROM favorite_albums t WHERE t.user_id=f.user_id AND t.group_id=$2::uuid)
		FROM favorite_albums f WHERE f.group_id=$1::uuid AND f.user_id=ANY($3::uuid[]) ORDER BY f.user_id`, source, target, users)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	before := []MergedAlbumFavorite{}
	for rows.Next() {
		var f MergedAlbumFavorite
		if err := rows.Scan(&f.UserID, &f.GroupID, &f.CreatedAt, &f.SavedTitle, &f.SavedArtist, &f.TargetExisted); err != nil {
			return nil, err
		}
		before = append(before, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return before, nil
}

// RestoreMergedAlbumState 共用用户锁，之后分别按各自的快照恢复。
func RestoreMergedAlbumState(ctx context.Context, tx *sql.Tx, target string, favorites []MergedAlbumFavorite, ratings []MergedAlbumRating) error {
	users := make([]string, 0, len(favorites)+len(ratings))
	for _, f := range favorites {
		users = append(users, f.UserID)
	}
	for _, r := range ratings {
		users = append(users, r.UserID)
	}
	if len(users) == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `SELECT user_id FROM personal_favorite_state WHERE user_id=ANY($1::uuid[]) ORDER BY user_id FOR UPDATE`, users); err != nil {
		return err
	}
	if len(favorites) > 0 {
		if err := restoreMergedAlbumFavorites(ctx, tx, target, favorites); err != nil {
			return err
		}
	}
	if len(ratings) > 0 {
		return restoreMergedAlbumRatings(ctx, tx, target, ratings)
	}
	return nil
}

func restoreMergedAlbumFavorites(ctx context.Context, tx *sql.Tx, target string, before []MergedAlbumFavorite) error {
	raw, err := json.Marshal(before)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `WITH snapshot AS MATERIALIZED (
		SELECT * FROM jsonb_to_recordset($1::jsonb) AS f(user_id uuid,group_id uuid,created_at timestamptz,saved_title text,saved_artist text,target_existed boolean)
	), restored AS (
		INSERT INTO favorite_albums(user_id,group_id,created_at,saved_title,saved_artist)
		SELECT user_id,group_id,created_at,saved_title,saved_artist FROM snapshot
		ON CONFLICT(user_id,group_id) DO UPDATE SET created_at=excluded.created_at,saved_title=excluded.saved_title,saved_artist=excluded.saved_artist
		WHERE (favorite_albums.created_at,favorite_albums.saved_title,favorite_albums.saved_artist)
			IS DISTINCT FROM (excluded.created_at,excluded.saved_title,excluded.saved_artist)
		RETURNING user_id
	), removed AS (
		DELETE FROM favorite_albums t USING snapshot f
		WHERE t.user_id=f.user_id AND t.group_id=$2 AND NOT f.target_existed
			AND (t.created_at,t.saved_title,t.saved_artist)=(f.created_at,f.saved_title,f.saved_artist)
		RETURNING t.user_id
	)
	UPDATE personal_favorite_state SET revision=revision+1 WHERE user_id IN (SELECT user_id FROM restored UNION SELECT user_id FROM removed)`, raw, target)
	return err
}
