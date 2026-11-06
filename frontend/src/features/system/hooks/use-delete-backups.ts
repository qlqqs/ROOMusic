import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { deleteBackups } from "../api/endpoints";
import { systemKeys } from "../query-keys";

/**
 * 一次删除多份备份。结果按名单分好（部分失败也是成功的响应），响应里删除后的状态与列表写进同一个查询缓存（先取消正在进行的读取）。
 * 请求本身失败时，文件可能已经删了一部分，刷新列表。
 */
export function useDeleteBackups() {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  const queryKey = systemKeys.backups(scope.generation);
  return useMutation({
    mutationFn: (names: readonly string[]) => deleteBackups(names),
    onSuccess: async (response) => {
      if (!scope.isCurrent()) return;
      await queryClient.cancelQueries({ queryKey });
      queryClient.setQueryData(queryKey, { status: response.status, data: response.data.overview });
    },
    onError: () => (scope.isCurrent() ? queryClient.invalidateQueries({ queryKey }) : undefined),
  });
}
