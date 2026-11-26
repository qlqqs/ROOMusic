import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { failureOf, isHttpFailure } from "../../../shared/api";
import { useViewerScope } from "../../auth";
import { governanceKeys } from "../../governance";
import { fetchOperations } from "../../governance/api/endpoints";
import type { CurationProgressDTO, CurationSettingsDTO, CurationSourceDTO, CurationSourcesDTO } from "../api/decoders";
import {
  acceptCurationException,
  clearSuppression,
  declineCurationException,
  fetchCurationBatches,
  fetchCurationClassifications,
  fetchCurationConversations,
  fetchCurationExceptions,
  fetchCurationProgress,
  fetchCurationSettings,
  fetchCurationSources,
  fetchSuppressions,
  requestRepatrol,
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

/** 设置里可以改的几项（至少一项）：总开关、暂停、整理用的模型（空串 = 对话模型）、每日 token 上限与联网兜底的每日上限（null = 不限）。 */
export type CurationSettingsChange = { enabled?: boolean; paused?: boolean; model?: string; daily_token_limit?: number | null; web_daily_token_limit?: number | null };

/**
 * 改设置：成功后写回设置；外部源的“能不能用”与后台进度（含模型层）都跟着变，让它们重新读取。
 * 同一页上连续切换按顺序发送（同一个 mutation scope）；失败时缓存不动，界面回到服务器的值。
 */
export function useUpdateCurationSettings() {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  return useMutation({
    scope: { id: "curation-settings" },
    mutationFn: (change: CurationSettingsChange) => updateCurationSettings(change),
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
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  return useMutation({
    scope: { id: "curation-sources" },
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) => setCurationSource(id, enabled),
    onSuccess: async (response) => {
      await writeBack(response.data);
      // 插件的启用状态就是它作为外部源的开关（M8）：插件卡片跟着重读。
      if (response.data.id.startsWith("plugin:") && scope.isCurrent()) void queryClient.invalidateQueries({ queryKey: curationKeys.plugins(scope.generation) });
    },
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
  // 模型层在跑或等重新审查时也 10 秒（M7 D）。
  if (progress.model.state === "running" || progress.model.state === "waiting_review") return 10_000;
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

/** 可见发行按本地分类的计数；每次实时计算，跟着页面读取，不轮询。 */
export function useCurationClassifications() {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: curationKeys.classifications(generation),
    queryFn: ({ signal }) => fetchCurationClassifications(signal),
    select: (response) => response.data,
  });
}

/** 来自某个来源（例如库内佐证、网页佐证）的自动修改条数：操作记录按来源筛选的总数，含撤销记录。 */
export function useEvidenceCount(provider: string) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: curationKeys.evidenceCount(generation, provider),
    queryFn: () => fetchOperations({ page: 1, pageSize: 1, provider }),
    select: (response) => response.data.pagination.total,
  });
}

/** 重新巡检全部名字：成功后进度跟着变（巡检进入模型队列）。 */
export function useRepatrol() {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  return useMutation({
    mutationFn: () => requestRepatrol(),
    onSuccess: () => {
      if (!scope.isCurrent()) return;
      void queryClient.invalidateQueries({ queryKey: curationKeys.progress(scope.generation) });
    },
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

/** 待处理的例外的一页（M7 D）。 */
export function useCurationExceptions(page: number) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: curationKeys.exceptionPage(generation, page),
    queryFn: ({ signal }) => fetchCurationExceptions("open", page, EXCEPTION_PAGE_SIZE, signal),
    select: (response) => response.data,
    placeholderData: keepPreviousData,
    staleTime: 0,
  });
}

/** 例外列表一页的条数。 */
export const EXCEPTION_PAGE_SIZE = 20;

/** 模型判断一组里列出的最近几个自动整理会话。 */
export const RECENT_CONVERSATION_COUNT = 3;

/** 当前管理员名下最近的自动整理会话（M7 D）。 */
export function useCurationConversations() {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: curationKeys.conversations(generation),
    queryFn: ({ signal }) => fetchCurationConversations(1, RECENT_CONVERSATION_COUNT, signal),
    select: (response) => response.data,
    staleTime: 0,
  });
}

/**
 * 采纳与不采纳之后：例外、进度、批次、会话与操作记录都重新读取（不采纳还多了“不再提出”的标记，在 curation 之下一并失效）。
 * 例外停在“正在采纳”（多步匹配写了一部分时外部源暂时失败）时不刷新列表，让同一个人在原来的确认框里重试续做。
 */
function useAfterExceptionDecision() {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  return () => {
    if (!scope.isCurrent()) return;
    void queryClient.invalidateQueries({ queryKey: curationKeys.all(scope.generation) });
    void queryClient.invalidateQueries({ queryKey: governanceKeys.all(scope.generation) });
  };
}

/** 503 source_unavailable：外部源暂时不可用，例外可能停在“正在采纳”，保留确认框重试，不刷新列表。 */
export function isRetryableAcceptFailure(error: unknown): boolean {
  return isHttpFailure(failureOf(error), 503, "source_unavailable");
}

/** 采纳一条例外；refresh 让目录与详情跟着更新（由 app 提供）。 */
export function useAcceptException(refresh: () => void) {
  const after = useAfterExceptionDecision();
  return useMutation({
    mutationFn: (id: string) => acceptCurationException(id),
    onSuccess: () => refresh(),
    onSettled: (_data, error) => {
      if (error !== null && isRetryableAcceptFailure(error)) return;
      after();
    },
  });
}

/** 不采纳一条例外。 */
export function useDeclineException() {
  const after = useAfterExceptionDecision();
  return useMutation({
    mutationFn: (id: string) => declineCurationException(id),
    onSettled: () => after(),
  });
}
