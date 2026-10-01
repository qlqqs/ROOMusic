import type { ScanDTO } from "./api/decoders";

/**
 * 界面区分的六种扫描阶段：后端只有五种状态，“取消请求中”是 running 加上 cancel_requested_at。
 * 失败、已取消、不完整都不会执行缺失对账，也都不是“成功”。
 */
export type ScanPhase = "running" | "cancel_requested" | "succeeded" | "failed" | "canceled" | "incomplete";

export function scanPhase(scan: Pick<ScanDTO, "status" | "cancel_requested_at">): ScanPhase {
  if (scan.status === "running") return scan.cancel_requested_at === null ? "running" : "cancel_requested";
  return scan.status;
}

export const scanPhaseLabels: Record<ScanPhase, string> = {
  running: "运行中",
  cancel_requested: "取消请求中",
  succeeded: "成功",
  failed: "失败",
  canceled: "已取消",
  incomplete: "不完整",
};

/** 扫描是否已经结束：只有 running 还会变化，轮询据此停止。 */
export function isScanFinished(scan: Pick<ScanDTO, "status">): boolean {
  return scan.status !== "running";
}

/**
 * 每种阶段的说明。失败、已取消、不完整都不会执行缺失对账（后端只有 succeeded 才会把此前入库、这次没有再出现的来源标为缺失），
 * 说明里要把这一点讲清楚；也不暗示扫描有进度：没有总量，不显示百分比或剩余时间。
 */
export const scanPhaseNotes: Record<ScanPhase, string> = {
  running: "扫描正在进行。扫描没有预先统计的总量，所以不显示百分比或剩余时间。",
  cancel_requested: "取消请求已提交，等待扫描停止。扫描会在当前步骤结束后停下，最终状态以服务器为准。",
  succeeded: "扫描完整完成。此前入库、这次没有再出现的来源已标为缺失；音乐文件本身不会被改动。",
  failed: "扫描失败，没有执行缺失对账：此前入库的内容不会因为这次扫描被标为缺失。",
  canceled: "扫描已取消，没有执行缺失对账：此前入库的内容不会因为这次扫描被标为缺失。",
  incomplete:
    "扫描不完整：部分目录或文件没能完整处理，或扫描被中断。没有执行缺失对账，此前入库的内容不会因此被标为缺失。请查看诊断，处理后再次扫描。",
};
