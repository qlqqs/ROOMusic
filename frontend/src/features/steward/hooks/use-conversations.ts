import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import { failureOf, isHttpFailure, type ApiResponse } from "../../../shared/api";
import { useViewerScope } from "../../auth";
import { MAX_CONVERSATION_PAGE_SIZE, type ConversationDetailDTO, type SettingsDTO, type StewardMode } from "../api/decoders";
import { createConversation, deleteConversation, fetchConversation, fetchConversations, markConversationRead, patchConversation, type ConversationPatch } from "../api/endpoints";
import { stewardKeys } from "../query-keys";

// 正在运行的会话不再轮询（C10）：对话区打开时接回运行的事件流，运行结束后重读；列表与入口的未读数在运行结束、打开会话、
// 窗口重新聚焦时重读。

/** 会话列表：最近的 100 个（按最后活动时间倒序）。 */
export function useConversations() {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: stewardKeys.conversations(generation),
    queryFn: ({ signal }) => fetchConversations(1, MAX_CONVERSATION_PAGE_SIZE, signal),
    select: (response) => response.data,
  });
}

/** 会话不存在：404，或地址里的 ID 不是 UUID（400 invalid_id）。 */
export function isMissingConversation(error: unknown): boolean {
  const failure = failureOf(error);
  return isHttpFailure(failure, 404) || isHttpFailure(failure, 400, "invalid_id");
}

/** 一个会话与它的全部消息。 */
export function useConversation(id: string | null) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: stewardKeys.conversation(generation, id ?? ""),
    queryFn: ({ signal }) => fetchConversation(id ?? "", signal),
    select: (response) => response.data,
    enabled: id !== null,
  });
}

/**
 * 打开会话后标记已读（C10）。这个会话原本是未读的、或入口还有提示点时，再重读列表与设置，让标记与提示点消失。
 * 标记失败不提示：下次打开时再标。
 */
export function useMarkRead() {
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  return useCallback(
    async (id: string, wasUnread: boolean) => {
      try {
        await markConversationRead(id);
      } catch {
        return;
      }
      const settings = queryClient.getQueryData<ApiResponse<SettingsDTO>>(stewardKeys.settings(generation));
      if (!wasUnread && (settings?.data.unread_conversation_count ?? 0) === 0) return;
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: stewardKeys.conversations(generation) }),
        queryClient.invalidateQueries({ queryKey: stewardKeys.settings(generation) }),
      ]);
    },
    [queryClient, generation],
  );
}

/** 新建会话（带空白新对话里选的模式）；成功后把空会话放进缓存，打开它不用再等一次读取。 */
export function useCreateConversation() {
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  return useMutation({
    mutationFn: async (mode: StewardMode) => (await createConversation(mode)).data,
    onSuccess: (conversation) => {
      const detail: ConversationDetailDTO = { ...conversation, messages: [], pending_plan: null };
      queryClient.setQueryData(stewardKeys.conversation(generation, conversation.id), { status: 200, data: detail });
      void queryClient.invalidateQueries({ queryKey: stewardKeys.conversations(generation) });
    },
  });
}

/** 改标题、模型或模式：成功后直接更新会话缓存里的这几项（响应里没有模式时取提交的值），再刷新列表。 */
export function usePatchConversation() {
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  return useMutation({
    mutationFn: async ({ id, patch }: { id: string; patch: ConversationPatch }) => ({ id, patch, result: (await patchConversation(id, patch)).data }),
    onSuccess: ({ id, patch, result }) => {
      queryClient.setQueryData<{ status: number; data: ConversationDetailDTO }>(stewardKeys.conversation(generation, id), (current) => {
        if (current === undefined) return current;
        const mode = result.mode ?? patch.mode ?? current.data.mode;
        return { ...current, data: { ...current.data, title: result.title, model: result.model, mode } };
      });
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
