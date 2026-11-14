import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useCallback, useSyncExternalStore } from "react";
import type { ApiResponse } from "../../../shared/api/request";
import { useViewerScope } from "../../auth";
import type { ClaimPageDTO, IssuePageDTO } from "../api/decoders";
import { governanceKeys } from "../query-keys";

/**
 * 缓存里最近一次读到的一页里取出的数（没有读过时为 null）。只看缓存，不发请求、不保活缓存条目：
 * 管理员打开过“元数据”“待认领”之后才有数字，缓存回收之后数字随之消失，不显示猜测的数。
 */
function latestCount<T>(client: QueryClient, queryKey: readonly unknown[], pick: (data: T) => number): number | null {
  let latest: { at: number; count: number } | null = null;
  for (const query of client.getQueryCache().findAll({ queryKey })) {
    const data = query.state.data as ApiResponse<T> | undefined;
    if (data === undefined) continue;
    if (latest === null || query.state.dataUpdatedAt > latest.at) latest = { at: query.state.dataUpdatedAt, count: pick(data.data) };
  }
  return latest?.count ?? null;
}

function useCachedCount<T>(queryKey: readonly unknown[], pick: (data: T) => number): number | null {
  const client = useQueryClient();
  const subscribe = useCallback((onChange: () => void) => client.getQueryCache().subscribe(onChange), [client]);
  // 快照是数字或 null，缓存的其他变化不会让订阅者重绘。
  return useSyncExternalStore(subscribe, () => latestCount(client, queryKey, pick));
}

const openIssuesOf = (page: IssuePageDTO) => page.summary.open.all;
const claimsOf = (page: ClaimPageDTO) => page.pagination.total;

/**
 * 侧栏待办入口旁的数字：待处理的问题数（问题列表任一页的 summary，不随筛选与分页变化）与待认领的旧版本数（列表的 total），
 * 都取自本会话已经读到的列表；没有读过时为 null，调用方不显示。
 */
export function useCachedTodoCounts(): { issues: number | null; claims: number | null } {
  const { generation } = useViewerScope();
  const issues = useCachedCount(governanceKeys.issues(generation), openIssuesOf);
  const claims = useCachedCount(governanceKeys.claims(generation), claimsOf);
  return { issues, claims };
}
