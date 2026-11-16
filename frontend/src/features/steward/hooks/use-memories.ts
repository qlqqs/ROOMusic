import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { clearMemories, createMemory, deleteMemory, fetchMemories, updateMemory } from "../api/endpoints";
import { stewardKeys } from "../query-keys";

// 记忆（C10）：管家在对话里记下的、或用户手动添加的内容。写操作没有幂等键，不自动重试；无论成败都重读列表核对。
// 管家在对话里用 remember、forget 改了记忆时，运行的回调让这个查询失效。

export function useMemories() {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: stewardKeys.memories(generation),
    queryFn: ({ signal }) => fetchMemories(signal),
    select: (response) => response.data,
  });
}

function useMemoryMutation<T>(mutationFn: (input: T) => Promise<unknown>) {
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  return useMutation({
    mutationFn,
    onSettled: () => queryClient.invalidateQueries({ queryKey: stewardKeys.memories(generation) }),
  });
}

export function useCreateMemory() {
  return useMemoryMutation((content: string) => createMemory(content));
}

export function useUpdateMemory() {
  return useMemoryMutation(({ id, content }: { id: string; content: string }) => updateMemory(id, content));
}

export function useDeleteMemory() {
  return useMemoryMutation((id: string) => deleteMemory(id));
}

export function useClearMemories() {
  return useMemoryMutation<void>(() => clearMemories());
}
