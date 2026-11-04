import { Link } from "@tanstack/react-router";
import { Panel, QueryContent } from "../../../shared/ui";
import { useRecentlyAdded } from "../hooks/use-recently-added";
import { AlbumGrid, AlbumGridSkeleton } from "./album-grid";

/** 卡片带进详情页的列表参数：“返回专辑列表”回到按加入时间排序的专辑库，与“查看全部”一致。 */
const recentListSearch = { sort: "added_at_desc" } as const;

/**
 * 概览的“最近新增”：按加入时间倒序最多 8 张专辑，卡片与专辑库一致；“查看全部”进入按“最近新增”排序的专辑库。
 * 加载与失败照常显示；成功但没有专辑时整块不渲染。
 */
export function RecentlyAddedPanel() {
  const recent = useRecentlyAdded();
  if (recent.status === "success" && recent.data.length === 0) return null;
  return (
    <Panel
      title="最近新增"
      action={
        <Link to="/albums" search={recentListSearch}>
          查看全部
        </Link>
      }
    >
      <QueryContent query={recent} loading={<AlbumGridSkeleton count={4} />}>
        {(groups) => <AlbumGrid groups={groups} search={recentListSearch} />}
      </QueryContent>
    </Panel>
  );
}
