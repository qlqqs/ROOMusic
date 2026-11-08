import { CircleCheck, CirclePause, FolderMinus } from "lucide-react";
import { formatDateTime } from "../../../shared/lib/time";
import { Button, DataTable, StatusTag, type Column } from "../../../shared/ui";
import type { RootDTO } from "../api/decoders";
import styles from "./root-table.module.css";

type RootTableProps = {
  roots: readonly RootDTO[];
  /** 有命令在进行中：所有操作按钮忙碌（点击被忽略），同一时刻只有一个目录命令。 */
  busy: boolean;
  onDisable: (root: RootDTO) => void;
  onRestore: (root: RootDTO) => void;
  onEditExclusions: (root: RootDTO) => void;
};

function RootStatusTag({ status }: { status: RootDTO["status"] }) {
  return status === "active" ? (
    <StatusTag tone="neutral" icon={<CircleCheck size={14} />}>
      启用
    </StatusTag>
  ) : (
    <StatusTag tone="outline" icon={<CirclePause size={14} />}>
      已停用
    </StatusTag>
  );
}

/** 目录列表：目录名（不显示服务器路径）、状态、更新时间与可用操作。停用要确认，恢复直接执行；不扫描的文件夹在对话框里设置。 */
export function RootTable({ roots, busy, onDisable, onRestore, onEditExclusions }: RootTableProps) {
  const columns: Column<RootDTO>[] = [
    { id: "name", header: "目录", cell: (root) => root.name, rowHeader: true },
    { id: "status", header: "状态", cell: (root) => <RootStatusTag status={root.status} /> },
    { id: "updated", header: "更新时间", cell: (root) => formatDateTime(root.updated_at) },
    {
      id: "actions",
      header: "操作",
      hideLabelWhenStacked: true,
      cell: (root) => (
        <span className={styles.actions}>
          <Button
            variant="icon"
            icon={<FolderMinus size={16} />}
            label={`目录 ${root.name} 里不扫描的文件夹`}
            tooltip="不扫描的文件夹"
            onClick={() => onEditExclusions(root)}
          />
          {root.status === "active" ? (
            <Button busy={busy} aria-label={`停用目录 ${root.name}`} onClick={() => onDisable(root)}>
              停用
            </Button>
          ) : (
            <Button busy={busy} aria-label={`恢复目录 ${root.name}`} onClick={() => onRestore(root)}>
              恢复
            </Button>
          )}
        </span>
      ),
    },
  ];
  return <DataTable caption="音乐目录列表" columns={columns} rows={roots} rowKey={(root) => root.id} />;
}
