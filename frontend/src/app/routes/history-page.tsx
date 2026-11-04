import { useCallback } from "react";
import { HistoryView, type HistoryItemDTO } from "../../features/listening";
import { HistoryPlayAll, HistoryTrackActions } from "./history-playback";

/**
 * “我的音乐”的历史页签：listening 给出列表、移除与清除，播放动作（播放全部、行内播放与“更多”菜单里的入队）由这里经插槽组合播放器。
 * page 来自地址（2 及以上才写进地址）。
 */
export function HistoryPage({ page }: { page: number }) {
  // 插槽保持同一个函数引用，表格的列定义才不会每次重建。
  const renderPlayAll = useCallback(() => <HistoryPlayAll />, []);
  const renderTrackActions = useCallback((item: HistoryItemDTO, requestRemove: () => void) => <HistoryTrackActions item={item} onRemove={requestRemove} />, []);
  return <HistoryView page={page} renderPlayAll={renderPlayAll} renderTrackActions={renderTrackActions} />;
}
