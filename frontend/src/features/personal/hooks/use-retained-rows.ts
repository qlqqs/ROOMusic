import { useState } from "react";

type Snapshot<T> = { token: string; order: readonly string[]; rows: ReadonlyMap<string, T> };

export type RetainedRows<T> = {
  /** 要显示的行：第一次读到时的顺序；服务端已经没有的行（刚取消收藏的）留在原处，用最后一次读到的内容。 */
  rows: readonly T[];
  /** 服务端有了这次没显示的新行（例如别处刚收藏的）：提示用户刷新，不自动挤进来。 */
  hasNew: boolean;
  /** 丢掉保留的行，按服务端现在的列表重新显示。 */
  refresh: () => void;
};

/**
 * 收藏页的“保留行”：在这一页里取消收藏后，这一行留在原处（心形变空心），后台重新读取、窗口重新聚焦都不让它消失，
 * 误点可以马上点回来；离开这一页（换页、换栏）、按“刷新”或重新加载页面才按服务端的列表重新显示。
 * 服务端的查询仍是真实列表，这里只是显示层，不往缓存里塞伪造的收藏。
 * items 为 undefined（还没读到，或显示的是翻页时保留的上一页）时不建立快照。
 */
export function useRetainedRows<T>(items: readonly T[] | undefined, idOf: (row: T) => string, scope: string): RetainedRows<T> {
  const [refreshCount, setRefreshCount] = useState(0);
  const token = `${scope}#${refreshCount}`;
  const [snapshot, setSnapshot] = useState<Snapshot<T> | null>(null);
  const refresh = () => setRefreshCount((count) => count + 1);
  let current = snapshot;
  if (items !== undefined && (current === null || current.token !== token)) {
    current = { token, order: items.map(idOf), rows: new Map(items.map((row) => [idOf(row), row])) };
    // 渲染期间按新的范围重建快照（React 认可的“记住上一次渲染的信息”写法），下一次渲染就用它。
    setSnapshot(current);
  }
  if (current === null) return { rows: [], hasNew: false, refresh };
  if (items === undefined) return { rows: current.order.flatMap((id) => current.rows.get(id) ?? []), hasNew: false, refresh };
  const latest = new Map(items.map((row) => [idOf(row), row]));
  const shown = new Set(current.order);
  const snapshotRows = current.rows;
  return {
    rows: current.order.flatMap((id) => latest.get(id) ?? snapshotRows.get(id) ?? []),
    hasNew: items.some((row) => !shown.has(idOf(row))),
    refresh,
  };
}
