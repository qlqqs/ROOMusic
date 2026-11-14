import { Link } from "@tanstack/react-router";
import { ChevronRight } from "lucide-react";
import { Panel, QueryContent, useShelf } from "../../../shared/ui";
import { MAX_RECENTLY_ADDED } from "../api/decoders";
import { useRecentlyAdded } from "../hooks/use-recently-added";
import { AlbumGrid, AlbumGridSkeleton } from "./album-grid";
import styles from "./recently-added-panel.module.css";

/** 卡片带进详情页的列表参数：“返回专辑列表”回到按加入时间排序的专辑库，与“全部专辑”一致。 */
const recentListSearch = { sort: "added_at_desc" } as const;

/**
 * 概览的“最近新增”：按加入时间倒序的专辑（接口给最多 8 张），一行横排，放不下时横向滚动、标题行有翻页按钮，不换行也不落单；
 * 卡片与专辑库一致。“全部专辑”进入按“最近新增”排序的专辑库。加载与失败照常显示；成功但没有专辑时整块不渲染。
 */
export function RecentlyAddedPanel() {
  const recent = useRecentlyAdded();
  const shelf = useShelf("最近新增");
  if (recent.status === "success" && recent.data.length === 0) return null;
  return (
    <Panel
      title="最近新增"
      action={
        <span className={styles.actions}>
          <Link to="/albums" search={recentListSearch} className={styles.all}>
            全部专辑
            <ChevronRight size={16} aria-hidden="true" />
          </Link>
          {shelf.controls}
        </span>
      }
    >
      <QueryContent
        query={recent}
        loading={
          <div className={shelf.hostClassName}>
            <AlbumGridSkeleton count={MAX_RECENTLY_ADDED} />
          </div>
        }
      >
        {(groups) => (
          <div className={shelf.hostClassName}>
            <AlbumGrid groups={groups} search={recentListSearch} listRef={shelf.ref} />
          </div>
        )}
      </QueryContent>
    </Panel>
  );
}
