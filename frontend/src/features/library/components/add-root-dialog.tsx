import { useId, useRef } from "react";
import { ModalDialog } from "../../../shared/ui";
import type { RootCommands } from "../hooks/use-root-commands";
import { AddRootForm } from "./add-root-form";
import styles from "./add-root-dialog.module.css";
import { RootCommandNotice } from "./root-command-notice";

type AddRootDialogProps = {
  open: boolean;
  commands: RootCommands;
  /** 路径由区段持有（“重试”成功后也要清空）；关闭对话框不保留输入。 */
  path: string;
  onPathChange: (path: string) => void;
  /** 结果不确定时用同一个幂等键重试；成功后由区段清空输入并关闭对话框。 */
  onRetry: () => void;
  onClose: () => void;
};

/**
 * “添加目录…”的对话框：路径框与文件夹浏览器（AddRootForm）。添加成功后关闭，结果显示在音乐目录卡片下方；
 * 失败的提示（结果不确定时可重试）留在对话框里，路径被拒绝显示在路径框旁，都保留输入。Esc 与“取消”直接关闭（提交中暂时无效）。
 */
export function AddRootDialog({ open, commands, path, onPathChange, onRetry, onClose }: AddRootDialogProps) {
  const titleId = useId();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const pending = commands.state.status === "pending";
  const cancel = () => {
    if (pending) return;
    commands.dismiss();
    onPathChange("");
    onClose();
  };
  return (
    <ModalDialog open={open} titleId={titleId} initialFocus={headingRef} className={styles.dialog} onRequestClose={cancel}>
      <div className={styles.content}>
        <h2 id={titleId} ref={headingRef} tabIndex={-1} className={styles.title}>
          添加音乐目录
        </h2>
        <p className={styles.hint}>输入服务器上的绝对路径，或点路径框选一个文件夹。</p>
        <AddRootForm commands={commands} path={path} onPathChange={onPathChange} onAdded={onClose} onCancel={cancel} />
        {commands.state.status === "failed" ? <RootCommandNotice state={commands.state} onRetry={onRetry} onDismiss={commands.dismiss} /> : null}
      </div>
    </ModalDialog>
  );
}
