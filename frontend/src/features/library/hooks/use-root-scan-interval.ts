import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { saveRootScanInterval } from "../api/endpoints";
import { rootsQueryOptions } from "./use-roots";

/**
 * 保存一个目录的定时扫描间隔：成功后把新间隔写进目录列表缓存，不重新拉列表。
 * 连续改动按顺序发送（同一个 mutation scope），最后落地的值留在缓存里；失败时缓存不动，界面回到保存前的值。
 */
export function useSaveRootScanInterval() {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  const { queryKey } = rootsQueryOptions(scope.generation);
  return useMutation({
    scope: { id: "root-scan-interval" },
    mutationFn: ({ id, minutes }: { id: string; minutes: number }) => saveRootScanInterval(id, minutes),
    onSuccess: async (response, { id }) => {
      if (!scope.isCurrent()) return;
      await queryClient.cancelQueries({ queryKey });
      const minutes = response.data.poll_interval_minutes;
      queryClient.setQueryData(queryKey, (current) =>
        current === undefined
          ? current
          : { ...current, data: { items: current.data.items.map((root) => (root.id === id ? { ...root, poll_interval_minutes: minutes } : root)) } },
      );
    },
  });
}
