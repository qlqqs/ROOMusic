import { useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import { useViewerScope } from "../../features/auth";
import { useCatalogRefresh } from "../../features/catalog";
import { scanReportsQueryKey } from "../../features/library";
import { listeningKeys } from "../../features/listening";

/**
 * 修改专辑数据（字段、归组、撤销）之后要重新读取的缓存：目录与版本详情，本人的播放历史（概览的“最近播放”与历史页显示的专辑），
 * 以及扫描报告（新增专辑现在所在的专辑与“可能接替了……”会随认领、归组变化）。
 * 由 app 组合：governance 不知道 catalog 与 listening 的查询键。返回稳定的函数，可以放进 hook 的依赖。
 */
export function useReleaseDataRefresh(): () => Promise<unknown> {
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  const refreshCatalog = useCatalogRefresh();
  return useCallback(
    () =>
      Promise.all([
        refreshCatalog(),
        queryClient.invalidateQueries({ queryKey: listeningKeys.all(generation) }),
        queryClient.invalidateQueries({ queryKey: scanReportsQueryKey(generation) }),
      ]),
    [refreshCatalog, queryClient, generation],
  );
}
