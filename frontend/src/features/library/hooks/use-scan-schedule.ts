import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import type { ScanScheduleDTO } from "../api/decoders";
import { fetchScanSchedule, saveScanSchedule } from "../api/endpoints";
import { libraryKeys } from "../query-keys";

/** 每天自动扫描的设置（管理员）。 */
export function useScanSchedule() {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: libraryKeys.scanSchedule(generation),
    queryFn: ({ signal }) => fetchScanSchedule(signal),
    select: (response) => response.data,
  });
}

/**
 * 保存每天自动扫描的设置：整份替换，成功后把响应写进缓存。同一页上连续改动按顺序发送（同一个 mutation scope），
 * 后发的保存总在先发的之后落地，缓存停在最后一次的值。失败时缓存不动，界面回到保存前的值。
 */
export function useSaveScanSchedule() {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  const queryKey = libraryKeys.scanSchedule(scope.generation);
  return useMutation({
    scope: { id: "scan-schedule" },
    mutationFn: (schedule: ScanScheduleDTO) => saveScanSchedule(schedule),
    onSuccess: async (response) => {
      if (!scope.isCurrent()) return;
      await queryClient.cancelQueries({ queryKey });
      queryClient.setQueryData(queryKey, response);
    },
  });
}
