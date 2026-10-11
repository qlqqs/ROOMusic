package catalog

import (
	"context"
	"fmt"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// maxPlacementTrackIDs 是一次 LoadTrackPlacements 最多接受的不同曲目数；调用方按批读取，超出说明调用方出了错。
const maxPlacementTrackIDs = 1000

// TrackPlacement 是一首 present 曲目当前所在的位置：曲目标题，以及它所在发行的组 ID 与摘要（含封面）。
// 曲目会随重扫换到另一个发行，所以位置只在读取的那一刻有效；调用方不应该保存它。
type TrackPlacement struct {
	TrackID string
	Title   string
	GroupID string
	Release ReleaseSummary
}

// LoadTrackPlacements 批量读取 present 曲目的位置，供按曲目保存的个人数据（如播放历史）显示成专辑。
// 返回值的键是小写 UUID 文本；不存在、已缺失或不是 UUID 的曲目没有键。重复的 ID 只算一次，空输入不访问数据库，
// 不同曲目超过 maxPlacementTrackIDs 个时返回错误。
//
// 摘要按发行读取：先把曲目换成发行 ID，再对不同的发行各算一次摘要（含封面 LEFT JOIN 与校验，和列表共用同一批列），
// 同一个发行的许多曲目不会重复计算摘要。它不检查目录状态，也不触碰文件系统。
func (s *Store) LoadTrackPlacements(ctx context.Context, trackIDs []string) (map[string]TrackPlacement, error) {
	placements := map[string]TrackPlacement{}
	ids := make([]string, 0, len(trackIDs))
	wanted := map[string]bool{}
	for _, id := range trackIDs {
		if !uuid.Valid(id) {
			continue
		}
		id = strings.ToLower(id)
		if !wanted[id] {
			wanted[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return placements, nil
	}
	if len(ids) > maxPlacementTrackIDs {
		return nil, fmt.Errorf("load track placements: %d tracks exceed the limit of %d", len(ids), maxPlacementTrackIDs)
	}

	type trackRow struct{ id, title, releaseID string }
	rows, err := s.database.QueryContext(ctx, `SELECT tracks.id::text, tracks.title, media.release_id::text
		FROM tracks JOIN media ON media.id = tracks.medium_id
		WHERE tracks.id = ANY($1::text[]::uuid[]) AND tracks.source_status = 'present'`, ids)
	if err != nil {
		return nil, fmt.Errorf("load track placements: %w", err)
	}
	defer rows.Close()
	var found []trackRow
	releaseIDs := make([]string, 0, len(ids))
	seenRelease := map[string]bool{}
	for rows.Next() {
		var row trackRow
		if err := rows.Scan(&row.id, &row.title, &row.releaseID); err != nil {
			return nil, fmt.Errorf("load track placements: %w", err)
		}
		found = append(found, row)
		if !seenRelease[row.releaseID] {
			seenRelease[row.releaseID] = true
			releaseIDs = append(releaseIDs, row.releaseID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load track placements: %w", err)
	}
	if len(found) == 0 {
		return placements, nil
	}

	type releaseRow struct {
		groupID string
		summary ReleaseSummary
	}
	releaseRows, err := s.database.QueryContext(ctx, "SELECT "+releaseSummaryProjection+", releases.group_id::text FROM releases"+
		releaseSummaryArtworkJoin+" WHERE releases.id = ANY($1::text[]::uuid[])", releaseIDs)
	if err != nil {
		return nil, fmt.Errorf("load track placements: %w", err)
	}
	defer releaseRows.Close()
	releases := make(map[string]releaseRow, len(releaseIDs))
	for releaseRows.Next() {
		var columns releaseSummaryColumns
		var groupID string
		if err := releaseRows.Scan(append(columns.targets(), &groupID)...); err != nil {
			return nil, fmt.Errorf("load track placements: %w", err)
		}
		summary, err := columns.summary()
		if err != nil {
			return nil, fmt.Errorf("load track placements: %w", err)
		}
		releases[summary.ID] = releaseRow{groupID: groupID, summary: summary}
	}
	if err := releaseRows.Err(); err != nil {
		return nil, fmt.Errorf("load track placements: %w", err)
	}

	for _, row := range found {
		release, ok := releases[row.releaseID]
		if !ok {
			continue // 两次读取之间发行消失了：这首曲目这次没有位置
		}
		placements[row.id] = TrackPlacement{TrackID: row.id, Title: row.title, GroupID: release.groupID, Release: release.summary}
	}
	return placements, nil
}
