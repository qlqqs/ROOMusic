import { Link } from "@tanstack/react-router";
import { RefreshCw } from "lucide-react";
import { PendingStatusRow, StatusRow, VisuallyHidden } from "../../../shared/ui";
import { useActiveScan } from "../hooks/use-active-scan";
import { progressPercent, progressStageText, progressValueText } from "../scan-progress";
import styles from "./scan-summary.module.css";

const icon = <RefreshCw size={16} />;

/**
 * 概览“资料库状态”里的扫描一行（所有成员可见）：没有活动扫描时写“没有正在运行的扫描”；扫描进行中写当前阶段（多个目录时说第几个，
 * 不显示目录名称），右侧是服务端给的整体百分比，下方一条细进度条。诊断数不是进度，这里不显示。
 * 管理员整行可点：进行中进入扫描详情，否则进入扫描目录；成员只能看状态。
 */
export function ScanSummary({ canManage }: { canManage: boolean }) {
  const scan = useActiveScan();
  if (scan.data === undefined) return <PendingStatusRow icon={icon} label="扫描" query={scan} />;
  const active = scan.data;
  if (active === null) {
    return (
      <StatusRow
        icon={icon}
        label="扫描"
        detail="没有正在运行的扫描"
        renderLink={canManage ? ({ className, children }) => <Link to="/settings/library" className={className}>{children}</Link> : undefined}
      />
    );
  }
  const percent = progressPercent(active);
  const stage = progressStageText(active);
  const detail = active.cancel_requested_at !== null ? "正在取消" : percent === null ? "正在准备" : stage === null ? "正在扫描" : `正在扫描 · ${stage}`;
  return (
    <StatusRow
      icon={icon}
      label="扫描"
      detail={detail}
      value={percent === null ? undefined : `${percent}%`}
      meter={
        <>
          <span
            role="progressbar"
            aria-label="扫描进度"
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={percent ?? undefined}
            aria-valuetext={progressValueText(active)}
            className={styles.track}
          >
            {percent === null ? null : <span className={styles.fill} style={{ width: `${percent}%` }} />}
          </span>
          {/* 礼貌型播报：只在阶段或目录变化时读一次，不读每次轮询的数字。 */}
          <VisuallyHidden>
            <span aria-live="polite">{stage ?? ""}</span>
          </VisuallyHidden>
        </>
      }
      renderLink={
        canManage
          ? ({ className, children }) => (
              <Link to="/settings/library/scans/$scanId" params={{ scanId: active.id }} className={className}>
                {children}
              </Link>
            )
          : undefined
      }
    />
  );
}
