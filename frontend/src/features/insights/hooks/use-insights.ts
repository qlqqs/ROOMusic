import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { acknowledgeInsightsNotice, fetchAbout, fetchInsightsStatus, setInsightsEnabled } from "../api/endpoints";
import { insightsKeys } from "../query-keys";

export function useInsightsStatus() {
  const { generation } = useViewerScope();
  return useQuery({ queryKey: insightsKeys.status(generation), queryFn: ({ signal }) => fetchInsightsStatus(signal), select: (response) => response.data });
}

/** 开关：接口返回改后的完整状态，直接写回缓存。连续切换按顺序发送。 */
export function useSetInsightsEnabled() {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  return useMutation({
    scope: { id: "insights-enabled" },
    mutationFn: (enabled: boolean) => setInsightsEnabled(enabled),
    onSuccess: async (response) => {
      if (!scope.isCurrent()) return;
      const key = insightsKeys.status(scope.generation);
      await queryClient.cancelQueries({ queryKey: key });
      queryClient.setQueryData(key, response);
    },
  });
}

/** 看过说明：成功后重读状态，概览上的说明随之消失。 */
export function useAcknowledgeInsightsNotice() {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  return useMutation({
    mutationFn: () => acknowledgeInsightsNotice(),
    onSuccess: () => {
      if (scope.isCurrent()) void queryClient.invalidateQueries({ queryKey: insightsKeys.status(scope.generation) });
    },
  });
}

/** 许可与源代码地址：进程不重启就不变。 */
export function useAbout() {
  return useQuery({ queryKey: insightsKeys.about, queryFn: ({ signal }) => fetchAbout(signal), select: (response) => response.data, staleTime: Number.POSITIVE_INFINITY });
}
