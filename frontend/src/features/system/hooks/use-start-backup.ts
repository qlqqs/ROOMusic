import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { startBackup } from "../api/endpoints";
import { systemKeys } from "../query-keys";

/**
 * 立即备份：202 开始了一份，200 已有备份在进行。备份是异步的，结果由之后的读取得到；
 * 无论成败都先刷新备份状态再结束，按钮忙碌到界面显示的是服务器的新状态（之后由状态里的 running 接着显示忙碌）。
 * 刷新前检查会话代次：换了会话之后，旧会话的迟到结果不再碰新会话的缓存。
 */
export function useStartBackup() {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  return useMutation({
    mutationFn: () => startBackup(),
    onSettled: () => (scope.isCurrent() ? queryClient.invalidateQueries({ queryKey: systemKeys.backups(scope.generation) }) : undefined),
  });
}
