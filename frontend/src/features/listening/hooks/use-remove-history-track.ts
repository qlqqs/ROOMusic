import { useMutation, useQueryClient } from "@tanstack/react-query";
import { failureOf, isOutcomeUncertain, type ApiResponse } from "../../../shared/api";
import { useViewerScope } from "../../auth";
import type { HistoryTracksDTO } from "../api/decoders";
import { deleteTrackPlays } from "../api/endpoints";
import { listeningKeys } from "../query-keys";

export type RemoveTrackState =
  | { status: "idle" }
  | { status: "pending" }
  /** uncertain 为 true 时移除可能已经执行；移除是幂等的删除，再试一次是安全的。 */
  | { status: "failed"; error: unknown; uncertain: boolean };

export type RemoveTrackCallbacks = {
  onSucceeded?: () => void;
};

/**
 * 从当前用户的历史中移除一首曲目的全部记录。成功后立刻从已缓存的历史页里去掉这一行（不等重新读取），
 * 再让本人的全部历史查询失效（历史页、“播放全部”的候选、概览的“最近播放”），由服务端的当前状态为准。
 * 失败时也重新读取；DELETE 是幂等的，失败后再点一次就是重试，不需要幂等键。
 */
export function useRemoveHistoryTrack() {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  const { generation } = scope;

  const mutation = useMutation({
    mutationFn: (trackId: string) => deleteTrackPlays(trackId),
    onSuccess: (_response, trackId) => {
      queryClient.setQueriesData<ApiResponse<HistoryTracksDTO>>({ queryKey: listeningKeys.tracks(generation) }, (previous) =>
        previous === undefined ? previous : { ...previous, data: { ...previous.data, items: previous.data.items.filter((item) => item.track_id !== trackId) } },
      );
    },
    // 不返回这个 Promise：对话框在移除成功时就关闭，重新读取在后台进行。
    onSettled: () => void queryClient.invalidateQueries({ queryKey: listeningKeys.all(generation) }),
  });

  const state: RemoveTrackState =
    mutation.status === "pending"
      ? { status: "pending" }
      : mutation.status === "error"
        ? { status: "failed", error: mutation.error, uncertain: isOutcomeUncertain(failureOf(mutation.error)) }
        : { status: "idle" };

  return {
    state,
    /** 发起移除；回调只在发起时的会话仍是当前会话时才触发。 */
    remove: (trackId: string, callbacks?: RemoveTrackCallbacks) =>
      mutation.mutate(trackId, {
        onSuccess: () => {
          if (scope.isCurrent()) callbacks?.onSucceeded?.();
        },
      }),
    /** 清除上一次的结果，回到没有命令的状态。 */
    dismiss: () => mutation.reset(),
  };
}
