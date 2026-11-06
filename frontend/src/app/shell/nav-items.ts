import { ArrowRightLeft, ClipboardCheck, Combine, Disc3, Library, MicVocal, Music2, LayoutDashboard, type LucideIcon } from "lucide-react";
import type { UserRole } from "../../features/auth";

export type NavItem = {
  to: "/" | "/albums" | "/artists" | "/me" | "/now-playing" | "/issues" | "/claims" | "/artist-candidates";
  label: string;
  icon: LucideIcon;
  /** 只有管理员看到；后端仍然独立鉴权，这里只决定显示。 */
  adminOnly: boolean;
  /** 分组小标题；null 表示不分组。 */
  group: string | null;
  /** 艺人功能（M5 ⑤）的入口：只在后端有艺人能力（整理中或已就绪）时出现。 */
  artists?: true;
};

// 只列真实可用的页面；Steward 留待对应阶段。扫描目录、成员、操作记录与备份在设置页里（入口是侧栏底部的用户菜单）。
const navItems: readonly NavItem[] = [
  { to: "/", label: "概览", icon: LayoutDashboard, adminOnly: false, group: "聆听" },
  { to: "/albums", label: "专辑", icon: Disc3, adminOnly: false, group: "聆听" },
  { to: "/artists", label: "艺人", icon: MicVocal, adminOnly: false, group: "聆听", artists: true },
  // “我的音乐”有收藏、歌单与历史三个页签（/me 转到 /me/favorites）。
  { to: "/me", label: "我的音乐", icon: Library, adminOnly: false, group: "聆听" },
  { to: "/now-playing", label: "正在播放", icon: Music2, adminOnly: false, group: "聆听" },
  { to: "/issues", label: "元数据问题", icon: ClipboardCheck, adminOnly: true, group: "资料库" },
  { to: "/claims", label: "待认领", icon: ArrowRightLeft, adminOnly: true, group: "资料库" },
  { to: "/artist-candidates", label: "可能是同一位", icon: Combine, adminOnly: true, group: "资料库", artists: true },
];

export type NavGroup = { label: string | null; items: readonly NavItem[] };

/**
 * 当前角色可见的导航，按分组小标题归并（保持首次出现的顺序）。artists 为 false 时不列艺人功能的入口
 * （后端的 artists_state 是 unavailable，或目录状态还没读到）。
 */
export function visibleNavGroups(role: UserRole, { artists = true }: { artists?: boolean } = {}): NavGroup[] {
  const groups: NavGroup[] = [];
  for (const item of navItems) {
    if (item.adminOnly && role !== "admin") continue;
    if (item.artists === true && !artists) continue;
    const existing = groups.find((group) => group.label === item.group);
    if (existing === undefined) groups.push({ label: item.group, items: [item] });
    else groups.splice(groups.indexOf(existing), 1, { label: existing.label, items: [...existing.items, item] });
  }
  return groups;
}
