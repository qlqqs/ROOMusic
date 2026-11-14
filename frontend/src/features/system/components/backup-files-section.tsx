import { Download, MoreHorizontal, Trash2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { formatBytes } from "../../../shared/lib/bytes";
import { formatDateTime } from "../../../shared/lib/time";
import { Button, ConfirmDialog, DataTable, FailureNotice, MenuButton, Notice, QueryContent, SettingsGroup, Skeleton, VisuallyHidden, type Column } from "../../../shared/ui";
import { BACKUP_LIST_LIMIT, type BackupFileDTO } from "../api/decoders";
import { backupDownloadPath } from "../api/endpoints";
import { useBackupOverview } from "../hooks/use-backup-overview";
import { isBackupNotFound, useDeleteBackup } from "../hooks/use-delete-backup";
import { useDeleteBackups } from "../hooks/use-delete-backups";
import { backupSourceLabels } from "./backup-labels";
import {
  BACKUP_DELETE_MESSAGES,
  BACKUP_DELETE_WARNING,
  deletedManyAnnouncement,
  deleteManyFailedTitle,
  deleteManyTitle,
  selectedCountText,
} from "./backup-messages";
import styles from "./backup-files-section.module.css";

const noBackups: readonly BackupFileDTO[] = [];

/**
 * 下载备份：下载是直链，交给浏览器下载（附件响应），不经过 JSON 请求。一次下载多份时逐个触发，
 * 浏览器可能先询问是否允许这个网站下载多个文件。
 */
function downloadBackups(files: readonly BackupFileDTO[]) {
  for (const file of files) {
    const link = document.createElement("a");
    link.href = backupDownloadPath(file.name);
    link.download = "";
    document.body.append(link);
    link.click();
    link.remove();
  }
}

/**
 * 备份页的“备份文件”卡片：最新的在前，最多列出最近 500 份。与状态区段共用同一个查询。
 * 全选是表头的复选框（部分勾选时半选）；勾选后表格上方出现批量操作条（已选几份、下载、删除…）。每行的下载与删除在行尾的“更多”菜单里。
 * 窄容器里表头是隐藏的，全选改放在操作条上（两个全选同一时刻只显示一个）。勾选按文件名记，列表变化后已经不在的自动去掉。
 * 删除成功后被删的行已经不在，焦点回到区段标题，结果只告诉读屏。已经不在的备份（404 或 missing）按删除成功处理。
 */
export function BackupFilesSection() {
  const overview = useBackupOverview();
  const remove = useDeleteBackup();
  const removeMany = useDeleteBackups();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const focusHeadingOnClose = useRef(false);
  const [selected, setSelected] = useState<ReadonlySet<string>>(() => new Set());
  const [removing, setRemoving] = useState<BackupFileDTO | null>(null);
  const [removingMany, setRemovingMany] = useState(false);
  const [failedNames, setFailedNames] = useState<readonly string[]>([]);
  const [announcement, setAnnouncement] = useState("");

  const backups = overview.data?.backups ?? noBackups;
  const chosen = backups.filter((file) => selected.has(file.name));
  const dialogOpen = removing !== null || removingMany;

  // 列表变化（轮询、删除、保存清理规则后的清理）后，去掉已经不在列表里的勾选：在渲染时按上一次见到的列表比较，不另起一次提交。
  const [listedBackups, setListedBackups] = useState(backups);
  if (listedBackups !== backups) {
    setListedBackups(backups);
    const listed = new Set(backups.map((file) => file.name));
    const kept = [...selected].filter((name) => listed.has(name));
    if (kept.length !== selected.size) setSelected(new Set(kept));
  }

  useEffect(() => {
    // 对话框的清理先把焦点还给打开它的控件；删除后那个控件（被删的行、“删除所选”）已经不在，这里再把焦点带到标题。
    if (!dialogOpen && focusHeadingOnClose.current) {
      focusHeadingOnClose.current = false;
      headingRef.current?.focus();
    }
  }, [dialogOpen]);

  const toggle = (name: string, checked: boolean) =>
    setSelected((previous) => {
      const next = new Set(previous);
      if (checked) next.add(name);
      else next.delete(name);
      return next;
    });

  const closeAfterDelete = (text: string) => {
    focusHeadingOnClose.current = true;
    setAnnouncement(text);
  };

  const confirmRemove = () => {
    if (removing === null) return;
    setAnnouncement("");
    remove.mutate(removing.name, {
      onSuccess: () => {
        closeAfterDelete("已删除。");
        setRemoving(null);
      },
      onError: (error) => {
        if (!isBackupNotFound(error)) return;
        closeAfterDelete("");
        setRemoving(null);
      },
    });
  };

  const confirmRemoveMany = () => {
    const names = chosen.map((file) => file.name);
    if (names.length === 0) return;
    setAnnouncement("");
    removeMany.mutate(names, {
      onSuccess: (response) => {
        const { deleted, missing, failed } = response.data;
        const done = new Set([...deleted, ...missing]);
        setSelected((previous) => new Set([...previous].filter((name) => !done.has(name))));
        setFailedNames(failed);
        if (failed.length > 0) return;
        closeAfterDelete(deletedManyAnnouncement(done.size));
        setRemovingMany(false);
      },
    });
  };

  // 部分失败后对话框留着：只列出、只重试还勾选着的那几份。
  const stillFailed = chosen.filter((file) => failedNames.includes(file.name));

  const selectAll = (all: boolean) => setSelected(all ? new Set(backups.map((file) => file.name)) : new Set());
  const askRemoveMany = () => {
    removeMany.reset();
    setFailedNames([]);
    setRemovingMany(true);
  };

  const columns: Column<BackupFileDTO>[] = [
    {
      id: "select",
      header: "选择",
      headerContent: (
        <span className={styles.headSelectAll}>
          <SelectAll total={backups.length} count={chosen.length} label="全选" labelHidden onChange={selectAll} />
        </span>
      ),
      hideLabelWhenStacked: true,
      cell: (file) => (
        <label className={styles.check}>
          <input type="checkbox" checked={selected.has(file.name)} onChange={(event) => toggle(file.name, event.target.checked)} />
          <VisuallyHidden>选择 {formatDateTime(file.created_at)} 的备份</VisuallyHidden>
        </label>
      ),
    },
    { id: "time", header: "时间", rowHeader: true, cell: (file) => formatDateTime(file.created_at) },
    { id: "source", header: "来源", cell: (file) => backupSourceLabels[file.source] },
    { id: "size", header: "大小", numeric: true, cell: (file) => formatBytes(file.size_bytes) },
    {
      id: "actions",
      header: "操作",
      headerContent: <VisuallyHidden>操作</VisuallyHidden>,
      hideLabelWhenStacked: true,
      besideRowHeader: true,
      cell: (file) => (
        <MenuButton
          iconOnly
          icon={<MoreHorizontal size={18} />}
          label={`${formatDateTime(file.created_at)} 的备份的更多操作`}
          actions={[
            { id: "download", label: "下载", icon: <Download size={16} />, onSelect: () => downloadBackups([file]) },
            {
              id: "delete",
              label: "删除…",
              icon: <Trash2 size={16} />,
              danger: true,
              onSelect: () => {
                remove.reset();
                setRemoving(file);
              },
            },
          ]}
        />
      ),
    },
  ];

  return (
    <>
      <SettingsGroup
        title="备份文件"
        headingRef={headingRef}
        footer={overview.data !== undefined && overview.data.total > overview.data.backups.length ? <p className={styles.note}>只显示最近 {BACKUP_LIST_LIMIT} 份。</p> : null}
      >
        <QueryContent query={overview} loading={<Skeleton label="正在加载备份文件" lines={3} />}>
          {(data) =>
            data.backups.length === 0 ? (
              <p>还没有备份。</p>
            ) : (
              <>
                {/* 操作条：宽容器里只在有勾选时出现；窄容器里一直在，带着全选。 */}
                <div className={styles.bulk} data-empty={chosen.length === 0 ? "" : undefined}>
                  <span className={styles.barSelectAll}>
                    <SelectAll total={data.backups.length} count={chosen.length} label="全选" onChange={selectAll} />
                  </span>
                  {chosen.length === 0 ? null : (
                    <>
                      <span className={styles.selectedCount}>{selectedCountText(chosen.length)}</span>
                      <span className={styles.bulkActions}>
                        <Button icon={<Download size={16} />} onClick={() => downloadBackups(chosen)}>
                          <span>
                            下载<VisuallyHidden>所选的 {chosen.length} 份备份</VisuallyHidden>
                          </span>
                        </Button>
                        <span className={styles.danger}>
                          <Button icon={<Trash2 size={16} />} onClick={askRemoveMany}>
                            <span>
                              删除<VisuallyHidden>所选的 {chosen.length} 份备份</VisuallyHidden>…
                            </span>
                          </Button>
                        </span>
                      </span>
                    </>
                  )}
                </div>
                <div className={styles.table}>
                  <DataTable caption="备份文件" columns={columns} rows={data.backups} rowKey={(file) => file.name} />
                </div>
              </>
            )
          }
        </QueryContent>
      </SettingsGroup>
      <VisuallyHidden>
        <span role="status">{announcement}</span>
      </VisuallyHidden>
      <ConfirmDialog
        open={removing !== null}
        title={removing === null ? "" : `删除 ${formatDateTime(removing.created_at)} 的备份？`}
        confirmLabel="删除"
        pending={remove.isPending}
        error={
          remove.isError && !isBackupNotFound(remove.error) ? (
            <FailureNotice error={remove.error} context="write" messages={BACKUP_DELETE_MESSAGES} />
          ) : undefined
        }
        onConfirm={confirmRemove}
        onCancel={() => setRemoving(null)}
      >
        <p>{BACKUP_DELETE_WARNING}</p>
      </ConfirmDialog>
      <ConfirmDialog
        open={removingMany && chosen.length > 0}
        title={deleteManyTitle(chosen.length)}
        confirmLabel="删除"
        pending={removeMany.isPending}
        error={
          removeMany.isError ? (
            <FailureNotice error={removeMany.error} context="write" messages={BACKUP_DELETE_MESSAGES} />
          ) : stillFailed.length > 0 ? (
            <Notice tone="error" title={deleteManyFailedTitle(stillFailed.length)}>
              <ul className={styles.failedList}>
                {stillFailed.map((file) => (
                  <li key={file.name}>{formatDateTime(file.created_at)}</li>
                ))}
              </ul>
            </Notice>
          ) : undefined
        }
        onConfirm={confirmRemoveMany}
        onCancel={() => setRemovingMany(false)}
      >
        <p>{BACKUP_DELETE_WARNING}</p>
      </ConfirmDialog>
    </>
  );
}

/** 全选：全部勾选时勾上，部分勾选时是半选状态；点它在“全部选中”与“全部取消”之间切换。labelHidden 时文字只给读屏（表头里）。 */
function SelectAll({ total, count, label, labelHidden = false, onChange }: { total: number; count: number; label: string; labelHidden?: boolean; onChange: (all: boolean) => void }) {
  const ref = useRef<HTMLInputElement>(null);
  const all = total > 0 && count === total;
  const some = count > 0 && !all;
  useEffect(() => {
    if (ref.current !== null) ref.current.indeterminate = some;
  }, [some]);
  return (
    <label className={styles.check}>
      <input ref={ref} type="checkbox" checked={all} onChange={() => onChange(!all)} />
      {labelHidden ? <VisuallyHidden>{label}</VisuallyHidden> : <span>{label}</span>}
    </label>
  );
}
