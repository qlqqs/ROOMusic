import { AudioLines, BarChart3, Bot, DatabaseBackup, FolderSearch, Globe, History, Info, MonitorSmartphone, Speaker, UserRound, Users, Wand, type LucideIcon } from "lucide-react";
import type { UserRole } from "../../features/auth";

export type SettingsSection = {
  to: "/settings/account" | "/settings/playback" | "/settings/agent" | "/settings/library" | "/settings/members" | "/settings/curation" | "/settings/operations" | "/settings/backups" | "/settings/devices" | "/settings/network" | "/settings/clients" | "/settings/insights" | "/settings/about";
  label: string;
  icon: LucideIcon;
  /** 只有管理员看到；页面仍由路由里的 AdminGate 把关，后端也独立鉴权，这里只决定显示。 */
  adminOnly: boolean;
  /** 分栏里的小标题；没有的栏目排在最前、不带小标题。 */
  group?: "管理";
  /** 内容区更宽（扫描目录里有目录列表与记录表格）。 */
  wide?: boolean;
};

export const settingsSections: readonly SettingsSection[] = [
  { to: "/settings/account", label: "账号", icon: UserRound, adminOnly: false },
  { to: "/settings/playback", label: "播放", icon: AudioLines, adminOnly: false },
  { to: "/settings/agent", label: "Agent", icon: Bot, adminOnly: false },
  { to: "/settings/about", label: "关于", icon: Info, adminOnly: false },
  { to: "/settings/library", label: "音乐目录", icon: FolderSearch, adminOnly: true, group: "管理", wide: true },
  { to: "/settings/members", label: "成员", icon: Users, adminOnly: true, group: "管理" },
  { to: "/settings/curation", label: "自动整理", icon: Wand, adminOnly: true, group: "管理" },
  { to: "/settings/devices", label: "局域网设备", icon: Speaker, adminOnly: true, group: "管理" },
  { to: "/settings/network", label: "网络", icon: Globe, adminOnly: true, group: "管理" },
  { to: "/settings/clients", label: "第三方客户端", icon: MonitorSmartphone, adminOnly: true, group: "管理" },
  { to: "/settings/operations", label: "操作记录", icon: History, adminOnly: true, group: "管理" },
  { to: "/settings/backups", label: "备份", icon: DatabaseBackup, adminOnly: true, group: "管理" },
  { to: "/settings/insights", label: "匿名统计", icon: BarChart3, adminOnly: true, group: "管理" },
];

/** 当前角色在设置分栏里看到的栏目：成员只有“账号”“播放”“Agent”与“关于”。 */
export function visibleSettingsSections(role: UserRole): SettingsSection[] {
  return settingsSections.filter((section) => !section.adminOnly || role === "admin");
}

/** 地址所在的栏目：扫描详情（/settings/library/scans/…）算在“音乐目录”下。 */
export function settingsSectionAt(pathname: string): SettingsSection | undefined {
  return settingsSections.find((section) => pathname === section.to || pathname.startsWith(`${section.to}/`));
}

/** 按小标题把栏目分段，保持原来的顺序；相邻且小标题相同的栏目在同一段。 */
export function groupSettingsSections(sections: readonly SettingsSection[]): { group: SettingsSection["group"]; sections: SettingsSection[] }[] {
  const groups: { group: SettingsSection["group"]; sections: SettingsSection[] }[] = [];
  for (const section of sections) {
    const last = groups.at(-1);
    if (last !== undefined && last.group === section.group) last.sections.push(section);
    else groups.push({ group: section.group, sections: [section] });
  }
  return groups;
}
