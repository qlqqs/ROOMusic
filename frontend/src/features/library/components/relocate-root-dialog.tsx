import { useId, useState } from "react";
import { Button, FailureNotice, ModalDialog } from "../../../shared/ui";
import type { RootDTO } from "../api/decoders";
import type { RootCommands } from "../hooks/use-root-commands";
import { FolderBrowser } from "./folder-browser";
import { relocateFailureMessages } from "./root-messages";
import styles from "./relocate-root-dialog.module.css";

type RelocateRootDialogProps = {
  /** 要改路径的目录（取自目录列表的缓存，revision 跟着列表刷新）；null 表示对话框关闭。 */
  root: RootDTO | null;
  commands: RootCommands;
  onClose: () => void;
  /** 改路径成功：对话框由调用方关闭，结果留在页面上。 */
  onRelocated: () => void;
};

/**
 * 改目录路径：用文件夹浏览器（从 `/` 开始，与添加目录相同）选新位置，选中后显示所选路径，点“确定”提交。
 * 失败显示在对话框里，只针对当时提交的那个路径；结果不确定时可以用同一个键重试。提交中不能关闭；取消时清除命令状态。
 */
export function RelocateRootDialog({ root, commands, onClose, onRelocated }: RelocateRootDialogProps) {
  const titleId = useId();
  const pending = commands.state.status === "pending";
  const close = () => {
    if (pending) return;
    commands.dismiss();
    onClose();
  };
  return (
    <ModalDialog open={root !== null} titleId={titleId} className={styles.dialog} onRequestClose={close}>
      {root === null ? null : <RelocateBody root={root} titleId={titleId} commands={commands} onCancel={close} onRelocated={onRelocated} />}
    </ModalDialog>
  );
}

type RelocateBodyProps = {
  root: RootDTO;
  titleId: string;
  commands: RootCommands;
  onCancel: () => void;
  onRelocated: () => void;
};

function RelocateBody({ root, titleId, commands, onCancel, onRelocated }: RelocateBodyProps) {
  const [selected, setSelected] = useState<string | null>(null);
  const browserId = useId();
  const { state } = commands;
  const pending = state.status === "pending";
  const failed = state.status === "failed" && state.command.kind === "relocate" && state.command.path === selected ? state : null;
  const callbacks = { onSucceeded: onRelocated };

  const submit = () => {
    if (pending || selected === null) return;
    commands.submit({ kind: "relocate", root: { id: root.id, name: root.name, revision: root.revision }, path: selected }, callbacks);
  };

  return (
    <div className={styles.content}>
      <h2 id={titleId} className={styles.title}>
        修改“{root.name}”的路径
      </h2>
      <FolderBrowser
        id={browserId}
        inline
        onSelect={(path) => {
          if (!pending) setSelected(path);
        }}
      />
      {selected === null ? null : (
        <p className={styles.selected}>
          <span className={styles.label}>新路径</span>
          <span className={styles.path}>{selected}</span>
        </p>
      )}
      {failed === null ? null : (
        <FailureNotice
          error={failed.error}
          context="write"
          messages={relocateFailureMessages}
          title={failed.uncertain ? "结果未确认" : "操作没有完成"}
          action={failed.uncertain ? <Button onClick={() => commands.retry(callbacks)}>重试</Button> : undefined}
        />
      )}
      <div className={styles.actions}>
        <Button disabled={pending} onClick={onCancel}>
          取消
        </Button>
        <Button variant="primary" busy={pending} disabled={selected === null} onClick={submit}>
          确定
        </Button>
      </div>
    </div>
  );
}
