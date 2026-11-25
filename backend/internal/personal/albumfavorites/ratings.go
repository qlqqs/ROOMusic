package albumfavorites

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type MergedAlbumRating struct {
	UserID        string    `json:"user_id"`
	GroupID       string    `json:"group_id"`
	Rating        int       `json:"rating"`
	RatedAt       time.Time `json:"rated_at"`
	SavedTitle    string    `json:"saved_title"`
	SavedArtist   string    `json:"saved_artist"`
	TargetExisted bool      `json:"target_existed"`
}

func moveLockedAlbumRatings(ctx context.Context, tx *sql.Tx, from, to, users []string) error {
	if len(users) == 0 {
		return nil
	}
	// 同一目标接收多个来源时保留最近一次评分；目标已有评分时保留目标。
	_, err := tx.ExecContext(ctx, `WITH moved AS (
		DELETE FROM rating_albums r USING unnest($1::uuid[],$2::uuid[]) AS m(source,target)
		WHERE r.group_id=m.source AND r.user_id=ANY($3::uuid[])
		RETURNING r.*,m.target
	)
	INSERT INTO rating_albums(user_id,group_id,rating,rated_at,saved_title,saved_artist)
	SELECT user_id,target,rating,rated_at,saved_title,saved_artist FROM moved
	ORDER BY user_id,target,rated_at DESC,group_id
	ON CONFLICT(user_id,group_id) DO NOTHING`, from, to, users)
	return err
}

func mergedRatingSnapshot(ctx context.Context, tx *sql.Tx, source, target string, users []string) ([]MergedAlbumRating, error) {
	rows, err := tx.QueryContext(ctx, `SELECT r.user_id,r.group_id,r.rating,r.rated_at,r.saved_title,r.saved_artist,
		EXISTS(SELECT FROM rating_albums t WHERE t.user_id=r.user_id AND t.group_id=$2)
		FROM rating_albums r WHERE r.group_id=$1 AND r.user_id=ANY($3::uuid[]) ORDER BY r.user_id`, source, target, users)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var before []MergedAlbumRating
	for rows.Next() {
		var r MergedAlbumRating
		if err := rows.Scan(&r.UserID, &r.GroupID, &r.Rating, &r.RatedAt, &r.SavedTitle, &r.SavedArtist, &r.TargetExisted); err != nil {
			return nil, err
		}
		before = append(before, r)
	}
	return before, rows.Err()
}

func restoreMergedAlbumRatings(ctx context.Context, tx *sql.Tx, target string, before []MergedAlbumRating) error {
	raw, err := json.Marshal(before)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `WITH snapshot AS MATERIALIZED (
		SELECT * FROM jsonb_to_recordset($1::jsonb) AS r(user_id uuid,group_id uuid,rating smallint,rated_at timestamptz,
			saved_title text,saved_artist text,target_existed boolean)
	), removed AS (
		DELETE FROM rating_albums t USING snapshot r
		WHERE t.user_id=r.user_id AND t.group_id=$2 AND NOT r.target_existed
			AND (t.rating,t.rated_at)=(r.rating,r.rated_at)
	)
	INSERT INTO rating_albums(user_id,group_id,rating,rated_at,saved_title,saved_artist)
	SELECT user_id,group_id,rating,rated_at,saved_title,saved_artist FROM snapshot
	ON CONFLICT(user_id,group_id) DO UPDATE SET rating=excluded.rating,rated_at=excluded.rated_at,
		saved_title=excluded.saved_title,saved_artist=excluded.saved_artist`, raw, target)
	return err
}
