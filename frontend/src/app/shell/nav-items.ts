import { FolderSearch, LayoutDashboard, type LucideIcon } from "lucide-react";
import type { UserRole } from "../../features/auth";

export type NavItem = {
  to: "/" | "/library";
  label: string;
  icon: LucideIcon;
  /** 只有管理员看到；后端仍然独立鉴权，这里只决定显示。 */
  adminOnly: boolean;
  /** 分组小标题；null 表示不分组。 */
  group: string | null;
};

// 只列真实可用的页面：没有播放、艺人、收藏或 Steward 入口。后续阶段在这里加入对应的页面。
const navItems: readonly NavItem[] = [
  { to: "/", label: "概览", icon: LayoutDashboard, adminOnly: false, group: null },
  { to: "/library", label: "扫描目录", icon: FolderSearch, adminOnly: true, group: "资料库" },
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
