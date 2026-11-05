import { useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { fetchReleaseGroupPage } from "../api/endpoints";
import { catalogKeys } from "../query-keys";

/** 挑选专辑时一次最多取的专辑数：只给最相关的前几张，更精确的搜索词由用户自己输入。 */
export const ALBUM_SEARCH_RESULT_LIMIT = 10;

/**
 * 按搜索词找专辑（所有登录用户可读），给别的功能挑选专辑用（例如把版本归入其他专辑）。
 * search 为 undefined 时不请求；搜索词由调用方先校验（去掉首尾空白、不超过 200 字节）。结果与列表页的缓存分开。
 */
export function useAlbumSearchResults(search: string | undefined) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: catalogKeys.albumSearch(generation, search ?? ""),
    queryFn: ({ signal }) => fetchReleaseGroupPage({ page: 1, pageSize: ALBUM_SEARCH_RESULT_LIMIT, attentionRequired: false, search, signal }),
    select: (response) => response.data,
    enabled: search !== undefined,
  });
}
