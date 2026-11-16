import { useId, useRef, useState, type FormEvent, type ReactNode } from "react";
import { failureOf, isHttpFailure } from "../../../shared/api";
import { Button, ConfirmDialog, FailureNotice, ModalDialog, Notice, TextField, type MenuAction } from "../../../shared/ui";
import type { ConversationDTO } from "../api/decoders";
import { useDeleteConversation, usePatchConversation } from "../hooks/use-conversations";
import { stopRun } from "../run/run-store";
import styles from "./thread.module.css";

/** 会话没有标题时显示的名字（标题在发第一条消息时由后端取前 32 个字）。 */
export const UNTITLED = "新对话";

export function titleOf(conversation: Pick<ConversationDTO, "title">): string {
  return conversation.title === "" ? UNTITLED : conversation.title;
}

type Target = Pick<ConversationDTO, "id" | "title">;

/**
 * 一个会话的“改名”“删除”：返回菜单项与对话框。正在回答的会话删除前要先停止：确认框改成“停止回答”。
 * running 是本页正在跑或后端标着正在回答。
 */
export function useConversationActions({ onDeleted }: { onDeleted: (id: string) => void }): {
  actionsFor: (conversation: Target, running: boolean) => MenuAction[];
  dialogs: ReactNode;
} {
  const [renaming, setRenaming] = useState<Target | null>(null);
  const [deleting, setDeleting] = useState<{ target: Target; running: boolean } | null>(null);
  const remove = useDeleteConversation();
  const actionsFor = (conversation: Target, running: boolean): MenuAction[] => [
    { id: "rename", label: "改名", onSelect: () => setRenaming(conversation) },
    {
      id: "delete",
      label: "删除",
      danger: true,
      onSelect: () => {
        remove.reset();
        setDeleting({ target: conversation, running });
      },
    },
  ];
  const busy = isHttpFailure(failureOf(remove.error), 409, "conversation_busy");
  const dialogs = (
    <>
      {/* 以会话为 key：每次打开都用当前标题填好输入框，上一次的错误不带过来。 */}
      <RenameDialog key={renaming?.id ?? ""} target={renaming} onClose={() => setRenaming(null)} />
      <ConfirmDialog
        open={deleting !== null}
        title={deleting?.running === true ? "这个对话正在回答" : "删除这个对话？"}
        confirmLabel={deleting?.running === true ? "停止回答" : "删除"}
        pending={remove.isPending}
        error={
          remove.error === null ? undefined : busy ? (
            <Notice tone="warning">这个对话正在回答，先停止再删除。</Notice>
          ) : (
            <FailureNotice error={remove.error} context="write" />
          )
        }
        onCancel={() => setDeleting(null)}
        onConfirm={() => {
          if (deleting === null) return;
          if (deleting.running) {
            stopRun(deleting.target.id);
            setDeleting(null);
            return;
          }
          const { id } = deleting.target;
          remove.mutate(id, {
            onSuccess: () => {
              setDeleting(null);
              onDeleted(id);
            },
          });
        }}
      >
        {deleting === null ? null : deleting.running ? (
          <p>先停止回答，再删除“{titleOf(deleting.target)}”。</p>
        ) : (
          <p>“{titleOf(deleting.target)}”里的消息会一起删除，删除后找不回来。</p>
        )}
      </ConfirmDialog>
    </>
  );
  return { actionsFor, dialogs };
}

function RenameDialog({ target, onClose }: { target: Target | null; onClose: () => void }) {
  const titleId = useId();
  const inputRef = useRef<HTMLInputElement>(null);
  const patch = usePatchConversation();
  const [value, setValue] = useState(target?.title ?? "");
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (target === null) return;
    patch.mutate({ id: target.id, patch: { title: value.trim() } }, { onSuccess: onClose });
  };
  return (
    <ModalDialog open={target !== null} titleId={titleId} initialFocus={inputRef} className={styles.dialog} onRequestClose={() => (patch.isPending ? undefined : onClose())}>
      <form className={styles.dialogContent} onSubmit={submit}>
        <h2 id={titleId} className={styles.dialogTitle}>
          改名
        </h2>
        <TextField ref={inputRef} label="标题" value={value} maxLength={200} onChange={(event) => setValue(event.target.value)} description="留空时显示“新对话”。" />
        {patch.error === null ? null : <FailureNotice error={patch.error} context="write" />}
        <div className={styles.dialogActions}>
          <Button onClick={onClose} disabled={patch.isPending}>
            取消
          </Button>
          <Button type="submit" variant="primary" busy={patch.isPending}>
            保存
          </Button>
        </div>
      </form>
    </ModalDialog>
  );
}
