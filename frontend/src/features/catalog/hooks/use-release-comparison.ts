import { useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { fetchReleaseComparison } from "../api/endpoints";
import { catalogKeys } from "../query-keys";

/**
 * 一张专辑各个版本的对比数据（所有登录用户可读）。只在对比对话框打开后才挂载这个 hook，所以没有人打开对比时不会有这个请求。
 * groupId 由调用方先校验为 UUID。
 */
export function useReleaseComparison(groupId: string) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: catalogKeys.comparison(generation, groupId),
    queryFn: ({ signal }) => fetchReleaseComparison(groupId, signal),
    select: (response) => response.data,
  });
}
