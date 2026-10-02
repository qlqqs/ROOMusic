import { UserPlus } from "lucide-react";
import { useRef, useState, type FormEvent } from "react";
import { isHttpFailure } from "../../../shared/api";
import { Button, TextField } from "../../../shared/ui";
import { passwordHint, validateNewAccount, type NewAccountErrors } from "../../auth";
import type { MemberCommands } from "../hooks/use-member-commands";
import styles from "./create-member-form.module.css";
import { memberFailureMessages } from "./member-messages";

/**
 * 新建成员：用户名与密码（规则与初始化管理员相同，密码按 UTF-8 字节计，需要 12 到 72 字节）。
 * 只能创建“成员”角色。失败时保留输入，成功后清空；用户名重复显示在用户名输入框旁。
 */
export function CreateMemberForm({ commands }: { commands: MemberCommands }) {
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
      {
        onSucceeded: () => {
          setUsername("");
          setPassword("");
          setConfirmation("");
        },
      },
    );
  }

  const busy = state.status === "pending";
  return (
    <form className={styles.form} onSubmit={submit} noValidate aria-label="新建成员">
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
      <div>
        <Button type="submit" variant="primary" icon={<UserPlus size={16} />} busy={busy}>
          {busy && state.command.kind === "create" ? "正在创建…" : "创建成员"}
        </Button>
      </div>
    </form>
  );
}
