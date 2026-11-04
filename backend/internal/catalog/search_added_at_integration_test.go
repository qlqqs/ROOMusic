package catalog

import (
	"context"
	"testing"
)

func TestPostgreSQLNormalizedSearchPreservesExplicitAddedAtSort(t *testing.T) {
	f := newPlacementFixture(t)
	old := f.release("匹配")
	f.track(old, "旧曲", "present", 120)
	newer := f.release("较新代表")
	f.track(newer, "新曲", "present", 120)
	member := f.release("包含匹配词")
	f.track(member, "成员曲", "present", 120)
	hidden := f.release("不可见成员")
	f.track(hidden, "缺失曲", "missing", 120)
	f.exec(`UPDATE releases SET created_at='2020-01-01' WHERE id=$1::uuid`, old.releaseID)
	f.exec(`UPDATE releases SET created_at='2026-01-01' WHERE id=$1::uuid`, newer.releaseID)
	f.exec(`UPDATE releases SET group_id=$1::uuid,created_at='2025-01-01' WHERE id=$2::uuid`, newer.groupID, member.releaseID)
	f.exec(`UPDATE releases SET group_id=$1::uuid,created_at='2010-01-01' WHERE id=$2::uuid`, newer.groupID, hidden.releaseID)
	f.exec(`UPDATE release_groups SET default_release_id=$2::uuid WHERE id=$1::uuid`, newer.groupID, newer.releaseID)
	publishSearchFixture(t, f)
	ctx := context.Background()
	params := ListParams{Search: "匹配", Page: 1, PageSize: 1}
	ranked, err := f.store.ListReleaseGroups(ctx, params)
	if err != nil || ranked.Total != 2 || len(ranked.Items) != 1 || ranked.Items[0].ID != old.groupID {
		t.Fatalf("默认搜索相关性：%+v, %v", ranked, err)
	}
	params.Sort = ReleaseGroupSortAddedAtDesc
	page, err := ReadReleaseGroupsByAddedAt(ctx, f.database, params)
	if err != nil || page.Total != 2 || len(page.Items) != 1 || page.Items[0].ID != newer.groupID || page.Items[0].AddedAt.Year() != 2025 || page.Items[0].Representative.ID != newer.releaseID {
		t.Fatalf("按加入时间搜索：%+v, %v", page, err)
	}
	params.Page = 2
	page, err = f.store.ListReleaseGroups(ctx, params)
	if err != nil || page.Total != 2 || len(page.Items) != 1 || page.Items[0].ID != old.groupID {
		t.Fatalf("时间排序第二页：%+v, %v", page, err)
	}
	params.Page = 3
	page, err = f.store.ListReleaseGroups(ctx, params)
	if err != nil || page.Total != 2 || page.Items == nil || len(page.Items) != 0 {
		t.Fatalf("时间排序空页：%+v, %v", page, err)
	}
	params.Page, params.AttentionRequired = 1, true
	page, err = f.store.ListReleaseGroups(ctx, params)
	if err != nil || page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("时间排序问题过滤：%+v, %v", page, err)
	}
}
