package catalog

import (
	"context"
	"fmt"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
)

// ReleaseGroupSort 是组列表的服务器排序白名单；零值沿用原有标题排序。
type ReleaseGroupSort string

const (
	ReleaseGroupSortTitle       ReleaseGroupSort = "title"
	ReleaseGroupSortAddedAtDesc ReleaseGroupSort = "added_at_desc"
)

func releaseGroupOrder(sort ReleaseGroupSort) string {
	if sort == ReleaseGroupSortAddedAtDesc {
		return "ranked.added_at DESC, ranked.group_id DESC"
	}
	return "LOWER(releases.title),LOWER(releases.artist),releases.id"
}

// ListRecentlyAddedReleaseGroups 使用完整组投影读取最近八张可见专辑，不执行总数查询。
func (s *Store) ListRecentlyAddedReleaseGroups(ctx context.Context) ([]ReleaseGroupSummary, error) {
	rows, err := s.database.QueryContext(ctx, "WITH "+releaseGroupRankedCTE(false)+
		" SELECT "+releaseGroupSummaryProjection+
		" FROM ranked JOIN "+releasesql.ReleaseSource+" ON releases.id = ranked.id"+releasesql.ReleaseSummaryArtworkJoin+
		" WHERE ranked.member_rank = 1 ORDER BY "+releaseGroupOrder(ReleaseGroupSortAddedAtDesc)+" LIMIT 8")
	if err != nil {
		return nil, fmt.Errorf("%w: recently added groups: %w", ErrQuery, err)
	}
	defer rows.Close()
	items := make([]ReleaseGroupSummary, 0, 8)
	for rows.Next() {
		item, err := scanReleaseGroupSummary(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
