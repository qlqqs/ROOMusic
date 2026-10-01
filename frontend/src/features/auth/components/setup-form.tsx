import { useRef, useState, type FormEvent } from "react";
import { Button, FailureNotice, TextField } from "../../../shared/ui";
import { passwordHint, validateNewAccount, type NewAccountErrors } from "../account-rules";
import { useSetupAdmin } from "../hooks/use-session-actions";
import styles from "./auth-form.module.css";

export function SetupForm() {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [errors, setErrors] = useState<NewAccountErrors>({});
  const usernameRef = useRef<HTMLInputElement>(null);
  const passwordRef = useRef<HTMLInputElement>(null);
  const confirmationRef = useRef<HTMLInputElement>(null);
  const setup = useSetupAdmin();

  function submit(event: FormEvent) {
    event.preventDefault();
    const next = validateNewAccount({ username, password, confirmation });
    setErrors(next);
    if (next.username !== undefined) return usernameRef.current?.focus();
    if (next.password !== undefined) return passwordRef.current?.focus();
    if (next.confirmation !== undefined) return confirmationRef.current?.focus();
    setup.mutate({ username: username.trim(), password });
  }

  return (
    <form className={styles.form} onSubmit={submit} noValidate aria-label="创建管理员">
      {setup.isError ? (
        <FailureNotice
          error={setup.error}
          context="write"
          messages={{
            invalid_input: "用户名不能为空，密码至少需要 12 个字节。",
            setup_closed: "初始化已经完成，请登录。",
          }}
        />
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
          autoComplete="new-password"
          description={passwordHint}
          value={password}
          error={errors.password ?? null}
          onChange={(event) => setPassword(event.target.value)}
        />
        <TextField
          ref={confirmationRef}
          label="确认密码"
          type="password"
          autoComplete="new-password"
          value={confirmation}
          error={errors.confirmation ?? null}
          onChange={(event) => setConfirmation(event.target.value)}
        />
      </div>
      <div className={styles.actions}>
        <Button type="submit" variant="primary" busy={setup.isPending}>
          {setup.isPending ? "正在创建…" : "创建管理员并登录"}
        </Button>
      </div>
    </form>
  );
}
