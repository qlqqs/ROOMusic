import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { Panel, QueryContent } from "../../../shared/ui";
import { MAX_RECENTLY_ADDED, type ReleaseGroupSummaryWithAddedAtDTO } from "../api/decoders";
import { useGridColumns } from "../hooks/use-grid-columns";
import { useRecentlyAdded } from "../hooks/use-recently-added";
import { AlbumGrid, AlbumGridSkeleton } from "./album-grid";

/** 卡片带进详情页的列表参数：“返回专辑列表”回到按加入时间排序的专辑库，与“查看全部”一致。 */
const recentListSearch = { sort: "added_at_desc" } as const;

/** 一行能放下这么多张及以上算宽屏，只显示一行；更窄时最多显示两行。 */
const WIDE_MIN_COLUMNS = 3;
const NARROW_MAX_ROWS = 2;

/**
 * 只显示能排满的行：宽屏一行，张数等于列数（最多 8）；窄屏（每行 1–2 张）最多两行。不知道列数时（还没量到）全部显示。
 * 多出的卡片不渲染，所以不会被 Tab 到，也不会被读屏读到；完整的列表在“查看全部”。
 */
export function recentlyAddedVisibleCount(columns: number | null, total: number): number {
  if (columns === null) return Math.min(total, MAX_RECENTLY_ADDED);
  const rows = columns >= WIDE_MIN_COLUMNS ? 1 : NARROW_MAX_ROWS;
  return Math.min(total, columns * rows, MAX_RECENTLY_ADDED);
}

function RecentGrid({ groups }: { groups: readonly ReleaseGroupSummaryWithAddedAtDTO[] }) {
  const [list, setList] = useState<HTMLUListElement | null>(null);
  const columns = useGridColumns(list);
  const visible = groups.slice(0, recentlyAddedVisibleCount(columns, groups.length));
  return <AlbumGrid groups={visible} search={recentListSearch} listRef={setList} />;
}

/** 加载占位：量同一个网格，张数按接口上限 8 张套用同一规则，数据到达时卡片数不变（数据更少时才变少）。 */
function RecentGridSkeleton() {
  const [list, setList] = useState<HTMLUListElement | null>(null);
  const columns = useGridColumns(list);
  return <AlbumGridSkeleton count={recentlyAddedVisibleCount(columns, MAX_RECENTLY_ADDED)} listRef={setList} />;
}

/**
 * 概览的“最近新增”：按加入时间倒序的专辑（接口给最多 8 张，只显示能排满的行），卡片与专辑库一致；“查看全部”进入按“最近新增”排序的专辑库。
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
      <QueryContent query={recent} loading={<RecentGridSkeleton />}>
        {(groups) => <RecentGrid groups={groups} />}
      </QueryContent>
    </Panel>
  );
}
