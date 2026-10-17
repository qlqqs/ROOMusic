import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { fetchClaim, fetchClaimHints, fetchClaimPreview, fetchClaims } from "../api/endpoints";
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
    // 线索不值得为它重试三次：失败就是没有提示。
    retry: false,
  });
}

/**
 * 认领的预览：把旧版本认领给新版本会怎样。targetId 为 undefined 时不请求。令牌要与服务器当前的情况一致，所以每次打开都重新读取，
 * 不保留上一对版本的数据；认领、撤销与别的修改之后随 governance 的查询一起失效。预览读取失败（旧版本现在不需要认领、新版本不可见……）由调用方说明。
 */
export function useClaimPreview(sourceId: string, targetId: string | undefined) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: governanceKeys.claimPreview(generation, targetId ?? "", sourceId),
    queryFn: ({ signal }) => fetchClaimPreview(targetId ?? "", sourceId, signal),
    select: (response) => response.data,
    enabled: targetId !== undefined,
    staleTime: 0,
    retry: false,
  });
}
