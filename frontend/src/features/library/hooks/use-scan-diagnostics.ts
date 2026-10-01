import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { useViewerScope } from "../../auth";
import { fetchScanDiagnostics } from "../api/endpoints";
import { libraryKeys } from "../query-keys";

/**
 * 扫描的诊断（管理员，最多 100 条明细）。不轮询：运行中由用户手动刷新，扫描从运行变为终态时自动刷新一次。
 * finished 是该扫描当前是否已结束；一进来就是终态时只有首次加载，不会多刷新。
 */
export function useScanDiagnostics(scanId: string, finished: boolean) {
  const { generation } = useViewerScope();
  const queryClient = useQueryClient();
  const query = useQuery({
    queryKey: libraryKeys.diagnostics(generation, scanId),
    queryFn: ({ signal }) => fetchScanDiagnostics(scanId, signal),
    select: (response) => response.data,
  });

  const wasFinished = useRef(finished);
  useEffect(() => {
    if (!wasFinished.current && finished) {
      void queryClient.invalidateQueries({ queryKey: libraryKeys.diagnostics(generation, scanId) });
    }
    wasFinished.current = finished;
  }, [finished, generation, queryClient, scanId]);

  return query;
}
