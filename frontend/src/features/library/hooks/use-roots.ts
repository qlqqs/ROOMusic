import { queryOptions, useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import type { RootDTO } from "../api/decoders";
import { fetchRoots } from "../api/endpoints";
import { libraryKeys } from "../query-keys";

/** 目录列表的查询定义：写命令也用它读取缓存（键带类型，读出的数据不需要断言）。 */
export function rootsQueryOptions(generation: number) {
  return queryOptions({
    queryKey: libraryKeys.roots(generation),
    queryFn: ({ signal }) => fetchRoots(signal),
  });
}

/** 管理员的目录列表；非管理员不应调用（后端会拒绝）。 */
export function useRoots() {
  const { generation } = useViewerScope();
  return useQuery({ ...rootsQueryOptions(generation), select: (response) => response.data.items });
}

export type RootSummary = { active: number; disabled: number };

export function summarizeRoots(roots: readonly RootDTO[]): RootSummary {
  const disabled = roots.filter((root) => root.status === "disabled").length;
  return { active: roots.length - disabled, disabled };
}
