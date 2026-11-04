import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { fetchReleaseGroupPage } from "../api/endpoints";
import type { AlbumSearch } from "../album-search";
import { catalogKeys } from "../query-keys";

/** 每页的专辑数：卡片网格，3 到 8 列都能排满；后端上限 100。 */
export const ALBUM_PAGE_SIZE = 48;

/**
 * 专辑列表的一页（所有登录用户可读）。翻页、改搜索词或筛选时保留上一份结果直到新结果到达，
 * 页面可以标明“正在更新”，不闪空；保留的结果是占位数据，不能用来判断页码是否越界。
 */
export function useAlbumPage(search: AlbumSearch) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: catalogKeys.albumPage(generation, search),
    queryFn: ({ signal }) =>
      fetchReleaseGroupPage({
        page: search.page ?? 1,
        pageSize: ALBUM_PAGE_SIZE,
        attentionRequired: search.attention === "required",
        search: search.q,
        sort: search.sort,
        signal,
      }),
    select: (response) => response.data,
    placeholderData: keepPreviousData,
  });
}
