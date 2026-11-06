import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import type { BackupRetentionDTO } from "../api/decoders";
import { saveRetention } from "../api/endpoints";
import { systemKeys } from "../query-keys";

/**
 * 保存自动清理的规则。需要确认时服务器什么都没改，缓存不动；保存了的话，响应里的状态与列表（含新规则与清理后的文件）写进同一个查询缓存。
 * 失败时刷新列表：确认后的清理可能已经删了一部分。
 */
export function useSaveRetention() {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  const queryKey = systemKeys.backups(scope.generation);
  return useMutation({
    mutationFn: ({ rule, confirm }: { rule: BackupRetentionDTO; confirm: boolean }) => saveRetention(rule, confirm),
    onSuccess: async (response) => {
      if (!scope.isCurrent() || response.data.confirmation_required) return;
      await queryClient.cancelQueries({ queryKey });
      queryClient.setQueryData(queryKey, { status: response.status, data: response.data.overview });
    },
    onError: () => (scope.isCurrent() ? queryClient.invalidateQueries({ queryKey }) : undefined),
  });
}
