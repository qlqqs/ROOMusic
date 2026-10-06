import { useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import { useViewerScope } from "../../auth";
import type { ReleaseDetailDTO } from "../api/decoders";
import { fetchRelease } from "../api/endpoints";
import { catalogKeys } from "../query-keys";

/**
 * 按需读取发行详情的函数（给播放器恢复队列用）：与详情页共用同一个查询缓存条目，所以看过的专辑不会重复请求，
 * 请求仍经 apiRequest，401 照常结束会话。返回的函数在同一个查看者范围内保持稳定。
 */
export function useReleaseLoader(): (releaseId: string) => Promise<ReleaseDetailDTO> {
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  return useCallback(
    async (releaseId) => {
      const response = await queryClient.fetchQuery({
        queryKey: catalogKeys.release(generation, releaseId),
        queryFn: ({ signal }) => fetchRelease(releaseId, signal),
        staleTime: 30_000,
      });
      return response.data;
    },
    [queryClient, generation],
  );
}
