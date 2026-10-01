import { LogOut } from "lucide-react";
import { roleLabels, useLogout, useViewerScope } from "../../features/auth";
import { Button, FailureNotice, StatusTag } from "../../shared/ui";
import styles from "./identity-panel.module.css";

/** 当前身份与退出：退出要等后端确认，失败时留在当前页并提示重试，不假装已退出。 */
export function IdentityPanel() {
  const { viewer } = useViewerScope();
  const logout = useLogout();
  return (
    <section className={styles.panel} aria-label="当前用户">
      <div className={styles.who}>
        <p className={styles.name}>{viewer.username}</p>
        <StatusTag>{roleLabels[viewer.role]}</StatusTag>
      </div>
      {logout.isError ? (
        <FailureNotice error={logout.error} context="write" title="退出失败" action={<Button onClick={() => logout.mutate()}>重试</Button>} />
      ) : null}
      <Button variant="text" icon={<LogOut size={16} />} busy={logout.isPending} onClick={() => logout.mutate()}>
        {logout.isPending ? "正在退出…" : "退出登录"}
      </Button>
    </section>
  );
}
