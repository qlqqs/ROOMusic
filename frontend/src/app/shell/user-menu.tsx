import { Link, useNavigate } from "@tanstack/react-router";
import { LogOut, MoreHorizontal, Settings, UserRound } from "lucide-react";
import { roleLabels, useLogout, useViewerScope } from "../../features/auth";
import { Avatar, Button, FailureNotice, MenuButton, useTooltip } from "../../shared/ui";
import styles from "./user-menu.module.css";

/**
 * 侧栏底部的当前用户：头像、用户名与角色，右侧是齿轮（一步进入设置，在设置页时是当前项）与“更多”（向上弹出“账号 / 退出登录”）。
 * 退出要等后端确认，失败时留在当前页并在这一行上方提示重试，不假装已退出。
 */
export function UserMenu() {
  const { viewer } = useViewerScope();
  const logout = useLogout();
  const navigate = useNavigate();
  const settingsTip = useTooltip("设置");
  const roleText = logout.isPending ? "正在退出…" : roleLabels[viewer.role];
  return (
    <section className={styles.panel} aria-label="当前用户">
      {logout.isError ? (
        <FailureNotice error={logout.error} context="write" title="退出失败" action={<Button onClick={() => logout.mutate()}>重试</Button>} />
      ) : null}
      <div className={styles.row} aria-busy={logout.isPending || undefined}>
        <Avatar name={viewer.username} />
        <span className={styles.who}>
          <span className={styles.name}>{viewer.username}</span>
          <span className={styles.role}>{roleText}</span>
        </span>
        <Link to="/settings" className={styles.iconLink} aria-label="设置" {...settingsTip.triggerProps}>
          <Settings size={18} aria-hidden="true" />
        </Link>
        {settingsTip.tooltip}
        <MenuButton
          label="账号"
          iconOnly
          icon={<MoreHorizontal size={16} />}
          placement="above"
          actions={[
            { id: "account", label: "账号", icon: <UserRound size={16} />, onSelect: () => void navigate({ to: "/settings/account" }) },
            { id: "logout", label: "退出登录", icon: <LogOut size={16} />, onSelect: () => logout.mutate(), disabled: logout.isPending },
          ]}
        />
      </div>
    </section>
  );
}
