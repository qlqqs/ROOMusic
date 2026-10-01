import { Ban, CircleCheck, CircleDashed, CircleX, Hourglass, TriangleAlert } from "lucide-react";
import type { ReactNode } from "react";
import { StatusTag } from "../../../shared/ui";
import { scanPhaseLabels, type ScanPhase } from "../scan-status";

// 状态由文字和图标表达，色调只区分层级：金色不等于成功，也不等于风险。
const phaseIcons: Record<ScanPhase, ReactNode> = {
  running: <CircleDashed size={14} />,
  cancel_requested: <Hourglass size={14} />,
  succeeded: <CircleCheck size={14} />,
  failed: <CircleX size={14} />,
  canceled: <Ban size={14} />,
  incomplete: <TriangleAlert size={14} />,
};

const phaseTones: Record<ScanPhase, "neutral" | "accent" | "outline"> = {
  running: "accent",
  cancel_requested: "accent",
  succeeded: "neutral",
  failed: "outline",
  canceled: "neutral",
  incomplete: "outline",
};

/** 扫描阶段标签：六种阶段各有不同的文字与图标，不会把失败、取消、不完整都显示成“完成”。 */
export function ScanPhaseTag({ phase }: { phase: ScanPhase }) {
  return (
    <StatusTag tone={phaseTones[phase]} icon={phaseIcons[phase]}>
      {scanPhaseLabels[phase]}
    </StatusTag>
  );
}
