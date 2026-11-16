import { ArrowRightLeft, ClipboardCheck, Combine, Disc3, Heart, History, LayoutDashboard, ListMusic, Loader, MicVocal, Star, type LucideIcon } from "lucide-react";
import type { UserRole } from "../../features/auth";

/** 管家入口的图标（原型里的八道光芒），侧栏与标签栏共用。 */
export const StewardIcon: LucideIcon = Loader;

export type NavItem = {
  to: "/" | "/albums" | "/artists" | "/steward" | "/me/liked" | "/me/favorites" | "/me/history" | "/me/playlists" | "/issues" | "/claims" | "/artist-candidates";
  label: string;
  icon: LucideIcon;
  /** 只有管理员看到；后端仍然独立鉴权，这里只决定显示。 */
  adminOnly: boolean;
  /** 分组小标题；null 表示不分组。 */
  group: string | null;
  /** 艺人功能（M5 ⑤）的入口：只在后端有艺人能力（整理中或已就绪）时出现。 */
  artists?: true;
  /** 管家：名字取自设置（默认“管家”），点击开合右侧面板；只在读到管家设置时出现。 */
  steward?: true;
  /** “我的歌单”：旁边有展开收起按钮，展开后列出歌单。 */
  playlists?: true;
  /** 待办入口：本会话已经读到数量时在右侧显示（取自缓存，不为此发请求）。 */
  todo?: "issues" | "claims";
};

// 只列真实可用的页面。扫描目录、成员、操作记录与备份在设置页里（入口是侧栏底部的齿轮按钮）。
// 管理员的待办工具叫“管理”而不是“资料库”：常见音乐应用里“资料库”指自己的音乐，容易误会。
const navItems: readonly NavItem[] = [
  { to: "/", label: "概览", icon: LayoutDashboard, adminOnly: false, group: "聆听" },
  { to: "/albums", label: "专辑", icon: Disc3, adminOnly: false, group: "聆听" },
  { to: "/artists", label: "艺人", icon: MicVocal, adminOnly: false, group: "聆听", artists: true },
  { to: "/steward", label: "管家", icon: StewardIcon, adminOnly: false, group: "聆听", steward: true },
  { to: "/me/liked", label: "我喜欢的音乐", icon: Heart, adminOnly: false, group: "我的音乐" },
  { to: "/me/favorites", label: "我的收藏", icon: Star, adminOnly: false, group: "我的音乐" },
  { to: "/me/history", label: "播放历史", icon: History, adminOnly: false, group: "我的音乐" },
  { to: "/me/playlists", label: "我的歌单", icon: ListMusic, adminOnly: false, group: "我的音乐", playlists: true },
  { to: "/issues", label: "元数据", icon: ClipboardCheck, adminOnly: true, group: "管理", todo: "issues" },
  { to: "/claims", label: "待认领", icon: ArrowRightLeft, adminOnly: true, group: "管理", todo: "claims" },
  { to: "/artist-candidates", label: "重复艺人", icon: Combine, adminOnly: true, group: "管理", artists: true },
];

/** 侧栏里这个地址的名字；不是侧栏入口时是 undefined（管家的页面上下文用它给列表页起名）。 */
export function navLabelOf(path: string): string | undefined {
  return navItems.find((item) => item.to === path)?.label;
}

export type NavGroup = { label: string | null; items: readonly NavItem[] };

/**
 * 当前角色可见的导航，按分组小标题归并（保持首次出现的顺序）。artists 为 false 时不列艺人功能的入口
 * （后端的 artists_state 是 unavailable，或目录状态还没读到）；steward 为 true 时才列管家入口（读到了管家设置）。
 */
export function visibleNavGroups(role: UserRole, { artists = true, steward = false }: { artists?: boolean; steward?: boolean } = {}): NavGroup[] {
  const groups: NavGroup[] = [];
  for (const item of navItems) {
    if (item.adminOnly && role !== "admin") continue;
    if (item.artists === true && !artists) continue;
    if (item.steward === true && !steward) continue;
    const existing = groups.find((group) => group.label === item.group);
    if (existing === undefined) groups.push({ label: item.group, items: [item] });
    else groups.splice(groups.indexOf(existing), 1, { label: existing.label, items: [...existing.items, item] });
  }
  return groups;
}
