import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import type { HistoryQueueDTO } from "../api/decoders";
import { fetchHistoryQueue } from "../api/endpoints";
import { listeningKeys } from "../query-keys";

export type HistoryQueueCallbacks = {
  onLoaded: (queue: HistoryQueueDTO) => void;
};

/**
 * “播放全部”的候选：每次按下都向服务端读一次最新的（最近最多 1,000 首，与历史页同一顺序），不沿用旧缓存。
 * 读取经 Query 缓存进行，所以上报、移除与清除让它失效的规则与历史页相同；结果只在发起时的会话仍是当前会话时交给调用方。
 * 能否播放、怎样放进队列由调用方（app 组合的播放器动作）决定，listening 不知道播放器。
 */
export function useHistoryQueue() {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  const { generation } = scope;
  const mutation = useMutation({
    mutationFn: async () => {
      const response = await queryClient.fetchQuery({
        queryKey: listeningKeys.queue(generation),
        queryFn: ({ signal }) => fetchHistoryQueue(signal),
        staleTime: 0,
      });
      return response.data;
    },
  });
  return {
    pending: mutation.isPending,
    error: mutation.isError ? mutation.error : null,
    load: (callbacks: HistoryQueueCallbacks) =>
      mutation.mutate(undefined, {
        onSuccess: (queue) => {
          if (scope.isCurrent()) callbacks.onLoaded(queue);
        },
      }),
    /** 清除上一次读取的错误。 */
    dismiss: () => mutation.reset(),
  };
}
