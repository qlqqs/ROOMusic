import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import type { CatalogStateDTO } from "../../catalog";
import { fetchAlbumResults, fetchArtistResults, fetchTrackResults } from "../api/endpoints";
import { searchKeys } from "../query-keys";
import { adoptNewerCatalogState, keepPreviousInSameEpoch, useSearchEpoch } from "./search-epoch";

/** 结果页每栏一页几条：专辑是卡片（两行左右），曲目是列表；两栏同在一页，第一屏能看到曲目栏的开头。 */
export const ARTIST_RESULTS_PAGE_SIZE = 12;
export const ALBUM_RESULTS_PAGE_SIZE = 12;
export const TRACK_RESULTS_PAGE_SIZE = 20;

/**
 * 结果页的一栏：每栏独立加载、失败与重试，翻页时保留上一页直到新页到达（占位数据，页面标明正在更新）。
 * 两栏的查询各自缓存；搜索响应带回更新的目录状态时，结果放进新代次的键下。
 */
function useResultsPage<T extends { catalog_state: CatalogStateDTO }>(
  type: "artists" | "albums" | "tracks",
  q: string,
  page: number,
  pageSize: number,
  fetchPage: (params: { q: string; page: number; pageSize: number; signal: AbortSignal }) => Promise<{ status: number; data: T }>,
  enabled = true,
) {
  const { generation } = useViewerScope();
  const queryClient = useQueryClient();
  const epoch = useSearchEpoch();
  return useQuery({
    queryKey: searchKeys.page(generation, epoch ?? -1, type, q, page, pageSize),
    queryFn: async ({ signal }) => {
      const response = await fetchPage({ q, page, pageSize, signal });
      adoptNewerCatalogState(queryClient, generation, response.data.catalog_state, (next) =>
        queryClient.setQueryData(searchKeys.page(generation, next, type, q, page, pageSize), response),
      );
      return response;
    },
    select: (response) => response.data,
    enabled: enabled && epoch !== undefined,
    placeholderData: keepPreviousInSameEpoch(epoch ?? -1),
  });
}

/** 艺人一栏（M5 ⑤）：只在艺人数据就绪时请求（enabled 由调用方按目录状态决定）。 */
export function useArtistResults(q: string, page: number, enabled: boolean) {
  return useResultsPage("artists", q, page, ARTIST_RESULTS_PAGE_SIZE, fetchArtistResults, enabled);
}

export function useAlbumResults(q: string, page: number) {
  return useResultsPage("albums", q, page, ALBUM_RESULTS_PAGE_SIZE, fetchAlbumResults);
}

export function useTrackResults(q: string, page: number) {
  return useResultsPage("tracks", q, page, TRACK_RESULTS_PAGE_SIZE, fetchTrackResults);
}
