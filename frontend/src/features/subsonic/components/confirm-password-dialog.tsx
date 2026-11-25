import { useEffect, useId, useRef, useState, type FormEvent, type RefObject } from "react";
import { describeFailure, failureOf, isHttpFailure, isOutcomeUncertain } from "../../../shared/api";
import { Button, ModalDialog, Notice, TextField } from "../../../shared/ui";
import { useConfirmSubsonicPassword } from "../hooks/use-subsonic";
import styles from "./subsonic.module.css";

type ConfirmPasswordDialogProps = {
  open: boolean;
  /** 放进隐藏的 username 字段，让密码管理器填对账号。 */
  username: string;
  /** confirmed：这次打开里确认成功过。 */
  onClose: (confirmed: boolean) => void;
};

/** 确认登录密码：提交中 Esc 与“取消”暂时无效；成功后对话框不关，换成结果与“完成”。 */
export function ConfirmPasswordDialog({ open, username, onClose }: ConfirmPasswordDialogProps) {
  const titleId = useId();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const mutation = useConfirmSubsonicPassword();
  return (
    <ModalDialog
      open={open}
      titleId={titleId}
      initialFocus={headingRef}
      className={styles.dialog}
      onRequestClose={() => (mutation.isPending ? undefined : onClose(mutation.isSuccess))}
    >
      <DialogContent titleId={titleId} headingRef={headingRef} username={username} mutation={mutation} onClose={onClose} />
    </ModalDialog>
  );
}

type ContentProps = {
  titleId: string;
  headingRef: RefObject<HTMLHeadingElement | null>;
  username: string;
  mutation: ReturnType<typeof useConfirmSubsonicPassword>;
  onClose: (confirmed: boolean) => void;
};

/** 内容只在打开时挂载：每次打开都从空表单开始。 */
function DialogContent(props: ContentProps) {
  // 上次打开留下的成功或失败不带进这一次。
  const { reset } = props.mutation;
  useEffect(() => reset(), [reset]);
  const [confirmed, setConfirmed] = useState(false);
  return confirmed ? <Confirmed {...props} /> : <ConfirmForm {...props} onConfirmed={() => setConfirmed(true)} />;
}

/** 成功：表单卸载后焦点移到“完成”上，结果文字经 aria-describedby 一起读出。 */
function Confirmed({ titleId, headingRef, onClose }: ContentProps) {
  const resultId = useId();
  const doneRef = useRef<HTMLButtonElement>(null);
  useEffect(() => doneRef.current?.focus(), []);
  return (
    <div className={styles.content}>
      <h2 id={titleId} ref={headingRef} tabIndex={-1} className={styles.title}>
        确认登录密码
      </h2>
      <p id={resultId} className={styles.lead}>
        已确认，客户端现在可以登录了。
      </p>
      <div className={styles.actions}>
        <Button ref={doneRef} variant="primary" aria-describedby={resultId} onClick={() => onClose(true)}>
          完成
        </Button>
      </div>
    </div>
  );
}

const failureText = {
  too_many_attempts: "密码输错次数太多，请过几分钟再试。",
  subsonic_disabled: "管理员刚刚关闭了第三方客户端连接。",
};

function ConfirmForm({ titleId, headingRef, username, mutation, onClose, onConfirmed }: ContentProps & { onConfirmed: () => void }) {
  const [password, setPassword] = useState("");
  const [fieldError, setFieldError] = useState<string | null>(null);
  const [failure, setFailure] = useState<unknown>(null);
  const passwordRef = useRef<HTMLInputElement>(null);
  const pending = mutation.isPending;

  function submit(event: FormEvent) {
    event.preventDefault();
    if (pending) return;
    if (password === "") {
      setFieldError("请输入登录密码。");
      passwordRef.current?.focus();
      return;
    }
    setFailure(null);
    mutation.mutate(password, {
      onSuccess: onConfirmed,
      onError: (error) => {
        if (isHttpFailure(failureOf(error), 403, "current_password_incorrect")) {
          setFieldError("密码不对。");
          passwordRef.current?.focus();
          return;
        }
        setFailure(error);
      },
    });
  }

  return (
    <form className={styles.content} onSubmit={submit} noValidate>
      <h2 id={titleId} ref={headingRef} tabIndex={-1} className={styles.title}>
        确认登录密码
      </h2>
      <p className={styles.lead}>服务器会用实例密钥加密保存一份登录密码，用来核对客户端的令牌登录。以后改密码会自动更新。</p>
      <input type="text" name="username" autoComplete="username" value={username} readOnly hidden />
      <TextField
        ref={passwordRef}
        label="登录密码"
        type="password"
        autoComplete="current-password"
        value={password}
        error={fieldError}
        onChange={(event) => {
          setPassword(event.target.value);
          setFieldError(null);
        }}
      />
      {failure === null ? null : <ConfirmFailure error={failure} />}
      <div className={styles.actions}>
        <Button onClick={() => onClose(false)} disabled={pending}>
          取消
        </Button>
        <Button type="submit" variant="primary" busy={pending}>
          {pending ? "正在确认…" : "确认"}
        </Button>
      </div>
    </form>
  );
}

/** 字段以外的失败：结果不确定时让用户刷新后看状态，不自动重试。 */
function ConfirmFailure({ error }: { error: unknown }) {
  const apiFailure = failureOf(error);
  const uncertain = isOutcomeUncertain(apiFailure);
  const description = apiFailure === null ? { message: "发生了未预期的错误，请刷新页面后重试。", requestId: null } : describeFailure(apiFailure, { context: "write", messages: failureText });
  return (
    <Notice tone="error" title={uncertain ? "结果未确认" : "没有确认"} requestId={description.requestId}>
      {uncertain ? "不确定有没有保存，刷新页面后看“密码”一行的状态。" : description.message}
    </Notice>
  );
}
