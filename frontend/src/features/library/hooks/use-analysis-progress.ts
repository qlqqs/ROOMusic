import { useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { fetchAnalysisProgress } from "../api/endpoints";
import { libraryKeys } from "../query-keys";

/** 分析进行中（或等扫描结束后继续）时的刷新间隔；页面不可见时 TanStack Query 暂停轮询。 */
export const ANALYSIS_PROGRESS_POLL_MS = 60_000;

/** 后台音频分析的进度。只在 running、waiting_for_scan 时轮询，空闲与未运行时不再刷新。 */
export function useAnalysisProgress() {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: libraryKeys.analysisProgress(generation),
    queryFn: async ({ signal }) => (await fetchAnalysisProgress(signal)).data,
    refetchInterval: (query) => (query.state.data?.state === "running" || query.state.data?.state === "waiting_for_scan" ? ANALYSIS_PROGRESS_POLL_MS : false),
  });
}
