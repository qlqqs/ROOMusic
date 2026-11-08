import { useNavigate, useRouterState } from "@tanstack/react-router";
import { ChevronsUpDown, LogOut, Settings } from "lucide-react";
import { roleLabels, useLogout, useViewerScope } from "../../features/auth";
import { Avatar, Button, FailureNotice, useMenu } from "../../shared/ui";
import styles from "./user-menu.module.css";

/**
 * 侧栏底部的当前用户：一整行是按钮，点开在上方弹出“设置 / 退出登录”。在设置页时这一行显示为当前项。
 * 退出要等后端确认，失败时留在当前页并在按钮上方提示重试，不假装已退出。onNavigate 让窄屏菜单对话框在进入设置时关闭。
 */
export function UserMenu({ onNavigate }: { onNavigate?: () => void }) {
  const { viewer } = useViewerScope();
  const logout = useLogout();
  const navigate = useNavigate();
  const inSettings = useRouterState({ select: (state) => state.location.pathname === "/settings" || state.location.pathname.startsWith("/settings/") });
  const menu = useMenu({
    label: "账号",
    placement: "above",
    matchTriggerWidth: true,
    actions: [
      {
        id: "settings",
        label: "设置",
        icon: <Settings size={16} />,
        onSelect: () => {
          onNavigate?.();
          void navigate({ to: "/settings" });
        },
      },
      { id: "logout", label: "退出登录", icon: <LogOut size={16} />, onSelect: () => logout.mutate(), disabled: logout.isPending },
    ],
  });
  const roleText = logout.isPending ? "正在退出…" : roleLabels[viewer.role];
  return (
    <section className={styles.panel} aria-label="当前用户">
      {logout.isError ? (
        <FailureNotice error={logout.error} context="write" title="退出失败" action={<Button onClick={() => logout.mutate()}>重试</Button>} />
      ) : null}
      <button type="button" className={styles.trigger} data-current={inSettings ? "" : undefined} aria-busy={logout.isPending || undefined}
        // 用户名与角色是网格里的两行，文字之间没有空白，读屏会连读；可访问名称显式写成“用户名 角色”，与看到的文字一致。
        aria-label={`${viewer.username} ${roleText}`} {...menu.triggerProps}>
        <Avatar name={viewer.username} />
        <span className={styles.who}>
          <span className={styles.name}>{viewer.username}</span>
          <span className={styles.role}>{roleText}</span>
        </span>
        <ChevronsUpDown className={styles.chevron} size={16} aria-hidden="true" />
      </button>
      {menu.menu}
    </section>
  );
}
