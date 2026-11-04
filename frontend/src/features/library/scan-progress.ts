import { formatCount } from "../../shared/lib/number";
import type { ScanDTO, ScanProgressPhase } from "./api/decoders";

/** 四个工作阶段的中文名：阶段说的是当前目录在做什么，不是整次扫描。 */
export const progressPhaseLabels: Record<ScanProgressPhase, string> = {
  counting: "数文件",
  reading_tags: "读取标签",
  organizing: "整理专辑",
  saving: "保存",
};

type ProgressSource = Pick<ScanDTO, "status" | "progress" | "root_progress">;

/**
 * 进度条显示的百分比：服务端基点向下取整，不在前端推算或补动画。没有成功结束的扫描最多显示 99%，
 * 100% 只属于成功的扫描。没有进度数据（旧服务、旧扫描、刚开始还没有快照）时为 null，进度条不给数值。
 */
export function progressPercent(scan: ProgressSource): number | null {
  const progress = scan.progress ?? null;
  if (progress === null) return null;
  const percent = Math.floor(progress.percent_bp / 100);
  return scan.status === "succeeded" ? percent : Math.min(percent, 99);
}

/** 多个目录时的“第 i / 共 R 个目录”；只有一个目录、或全部目录已处理完（没有当前目录）时为 null。 */
function rootPosition(scan: ProgressSource): string | null {
  const root = scan.root_progress ?? null;
  if (root === null || root.index === null || root.total <= 1) return null;
  return `第 ${formatCount(root.index)} / 共 ${formatCount(root.total)} 个目录`;
}

/**
 * 当前在做什么：计数时是“正在数文件”（多个目录时加上已检查几个目录），之后是当前目录与它的阶段；
 * 全部目录处理完、只剩保存扫描结果时没有当前目录。读屏只在这句话变化时播报（换阶段、换目录），不逐个文件播报。
 * 成功的扫描没有“当前”可说，为 null；失败、取消、不完整的扫描说的是停下的位置。
 */
export function progressStageText(scan: ProgressSource): string | null {
  const progress = scan.progress ?? null;
  if (progress === null || scan.status === "succeeded") return null;
  if (progress.phase === "counting") {
    const counting = scan.status === "running" ? "正在数文件" : "数文件";
    return progress.total > 1 ? `${counting}，已检查 ${formatCount(progress.completed)} / ${formatCount(progress.total)} 个目录` : counting;
  }
  const root = scan.root_progress ?? null;
  if (root !== null && root.index === null) return "保存扫描结果";
  const position = rootPosition(scan);
  const phase = progressPhaseLabels[progress.phase];
  return position === null ? phase : `${position}，${phase}`;
}

/**
 * 累计的文件数（全部目录合计，不是当前目录）。计数时说数到了多少；之后是“已处理 A / T 个文件”。
 * 计数之后目录里多了文件时不写成 A / T（会像是超过了 100%），改说“已处理 A 个文件（计数时 T 个）”；
 * 计数没有完成（取消在计数阶段）时没有总数，只说已处理多少。
 */
export function progressFilesText(scan: ProgressSource): string | null {
  const progress = scan.progress ?? null;
  if (progress === null) return null;
  if (progress.phase === "counting") return `已数到 ${formatCount(progress.files_counted)} 个文件`;
  const processed = formatCount(progress.files_processed);
  if (progress.files_total === null) return `已处理 ${processed} 个文件`;
  if (progress.files_processed > progress.files_total) return `已处理 ${processed} 个文件（计数时 ${formatCount(progress.files_total)} 个）`;
  return `已处理 ${processed} / ${formatCount(progress.files_total)} 个文件`;
}

/** 管理员详情里当前目录正在读取标签时的局部计数，明确是“这个目录”的数，不和全局累计混淆。 */
export function progressRootFilesText(scan: ProgressSource): string | null {
  const progress = scan.progress ?? null;
  if (progress === null || progress.phase !== "reading_tags" || progress.total === 0) return null;
  return `这个目录 ${formatCount(progress.completed)} / ${formatCount(progress.total)} 个文件`;
}

/** 进度条的 aria-valuetext：当前目录与阶段、全局文件累计与整体百分比，读屏不必再拼可见文字。 */
export function progressValueText(scan: ProgressSource): string {
  const percent = progressPercent(scan);
  if (percent === null) return scan.status === "running" ? "正在准备" : "没有进度数据";
  const stage = progressStageText(scan);
  const stopped = scan.status !== "running" && scan.status !== "succeeded";
  const parts = [stage === null ? null : stopped ? `停在${stage}` : stage, progressFilesText(scan), `整体进度 ${percent}%`];
  return parts.filter((part): part is string => part !== null).join("，");
}
