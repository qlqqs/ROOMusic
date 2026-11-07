import { useId, useRef, useState, type FormEvent, type RefObject } from "react";
import { isHttpFailure } from "../../../shared/api";
import { Button, ModalDialog, TextField } from "../../../shared/ui";
import { passwordHint, validateNewAccount, type NewAccountErrors } from "../../auth";
import type { MemberCommands } from "../hooks/use-member-commands";
import styles from "./create-member-form.module.css";
import { MemberCommandNotice } from "./member-command-notice";
import { memberFailureMessages } from "./member-messages";

type CreateMemberDialogProps = {
  open: boolean;
  commands: MemberCommands;
  onClose: () => void;
};

/**
 * 新建成员的对话框：用户名与密码（规则与初始化管理员相同，密码按 UTF-8 字节计，需要 12 到 72 字节），只能创建“成员”角色。
 * 成功后关闭，结果显示在列表上方；失败时错误留在对话框里、保留输入，用户名重复显示在用户名输入框旁。
 * Esc 与“取消”直接关闭（提交中暂时无效），关闭即丢弃输入。
 */
export function CreateMemberDialog({ open, commands, onClose }: CreateMemberDialogProps) {
  const titleId = useId();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const pending = commands.state.status === "pending";
  const cancel = () => {
    if (pending) return;
    commands.dismiss();
    onClose();
  };
  return (
    <ModalDialog open={open} titleId={titleId} initialFocus={headingRef} className={styles.dialog} onRequestClose={cancel}>
      <CreateMemberForm titleId={titleId} headingRef={headingRef} commands={commands} onCreated={onClose} onCancel={cancel} />
    </ModalDialog>
  );
}

type CreateMemberFormProps = {
  titleId: string;
  headingRef: RefObject<HTMLHeadingElement | null>;
  commands: MemberCommands;
  onCreated: () => void;
  onCancel: () => void;
};

function CreateMemberForm({ titleId, headingRef, commands, onCreated, onCancel }: CreateMemberFormProps) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [errors, setErrors] = useState<NewAccountErrors>({});
  const usernameRef = useRef<HTMLInputElement>(null);
  const passwordRef = useRef<HTMLInputElement>(null);
  const confirmationRef = useRef<HTMLInputElement>(null);
  const { state } = commands;
  const trimmed = username.trim();

  // 只对被拒绝的那个用户名显示：用户改了输入，这条错误自然消失。
  const taken =
    state.status === "failed" &&
    state.command.kind === "create" &&
    state.command.username === trimmed &&
    isHttpFailure(state.failure, 409, "username_taken");

  function submit(event: FormEvent) {
    event.preventDefault();
    if (state.status === "pending") return;
    const next = validateNewAccount({ username, password, confirmation });
    setErrors(next);
    if (next.username !== undefined) return usernameRef.current?.focus();
    if (next.password !== undefined) return passwordRef.current?.focus();
    if (next.confirmation !== undefined) return confirmationRef.current?.focus();
    commands.submit(
      { kind: "create", username: trimmed, password },
      { onSucceeded: onCreated },
    );
  }

  const busy = state.status === "pending";
  return (
    <form className={styles.content} onSubmit={submit} noValidate>
      <h2 id={titleId} ref={headingRef} tabIndex={-1} className={styles.title}>
        新建成员
      </h2>
      <div className={styles.fields}>
        <TextField
          ref={usernameRef}
          label="用户名"
          // 这是给另一个人新建账号：不要让浏览器把当前管理员保存的凭据填进来。
          autoComplete="off"
          value={username}
          error={errors.username ?? (taken ? memberFailureMessages.username_taken : null)}
          onChange={(event) => {
            setUsername(event.target.value);
            setErrors({});
          }}
        />
        <TextField
          ref={passwordRef}
          label="密码"
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
          label="确认密码"
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
      <MemberCommandNotice state={state} scope="create" onDismiss={commands.dismiss} />
      <div className={styles.actions}>
        <Button onClick={onCancel} disabled={busy}>
          取消
        </Button>
        <Button type="submit" variant="primary" busy={busy}>
          {busy ? "正在创建…" : "创建"}
        </Button>
      </div>
    </form>
  );
}
