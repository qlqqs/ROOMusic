import { useMutation, useQueryClient } from "@tanstack/react-query";
import { failureOf, isOutcomeUncertain, type ApiResponse } from "../../../shared/api";
import { useViewerScope } from "../../auth";
import type { HistoryTracksDTO, RecentReleasesDTO } from "../api/decoders";
import { deletePlayHistory } from "../api/endpoints";
import { listeningKeys } from "../query-keys";

export type ClearHistoryState =
  | { status: "idle" }
  | { status: "pending" }
  /** uncertain 为 true 时清除可能已经执行；清除是幂等的，再试一次是安全的。 */
  | { status: "failed"; error: unknown; uncertain: boolean };

export type ClearHistoryCallbacks = {
  onSucceeded?: () => void;
};

const nothingPlayed: ApiResponse<RecentReleasesDTO> = { status: 200, data: { items: [], has_history: false } };

/**
 * 清除当前用户的全部播放历史（概览与历史页共用）。成功后立刻把“最近播放”与历史页置空（不等重新读取），再让本人的全部历史查询失效，由服务端的当前状态为准；
 * 失败时也重新读取，让用户看到真实的状态。DELETE 是幂等的，所以失败后直接再点一次就是重试，不需要幂等键。
 */
export function useClearPlayHistory() {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  const { generation } = scope;

  const mutation = useMutation({
    mutationFn: () => deletePlayHistory(),
    onSuccess: () => {
      queryClient.setQueryData(listeningKeys.recentReleases(generation), nothingPlayed);
      queryClient.setQueriesData<ApiResponse<HistoryTracksDTO>>({ queryKey: listeningKeys.tracks(generation) }, (previous) =>
        previous === undefined ? previous : { ...previous, data: { ...previous.data, items: [], pagination: { ...previous.data.pagination, total: 0 }, has_history: false } },
      );
    },
    // 不返回这个 Promise：对话框在清除成功时就关闭，重新读取在后台进行。
    onSettled: () => void queryClient.invalidateQueries({ queryKey: listeningKeys.all(generation) }),
  });

  const state: ClearHistoryState =
    mutation.status === "pending"
      ? { status: "pending" }
      : mutation.status === "error"
        ? { status: "failed", error: mutation.error, uncertain: isOutcomeUncertain(failureOf(mutation.error)) }
        : { status: "idle" };

  return {
    state,
    /** 发起清除；回调只在发起时的会话仍是当前会话时才触发。 */
    clear: (callbacks?: ClearHistoryCallbacks) =>
      mutation.mutate(undefined, {
        onSuccess: () => {
          if (scope.isCurrent()) callbacks?.onSucceeded?.();
        },
      }),
    /** 清除上一次的结果，回到没有命令的状态。 */
    dismiss: () => mutation.reset(),
  };
}
