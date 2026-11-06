import { useMutation, useQueryClient } from "@tanstack/react-query";
import { failureOf, isHttpFailure } from "../../../shared/api";
import { useViewerScope } from "../../auth";
import { deleteBackup } from "../api/endpoints";
import { systemKeys } from "../query-keys";

/** 要删除的备份已经不在（被自动清理或别处删掉了）：正是用户要的结果，不算失败。 */
export const isBackupNotFound = (error: unknown) => isHttpFailure(failureOf(error), 404, "backup_not_found");

/**
 * 删除一份备份。响应就是删除后的状态与列表，直接写进同一个查询缓存：先取消正在进行的读取（备份进行中每 2 秒轮询一次），
 * 免得删除前的列表把新列表盖回去。已经不在（404）时刷新列表。写缓存前检查会话代次，旧会话的迟到结果不碰新会话。
 */
export function useDeleteBackup() {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  const queryKey = systemKeys.backups(scope.generation);
  return useMutation({
    mutationFn: (name: string) => deleteBackup(name),
    onSuccess: async (response) => {
      if (!scope.isCurrent()) return;
      await queryClient.cancelQueries({ queryKey });
      queryClient.setQueryData(queryKey, response);
    },
    onError: (error) => {
      if (scope.isCurrent() && isBackupNotFound(error)) void queryClient.invalidateQueries({ queryKey });
    },
  });
}
