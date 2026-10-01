import { useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { fetchActiveScan } from "../api/endpoints";
import { libraryKeys } from "../query-keys";

/** 有活动扫描时的刷新间隔：让概览与扫描目录页在扫描结束后自己更新；页面不可见时 TanStack Query 会暂停。 */
export const ACTIVE_SCAN_POLL_MS = 5_000;

/** 最早开始的运行中扫描；没有时为 null。任何已登录用户都可以读取。只在确有活动扫描时才轮询。 */
export function useActiveScan() {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: libraryKeys.activeScan(generation),
    queryFn: ({ signal }) => fetchActiveScan(signal),
    select: (response) => response.data.scan,
    refetchInterval: (query) => (query.state.data?.data.scan ? ACTIVE_SCAN_POLL_MS : false),
  });
}
