import { queryOptions, useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { fetchTrackAnalysis } from "../api/endpoints";
import type { TrackAnalysisDTO } from "../api/decoders";
import { playerKeys } from "../query-keys";

const UNFINISHED_STALE_MS = 5 * 60_000;

/**
 * 一首曲目的音频分析查询：完成的结果会话内不过期，还没完成或失败的 5 分钟后再读。波形组件与引擎（均衡、交叉渐变）共用这份缓存。
 * 读取失败不重试：一律按“没有分析结果”处理，不提示用户。
 */
export function trackAnalysisQuery(generation: number, trackId: string) {
  return queryOptions({
    queryKey: playerKeys.analysis(generation, trackId),
    queryFn: async ({ signal }): Promise<TrackAnalysisDTO> => (await fetchTrackAnalysis(trackId, signal)).data,
    staleTime: (query) => (query.state.data?.status === "done" ? Infinity : UNFINISHED_STALE_MS),
    retry: false,
  });
}

/** 当前曲目的分析结果；没有完成、读取失败或还在加载时是 null。 */
export function useTrackAnalysis(trackId: string | null): TrackAnalysisDTO["analysis"] {
  const { generation } = useViewerScope();
  const query = useQuery({ ...trackAnalysisQuery(generation, trackId ?? ""), enabled: trackId !== null });
  return query.data?.analysis ?? null;
}
