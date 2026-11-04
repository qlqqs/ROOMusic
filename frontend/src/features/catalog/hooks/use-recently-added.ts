import { useQuery } from "@tanstack/react-query";
import { failureOf, isHttpFailure } from "../../../shared/api";
import { useViewerScope } from "../../auth";
import { fetchRecentlyAdded } from "../api/endpoints";
import type { ReleaseGroupSummaryWithAddedAtDTO } from "../api/decoders";
import { catalogKeys } from "../query-keys";

/**
 * 概览的“最近新增”（所有登录用户，最多 8 张，按加入时间倒序）。扫描进行中与结束时由 library 让 catalog 整体失效，这里不自己轮询。
 * 接口不存在（404，服务还是旧版本）时得到空列表，区块随之隐藏；其他失败照常报错，不当成“没有新增”。
 */
export function useRecentlyAdded() {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: catalogKeys.recentlyAdded(generation),
    queryFn: async ({ signal }): Promise<readonly ReleaseGroupSummaryWithAddedAtDTO[]> => {
      try {
        return (await fetchRecentlyAdded(signal)).data.items;
      } catch (error) {
        if (isHttpFailure(failureOf(error), 404)) return [];
        throw error;
      }
    },
  });
}
