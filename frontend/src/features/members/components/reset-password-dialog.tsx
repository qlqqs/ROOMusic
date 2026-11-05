import { useId, useRef, useState, type FormEvent, type RefObject } from "react";
import { Button, ModalDialog, TextField } from "../../../shared/ui";
import { passwordHint, validateNewPassword, type NewPasswordErrors } from "../../auth";
import type { MemberDTO } from "../api/decoders";
import styles from "./reset-password-dialog.module.css";

type ResetPasswordDialogProps = {
  /** 要重置密码的成员；null 表示对话框关闭。 */
  member: MemberDTO | null;
  pending: boolean;
  onSubmit: (password: string) => void;
  onCancel: () => void;
};

/**
 * 替成员重置密码：输入新密码两次，本地先按新密码的规则检查。与其他成员对话框一样，命令结束（成功或失败）时由页面关闭，
 * 结果显示在列表上方；提交中 Esc 与“取消”暂时无效。
 */
export function ResetPasswordDialog({ member, pending, onSubmit, onCancel }: ResetPasswordDialogProps) {
  const titleId = useId();
  const headingRef = useRef<HTMLHeadingElement>(null);
  return (
    <ModalDialog open={member !== null} titleId={titleId} initialFocus={headingRef} className={styles.dialog} onRequestClose={() => (pending ? undefined : onCancel())}>
      {member === null ? null : (
        <ResetPasswordForm titleId={titleId} headingRef={headingRef} member={member} pending={pending} onSubmit={onSubmit} onCancel={onCancel} />
      )}
    </ModalDialog>
  );
}

function ResetPasswordForm({
  titleId,
  headingRef,
  member,
  pending,
  onSubmit,
  onCancel,
}: Omit<ResetPasswordDialogProps, "member"> & { member: MemberDTO; titleId: string; headingRef: RefObject<HTMLHeadingElement | null> }) {
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [errors, setErrors] = useState<NewPasswordErrors>({});
  const passwordRef = useRef<HTMLInputElement>(null);
  const confirmationRef = useRef<HTMLInputElement>(null);

  function submit(event: FormEvent) {
    event.preventDefault();
    if (pending) return;
    const next = validateNewPassword(password, confirmation);
    setErrors(next);
    if (next.password !== undefined) return passwordRef.current?.focus();
    if (next.confirmation !== undefined) return confirmationRef.current?.focus();
    onSubmit(password);
  }

  return (
    <form className={styles.content} onSubmit={submit} noValidate>
      <h2 id={titleId} ref={headingRef} tabIndex={-1} className={styles.title}>
        重置“{member.username}”的密码
      </h2>
      <p className={styles.body}>{member.disabled ? "对方仍是停用状态，重新启用后才能登录。" : "对方需要用新密码重新登录。"}</p>
      <div className={styles.fields}>
        <TextField
          ref={passwordRef}
          label="新密码"
          type="password"
          autoComplete="new-password"
          description={passwordHint}
          value={password}
          error={errors.password ?? null}
          onChange={(event) => {
            setPassword(event.target.value);
            setErrors({});
          }}
        />
        <TextField
          ref={confirmationRef}
          label="确认新密码"
          type="password"
          autoComplete="new-password"
          value={confirmation}
          error={errors.confirmation ?? null}
          onChange={(event) => {
            setConfirmation(event.target.value);
            setErrors({});
          }}
        />
      </div>
      <div className={styles.actions}>
        <Button onClick={onCancel} disabled={pending}>
          取消
        </Button>
        <Button type="submit" variant="primary" busy={pending}>
          {pending ? "正在重置…" : "重置密码"}
        </Button>
      </div>
    </form>
  );
}
