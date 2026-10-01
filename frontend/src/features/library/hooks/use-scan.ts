import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { useViewerScope } from "../../auth";
import { fetchScan } from "../api/endpoints";
import { libraryKeys } from "../query-keys";
import { isScanFinished } from "../scan-status";

/** 运行中扫描的轮询间隔：页面不可见时 TanStack Query 暂停，到终态即停止。 */
export const SCAN_POLL_MS = 2_000;

/**
 * 指定扫描的状态。只有运行中（含取消请求中）才轮询；一旦读到终态就停止，并让活动扫描的缓存过期，
 * 这样回到扫描目录页时不会还显示一个已经结束的“活动扫描”。
 */
export function useScan(scanId: string) {
  const { generation } = useViewerScope();
  const queryClient = useQueryClient();
  const query = useQuery({
    queryKey: libraryKeys.scan(generation, scanId),
    queryFn: ({ signal }) => fetchScan(scanId, signal),
    select: (response) => response.data,
    // 轮询需要每次都取新值；页面重新可见时也要立刻刷新，不等 30 秒的默认保鲜期。
    staleTime: 0,
    refetchInterval: (current) => {
      const scan = current.state.data?.data;
      return scan !== undefined && !isScanFinished(scan) ? SCAN_POLL_MS : false;
    },
  });

  const finished = query.data !== undefined && isScanFinished(query.data);
  useEffect(() => {
    if (finished) void queryClient.invalidateQueries({ queryKey: libraryKeys.activeScan(generation) });
  }, [finished, generation, queryClient]);

  return query;
}
