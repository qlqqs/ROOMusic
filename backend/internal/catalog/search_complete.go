package catalog

import (
	"context"
	"fmt"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
)

// completeSearch 只补全一页，并复用已有摘要的安全校验；缺项使整个请求失败。
func (s *Store) completeSearch(ctx context.Context, kind string, hits []searchHit) (any, error) {
	ids := make([]string, len(hits))
	for i, hit := range hits {
		ids[i] = hit.ID
	}
	if kind == "tracks" {
		items := make([]SearchTrackItem, 0, len(hits))
		summaries, err := s.LoadTrackSummaries(ctx, ids)
		if err != nil {
			return nil, err
		}
		for _, hit := range hits {
			summary, ok := summaries[hit.ID]
			if !ok || !summary.Present {
				return nil, fmt.Errorf("search track summary unavailable")
			}
			items = append(items, SearchTrackItem{TrackSummary: summary, Match: hit.Match})
		}
		return items, nil
	}
	items := make([]SearchAlbumItem, 0, len(hits))
	summaries, err := s.loadSearchGroups(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, hit := range hits {
		summary, ok := summaries[hit.ID]
		if !ok {
			return nil, fmt.Errorf("search album summary unavailable")
		}
		items = append(items, SearchAlbumItem{Group: summary, MatchedReleaseID: hit.MatchedReleaseID, Match: hit.Match})
	}
	return items, nil
}

func (s *Store) loadSearchGroups(ctx context.Context, ids []string) (map[string]ReleaseGroupSummary, error) {
	result := make(map[string]ReleaseGroupSummary, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := s.database.QueryContext(ctx, `WITH ranked AS (
	 SELECT releases.id,releases.group_id,
	 ROW_NUMBER() OVER (PARTITION BY releases.group_id ORDER BY `+releasesql.ReleaseGroupMemberOrder+`) AS member_rank,
	 COUNT(*) OVER (PARTITION BY releases.group_id) AS release_count,
	 MIN(releases.created_at) OVER (PARTITION BY releases.group_id) AS added_at
	 FROM `+releasesql.ReleaseSource+` WHERE releases.group_id=ANY($1::uuid[]) AND `+releasesql.ReleaseVisiblePredicate+`)
	 SELECT `+releaseGroupSummaryProjection+` FROM ranked JOIN `+releasesql.ReleaseSource+` ON releases.id=ranked.id`+
		releasesql.ReleaseSummaryArtworkJoin+` WHERE ranked.member_rank=1`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanReleaseGroupSummary(rows)
		if err != nil {
			return nil, err
		}
		result[item.ID] = item
	}
	return result, rows.Err()
}

func (s *Store) loadSearchReleases(ctx context.Context, ids []string) (map[string]ReleaseSummary, error) {
	result := make(map[string]ReleaseSummary, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := s.database.QueryContext(ctx, `SELECT `+releasesql.ReleaseSummaryProjection+` FROM `+releasesql.ReleaseSource+
		releasesql.ReleaseSummaryArtworkJoin+` WHERE releases.id=ANY($1::uuid[]) AND `+releasesql.ReleaseVisiblePredicate, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanReleaseSummary(rows)
		if err != nil {
			return nil, err
		}
		result[item.ID] = item
	}
	return result, rows.Err()
}
