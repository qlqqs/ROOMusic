import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { failureOf, isHttpFailure } from "../../../shared/api";
import { useViewerScope } from "../../auth";
import type { CurationProgressDTO, CurationSettingsDTO, CurationSourceDTO, CurationSourcesDTO } from "../api/decoders";
import {
  clearSuppression,
  fetchCurationBatches,
  fetchCurationProgress,
  fetchCurationSettings,
  fetchCurationSources,
  fetchSuppressions,
  setCurationSource,
  setCurationSourceKey,
  updateCurationSettings,
} from "../api/endpoints";
import { curationKeys } from "../query-keys";

/** 总开关、开启者与暂停（管理员）。 */
export function useCurationSettings() {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: curationKeys.settings(generation),
    queryFn: ({ signal }) => fetchCurationSettings(signal),
    select: (response) => response.data,
  });
}

/**
 * 改总开关或暂停：成功后写回设置；外部源的“能不能用”与后台进度都跟着变，让它们重新读取。
 * 同一页上连续切换按顺序发送（同一个 mutation scope）；失败时缓存不动，界面回到服务器的值。
 */
export function useUpdateCurationSettings() {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  return useMutation({
    scope: { id: "curation-settings" },
    mutationFn: (change: { enabled?: boolean; paused?: boolean }) => updateCurationSettings(change),
    onSuccess: async (response) => {
      if (!scope.isCurrent()) return;
      const key = curationKeys.settings(scope.generation);
      await queryClient.cancelQueries({ queryKey: key });
      queryClient.setQueryData(key, response);
      void queryClient.invalidateQueries({ queryKey: curationKeys.sources(scope.generation) });
      void queryClient.invalidateQueries({ queryKey: curationKeys.progress(scope.generation) });
    },
  });
}

/** 各外部源。 */
export function useCurationSources() {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: curationKeys.sources(generation),
    queryFn: ({ signal }) => fetchCurationSources(signal),
    select: (response) => response.data,
  });
}

/** 把接口返回的一个源写回列表（开关与密钥的修改共用）。 */
function useWriteBackSource() {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  return async (source: CurationSourceDTO) => {
    if (!scope.isCurrent()) return;
    const key = curationKeys.sources(scope.generation);
    await queryClient.cancelQueries({ queryKey: key });
    queryClient.setQueryData<{ status: number; data: CurationSourcesDTO }>(key, (current) =>
      current === undefined ? current : { ...current, data: { items: current.data.items.map((item) => (item.id === source.id ? source : item)) } },
    );
  };
}

/** 改一个源的开关：把返回的那一项写回列表。 */
export function useSetCurationSource() {
  const writeBack = useWriteBackSource();
  return useMutation({
    scope: { id: "curation-sources" },
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) => setCurationSource(id, enabled),
    onSuccess: (response) => writeBack(response.data),
  });
}

/** 填写、更换（key 是新密钥）或清除（key 为 null）一个源的某一把密钥：把返回的那一项写回列表。 */
export function useSetCurationSourceKey() {
  const writeBack = useWriteBackSource();
  return useMutation({
    scope: { id: "curation-sources" },
    mutationFn: ({ id, name, key }: { id: string; name: string; key: string | null }) => setCurationSourceKey(id, name, key),
    onSuccess: (response) => writeBack(response.data),
  });
}

/**
 * 后台什么时候需要重新读进度：工作或等扫描时 10 秒；设置与进度还没对上（后台最长一分钟才发现开关变化）时也 10 秒；
 * 开着、已处理完时 60 秒；其余（未打开、暂停且已停下、没有运行）不轮询。
 */
export function progressRefetchInterval(progress: CurationProgressDTO | undefined, settings: CurationSettingsDTO | undefined): number | false {
  if (progress === undefined) return false;
  if (progress.state === "running" || progress.state === "waiting_for_scan") return 10_000;
  if (settings !== undefined) {
    const shouldRun = settings.enabled && settings.active && !settings.paused;
    if (shouldRun && (progress.state === "disabled" || progress.state === "paused")) return 10_000;
    if (!shouldRun && progress.state === "idle") return 10_000;
    if (shouldRun && progress.state === "idle") return 60_000;
  }
  return false;
}

/** 后台的进度；settings 用来决定轮询（见 progressRefetchInterval）。 */
export function useCurationProgress(settings?: CurationSettingsDTO) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: curationKeys.progress(generation),
    queryFn: ({ signal }) => fetchCurationProgress(signal),
    select: (response) => response.data,
    refetchInterval: (query) => progressRefetchInterval(query.state.data?.data, settings),
  });
}

/** 批次的一页；回到页面时重新读取（单条撤销可能发生在操作记录页）。 */
export function useCurationBatches(page: number) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: curationKeys.batchPage(generation, page),
    queryFn: ({ signal }) => fetchCurationBatches(page, signal),
    select: (response) => response.data,
    placeholderData: keepPreviousData,
    staleTime: 0,
  });
}

/** “不再提出”标记的一页。 */
export function useSuppressions(page: number) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: curationKeys.suppressionPage(generation, page),
    queryFn: ({ signal }) => fetchSuppressions(page, signal),
    select: (response) => response.data,
    placeholderData: keepPreviousData,
    staleTime: 0,
  });
}

/** 清除一条标记：404（已经不在）也当作清除了，之后刷新列表。 */
export function useClearSuppression() {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  return useMutation({
    mutationFn: async (id: string) => {
      try {
        await clearSuppression(id);
      } catch (error) {
        if (!isHttpFailure(failureOf(error), 404)) throw error;
      }
    },
    onSettled: () => {
      if (scope.isCurrent()) void queryClient.invalidateQueries({ queryKey: curationKeys.suppressions(scope.generation) });
    },
  });
}
