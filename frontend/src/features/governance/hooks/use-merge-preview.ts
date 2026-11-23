import { useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { fetchMergePreview } from "../api/endpoints";
import { governanceKeys } from "../query-keys";

/**
 * 整张并入的预览（M7 H）：把 sourceId 并入 releaseId 会怎样。sourceId 为 undefined 时不请求。令牌要与服务器当前的情况一致，
 * 所以每次选中都重新读取；任何治理之后随 governance 的查询一起失效。读取失败（已并入别处、超过上限……）由调用方说明。
 */
export function useMergePreview(releaseId: string, sourceId: string | undefined) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: governanceKeys.mergePreview(generation, releaseId, sourceId ?? ""),
    queryFn: ({ signal }) => fetchMergePreview(releaseId, sourceId ?? "", signal),
    select: (response) => response.data,
    enabled: sourceId !== undefined,
    staleTime: 0,
    retry: false,
  });
}
