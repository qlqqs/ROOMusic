package catalog

import (
	"context"
	"fmt"
	"math"
)

// TrackCollectionStats 是曲目集合的轻量统计，不含逐曲事实、发行摘要或存储身份。
type TrackCollectionStats struct {
	MatchedCount         int64
	MissingCount         int64
	KnownDurationSeconds float64
	UnknownDurationCount int64
}

const loadTrackCollectionStatsSQL = `SELECT COUNT(*),COUNT(*) FILTER (WHERE t.source_status='missing'),
 COALESCE(SUM(CASE WHEN dd.duration_seconds>0 AND dd.duration_seconds<'Infinity'::double precision THEN dd.duration_seconds END),0),
 COUNT(*)-COUNT(CASE WHEN dd.duration_seconds>0 AND dd.duration_seconds<'Infinity'::double precision THEN dd.duration_seconds END)
 FROM tracks t LEFT JOIN track_display_durations dd ON dd.track_id=t.id
 WHERE t.id=ANY($1::text[]::uuid[])`

// LoadTrackCollectionStats 在同一语句快照统计至多 10,000 个不同有效 UUID，包含 missing 的上次已知时长。
// 输入按既有 facts 规则小写去重、忽略非法 ID；调用方必须核对 MatchedCount，不把不存在身份当作 missing。
func (s *Store) LoadTrackCollectionStats(ctx context.Context, trackIDs []string) (TrackCollectionStats, error) {
	ids, err := personalReadIDs(trackIDs, maxFactTrackIDs)
	if err != nil {
		return TrackCollectionStats{}, fmt.Errorf("load track collection stats: %w", err)
	}
	if len(ids) == 0 {
		return TrackCollectionStats{}, nil
	}
	var result TrackCollectionStats
	if err := s.database.QueryRowContext(ctx, loadTrackCollectionStatsSQL, ids).Scan(
		&result.MatchedCount, &result.MissingCount, &result.KnownDurationSeconds, &result.UnknownDurationCount,
	); err != nil {
		return TrackCollectionStats{}, fmt.Errorf("%w: load track collection stats: %w", ErrQuery, err)
	}
	if math.IsNaN(result.KnownDurationSeconds) || math.IsInf(result.KnownDurationSeconds, 0) {
		return TrackCollectionStats{}, fmt.Errorf("load track collection stats: duration overflow")
	}
	return result, nil
}
