import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { fetchArtistCandidates } from "../api/endpoints";
import { governanceKeys } from "../query-keys";

/** “可能是同一位”每页的条数（后端默认 50，上限 100）。 */
export const ARTIST_CANDIDATE_PAGE_SIZE = 20;

/** 候选的一页（管理员）。合并、忽略之后随 governance 的全部查询一起失效；每次打开都重新读（别的管理员可能已经处理过）。 */
export function useArtistCandidates(page: number) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: governanceKeys.artistCandidatePage(generation, { page, pageSize: ARTIST_CANDIDATE_PAGE_SIZE }),
    queryFn: ({ signal }) => fetchArtistCandidates({ page, pageSize: ARTIST_CANDIDATE_PAGE_SIZE, signal }),
    select: (response) => response.data,
    placeholderData: keepPreviousData,
    staleTime: 0,
  });
}
