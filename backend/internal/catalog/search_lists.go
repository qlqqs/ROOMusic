package catalog

import (
	"context"
	"fmt"
)

// ListReleases 无 q 时保持原浏览路径；有 q 时在同一快照选择旧匹配或规范化相关性。
func (s *Store) ListReleases(ctx context.Context, params ListParams) (ReleasePage, error) {
	if params.Search == "" {
		return s.listReleasesLegacy(ctx, params)
	}
	return searchSnapshot(ctx, s, func(snapshot *Store, state CatalogState, generation int64) (ReleasePage, error) {
		if state.SearchMode == "legacy" {
			return snapshot.listReleasesLegacy(ctx, params)
		}
		result := ReleasePage{Items: []ReleaseSummary{}}
		plan := newSearchPlan(params.Search, "releases", state.SearchMode, generation)
		plan.artistAliases = state.ArtistsState == "ready"
		if plan.empty {
			return result, nil
		}
		plan, err := snapshot.prepareSearchPlan(ctx, plan)
		if err != nil {
			return result, err
		}
		if err := snapshot.database.QueryRowContext(ctx, plan.countSQL(false, params.AttentionRequired), plan.args...).Scan(&result.Total); err != nil {
			return result, err
		}
		hits, _, err := snapshot.searchPageFiltered(ctx, plan, params.PageSize, (params.Page-1)*params.PageSize, params.AttentionRequired)
		if err != nil {
			return result, err
		}
		ids := make([]string, len(hits))
		for i, hit := range hits {
			ids[i] = hit.ID
		}
		summaries, err := snapshot.loadSearchReleases(ctx, ids)
		if err != nil {
			return result, err
		}
		for _, hit := range hits {
			summary, ok := summaries[hit.ID]
			if !ok {
				return result, fmt.Errorf("search release summary unavailable")
			}
			result.Items = append(result.Items, summary)
		}
		return result, nil
	})
}

// ListReleaseGroups 的总数仍精确，代表版本与 attention 保持原合同。
func (s *Store) ListReleaseGroups(ctx context.Context, params ListParams) (ReleaseGroupPage, error) {
	if params.Search == "" {
		return s.listReleaseGroupsLegacy(ctx, params)
	}
	return searchSnapshot(ctx, s, func(snapshot *Store, state CatalogState, generation int64) (ReleaseGroupPage, error) {
		return snapshot.listReleaseGroupsSearch(ctx, params, state, generation)
	})
}

// listReleaseGroupsSearch 只在调用方已建立的只读快照中执行，避免加入时间入口嵌套事务。
func (snapshot *Store) listReleaseGroupsSearch(ctx context.Context, params ListParams, state CatalogState, generation int64) (ReleaseGroupPage, error) {
	if state.SearchMode == "legacy" {
		return snapshot.listReleaseGroupsLegacy(ctx, params)
	}
	result := ReleaseGroupPage{Items: []ReleaseGroupSummary{}}
	plan := newSearchPlan(params.Search, "albums", state.SearchMode, generation)
	plan.artistAliases = state.ArtistsState == "ready"
	if plan.empty {
		return result, nil
	}
	plan, err := snapshot.prepareSearchPlan(ctx, plan)
	if err != nil {
		return result, err
	}
	if err := snapshot.database.QueryRowContext(ctx, plan.countSQL(false, params.AttentionRequired), plan.args...).Scan(&result.Total); err != nil {
		return result, err
	}
	if params.Sort == ReleaseGroupSortAddedAtDesc {
		return snapshot.searchGroupsByAddedAt(ctx, plan, params, result.Total)
	}
	hits, _, err := snapshot.searchPageFiltered(ctx, plan, params.PageSize, (params.Page-1)*params.PageSize, params.AttentionRequired)
	if err != nil {
		return result, err
	}
	ids := make([]string, len(hits))
	for i, hit := range hits {
		ids[i] = hit.ID
	}
	summaries, err := snapshot.loadSearchGroups(ctx, ids)
	if err != nil {
		return result, err
	}
	for _, hit := range hits {
		summary, ok := summaries[hit.ID]
		if !ok {
			return result, fmt.Errorf("search group summary unavailable")
		}
		result.Items = append(result.Items, summary)
	}
	return result, nil
}
