package personal

import (
	"context"
	"database/sql"
	"fmt"
)

type FavoriteQueue struct {
	QueueTracks
	Revision int64 `json:"revision"`
}

func (s *Service) FavoriteQueue(ctx context.Context, userID string) (FavoriteQueue, error) {
	tx, err := s.readTx(ctx)
	if err != nil {
		return FavoriteQueue{}, fmt.Errorf("begin favorite queue: %w", err)
	}
	defer tx.Rollback()
	revision, err := favoriteRevision(ctx, tx, userID, nil)
	if err != nil {
		return FavoriteQueue{}, err
	}
	source := s.catalogFor(tx)
	selection := queueSelection{ids: make([]string, 0, TrackLimit)}
	var cursor *favoriteTrackRef
	for {
		refs, err := readFavoriteTrackBatch(ctx, tx, userID, cursor)
		if err != nil {
			return FavoriteQueue{}, err
		}
		ids := make([]string, len(refs))
		for i, ref := range refs {
			ids[i] = ref.id
		}
		if err := selection.add(ctx, source, ids); err != nil {
			return FavoriteQueue{}, err
		}
		if len(refs) < TrackLimit {
			break
		}
		last := refs[len(refs)-1]
		cursor = &last
	}
	queue, err := selection.project(ctx, source)
	if err != nil {
		return FavoriteQueue{}, err
	}
	if err := tx.Commit(); err != nil {
		return FavoriteQueue{}, fmt.Errorf("finish favorite queue: %w", err)
	}
	return FavoriteQueue{QueueTracks: queue, Revision: revision}, nil
}

// 相反方向的时间/UUID排序拆成两个索引范围，避免OR谓词每批重新过滤已遍历的前缀。
func readFavoriteTrackBatch(ctx context.Context, tx *sql.Tx, userID string, cursor *favoriteTrackRef) ([]favoriteTrackRef, error) {
	if cursor == nil {
		return readFavoriteTrackRefs(ctx, tx, `SELECT track_id::text,created_at FROM favorite_tracks WHERE user_id=$1::uuid ORDER BY created_at DESC,favorite_tracks.track_id ASC LIMIT $2`, userID, TrackLimit)
	}
	sameTime, err := readFavoriteTrackRefs(ctx, tx, `SELECT track_id::text,created_at FROM favorite_tracks WHERE user_id=$1::uuid AND created_at=$2 AND track_id>$3::uuid ORDER BY favorite_tracks.track_id ASC LIMIT $4`, userID, cursor.created, cursor.id, TrackLimit)
	if err != nil || len(sameTime) == TrackLimit {
		return sameTime, err
	}
	older, err := readFavoriteTrackRefs(ctx, tx, `SELECT track_id::text,created_at FROM favorite_tracks WHERE user_id=$1::uuid AND created_at<$2 ORDER BY created_at DESC,favorite_tracks.track_id ASC LIMIT $3`, userID, cursor.created, TrackLimit-len(sameTime))
	if err != nil {
		return nil, err
	}
	return append(sameTime, older...), nil
}
