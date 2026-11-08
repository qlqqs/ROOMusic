import { Play, RefreshCw } from "lucide-react";
import { Button, Panel, QueryContent, Skeleton } from "../../../shared/ui";
import { useActiveScan } from "../hooks/use-active-scan";
import { useBeginScan } from "../hooks/use-scan-commands";
import { ActiveScan } from "./active-scan";
import styles from "./scan-section.module.css";
import { StartScanFeedback } from "./start-scan-feedback";

/**
 * 扫描目录页的“扫描”区段：有活动扫描时显示它并链接到详情，没有时提供“开始扫描”与“完整重扫”（每个文件重新读取）。
 * 新建的扫描（202）直接进入详情页；已有扫描在运行（200）时不新建，提示并留在本页，由活动扫描摘要里的链接进入。
 */
export function ScanSection() {
  const active = useActiveScan();
  const { start, begin } = useBeginScan();
  const pendingMode = start.isPending ? start.variables : null;

  return (
    <Panel title="扫描">
      <QueryContent query={active} loading={<Skeleton label="正在加载扫描状态" lines={2} />}>
        {(scan) =>
          scan === null ? (
            <div className={styles.idle}>
              <p>没有正在运行的扫描。</p>
              <div className={styles.actions}>
                <Button variant="primary" icon={<Play size={16} />} busy={start.isPending} onClick={() => begin()}>
                  {pendingMode === "incremental" ? "正在开始…" : "开始扫描"}
                </Button>
                <Button icon={<RefreshCw size={16} />} busy={start.isPending} onClick={() => begin("full")}>
                  {pendingMode === "full" ? "正在开始…" : "完整重扫"}
                </Button>
              </div>
            </div>
          ) : (
            <ActiveScan scan={scan} detailLink />
          )
        }
      </QueryContent>
      <StartScanFeedback start={start} begin={begin} />
    </Panel>
  );
}
