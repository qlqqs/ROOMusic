import { Plus, X } from "lucide-react";
import { useId, useRef, useState } from "react";
import { Button, FailureNotice, ModalDialog } from "../../../shared/ui";
import type { RootDTO } from "../api/decoders";
import type { RootCommands } from "../hooks/use-root-commands";
import { FolderBrowser } from "./folder-browser";
import { exclusionFailureMessages } from "./root-messages";
import styles from "./root-exclusions-dialog.module.css";

type RootExclusionsDialogProps = {
  /** 要设置的目录（取自目录列表的缓存，增删后跟着刷新）；null 表示对话框关闭。 */
  root: RootDTO | null;
  commands: RootCommands;
  onClose: () => void;
};

/**
 * 一个目录里不扫描的子文件夹：列出已排除的（每项可以移除），“添加”在对话框里打开只限这个目录之内的文件夹浏览器，
 * 点“选择”就提交。列表本身就是结果，成功不另外提示；失败显示在对话框里，结果不确定时可以用同一个键重试。
 * 提交中不能关闭；关闭时清除命令状态，页面上不留这里的结果。增删成功后收起浏览器，被点的按钮已经不在了，焦点回到“添加”。
 */
export function RootExclusionsDialog({ root, commands, onClose }: RootExclusionsDialogProps) {
  const titleId = useId();
  const pending = commands.state.status === "pending";
  const close = () => {
    if (pending) return;
    commands.dismiss();
    onClose();
  };
  return (
    <ModalDialog open={root !== null} titleId={titleId} className={styles.dialog} onRequestClose={close}>
      {root === null ? null : <ExclusionsBody root={root} titleId={titleId} commands={commands} onClose={close} />}
    </ModalDialog>
  );
}

function ExclusionsBody({ root, titleId, commands, onClose }: { root: RootDTO; titleId: string; commands: RootCommands; onClose: () => void }) {
  const [adding, setAdding] = useState(false);
  const browserId = useId();
  const addRef = useRef<HTMLButtonElement>(null);
  const focusAdd = () => addRef.current?.focus();
  const settled = {
    onSucceeded: () => {
      setAdding(false);
      focusAdd();
    },
  };
  const { state } = commands;
  const pending = state.status === "pending";
  const target = { id: root.id, name: root.name, revision: root.revision };
  const failed = state.status === "failed" && (state.command.kind === "exclude" || state.command.kind === "unexclude") ? state : null;

  return (
    <div className={styles.content}>
      <h2 id={titleId} className={styles.title}>
        “{root.name}”里不扫描的文件夹
      </h2>
      {root.excluded_paths.length === 0 ? null : (
        <ul className={styles.list}>
          {root.excluded_paths.map((path) => (
            <li key={path} className={styles.row}>
              <span className={styles.path}>{path}</span>
              <Button
                variant="icon"
                icon={<X size={16} />}
                label={`恢复扫描 ${path}`}
                tooltip="恢复扫描"
                busy={pending}
                onClick={() => commands.submit({ kind: "unexclude", root: target, relativePath: path }, settled)}
              />
            </li>
          ))}
        </ul>
      )}
      {adding ? (
        <FolderBrowser
          id={browserId}
          rootId={root.id}
          inline
          onSelect={(path) => {
            if (pending) return;
            commands.submit({ kind: "exclude", root: target, path }, settled);
          }}
        />
      ) : null}
      {failed === null ? null : (
        <FailureNotice
          error={failed.error}
          context="write"
          messages={exclusionFailureMessages}
          title={failed.uncertain ? "结果未确认" : "操作没有完成"}
          action={failed.uncertain ? <Button onClick={() => commands.retry(settled)}>重试</Button> : undefined}
        />
      )}
      <div className={styles.actions}>
        <Button ref={addRef} icon={<Plus size={16} />} aria-expanded={adding} aria-controls={adding ? browserId : undefined} onClick={() => setAdding(!adding)}>
          添加
        </Button>
        <Button variant="primary" disabled={pending} onClick={onClose}>
          关闭
        </Button>
      </div>
    </div>
  );
}
