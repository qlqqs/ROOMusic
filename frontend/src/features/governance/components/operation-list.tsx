import { Link } from "@tanstack/react-router";
import { useMemo } from "react";
import { formatDateTime } from "../../../shared/lib/time";
import { Button, DataTable, VisuallyHidden, type Column } from "../../../shared/ui";
import type { OperationItemDTO } from "../api/decoders";
import { changeLines, notRevertibleReason, operationTypeLabel } from "../change-summary";
import styles from "./operation-list.module.css";

type OperationListProps = {
  /** 表格的可访问名称。 */
  caption: string;
  items: readonly OperationItemDTO[];
  /** 是否有“专辑”列：某个版本自己的记录里专辑就是这个版本，不重复。 */
  showRelease: boolean;
  /** 点“撤销”：由调用方打开确认对话框，列表不直接发请求。 */
  onRevert: (item: OperationItemDTO) => void;
};

/** 专辑列：可见的版本链接到它现在所在的专辑（选中这个版本）；已不可见的只写标题，不给打不开的链接。 */
function ReleaseCell({ release }: { release: OperationItemDTO["release"] }) {
  if (release === null) return <span className={styles.muted}>专辑已不存在</span>;
  if (!release.visible) {
    return (
      <>
        {release.title}
        <span className={styles.muted}>（已不可见）</span>
      </>
    );
  }
  return (
    <Link to="/albums/$groupId" params={{ groupId: release.group_id }} search={{ release: release.id }}>
      {release.title}
    </Link>
  );
}

function ChangeCell({ item }: { item: OperationItemDTO }) {
  return (
    <div className={styles.change}>
      <p className={styles.type}>{operationTypeLabel(item)}</p>
      <ul className={styles.lines}>
        {changeLines(item).map((line, index) => (
          <li key={index}>{line}</li>
        ))}
      </ul>
    </div>
  );
}

/**
 * 操作记录表：时间（行标题）、操作者、专辑、改动与操作。可以撤销的有“撤销”按钮，其余在按钮的位置写出原因
 * （已撤销、之后又有修改……），不让用户猜。每行的按钮文字相同，时间与类型放进可访问名称，读屏的按钮列表里能分辨是哪一条。
 */
export function OperationList({ caption, items, showRelease, onRevert }: OperationListProps) {
  const columns = useMemo((): Column<OperationItemDTO>[] => {
    const release: Column<OperationItemDTO>[] = showRelease ? [{ id: "release", header: "专辑", cell: (item) => <ReleaseCell release={item.release} /> }] : [];
    return [
      { id: "time", header: "时间", rowHeader: true, cell: (item) => formatDateTime(item.created_at) },
      { id: "actor", header: "操作者", cell: (item) => item.actor.username },
      ...release,
      { id: "change", header: "改动", cell: (item) => <ChangeCell item={item} /> },
      {
        id: "action",
        header: "操作",
        cell: (item) => {
          const reason = notRevertibleReason(item);
          if (reason !== null) return <span className={styles.muted}>{reason}</span>;
          return (
            <span className={styles.revert}>
              <Button variant="text" onClick={() => onRevert(item)}>
                撤销
                <VisuallyHidden>
                  （{formatDateTime(item.created_at)} 的{operationTypeLabel(item)}）
                </VisuallyHidden>
              </Button>
            </span>
          );
        },
      },
    ];
  }, [showRelease, onRevert]);
  return <DataTable caption={caption} columns={columns} rows={items} rowKey={(item) => item.id} />;
}
