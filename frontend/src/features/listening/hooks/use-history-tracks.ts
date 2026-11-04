import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { fetchHistoryTracks } from "../api/endpoints";
import { listeningKeys } from "../query-keys";

/** 历史页每页的曲目数（后端默认 50，上限 100）；显式传给后端，不依赖它的默认值。 */
export const HISTORY_PAGE_SIZE = 50;

/**
 * 历史页的一页：当前用户播放过的曲目，一首一行，按最后一次播放倒序。翻页时保留上一页直到新一页到达，表格不会闪空。
 * 上报、移除与清除之后由各自的命令让本人的历史查询失效。
 */
export function useHistoryTracks(page: number) {
  const { generation } = useViewerScope();
  const params = { page, pageSize: HISTORY_PAGE_SIZE };
  return useQuery({
    queryKey: listeningKeys.trackPage(generation, params),
    queryFn: ({ signal }) => fetchHistoryTracks({ ...params, signal }),
    select: (response) => response.data,
    placeholderData: keepPreviousData,
  });
}
