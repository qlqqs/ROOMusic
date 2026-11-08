import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useViewerScope } from "../../auth";
import type { ScanMode } from "../api/decoders";
import { cancelScan, startScan } from "../api/endpoints";
import { libraryKeys } from "../query-keys";

/**
 * 开始扫描：202 新建，200 已有扫描在运行（返回的是那一个）。没有幂等键也不需要：重复开始只会得到同一个扫描。
 * 无论成败都刷新活动扫描，让界面显示服务器当前的状态（连接中断时扫描可能已经开始）；扫描记录同样过期，
 * 但不等它刷新完：它不影响“开始扫描”的结果，也不该拖慢进入详情页。
 */
export function useStartScan() {
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  return useMutation({
    mutationFn: (mode: ScanMode) => startScan(mode),
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: libraryKeys.scanHistory(generation) });
      return queryClient.invalidateQueries({ queryKey: libraryKeys.activeScan(generation) });
    },
  });
}

/**
 * 开始扫描并进入它：新建的扫描（202）直接打开详情页；已有扫描在运行（200）时不新建，留在本页由调用方提示。
 * 扫描区段的“开始扫描”“完整重扫”与改完目录路径后的自动扫描共用；不给模式就是增量扫描。
 */
export function useBeginScan() {
  const scope = useViewerScope();
  const navigate = useNavigate();
  const start = useStartScan();
  const begin = (mode: ScanMode = "incremental") =>
    start.mutate(mode, {
      onSuccess: (response) => {
        if (!scope.isCurrent() || response.status !== 202) return;
        void navigate({ to: "/settings/library/scans/$scanId", params: { scanId: response.data.id } });
      },
    });
  return { start, begin };
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
