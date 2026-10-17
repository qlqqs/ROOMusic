import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { fetchClaim, fetchClaimHints, fetchClaims } from "../api/endpoints";
import { governanceKeys } from "../query-keys";

/** 待认领列表每页的条数（后端默认 50，上限 100）；显式传给后端，不依赖它的默认值。 */
export const CLAIM_PAGE_SIZE = 30;

/**
 * 待认领的旧版本的一页（仅管理员；非管理员不应调用，后端会拒绝）。翻页时保留上一页的数据直到新一页到达，列表不会闪空。
 * 旧版本的集合随扫描与认领变化：每次打开都重新读取（staleTime 为 0）；认领与撤销之后由命令让 governance 的全部查询失效。
 */
export function useClaims(page: number) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: governanceKeys.claimPage(generation, { page, pageSize: CLAIM_PAGE_SIZE }),
    queryFn: ({ signal }) => fetchClaims({ page, pageSize: CLAIM_PAGE_SIZE, signal }),
    select: (response) => response.data,
    placeholderData: keepPreviousData,
    staleTime: 0,
  });
}

/**
 * 一个待认领的旧版本的详情（修改明细与推荐）。推荐随扫描变化，同样每次打开都重新读取；换版本时不保留上一个版本的数据（那是另一个版本）。
 */
export function useClaim(releaseId: string) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: governanceKeys.claim(generation, releaseId),
    queryFn: ({ signal }) => fetchClaim(releaseId, signal),
    select: (response) => response.data,
    staleTime: 0,
  });
}

/**
 * 专辑详情的提示：把这个可见版本列为推荐的待认领旧版本（仅管理员的操作区请求它）。提示只是线索，失败时不打扰页面（调用方忽略错误态）；
 * 每次打开都重新读取，因为认领、撤销与重新扫描都会改变它。
 */
export function useClaimHints(releaseId: string) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: governanceKeys.claimHints(generation, releaseId),
    queryFn: ({ signal }) => fetchClaimHints(releaseId, signal),
    select: (response) => response.data.items,
    staleTime: 0,
  });
}
