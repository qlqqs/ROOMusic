import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { cancelScan, startScan } from "../api/endpoints";
import { libraryKeys } from "../query-keys";

/**
 * 开始扫描：202 新建，200 已有扫描在运行（返回的是那一个）。没有幂等键也不需要：重复开始只会得到同一个扫描。
 * 无论成败都刷新活动扫描，让界面显示服务器当前的状态（连接中断时扫描可能已经开始）。
 */
export function useStartScan() {
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  return useMutation({
    mutationFn: () => startScan(),
    onSettled: () => queryClient.invalidateQueries({ queryKey: libraryKeys.activeScan(generation) }),
  });
}

/**
 * 请求取消扫描：202 已请求，200 扫描已经是终态。请求被接受不等于已取消，终态以后续读取为准；
 * 因此命令结束前先刷新扫描状态与活动扫描，按钮保持忙碌直到界面显示的是服务器的新状态。
 */
export function useCancelScan(scanId: string) {
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  return useMutation({
    mutationFn: () => cancelScan(scanId),
    onSettled: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: libraryKeys.scan(generation, scanId) }),
        queryClient.invalidateQueries({ queryKey: libraryKeys.activeScan(generation) }),
      ]),
  });
}
