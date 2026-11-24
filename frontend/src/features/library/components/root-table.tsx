import { CirclePause, Folder, FolderMinus, FolderPen, MoreHorizontal, RotateCcw, Ban } from "lucide-react";
import { MenuButton, StatusTag, type MenuAction } from "../../../shared/ui";
import type { RootDTO } from "../api/decoders";
import styles from "./root-table.module.css";

type RootTableProps = {
  roots: readonly RootDTO[];
  /** 有命令在进行中：改路径、停用与恢复暂时不可选，同一时刻只有一个目录命令。 */
  busy: boolean;
  onDisable: (root: RootDTO) => void;
  onRestore: (root: RootDTO) => void;
  onEditExclusions: (root: RootDTO) => void;
  onRelocate: (root: RootDTO) => void;
};

/** 只有停用的目录有状态标签：正常扫描的目录是常态，每行都写“启用”只是噪声。 */
function RootStatusTag({ status }: { status: RootDTO["status"] }) {
  return status === "active" ? null : (
    <StatusTag tone="outline" icon={<CirclePause size={14} />}>
      已停用
    </StatusTag>
  );
}

/**
 * 音乐目录列表：每个目录一行——文件夹图标、目录名（接口只给路径的最后一段，不显示服务器路径）、停用时的状态，行尾“更多”菜单里是不扫描的文件夹、改路径与停用/恢复。
 * 停用要确认，恢复直接执行；改路径与不扫描的文件夹在对话框里设置。
 */
export function RootTable({ roots, busy, onDisable, onRestore, onEditExclusions, onRelocate }: RootTableProps) {
  return (
    <ul className={styles.list} aria-label="音乐目录列表" data-settings-flush="">
      {roots.map((root) => {
        const actions: MenuAction[] = [
          { id: "exclusions", label: "不扫描的文件夹…", icon: <FolderMinus size={16} />, onSelect: () => onEditExclusions(root) },
          { id: "relocate", label: "改路径…", icon: <FolderPen size={16} />, disabled: busy, onSelect: () => onRelocate(root) },
          root.status === "active"
            ? { id: "disable", label: "停用目录", icon: <Ban size={16} />, disabled: busy, danger: true, onSelect: () => onDisable(root) }
            : { id: "restore", label: "恢复目录", icon: <RotateCcw size={16} />, disabled: busy, onSelect: () => onRestore(root) },
        ];
        return (
          <li key={root.id} className={styles.row}>
            <Folder className={styles.icon} size={18} aria-hidden="true" />
            <span className={styles.name}>{root.name}</span>
            <span className={styles.status}>
              <RootStatusTag status={root.status} />
            </span>
            <MenuButton iconOnly icon={<MoreHorizontal size={18} />} label={`目录 ${root.name} 的更多操作`} actions={actions} />
          </li>
        );
      })}
    </ul>
  );
}
