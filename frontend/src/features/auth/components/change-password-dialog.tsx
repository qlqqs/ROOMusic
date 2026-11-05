import { KeyRound } from "lucide-react";
import { useId, useRef, useState, type FormEvent, type RefObject } from "react";
import { describeFailure, failureOf, isHttpFailure, isOutcomeUncertain } from "../../../shared/api";
import { Button, ModalDialog, Notice, TextField } from "../../../shared/ui";
import { newPasswordRuleMessage, passwordHint, validatePasswordChange, type PasswordChangeErrors } from "../account-rules";
import { usePasswordChange, type PasswordChangeMutation } from "../hooks/use-password-change";
import { useViewerScope } from "../session/session-context";
import styles from "./change-password-dialog.module.css";

/**
 * 侧栏身份区的“修改密码”：按钮、对话框与成功提示。成功后对话框关闭，提示留在身份区，当前页面不掉线。
 */
export function ChangePasswordButton() {
  const { viewer } = useViewerScope();
  const mutation = usePasswordChange();
  const [open, setOpen] = useState(false);
  const [changed, setChanged] = useState(false);
  return (
    <>
      {changed ? (
        <Notice
          tone="success"
          action={
            <Button variant="text" onClick={() => setChanged(false)}>
              关闭
            </Button>
          }
        >
          密码已修改。
        </Notice>
      ) : null}
      <Button
        variant="text"
        icon={<KeyRound size={16} />}
        onClick={() => {
          mutation.reset();
          setChanged(false);
          setOpen(true);
        }}
      >
        修改密码
      </Button>
      <ChangePasswordDialog
        open={open}
        username={viewer.username}
        mutation={mutation}
        onClose={() => setOpen(false)}
        onSucceeded={() => {
          setOpen(false);
          setChanged(true);
        }}
      />
    </>
  );
}

type ChangePasswordDialogProps = {
  open: boolean;
  /** 当前账号的用户名：放进隐藏的 username 字段，让密码管理器把新密码记到这个账号下。 */
  username: string;
  mutation: PasswordChangeMutation;
  onClose: () => void;
  onSucceeded: () => void;
};

/** 修改密码的对话框：提交中 Esc 与“取消”暂时无效；当前密码不对、新密码不合规显示在字段旁，其余失败留在对话框里。 */
export function ChangePasswordDialog({ open, username, mutation, onClose, onSucceeded }: ChangePasswordDialogProps) {
  const titleId = useId();
  const headingRef = useRef<HTMLHeadingElement>(null);
  return (
    <ModalDialog open={open} titleId={titleId} initialFocus={headingRef} className={styles.dialog} onRequestClose={() => (mutation.pending ? undefined : onClose())}>
      <ChangePasswordForm titleId={titleId} headingRef={headingRef} username={username} mutation={mutation} onClose={onClose} onSucceeded={onSucceeded} />
    </ModalDialog>
  );
}

type Failure = { error: unknown; uncertain: boolean };

function ChangePasswordForm({
  titleId,
  headingRef,
  username,
  mutation,
  onClose,
  onSucceeded,
}: Omit<ChangePasswordDialogProps, "open"> & { titleId: string; headingRef: RefObject<HTMLHeadingElement | null> }) {
  const [current, setCurrent] = useState("");
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [errors, setErrors] = useState<PasswordChangeErrors>({});
  const [failure, setFailure] = useState<Failure | null>(null);
  const currentRef = useRef<HTMLInputElement>(null);
  const passwordRef = useRef<HTMLInputElement>(null);
  const confirmationRef = useRef<HTMLInputElement>(null);
  const { pending } = mutation;

  const edited = (set: (value: string) => void) => (event: { target: { value: string } }) => {
    set(event.target.value);
    setErrors({});
  };

  function submit(event: FormEvent) {
    event.preventDefault();
    if (pending) return;
    const next = validatePasswordChange({ current, password, confirmation });
    setErrors(next);
    if (next.current !== undefined) return currentRef.current?.focus();
    if (next.password !== undefined) return passwordRef.current?.focus();
    if (next.confirmation !== undefined) return confirmationRef.current?.focus();
    setFailure(null);
    mutation.submit(
      { currentPassword: current, newPassword: password },
      {
        onSucceeded,
        onFailed: (error) => {
          const apiFailure = failureOf(error);
          if (isHttpFailure(apiFailure, 403, "current_password_incorrect")) {
            setErrors({ current: "当前密码不对。" });
            currentRef.current?.focus();
            return;
          }
          if (isHttpFailure(apiFailure, 400, "invalid_input")) {
            setErrors({ password: newPasswordRuleMessage });
            passwordRef.current?.focus();
            return;
          }
          setFailure({ error, uncertain: isOutcomeUncertain(apiFailure) });
        },
      },
    );
  }

  return (
    <form className={styles.content} onSubmit={submit} noValidate>
      <h2 id={titleId} ref={headingRef} tabIndex={-1} className={styles.title}>
        修改密码
      </h2>
      <p className={styles.lead}>改好后，其他设备需要重新登录。</p>
      <input type="text" name="username" autoComplete="username" value={username} readOnly hidden />
      <div className={styles.fields}>
        <TextField
          ref={currentRef}
          label="当前密码"
          type="password"
          autoComplete="current-password"
          value={current}
          error={errors.current ?? null}
          onChange={edited(setCurrent)}
        />
        <TextField
          ref={passwordRef}
          label="新密码"
          type="password"
          autoComplete="new-password"
          description={passwordHint}
          value={password}
          error={errors.password ?? null}
          onChange={edited(setPassword)}
        />
        <TextField
          ref={confirmationRef}
          label="确认新密码"
          type="password"
          autoComplete="new-password"
          value={confirmation}
          error={errors.confirmation ?? null}
          onChange={edited(setConfirmation)}
        />
      </div>
      {failure === null ? null : <PasswordChangeFailure failure={failure} />}
      <div className={styles.actions}>
        <Button onClick={onClose} disabled={pending}>
          取消
        </Button>
        <Button type="submit" variant="primary" busy={pending}>
          {pending ? "正在修改…" : "修改密码"}
        </Button>
      </div>
    </form>
  );
}

/** 字段以外的失败：结果不确定时告诉用户怎么核对（不自动重试），其余按错误码选文案。 */
function PasswordChangeFailure({ failure }: { failure: Failure }) {
  const apiFailure = failureOf(failure.error);
  const description = apiFailure === null ? { message: "发生了未预期的错误，请刷新页面后重试。", requestId: null } : describeFailure(apiFailure, { context: "write" });
  return (
    <Notice tone="error" title={failure.uncertain ? "结果未确认" : "没有修改"} requestId={description.requestId}>
      {failure.uncertain ? "不确定改好了没有，可以退出后用新密码登录试试。" : description.message}
    </Notice>
  );
}
