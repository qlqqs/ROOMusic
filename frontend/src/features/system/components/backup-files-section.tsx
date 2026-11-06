import { formatBytes } from "../../../shared/lib/bytes";
import { formatDateTime } from "../../../shared/lib/time";
import { DataTable, Panel, QueryContent, Skeleton, VisuallyHidden, type Column } from "../../../shared/ui";
import { BACKUP_LIST_LIMIT, type BackupFileDTO } from "../api/decoders";
import { backupDownloadPath } from "../api/endpoints";
import { useBackupOverview } from "../hooks/use-backup-overview";
import { backupSourceLabels } from "./backup-labels";
import styles from "./backup-files-section.module.css";

const columns: Column<BackupFileDTO>[] = [
  { id: "time", header: "时间", rowHeader: true, cell: (file) => formatDateTime(file.created_at) },
  { id: "source", header: "来源", cell: (file) => backupSourceLabels[file.source] },
  { id: "size", header: "大小", numeric: true, cell: (file) => formatBytes(file.size_bytes) },
  {
    id: "download",
    header: "下载",
    hideLabelWhenStacked: true,
    // 直链交给浏览器下载（附件响应），不经过 JSON 请求；每一行的链接文字相同，时间放进可访问名称。
    cell: (file) => (
      <a href={backupDownloadPath(file.name)} download>
        下载
        <VisuallyHidden> {formatDateTime(file.created_at)} 的备份</VisuallyHidden>
      </a>
    ),
  },
];

/** 备份页的“备份文件”区段：最新的在前，最多列出最近 500 份。与状态区段共用同一个查询。 */
export function BackupFilesSection() {
  const overview = useBackupOverview();
  return (
    <Panel title="备份文件">
      <QueryContent query={overview} loading={<Skeleton label="正在加载备份文件" lines={3} />}>
        {(data) =>
          data.backups.length === 0 ? (
            <p>还没有备份。</p>
          ) : (
            <>
              <DataTable caption="备份文件" columns={columns} rows={data.backups} rowKey={(file) => file.name} />
              {data.total > data.backups.length ? <p className={styles.note}>只显示最近 {BACKUP_LIST_LIMIT} 份。</p> : null}
            </>
          )
        }
      </QueryContent>
    </Panel>
  );
}
