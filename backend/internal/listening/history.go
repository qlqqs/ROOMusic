package listening

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

// HistoryQueueLimit 限制最近历史候选数，含 missing，不向更旧历史补位。
const HistoryQueueLimit = 1000

// HistoryItem 是一首曲目的当前摘要及本人的播放统计。
type HistoryItem struct {
	catalog.TrackSummary
	LastPlayedAt time.Time `json:"last_played_at"`
	PlayCount    int64     `json:"play_count"`
}

type HistoryPagination struct {
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
	Total    int64 `json:"total"`
}

type HistoryTracks struct {
	Items      []HistoryItem     `json:"items"`
	Pagination HistoryPagination `json:"pagination"`
	HasHistory bool              `json:"has_history"`
}

type HistoryQueue struct {
	Items      []HistoryItem `json:"items"`
	Total      int64         `json:"total"`
	Limit      int           `json:"limit"`
	Truncated  bool          `json:"truncated"`
	HasHistory bool          `json:"has_history"`
}

// Tracks 按曲目汇总后分页；参数由传输层校验，总数含 missing，超末页仍保留总数。
func (s *Service) Tracks(ctx context.Context, userID string, page, pageSize int) (HistoryTracks, error) {
	items, total, err := s.history(ctx, userID, pageSize, (page-1)*pageSize)
	if err != nil {
		return HistoryTracks{}, err
	}
	return HistoryTracks{Items: items, Pagination: HistoryPagination{Page: page, PageSize: pageSize, Total: total}, HasHistory: total > 0}, nil
}

// Queue 返回固定最多 1,000 个历史候选，能力过滤交给前端，不接受分页或排序参数。
func (s *Service) Queue(ctx context.Context, userID string) (HistoryQueue, error) {
	items, total, err := s.history(ctx, userID, HistoryQueueLimit, 0)
	if err != nil {
		return HistoryQueue{}, err
	}
	return HistoryQueue{Items: items, Total: total, Limit: HistoryQueueLimit, Truncated: total > HistoryQueueLimit, HasHistory: total > 0}, nil
}

const historySQL = `WITH grouped AS MATERIALIZED (
 SELECT track_id, MAX(played_at) AS last_played_at, COUNT(*) AS play_count
 FROM play_history WHERE user_id=$1::uuid GROUP BY track_id
), page_rows AS (
 SELECT track_id,last_played_at,play_count FROM grouped
 ORDER BY last_played_at DESC,track_id ASC LIMIT $2 OFFSET $3
)
SELECT totals.total,p.track_id::text,p.last_played_at,p.play_count
FROM (SELECT COUNT(*) AS total FROM grouped) totals
LEFT JOIN page_rows p ON TRUE
ORDER BY p.last_played_at DESC,p.track_id ASC`

func (s *Service) history(ctx context.Context, userID string, limit, offset int) ([]HistoryItem, int64, error) {
	items, total, err := s.historyRows(ctx, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	if len(items) == 0 {
		return items, total, nil
	}
	ids := make([]string, len(items))
	for i := range items {
		ids[i] = items[i].TrackID
	}
	// historyRows 已关闭 rows，连接数为 1 时也能继续补全目录信息。
	summaries, err := s.catalog.LoadTrackSummaries(ctx, ids)
	if err != nil {
		return nil, 0, fmt.Errorf("summarize history tracks: %w", err)
	}
	for i := range items {
		summary, ok := summaries[items[i].TrackID]
		if !ok {
			return nil, 0, fmt.Errorf("summarize history tracks: missing referenced track")
		}
		items[i].TrackSummary = summary
	}
	return items, total, nil
}

func (s *Service) historyRows(ctx context.Context, userID string, limit, offset int) ([]HistoryItem, int64, error) {
	rows, err := s.database.QueryContext(ctx, historySQL, userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("read history tracks: %w", err)
	}
	defer rows.Close()
	items := make([]HistoryItem, 0, limit)
	var total int64
	for rows.Next() {
		var id sql.NullString
		var at sql.NullTime
		var count sql.NullInt64
		if err := rows.Scan(&total, &id, &at, &count); err != nil {
			return nil, 0, fmt.Errorf("read history tracks: %w", err)
		}
		// 空页的总数元数据行，不是曲目。
		if !id.Valid {
			continue
		}
		items = append(items, HistoryItem{TrackSummary: catalog.TrackSummary{TrackID: id.String}, LastPlayedAt: at.Time.UTC(), PlayCount: count.Int64})
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("read history tracks: %w", err)
	}
	return items, total, nil
}

const deleteTrackPlaysSQL = `DELETE FROM play_history WHERE user_id=$1::uuid AND track_id=$2::uuid`

// DeleteTrack 只删本人该曲目的全部播放，不查 catalog；无记录时返回 0。
func (s *Service) DeleteTrack(ctx context.Context, userID, trackID string) (int64, error) {
	result, err := s.database.ExecContext(ctx, deleteTrackPlaysSQL, userID, trackID)
	if err != nil {
		return 0, fmt.Errorf("delete track plays: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("delete track plays: %w", err)
	}
	return count, nil
}
