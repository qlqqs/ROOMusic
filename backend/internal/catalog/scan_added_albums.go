package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
)

// ScanAddedAlbum 保留扫描时的历史身份与文字；动态摘要只在该新发行目前可见时存在。
// 认领提示由调用方经 governance 的批量入口补齐，避免 catalog 反向依赖治理包。
type ScanAddedAlbum struct {
	GroupIDAtScan  string               `json:"group_id_at_scan"`
	AddedAt        time.Time            `json:"added_at"`
	TitleAtScan    string               `json:"title_at_scan"`
	ArtistAtScan   string               `json:"artist_at_scan"`
	ReleaseID      string               `json:"release_id"`
	CurrentGroupID *string              `json:"current_group_id"`
	Album          *ReleaseGroupSummary `json:"album"`
}

// ScanAddedAlbumPage 的总数按历史关联计算，不因后来归组、认领或不可见而减少。
type ScanAddedAlbumPage struct {
	Items []ScanAddedAlbum
	Total int64
}

// ListScanAddedAlbums 读取历史页并批量补当前卡片。调用方必须用同一只读 REPEATABLE READ
// Queryer 读取报告、此页和治理提示；本入口不另开事务，也不查询扫描状态或承担鉴权。
func (s *Store) ListScanAddedAlbums(ctx context.Context, scanID string, page, pageSize int) (ScanAddedAlbumPage, error) {
	if page < 1 || pageSize < 1 || pageSize > 100 || page-1 > int(^uint(0)>>1)/pageSize {
		return ScanAddedAlbumPage{}, fmt.Errorf("invalid scan added album pagination")
	}
	result := ScanAddedAlbumPage{Items: make([]ScanAddedAlbum, 0, pageSize)}
	if err := s.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM scan_added_releases WHERE scan_run_id=$1::uuid`, scanID).Scan(&result.Total); err != nil {
		return ScanAddedAlbumPage{}, fmt.Errorf("%w: count scan added albums: %w", ErrQuery, err)
	}
	rows, err := s.database.QueryContext(ctx, `SELECT added.group_id_at_creation::text, added.added_at,
  added.title_at_creation, added.artist_at_creation, added.release_id::text,
  CASE WHEN `+releasesql.ReleaseVisiblePredicate+` THEN releases.group_id::text ELSE NULL END
  FROM scan_added_releases added LEFT JOIN releases ON releases.id=added.release_id
  WHERE added.scan_run_id=$1::uuid
  ORDER BY added.added_at DESC, added.release_id DESC LIMIT $2 OFFSET $3`, scanID, pageSize, (page-1)*pageSize)
	if err != nil {
		return ScanAddedAlbumPage{}, fmt.Errorf("%w: scan added albums: %w", ErrQuery, err)
	}
	groupIDs := make([]string, 0, pageSize)
	for rows.Next() {
		var item ScanAddedAlbum
		var groupID sql.NullString
		if err := rows.Scan(&item.GroupIDAtScan, &item.AddedAt, &item.TitleAtScan, &item.ArtistAtScan, &item.ReleaseID, &groupID); err != nil {
			rows.Close()
			return ScanAddedAlbumPage{}, err
		}
		item.AddedAt = item.AddedAt.UTC()
		if groupID.Valid {
			item.CurrentGroupID = &groupID.String
			groupIDs = append(groupIDs, groupID.String)
		}
		result.Items = append(result.Items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return ScanAddedAlbumPage{}, err
	}
	if len(groupIDs) == 0 {
		return result, nil
	}
	summaries, err := s.loadScanAddedGroupSummaries(ctx, groupIDs)
	if err != nil {
		return ScanAddedAlbumPage{}, err
	}
	for i := range result.Items {
		item := &result.Items[i]
		if item.CurrentGroupID == nil {
			continue
		}
		summary, ok := summaries[*item.CurrentGroupID]
		if !ok {
			return ScanAddedAlbumPage{}, fmt.Errorf("%w: scan added album snapshot is inconsistent", ErrQuery)
		}
		item.Album = &summary
	}
	return result, nil
}

func (s *Store) loadScanAddedGroupSummaries(ctx context.Context, groupIDs []string) (map[string]ReleaseGroupSummary, error) {
	rows, err := s.database.QueryContext(ctx, "WITH "+releaseGroupRankedCTE(false)+" SELECT "+releaseGroupSummaryProjection+
		" FROM ranked JOIN "+releasesql.ReleaseSource+" ON releases.id=ranked.id"+releasesql.ReleaseSummaryArtworkJoin+
		" WHERE ranked.member_rank=1 AND ranked.group_id=ANY($1::uuid[])", groupIDs)
	if err != nil {
		return nil, fmt.Errorf("%w: scan added album summaries: %w", ErrQuery, err)
	}
	defer rows.Close()
	summaries := make(map[string]ReleaseGroupSummary, len(groupIDs))
	for rows.Next() {
		summary, err := scanReleaseGroupSummary(rows)
		if err != nil {
			return nil, err
		}
		summaries[summary.ID] = summary
	}
	return summaries, rows.Err()
}
