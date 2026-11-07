import { useCallback, type ReactNode } from "react";
import { HistoryView, type HistoryItemDTO } from "../../features/listening";
import { Page } from "../../shared/ui";
import { HistoryPlayAll, HistoryTrackActions } from "./history-playback";
import { TrackFavoritesScope } from "./track-favorites-scope";

/**
 * 播放历史：listening 给出列表、移除与清除；播放全部、每行的播放、心形与“更多”菜单由这里经插槽组合，
 * 这一页的收藏状态一次读出（包住表格的范围）。
 * page 来自地址（2 及以上才写进地址）。
 */
export function HistoryPage({ page }: { page: number }) {
  // 插槽保持同一个函数引用，表格的列定义才不会每次重建。
  const renderPlayAll = useCallback(() => <HistoryPlayAll />, []);
  const renderTrackActions = useCallback((item: HistoryItemDTO, requestRemove: () => void) => <HistoryTrackActions item={item} onRemove={requestRemove} />, []);
  const renderRowsScope = useCallback((items: readonly HistoryItemDTO[], children: ReactNode) => <TrackFavoritesScope tracks={items}>{children}</TrackFavoritesScope>, []);
  return (
    <Page title="播放历史">
      <HistoryView page={page} renderPlayAll={renderPlayAll} renderTrackActions={renderTrackActions} renderRowsScope={renderRowsScope} />
    </Page>
  );
}
