import { useRef, useState, type FormEvent } from "react";
import { FailureNotice, Notice, TextField, Button } from "../../../shared/ui";
import type { SessionNotice } from "../session/session-controller";
import { useLogin } from "../hooks/use-session-actions";
import styles from "./auth-form.module.css";

const noticeText: Record<SessionNotice, string> = {
  signed_out: "已退出登录。",
  expired: "登录已失效，请重新登录。",
  setup_complete: "管理员已创建，请登录。",
  setup_closed: "初始化已经完成，请登录。",
};

const failureText = {
  invalid_credentials: "用户名或密码错误。",
  too_many_attempts: "登录失败次数太多，请过几分钟再试。",
};

type FieldErrors = { username?: string; password?: string };

type LoginFormProps = {
  /** 为什么回到了登录页：退出、失效、初始化后自动登录失败。 */
  notice: SessionNotice | null;
  initialUsername: string | null;
};

export function LoginForm({ notice, initialUsername }: LoginFormProps) {
  const [username, setUsername] = useState(initialUsername ?? "");
  const [password, setPassword] = useState("");
  const [errors, setErrors] = useState<FieldErrors>({});
  const usernameRef = useRef<HTMLInputElement>(null);
  const passwordRef = useRef<HTMLInputElement>(null);
  const login = useLogin();

  function submit(event: FormEvent) {
    event.preventDefault();
    const next: FieldErrors = {};
    if (username.trim() === "") next.username = "请输入用户名。";
    if (password === "") next.password = "请输入密码。";
    setErrors(next);
    if (next.username !== undefined) return usernameRef.current?.focus();
    if (next.password !== undefined) return passwordRef.current?.focus();
    login.mutate({ username: username.trim(), password });
  }

  return (
    <form className={styles.form} onSubmit={submit} noValidate aria-label="登录">
      {notice === null ? null : <Notice>{noticeText[notice]}</Notice>}
      {login.isError ? (
        <FailureNotice error={login.error} messages={failureText} />
      ) : null}
      <div className={styles.fields}>
        <TextField
          ref={usernameRef}
          label="用户名"
          autoComplete="username"
          value={username}
          error={errors.username ?? null}
          onChange={(event) => setUsername(event.target.value)}
        />
        <TextField
          ref={passwordRef}
          label="密码"
          type="password"
          autoComplete="current-password"
          value={password}
          error={errors.password ?? null}
          onChange={(event) => setPassword(event.target.value)}
        />
      </div>
      <div className={styles.actions}>
        <Button type="submit" variant="primary" busy={login.isPending}>
          {login.isPending ? "正在登录…" : "登录"}
        </Button>
      </div>
    </form>
  );
}
