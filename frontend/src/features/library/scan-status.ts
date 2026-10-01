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
