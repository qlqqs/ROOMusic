import { DatabaseBackup } from "lucide-react";
import { failureOf, isHttpFailure } from "../../../shared/api";
import { formatDateTime, UNKNOWN_TIME } from "../../../shared/lib/time";
import { Button, FailureNotice, Notice, Panel, QueryContent, Skeleton, StatList } from "../../../shared/ui";
import type { BackupStatusDTO, BuildInfoDTO } from "../api/decoders";
import { useBackupOverview } from "../hooks/use-backup-overview";
import { useBuildInfo } from "../hooks/use-build-info";
import { useStartBackup } from "../hooks/use-start-backup";
import { BACKUP_BUSY_MESSAGE } from "./backup-messages";
import styles from "./backup-status-section.module.css";

const versionText = (info: BuildInfoDTO | undefined) => (info === undefined ? UNKNOWN_TIME : (info.version ?? "开发版"));

/**
 * 备份页的“状态”区段：版本、最近一次成功与下次自动备份，标题右侧是“立即备份”。
 * 任何来源的备份（定时、迁移前）正在进行时按钮都显示忙碌，点击不发请求；备份目录不可用或最近一次失败时各用一句话提示。
 * 另一个进程正在备份（409 backup_busy）不是错误，只用一句普通提示；其他写失败照常显示错误与“重试”。
 */
export function BackupStatusSection() {
  const overview = useBackupOverview();
  const version = useBuildInfo();
  const start = useStartBackup();

  const running = overview.data?.status.running != null;
  const busy = start.isPending || running;
  const begin = () => start.mutate();
  const otherBackupRunning = start.isError && isHttpFailure(failureOf(start.error), 409, "backup_busy");

  return (
    <Panel
      title="状态"
      action={
        <Button variant="primary" icon={<DatabaseBackup size={16} />} busy={busy} onClick={begin}>
          {busy ? "正在备份…" : "立即备份"}
        </Button>
      }
    >
      <QueryContent query={overview} loading={<Skeleton label="正在加载备份状态" lines={2} />}>
        {(data) => (
          <div className={styles.status}>
            <StatList
              items={[
                { label: "版本", value: versionText(version.data) },
                { label: "最近一次成功", value: data.status.last_success_at === null ? "还没有" : formatDateTime(data.status.last_success_at) },
                { label: "下次自动备份", value: data.status.next_scheduled_at === null ? UNKNOWN_TIME : formatDateTime(data.status.next_scheduled_at) },
              ]}
            />
            <StatusWarning status={data.status} />
          </div>
        )}
      </QueryContent>
      {version.isError ? <FailureNotice error={version.error} action={<Button onClick={() => void version.refetch()}>重试</Button>} /> : null}
      {otherBackupRunning ? <Notice tone="info">{BACKUP_BUSY_MESSAGE}</Notice> : null}
      {start.isError && !otherBackupRunning ? (
        <FailureNotice error={start.error} context="write" action={<Button onClick={begin}>重试</Button>} />
      ) : null}
    </Panel>
  );
}

/** 目录不可用时备份一定会失败，只说目录；目录正常而最近一次失败时说一句（正在备份时不提示，结果马上就有）。 */
function StatusWarning({ status }: { status: BackupStatusDTO }) {
  if (status.directory !== "ready") return <Notice tone="warning">备份目录无法写入，请检查部署设置。</Notice>;
  if (status.last_failure === null || status.running !== null) return null;
  return <Notice tone="warning">{formatDateTime(status.last_failure.at)} 的备份没有成功。</Notice>;
}
