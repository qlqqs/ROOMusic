import { Disc3, History, Music2, FolderSearch, LayoutDashboard, Users, type LucideIcon } from "lucide-react";
import type { UserRole } from "../../features/auth";

export type NavItem = {
  to: "/" | "/albums" | "/now-playing" | "/library" | "/members" | "/operations";
  label: string;
  icon: LucideIcon;
  /** 只有管理员看到；后端仍然独立鉴权，这里只决定显示。 */
  adminOnly: boolean;
  /** 分组小标题；null 表示不分组。 */
  group: string | null;
};

// 只列真实可用的页面；艺人、收藏与 Steward 留待对应阶段。
const navItems: readonly NavItem[] = [
  { to: "/", label: "概览", icon: LayoutDashboard, adminOnly: false, group: "聆听" },
  { to: "/albums", label: "专辑", icon: Disc3, adminOnly: false, group: "聆听" },
  { to: "/now-playing", label: "正在播放", icon: Music2, adminOnly: false, group: "聆听" },
  { to: "/library", label: "扫描目录", icon: FolderSearch, adminOnly: true, group: "资料库" },
  { to: "/members", label: "成员", icon: Users, adminOnly: true, group: "资料库" },
  { to: "/operations", label: "操作记录", icon: History, adminOnly: true, group: "资料库" },
];

export type NavGroup = { label: string | null; items: readonly NavItem[] };

/** 当前角色可见的导航，按分组小标题归并（保持首次出现的顺序）。 */
export function visibleNavGroups(role: UserRole): NavGroup[] {
  const groups: NavGroup[] = [];
  for (const item of navItems) {
    if (item.adminOnly && role !== "admin") continue;
    const existing = groups.find((group) => group.label === item.group);
    if (existing === undefined) groups.push({ label: item.group, items: [item] });
    else groups.splice(groups.indexOf(existing), 1, { label: existing.label, items: [...existing.items, item] });
  }
  return groups;
}
