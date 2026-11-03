package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
)

// 按组读取：把可见 Release 按 group_id 归拢，给出成员数、attention 之和与代表版本。组的可见性、搜索与 attention
// 语义都建立在 /releases 使用的同一批 SQL 常量之上，因此每组一个版本时，列表的总数、顺序与内容和 /releases 逐项对应。

// maxReleaseGroupMembers 是组详情返回的成员摘要上限；release_count 仍是完整数量。
const maxReleaseGroupMembers = 100

// ErrReleaseGroupNotFound 表示 ReleaseGroup 不存在，或它没有任何可见成员。
var ErrReleaseGroupNotFound = errors.New("release group not found")

const (
	// releaseGroupAttentionExpression 是组内全部可见成员的 attention 之和。子查询自己的 FROM 定义了 releases，
	// 常量里的 releases.* 都解析到子查询自己的行，而不是外层的代表版本。
	releaseGroupAttentionExpression = `(SELECT COALESCE(SUM(` + releasesql.ReleaseAttentionExpression + `), 0)::bigint
		FROM ` + releasesql.ReleaseSource + ` WHERE releases.group_id = ranked.group_id AND ` + releasesql.ReleaseVisiblePredicate + `)`

	// releaseGroupAnyMemberMatchesSearch 在任一可见成员命中 /releases 的搜索条件时为真。
	releaseGroupAnyMemberMatchesSearch = `EXISTS (SELECT 1 FROM ` + releasesql.ReleaseSource + `
		WHERE releases.group_id = ranked.group_id AND ` + releasesql.ReleaseVisiblePredicate + ` AND ` + releaseSearchPredicate + `)`
)

// releaseGroupRankedCTE 为每个可见 Release 标出它在组内的名次（1 为代表版本）与组内可见成员数。onlyGroup 为真时只看
// 参数 $1 指定的组（详情）。
func releaseGroupRankedCTE(onlyGroup bool) string {
	groupFilter := ""
	if onlyGroup {
		groupFilter = " AND releases.group_id = $1::uuid"
	}
	return `ranked AS (
		SELECT releases.id, releases.group_id,
		       ROW_NUMBER() OVER (PARTITION BY releases.group_id ORDER BY ` + releasesql.ReleaseGroupMemberOrder + `) AS member_rank,
		       COUNT(*) OVER (PARTITION BY releases.group_id) AS release_count
		FROM ` + releasesql.ReleaseSource + `
		WHERE ` + releasesql.ReleaseVisiblePredicate + groupFilter + `
	)`
}

// ReleaseGroupPage 是一页组摘要和满足条件的组总数；Items 永远非 nil。
type ReleaseGroupPage struct {
	Items []ReleaseGroupSummary
	Total int64
}

// ListReleaseGroups 返回至少有一个可见成员的组的一页。排序取代表版本的标题、艺人与 ID，与 /releases 的排序一致；
// 任一可见成员命中搜索、可见成员的 attention 之和大于 0 时组才命中对应的筛选。查询执行失败时错误包含 ErrQuery。
func (s *Store) ListReleaseGroups(ctx context.Context, params ListParams) (ReleaseGroupPage, error) {
	filters := ""
	queryArgs := []any{}
	if params.Search != "" {
		filters += " AND " + releaseGroupAnyMemberMatchesSearch
		queryArgs = append(queryArgs, escapeLikePattern(params.Search))
	}
	if params.AttentionRequired {
		filters += " AND " + releaseGroupAttentionExpression + " > 0"
	}

	var total int64
	if err := s.database.QueryRowContext(ctx, "WITH "+releaseGroupRankedCTE(false)+" SELECT COUNT(*) FROM ranked WHERE ranked.member_rank = 1"+filters, queryArgs...).Scan(&total); err != nil {
		return ReleaseGroupPage{}, fmt.Errorf("%w: count release groups: %w", ErrQuery, err)
	}
	listQuery := "WITH " + releaseGroupRankedCTE(false) + " SELECT " + releaseGroupSummaryProjection +
		" FROM ranked JOIN " + releasesql.ReleaseSource + " ON releases.id = ranked.id" + releasesql.ReleaseSummaryArtworkJoin +
		" WHERE ranked.member_rank = 1" + filters +
		" ORDER BY LOWER(releases.title),LOWER(releases.artist),releases.id LIMIT $" + strconv.Itoa(len(queryArgs)+1) +
		" OFFSET $" + strconv.Itoa(len(queryArgs)+2)
	listArgs := append(append([]any{}, queryArgs...), params.PageSize, (params.Page-1)*params.PageSize)
	rows, err := s.database.QueryContext(ctx, listQuery, listArgs...)
	if err != nil {
		return ReleaseGroupPage{}, fmt.Errorf("%w: list release groups: %w", ErrQuery, err)
	}
	defer rows.Close()
	items := make([]ReleaseGroupSummary, 0, params.PageSize)
	for rows.Next() {
		item, scanErr := scanReleaseGroupSummary(rows)
		if scanErr != nil {
			return ReleaseGroupPage{}, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return ReleaseGroupPage{}, err
	}
	return ReleaseGroupPage{Items: items, Total: total}, nil
}

// LoadReleaseGroup 读取一个组的摘要、成员摘要（按代表版本规则排序，最多 maxReleaseGroupMembers 条）、人工修改版本号与生效中的
// 默认版本。组不存在或没有可见成员时返回 ErrReleaseGroupNotFound。
func (s *Store) LoadReleaseGroup(ctx context.Context, groupID string) (ReleaseGroupDetail, error) {
	summary, err := scanReleaseGroupSummary(s.database.QueryRowContext(ctx,
		"WITH "+releaseGroupRankedCTE(true)+" SELECT "+releaseGroupSummaryProjection+
			" FROM ranked JOIN "+releasesql.ReleaseSource+" ON releases.id = ranked.id"+releasesql.ReleaseSummaryArtworkJoin+
			" WHERE ranked.member_rank = 1",
		groupID))
	if errors.Is(err, sql.ErrNoRows) {
		return ReleaseGroupDetail{}, ErrReleaseGroupNotFound
	}
	if err != nil {
		return ReleaseGroupDetail{}, err
	}
	rows, err := s.database.QueryContext(ctx,
		"SELECT "+releasesql.ReleaseSummaryProjection+" FROM "+releasesql.ReleaseSource+releasesql.ReleaseSummaryArtworkJoin+
			" WHERE releases.group_id = $1::uuid AND "+releasesql.ReleaseVisiblePredicate+
			" ORDER BY "+releasesql.ReleaseGroupMemberOrder+" LIMIT $2",
		groupID, maxReleaseGroupMembers)
	if err != nil {
		return ReleaseGroupDetail{}, err
	}
	defer rows.Close()
	members := make([]ReleaseSummary, 0, maxReleaseGroupMembers)
	for rows.Next() {
		member, scanErr := scanReleaseSummary(rows)
		if scanErr != nil {
			return ReleaseGroupDetail{}, scanErr
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return ReleaseGroupDetail{}, err
	}
	detail := ReleaseGroupDetail{ReleaseGroupSummary: summary, Releases: members}
	var stored sql.NullString
	if err := s.database.QueryRowContext(ctx, `SELECT edit_revision, default_release_id::text FROM release_groups WHERE id = $1::uuid`, groupID).
		Scan(&detail.Revision, &stored); err != nil {
		return ReleaseGroupDetail{}, err
	}
	// 已存的默认版本生效，当且仅当它是排在最前的可见成员：这与成员顺序用的是同一条规则，所以 default_release_id
	// 与 releases[0] 在同一份响应里总是一致的。
	if stored.Valid && len(members) > 0 && members[0].ID == stored.String {
		detail.DefaultReleaseID = &stored.String
	}
	return detail, nil
}

// releaseGroupSummaryProjection 是代表版本的摘要投影，后面追加组 ID、可见成员数与 attention 之和。
const releaseGroupSummaryProjection = releasesql.ReleaseSummaryProjection + `,
		ranked.group_id::text,
		ranked.release_count,
		` + releaseGroupAttentionExpression

func scanReleaseGroupSummary(row interface{ Scan(...any) error }) (ReleaseGroupSummary, error) {
	var columns releaseSummaryColumns
	var group ReleaseGroupSummary
	if err := row.Scan(append(columns.targets(), &group.ID, &group.ReleaseCount, &group.AttentionCount)...); err != nil {
		return ReleaseGroupSummary{}, err
	}
	representative, err := columns.summary()
	if err != nil {
		return ReleaseGroupSummary{}, err
	}
	group.Representative = representative
	return group, nil
}
