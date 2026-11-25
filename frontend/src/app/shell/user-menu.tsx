import { useNavigate } from "@tanstack/react-router";
import { LogOut, Settings, UserRound } from "lucide-react";
import { roleLabels, useLogout, useViewerScope } from "../../features/auth";
import { Avatar, Button, FailureNotice, useMenu, useTooltip } from "../../shared/ui";
import styles from "./user-menu.module.css";

// 退出登录单独一组的分组键：前一组以用户名为键（也是组标题），用户名可以是任何文字，但存不进 NUL（PostgreSQL 的 text 不收），所以两组不会撞名而合并。
const LOGOUT_GROUP = "\u0000logout";

/**
 * 顶栏右端的当前用户：一个头像按钮，点开向下弹出菜单（标题是用户名，账号、设置，最后一组退出登录）。
 * 用户名与角色在悬停提示和读屏名里。退出要等后端确认，失败时在头像下方浮出提示与重试，不假装已退出。
 */
export function UserMenu() {
  const { viewer } = useViewerScope();
  const logout = useLogout();
  const navigate = useNavigate();
  const name = `${viewer.username}，${logout.isPending ? "正在退出…" : roleLabels[viewer.role]}`;
  const tip = useTooltip(name);
  const menu = useMenu({
    label: "账号",
    actions: [
      { id: "account", label: "账号", icon: <UserRound size={16} />, group: viewer.username, onSelect: () => void navigate({ to: "/settings/account" }) },
      { id: "settings", label: "设置", icon: <Settings size={16} />, group: viewer.username, onSelect: () => void navigate({ to: "/settings" }) },
      { id: "logout", label: "退出登录", icon: <LogOut size={16} />, group: LOGOUT_GROUP, untitledGroup: true, onSelect: () => logout.mutate(), disabled: logout.isPending },
    ],
  });
  return (
    <>
      <button
        type="button"
        className={styles.trigger}
        aria-label={name}
        aria-busy={logout.isPending || undefined}
        {...menu.triggerProps}
        onPointerEnter={tip.triggerProps.onPointerEnter}
        onPointerLeave={tip.triggerProps.onPointerLeave}
        onPointerDown={tip.triggerProps.onPointerDown}
      >
        <Avatar name={viewer.username} />
      </button>
      {tip.tooltip}
      {menu.menu}
      {logout.isError ? (
        <div className={styles.error}>
          <FailureNotice error={logout.error} context="write" title="退出失败" action={<Button onClick={() => logout.mutate()}>重试</Button>} />
        </div>
      ) : null}
    </>
  );
}
