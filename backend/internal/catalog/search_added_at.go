package catalog

import (
	"context"
	"strconv"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
)

// searchGroupsByAddedAt 保留显式加入时间排序；搜索只决定完整匹配集合，不改变时间顺序。
func (s *Store) searchGroupsByAddedAt(ctx context.Context, plan searchPlan, params ListParams, total int64) (ReleaseGroupPage, error) {
	result := ReleaseGroupPage{Items: []ReleaseGroupSummary{}, Total: total}
	query := "WITH " + releaseGroupRankedCTE(false) + " SELECT " + releaseGroupSummaryProjection +
		" FROM ranked JOIN " + releasesql.ReleaseSource + " ON releases.id=ranked.id" + releasesql.ReleaseSummaryArtworkJoin +
		" WHERE ranked.member_rank=1 AND " + plan.normalizedEntityMatchSQL("ranked.group_id", "", plan.matchPredicate)
	if params.AttentionRequired {
		query += " AND " + releaseGroupAttentionExpression + ">0"
	}
	query += " ORDER BY " + releaseGroupOrder(params.Sort) + " LIMIT $" + strconv.Itoa(len(plan.args)+1) + " OFFSET $" + strconv.Itoa(len(plan.args)+2)
	args := append(append([]any{}, plan.args...), params.PageSize, (params.Page-1)*params.PageSize)
	rows, err := s.database.QueryContext(ctx, plan.withArtistAliases(query), args...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanReleaseGroupSummary(rows)
		if err != nil {
			return result, err
		}
		result.Items = append(result.Items, item)
	}
	return result, rows.Err()
}
