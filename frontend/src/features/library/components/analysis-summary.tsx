import { AudioLines } from "lucide-react";
import { formatCount } from "../../../shared/lib/number";
import { PendingStatusRow, StatusRow } from "../../../shared/ui";
import { useAnalysisProgress } from "../hooks/use-analysis-progress";

const icon = <AudioLines size={16} />;

/**
 * 概览“资料库状态”里的音频分析一行（所有成员可见，没有去处，不是链接）：进行中写“已分析 X / Y 首”，右侧是百分比；
 * 等扫描时加一句扫描结束后继续；全部分析过写“已完成”；有不能分析的曲目时写出首数。分析没有运行时只写“未运行”。
 * 曲库里还没有曲目时不出现：“已分析 0 首、已完成”没有意义。
 */
export function AnalysisSummary() {
  const progress = useAnalysisProgress();
  if (progress.data === undefined) return <PendingStatusRow icon={icon} label="音频分析" query={progress} />;
  const data = progress.data;
  if (data.state === "unavailable") return <StatusRow icon={icon} label="音频分析" value="未运行" />;
  if (data.total === 0) return null;
  const failed = data.failed > 0 ? `${formatCount(data.failed)} 首无法分析` : null;
  if (data.analyzed + data.failed >= data.total) {
    return <StatusRow icon={icon} label="音频分析" detail={failed ?? `已分析 ${formatCount(data.analyzed)} 首`} value="已完成" />;
  }
  const parts = [`已分析 ${formatCount(data.analyzed)} / ${formatCount(data.total)} 首`, data.state === "waiting_for_scan" ? "扫描结束后继续" : null, failed];
  return (
    <StatusRow
      icon={icon}
      label="音频分析"
      detail={parts.filter((part): part is string => part !== null).join("，")}
      value={`${Math.floor((data.analyzed / data.total) * 100)}%`}
    />
  );
}
