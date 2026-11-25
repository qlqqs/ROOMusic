package personal

import (
	"context"
	"database/sql"
	"fmt"
)

func (s *Service) RatedTrackQueue(ctx context.Context, userID string, minRating int, sort string) (QueueTracks, error) {
	p := RatedListParams{MinRating: minRating, Sort: sort}
	if err := p.normalizeFilter(); err != nil {
		return QueueTracks{}, err
	}
	tx, err := s.readTx(ctx)
	if err != nil {
		return QueueTracks{}, fmt.Errorf("begin rated track queue: %w", err)
	}
	defer tx.Rollback()
	source := s.catalogFor(tx)
	selection := queueSelection{ids: make([]string, 0, TrackLimit)}
	var cursor *ratingRef
	for {
		refs, err := readRatedTrackBatch(ctx, tx, userID, p, cursor)
		if err != nil {
			return QueueTracks{}, err
		}
		if err := selection.add(ctx, source, ratingRefIDs(refs)); err != nil {
			return QueueTracks{}, err
		}
		if len(refs) < TrackLimit {
			break
		}
		cursor = &refs[len(refs)-1]
	}
	queue, err := selection.project(ctx, source)
	if err != nil {
		return QueueTracks{}, err
	}
	if err := tx.Commit(); err != nil {
		return QueueTracks{}, fmt.Errorf("finish rated track queue: %w", err)
	}
	return queue, nil
}

func readRatedTrackBatch(ctx context.Context, tx *sql.Tx, userID string, p RatedListParams, cursor *ratingRef) ([]ratingRef, error) {
	const source = `SELECT track_id,rating,rated_at FROM rating_tracks WHERE user_id=$1 AND rating >= $2`
	suffix := " ORDER BY " + p.orderBy("track_id") + " LIMIT $3"
	if cursor == nil {
		return readRatingRefs(ctx, tx, source+suffix, false, userID, p.MinRating, TrackLimit)
	}

	// 分段走索引范围，避免相反方向的排序条件用 OR 时反复扫描已读取的前缀。
	type seekRange struct {
		predicate string
		args      []any
	}
	var ranges []seekRange
	if p.Sort == "recent" {
		ranges = []seekRange{
			{" AND rated_at=$4 AND track_id>$5", []any{cursor.ratedAt, cursor.id}},
			{" AND rated_at<$4", []any{cursor.ratedAt}},
		}
	} else {
		ranges = []seekRange{
			{" AND rating=$4 AND rated_at=$5 AND track_id>$6", []any{cursor.rating, cursor.ratedAt, cursor.id}},
			{" AND rating=$4 AND rated_at<$5", []any{cursor.rating, cursor.ratedAt}},
			{" AND rating<$4", []any{cursor.rating}},
		}
	}
	var refs []ratingRef
	for _, seek := range ranges {
		args := append([]any{userID, p.MinRating, TrackLimit - len(refs)}, seek.args...)
		part, err := readRatingRefs(ctx, tx, source+seek.predicate+suffix, false, args...)
		if err != nil {
			return nil, err
		}
		refs = append(refs, part...)
		if len(refs) == TrackLimit {
			break
		}
	}
	return refs, nil
}
