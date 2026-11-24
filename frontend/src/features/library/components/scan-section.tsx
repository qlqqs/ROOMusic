import { Play, RefreshCw } from "lucide-react";
import { Button, QueryContent, SettingRow, SettingsGroup, Skeleton } from "../../../shared/ui";
import { useActiveScan } from "../hooks/use-active-scan";
import { summarizeRoots, useRoots } from "../hooks/use-roots";
import { useBeginScan } from "../hooks/use-scan-commands";
import { ActiveScan } from "./active-scan";
import { ScanScheduleControl } from "./scan-schedule-control";
import { StartScanFeedback } from "./start-scan-feedback";

/**
 * 音乐目录页的“扫描”卡片：有活动扫描时显示它并链接到详情，没有时一行写明现状，右侧是“完整重扫”（每个文件重新读取）与本页唯一的主要按钮“开始扫描”。
 * 还没有启用的目录时两个按钮不可用，说明先添加目录（扫描一个空的目录列表没有意义）。
 * 新建的扫描（202）直接进入详情页；已有扫描在运行（200）时不新建，提示并留在本页，由活动扫描摘要里的链接进入。
 * 卡片第二行是“每天自动扫描”的开关与整点，不论有没有扫描在运行都显示。开始扫描的结果在卡片下方。
 */
export function ScanSection() {
  const active = useActiveScan();
  const roots = useRoots();
  const { start, begin } = useBeginScan();
  const pendingMode = start.isPending ? start.variables : null;
  // 目录列表还没读到时按有目录处理，按钮不先禁用再放开。
  const noRoots = roots.data !== undefined && summarizeRoots(roots.data).active === 0;

  return (
    <SettingsGroup title="扫描" footer={<StartScanFeedback start={start} begin={begin} />}>
      <QueryContent query={active} loading={<Skeleton label="正在加载扫描状态" lines={2} />}>
        {(scan) =>
          scan === null ? (
            <SettingRow
              label={noRoots ? "还没有可以扫描的目录。" : "没有正在运行的扫描。"}
              description={noRoots ? "先在上面添加音乐目录。" : "完整重扫会重新读取每个文件。"}
              plainLabel
            >
              <Button icon={<RefreshCw size={16} />} busy={start.isPending} disabled={noRoots} onClick={() => begin("full")}>
                {pendingMode === "full" ? "正在开始…" : "完整重扫"}
              </Button>
              <Button variant="primary" icon={<Play size={16} />} busy={start.isPending} disabled={noRoots} onClick={() => begin()}>
                {pendingMode === "incremental" ? "正在开始…" : "开始扫描"}
              </Button>
            </SettingRow>
          ) : (
            <ActiveScan scan={scan} detailLink />
          )
        }
      </QueryContent>
      <ScanScheduleControl />
    </SettingsGroup>
  );
}
