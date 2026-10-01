import { Link } from "@tanstack/react-router";
import { formatCount } from "../../../shared/lib/number";
import { formatDateTime } from "../../../shared/lib/time";
import type { ScanDTO } from "../api/decoders";
import { scanPhase } from "../scan-status";
import styles from "./active-scan.module.css";
import { ScanPhaseTag } from "./scan-phase-tag";

/** 活动扫描的一行摘要：阶段、开始时间与诊断数（诊断数不是进度）。管理员可以从这里进入扫描详情。 */
export function ActiveScan({ scan, detailLink = false }: { scan: ScanDTO; detailLink?: boolean }) {
  return (
    <div className={styles.active}>
      <p>
        <ScanPhaseTag phase={scanPhase(scan)} />
      </p>
      <p className={styles.meta}>
        开始于 {formatDateTime(scan.started_at)}，已发现 {formatCount(scan.diagnostics.total)} 条诊断。
      </p>
      {detailLink ? (
        <p>
          <Link to="/library/scans/$scanId" params={{ scanId: scan.id }}>
            查看扫描详情
          </Link>
        </p>
      ) : null}
    </div>
  );
}
