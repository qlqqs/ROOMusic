package listening

import (
	"context"
	"fmt"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

// RecentTrack 是一个发行里最近被播放的那首曲目。
type RecentTrack struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// RecentRelease 是“最近播放”里的一个发行：当前所在的组与发行摘要（含封面）、该发行最后一次被播放的时间与曲目。
type RecentRelease struct {
	GroupID      string                 `json:"group_id"`
	Release      catalog.ReleaseSummary `json:"release"`
	LastPlayedAt time.Time              `json:"last_played_at"`
	LastTrack    RecentTrack            `json:"last_track"`
}

// RecentReleases 是最近播放的发行；HasHistory 表示用户是否有任何记录（含当前不显示的），界面据此决定是否提供清除。
type RecentReleases struct {
	Items      []RecentRelease `json:"items"`
	HasHistory bool            `json:"has_history"`
}

type lastPlay struct {
	trackID  string
	playedAt time.Time
}

// RecentReleases 返回用户最近播放的发行：按曲目的最后播放时间倒序（同一时间按曲目 ID），每个发行只出现一次，
// 代表它最近一次被播放的曲目，最多 RecentReleaseLimit 个。曲目已缺失（不再 present）的记录不显示但仍保存，文件回来后照常出现；
// 曲目随重扫换到另一个发行时，按它当前所在的发行显示（记录引用曲目，不引用发行）。
// 分批读取直到凑满或没有更多记录（见 recentBatchSize 与 recentMaxBatches）。
func (s *Service) RecentReleases(ctx context.Context, userID string) (RecentReleases, error) {
	result := RecentReleases{Items: []RecentRelease{}}
	shown := map[string]bool{}
	for batch := 0; batch < s.maxBatches && len(result.Items) < RecentReleaseLimit; batch++ {
		plays, err := s.lastPlays(ctx, userID, s.batchSize, batch*s.batchSize)
		if err != nil {
			return RecentReleases{}, err
		}
		if len(plays) == 0 {
			break
		}
		trackIDs := make([]string, len(plays))
		for index, play := range plays {
			trackIDs[index] = play.trackID
		}
		placements, err := s.catalog.LoadTrackPlacements(ctx, trackIDs)
		if err != nil {
			return RecentReleases{}, fmt.Errorf("place recently played tracks: %w", err)
		}
		for _, play := range plays {
			placement, ok := placements[play.trackID]
			if !ok || shown[placement.Release.ID] {
				continue
			}
			shown[placement.Release.ID] = true
			result.Items = append(result.Items, RecentRelease{
				GroupID:      placement.GroupID,
				Release:      placement.Release,
				LastPlayedAt: play.playedAt,
				LastTrack:    RecentTrack{ID: placement.TrackID, Title: placement.Title},
			})
			if len(result.Items) == RecentReleaseLimit {
				break
			}
		}
		if len(plays) < s.batchSize {
			break
		}
	}
	result.HasHistory = len(result.Items) > 0
	if !result.HasHistory {
		if err := s.database.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM play_history WHERE user_id = $1::uuid)`, userID).Scan(&result.HasHistory); err != nil {
			return RecentReleases{}, fmt.Errorf("check play history: %w", err)
		}
	}
	return result, nil
}

// lastPlays 读取用户一批不同的曲目，按各自最后一次播放的时间倒序（同一时间按曲目 ID，保证分批稳定）。
func (s *Service) lastPlays(ctx context.Context, userID string, limit, offset int) ([]lastPlay, error) {
	rows, err := s.database.QueryContext(ctx, `SELECT track_id::text, MAX(played_at) AS last_played_at
		FROM play_history WHERE user_id = $1::uuid
		GROUP BY track_id
		ORDER BY last_played_at DESC, track_id
		LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("read recent plays: %w", err)
	}
	defer rows.Close()
	plays := make([]lastPlay, 0, limit)
	for rows.Next() {
		var play lastPlay
		if err := rows.Scan(&play.trackID, &play.playedAt); err != nil {
			return nil, fmt.Errorf("read recent plays: %w", err)
		}
		play.playedAt = play.playedAt.UTC()
		plays = append(plays, play)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read recent plays: %w", err)
	}
	return plays, nil
}
