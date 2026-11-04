import type { Query, QueryClient } from "@tanstack/react-query";
import type { ApiResponse } from "../../../shared/api";
import { catalogStateKeys, useCatalogState, type CatalogStateDTO } from "../../catalog";
import { epochOfSearchKey } from "../query-keys";

/**
 * 查询键用的 epoch：目录状态读到之前是 undefined（搜索先不发请求）；状态读取失败时用 -1，搜索照常进行，
 * 搜索响应里带回的状态会补上。
 */
export function useSearchEpoch(): number | undefined {
  const state = useCatalogState();
  return state.data?.epoch ?? (state.isError ? -1 : undefined);
}

/**
 * 搜索响应带回的目录状态比缓存里的新：先把新结果放进新代次的键下，再更新目录状态，页面按正确的键显示，不再重复请求一次。
 * 返回 true 表示状态已更新（调用方据此把结果放进新键）；旧代次的迟到响应不会把状态改回去。
 */
export function adoptNewerCatalogState(queryClient: QueryClient, generation: number, state: CatalogStateDTO, seed: (epoch: number) => void): boolean {
  const key = catalogStateKeys.current(generation);
  const cached = queryClient.getQueryData<ApiResponse<CatalogStateDTO>>(key);
  if (cached !== undefined && cached.data.epoch >= state.epoch) return false;
  seed(state.epoch);
  queryClient.setQueryData<ApiResponse<CatalogStateDTO>>(key, { status: 200, data: state });
  return true;
}

/**
 * 换词、翻页时保留上一份结果作占位（页面标明正在更新），但只在同一 epoch 内：epoch 变大后旧代次的结果按旧规则匹配，
 * 新代次的请求还在路上时不能拿它冒充新结果，此时回到加载状态。
 */
export function keepPreviousInSameEpoch(epoch: number) {
  return <T>(previous: T | undefined, previousQuery: Query<T, Error, T, readonly unknown[]> | undefined): T | undefined =>
    previousQuery !== undefined && epochOfSearchKey(previousQuery.queryKey) === epoch ? previous : undefined;
}
