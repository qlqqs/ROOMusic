import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { useViewerScope } from "../../auth";
import { fetchActiveScan } from "../api/endpoints";
import { libraryKeys } from "../query-keys";

/** 有活动扫描时的刷新间隔：让概览与扫描目录页在扫描结束后自己更新；页面不可见时 TanStack Query 会暂停。 */
export const ACTIVE_SCAN_POLL_MS = 5_000;

/**
 * 最早开始的运行中扫描；没有时为 null。任何已登录用户都可以读取。只在确有活动扫描时才轮询。
 * 活动扫描消失（扫描结束）或换成另一个时，让扫描记录过期：扫描记录自己不轮询，靠这里知道哪一条变了。
 */
export function useActiveScan() {
  const { generation } = useViewerScope();
  const queryClient = useQueryClient();
  const query = useQuery({
    queryKey: libraryKeys.activeScan(generation),
    queryFn: ({ signal }) => fetchActiveScan(signal),
    select: (response) => response.data.scan,
    refetchInterval: (current) => (current.state.data?.data.scan ? ACTIVE_SCAN_POLL_MS : false),
  });

  const activeId = query.data === undefined ? undefined : (query.data?.id ?? null);
  const previousId = useRef<string | null>(null);
  useEffect(() => {
    if (activeId === undefined) return;
    if (previousId.current !== null && previousId.current !== activeId) {
      void queryClient.invalidateQueries({ queryKey: libraryKeys.scanHistory(generation) });
    }
    previousId.current = activeId;
  }, [activeId, generation, queryClient]);

  return query;
}
