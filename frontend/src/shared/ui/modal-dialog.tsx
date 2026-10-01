import type { ReactNode, RefObject } from "react";
import { useEffect, useRef } from "react";
import { cx } from "../lib/class-names";
import styles from "./modal-dialog.module.css";

type ModalDialogProps = {
  open: boolean;
  /** 对话框内标题元素的 id，作为对话框的可访问名称。 */
  titleId: string;
  /** 用户按 Esc 时调用；是否真的关闭由调用方决定（例如提交中忽略）。 */
  onRequestClose: () => void;
  /** 打开后获得焦点的元素；缺省时由浏览器聚焦第一个可聚焦元素。 */
  initialFocus?: RefObject<HTMLElement | null>;
  className?: string | undefined;
  children: ReactNode;
};

/**
 * 原生 <dialog> 模态：打开时焦点进入并被约束在其中，关闭后焦点回到打开前的元素，不只靠点遮罩关闭。
 * 子内容只在打开时渲染，关闭即丢弃其中的私有状态。
 */
export function ModalDialog({ open, titleId, onRequestClose, initialFocus, className, children }: ModalDialogProps) {
  const dialogRef = useRef<HTMLDialogElement>(null);

  useEffect(() => {
    const dialog = dialogRef.current;
    if (!open || dialog === null) return;
    const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    if (!dialog.open) dialog.showModal();
    initialFocus?.current?.focus();
    return () => {
      dialog.close();
      if (opener?.isConnected) opener.focus();
    };
  }, [open, initialFocus]);

  return (
    <dialog
      ref={dialogRef}
      className={cx(styles.dialog, className)}
      aria-labelledby={titleId}
      onCancel={(event) => {
        event.preventDefault();
        onRequestClose();
      }}
      onClose={() => {
        // 浏览器自行关闭了对话框（例如连续按 Esc），而调用方仍要求打开：重新打开，由调用方决定何时关闭。
        const dialog = dialogRef.current;
        if (open && dialog !== null && !dialog.open) dialog.showModal();
      }}
    >
      {open ? children : null}
    </dialog>
  );
}
