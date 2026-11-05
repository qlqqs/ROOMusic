import { useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import type { CatalogStateDTO } from "../api/decoders";
import { fetchCatalogStatus } from "../api/endpoints";
import { catalogStateKeys } from "../query-keys";

/** 新搜索或艺人数据还在后台准备时，每隔这么久看一次是否已经好了。 */
export const CATALOG_STATE_POLL_MS = 3_000;

/** 还有能力在后台准备：新搜索未切换，或艺人数据正在整理。④ 时 artists_state 固定为 unavailable，不算在准备。 */
export function isCatalogPreparing(state: CatalogStateDTO): boolean {
  return state.search_mode === "legacy" || state.artists_state === "preparing";
}

/**
 * 目录状态（服务器状态，会话内共享一个条目）。准备期间每 3 秒轻量轮询，页面不可见时暂停（Query 默认），全部就绪后停止；
 * 回到前台时总是重读一次。多个组件同时使用时只有第一次挂载发请求。
 */
export function useCatalogState() {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: catalogStateKeys.current(generation),
    queryFn: ({ signal }) => fetchCatalogStatus(signal),
    select: (response) => response.data,
    refetchInterval: (query) => {
      const data = query.state.data?.data;
      return data !== undefined && isCatalogPreparing(data) ? CATALOG_STATE_POLL_MS : false;
    },
    refetchOnMount: false,
    refetchOnWindowFocus: "always",
  });
}
