import type { ReactNode } from "react";
import { useId, useRef } from "react";
import { Button } from "./button";
import styles from "./confirm-dialog.module.css";
import { ModalDialog } from "./modal-dialog";

type ConfirmDialogProps = {
  open: boolean;
  title: string;
  /** 影响范围与后果的说明。 */
  children: ReactNode;
  confirmLabel: string;
  /** 请求进行中：确认按钮忙碌、取消与 Esc 暂时无效，避免关闭一个仍在提交的操作。 */
  pending?: boolean;
  /** 阻塞性错误留在对话框内，不只用短暂提示。 */
  error?: ReactNode;
  onConfirm: () => void;
  onCancel: () => void;
};

/** 破坏性或影响范围较大的操作确认。初始焦点在“取消”上，避免回车直接确认。 */
export function ConfirmDialog({ open, title, children, confirmLabel, pending = false, error, onConfirm, onCancel }: ConfirmDialogProps) {
  const titleId = useId();
  const cancelRef = useRef<HTMLButtonElement>(null);

  return (
    <ModalDialog
      open={open}
      titleId={titleId}
      initialFocus={cancelRef}
      className={styles.dialog}
      onRequestClose={() => {
        if (!pending) onCancel();
      }}
    >
      <div className={styles.content}>
        <h2 id={titleId} className={styles.title}>
          {title}
        </h2>
        <div className={styles.body}>{children}</div>
        {error}
        <div className={styles.actions}>
          <Button ref={cancelRef} onClick={onCancel} disabled={pending}>
            取消
          </Button>
          <Button variant="primary" busy={pending} onClick={onConfirm}>
            {confirmLabel}
          </Button>
        </div>
      </div>
    </ModalDialog>
  );
}
