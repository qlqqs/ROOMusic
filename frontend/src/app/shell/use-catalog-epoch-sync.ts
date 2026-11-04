import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { useViewerScope } from "../../features/auth";
import { useCatalogRefresh, useCatalogState } from "../../features/catalog";
import { epochOfSearchKey, searchKeys } from "../../features/search";

/**
 * 已登录外壳读取目录状态（挂载、回到前台、准备期间每 3 秒），epoch 变大时：取消还在路上的旧代次搜索请求，
 * 并让目录缓存失效（专辑页带搜索词的结果换成新的匹配方式）。搜索自己的查询键带 epoch，新代次自然重新请求；
 * 不动地址里的查询词与页码，不碰播放器。由 app 协调：catalog 与 search 互不知道对方的查询键。
 */
export function useCatalogEpochSync(): void {
  const state = useCatalogState();
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  const refreshCatalog = useCatalogRefresh();
  const epoch = state.data?.epoch;
  const seen = useRef<number | undefined>(undefined);
  useEffect(() => {
    if (epoch === undefined) return;
    const previous = seen.current;
    seen.current = epoch;
    if (previous === undefined || epoch <= previous) return;
    void queryClient.cancelQueries({
      queryKey: searchKeys.all(generation),
      predicate: (query) => {
        const keyEpoch = epochOfSearchKey(query.queryKey);
        return keyEpoch !== null && keyEpoch < epoch;
      },
    });
    void refreshCatalog();
  }, [epoch, queryClient, generation, refreshCatalog]);
}
