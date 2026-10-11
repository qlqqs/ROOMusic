import { queryOptions, useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { fetchRecentReleases } from "../api/endpoints";
import { listeningKeys } from "../query-keys";

export function recentReleasesQueryOptions(generation: number) {
  return queryOptions({
    queryKey: listeningKeys.recentReleases(generation),
    queryFn: ({ signal }) => fetchRecentReleases(signal),
  });
}

/** 概览的“最近播放”：当前用户最近播放的发行（最多 8 个，每个一次）与是否有任何历史。 */
export function useRecentReleases() {
  const { generation } = useViewerScope();
  return useQuery({ ...recentReleasesQueryOptions(generation), select: (response) => response.data });
}
