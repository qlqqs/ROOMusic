import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { fetchSearchSuggestions } from "../api/endpoints";
import { searchKeys } from "../query-keys";
import { adoptNewerCatalogState, keepPreviousInSameEpoch, useSearchEpoch } from "./search-epoch";

/** 每类建议几条。 */
export const SUGGESTION_LIMIT = 5;

/**
 * 输入框下方的建议（q 是防抖之后、去掉首尾空白的查询词；null 时不查询）。换词时保留上一份建议直到新的到达，
 * 慢响应只会落在它自己的键下，不会盖掉新词的结果；换词时旧请求经 AbortSignal 取消。
 */
export function useSearchSuggestions(q: string | null) {
  const { generation } = useViewerScope();
  const queryClient = useQueryClient();
  const epoch = useSearchEpoch();
  return useQuery({
    queryKey: searchKeys.suggestions(generation, epoch ?? -1, q ?? "", SUGGESTION_LIMIT),
    queryFn: async ({ signal }) => {
      const response = await fetchSearchSuggestions({ q: q ?? "", limit: SUGGESTION_LIMIT, signal });
      adoptNewerCatalogState(queryClient, generation, response.data.catalog_state, (next) =>
        queryClient.setQueryData(searchKeys.suggestions(generation, next, q ?? "", SUGGESTION_LIMIT), response),
      );
      return response;
    },
    select: (response) => response.data,
    enabled: q !== null && epoch !== undefined,
    placeholderData: keepPreviousInSameEpoch(epoch ?? -1),
  });
}
