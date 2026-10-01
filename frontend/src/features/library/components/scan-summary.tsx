import { CircleDashed, Hourglass } from "lucide-react";
import { formatDateTime } from "../../../shared/lib/time";
import { Panel, QueryContent, Skeleton, StatusTag } from "../../../shared/ui";
import type { ScanDTO } from "../api/decoders";
import { useActiveScan } from "../hooks/use-active-scan";
import { scanPhase, scanPhaseLabels } from "../scan-status";
import styles from "./scan-summary.module.css";

function ActiveScan({ scan }: { scan: ScanDTO }) {
  const phase = scanPhase(scan);
  return (
    <div className={styles.active}>
      <p>
        <StatusTag tone="accent" icon={phase === "cancel_requested" ? <Hourglass size={14} /> : <CircleDashed size={14} />}>
          {scanPhaseLabels[phase]}
        </StatusTag>
      </p>
      <p className={styles.meta}>
        开始于 {formatDateTime(scan.started_at)}，已发现 {scan.diagnostics.total} 条诊断。
      </p>
    </div>
  );
}

/** 概览的扫描区块：活动扫描的状态，或“没有正在运行的扫描”。没有进度百分比，诊断数不是进度。 */
export function ScanSummary() {
  const scan = useActiveScan();
  return (
    <Panel title="扫描">
      <QueryContent query={scan} loading={<Skeleton label="正在加载扫描状态" lines={2} />}>
        {(active) => (active === null ? <p>没有正在运行的扫描。</p> : <ActiveScan scan={active} />)}
      </QueryContent>
    </Panel>
  );
}
