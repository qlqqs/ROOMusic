import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { failureOf, isHttpFailure } from "../../../shared/api";
import { useViewerScope } from "../../auth";
import { MAX_CONVERSATION_PAGE_SIZE, type ConversationDetailDTO } from "../api/decoders";
import { createConversation, deleteConversation, fetchConversation, fetchConversations, patchConversation } from "../api/endpoints";
import { stewardKeys } from "../query-keys";
import { useRuns } from "./use-run";

/** 另一个标签页正在回答时，每隔这么久重读一次，直到会话变回空闲。 */
export const BUSY_POLL_MS = 3_000;

/** “正在别处回答”的轮询间隔：有不是本页发起的运行时轮询；上一次读取出错（会话被删、管家被关闭等）时停止。 */
export function busyPollInterval(lastError: unknown, busyElsewhere: boolean): number | false {
  return lastError === null && busyElsewhere ? BUSY_POLL_MS : false;
}

/** 会话列表：最近的 100 个（按最后活动时间倒序）。 */
export function useConversations() {
  const { generation } = useViewerScope();
  const runs = useRuns();
  return useQuery({
    queryKey: stewardKeys.conversations(generation),
    queryFn: ({ signal }) => fetchConversations(1, MAX_CONVERSATION_PAGE_SIZE, signal),
    select: (response) => response.data,
    // 列表里有正在回答、但不是本页发起的会话时轮询，标记随它消失。
    refetchInterval: (query) => busyPollInterval(query.state.error, query.state.data?.data.items.some((item) => item.status === "running" && !runs.has(item.id)) === true),
  });
}

/** 会话不存在：404，或地址里的 ID 不是 UUID（400 invalid_id）。 */
export function isMissingConversation(error: unknown): boolean {
  const failure = failureOf(error);
  return isHttpFailure(failure, 404) || isHttpFailure(failure, 400, "invalid_id");
}

/** 一个会话与它的全部消息。会话正在回答、本页又没有这次运行时（别的标签页在跑，或刚刷新），每 3 秒重读直到空闲。 */
export function useConversation(id: string | null) {
  const { generation } = useViewerScope();
  const runs = useRuns();
  const local = id !== null && runs.has(id);
  return useQuery({
    queryKey: stewardKeys.conversation(generation, id ?? ""),
    queryFn: ({ signal }) => fetchConversation(id ?? "", signal),
    select: (response) => response.data,
    enabled: id !== null,
    refetchInterval: (query) => busyPollInterval(query.state.error, query.state.data?.data.status === "running" && !local),
  });
}

/** 新建会话；成功后把空会话放进缓存，打开它不用再等一次读取。 */
export function useCreateConversation() {
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  return useMutation({
    mutationFn: async () => (await createConversation()).data,
    onSuccess: (conversation) => {
      const detail: ConversationDetailDTO = { ...conversation, messages: [] };
      queryClient.setQueryData(stewardKeys.conversation(generation, conversation.id), { status: 200, data: detail });
      void queryClient.invalidateQueries({ queryKey: stewardKeys.conversations(generation) });
    },
  });
}

/** 改标题或模型：成功后直接更新会话缓存里的这两项，再刷新列表。 */
export function usePatchConversation() {
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  return useMutation({
    mutationFn: async ({ id, patch }: { id: string; patch: { title?: string; model?: string } }) => ({ id, result: (await patchConversation(id, patch)).data }),
    onSuccess: ({ id, result }) => {
      queryClient.setQueryData<{ status: number; data: ConversationDetailDTO }>(stewardKeys.conversation(generation, id), (current) =>
        current === undefined ? current : { ...current, data: { ...current.data, title: result.title, model: result.model } },
      );
      void queryClient.invalidateQueries({ queryKey: stewardKeys.conversations(generation) });
    },
  });
}

export function useDeleteConversation() {
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  return useMutation({
    mutationFn: async (id: string) => {
      await deleteConversation(id);
      return id;
    },
    onSuccess: (id) => queryClient.removeQueries({ queryKey: stewardKeys.conversation(generation, id) }),
    onSettled: () => queryClient.invalidateQueries({ queryKey: stewardKeys.conversations(generation) }),
  });
}
