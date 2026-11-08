import { formatCount } from "../../shared/lib/number";
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
 * 每种阶段的一句说明（运行中由进度条说明，没有额外的话）。失败、已取消、不完整都不会执行全部目录的缺失对账
 * （后端只有 succeeded 才会把此前入库、这次没有再出现的来源标为缺失），所以这几种说没扫到的曲目保持原样；
 * 不再说“已有的曲目不受影响”：停下之前已经保存的变化（包括确认被新文件替换的旧曲目）会保留。
 * 不完整只剩整个目录用不了（没挂载、是空的、数据库出错）这类情况，单个文件或文件夹读不了不再让扫描不完整。
 */
export const scanPhaseNotes: Record<ScanPhase, string | null> = {
  running: null,
  cancel_requested: "正在取消，当前这一步做完就会停下。",
  succeeded: "扫描完成。这次没找到的文件已标为缺失。",
  failed: "扫描失败，没扫到的曲目保持原样。",
  canceled: "扫描已取消，没扫到的曲目保持原样。",
  incomplete: "有目录没扫完，那里的曲目保持原样。",
};

/**
 * 成功的扫描跳过了几个文件（读不了的文件、文件夹与 CUE 各计 1，那里已有的曲目保持原样）：有跳过时返回这个数，
 * 没有跳过、不是成功的扫描或之前的扫描（null）返回 null。
 */
export function skippedFileCount(scan: Pick<ScanDTO, "status" | "skipped_files">): number | null {
  return scan.status === "succeeded" && scan.skipped_files !== null && scan.skipped_files > 0 ? scan.skipped_files : null;
}

export const skippedFilesText = (count: number) => `跳过 ${formatCount(count)} 个文件`;
