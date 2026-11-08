import { AudioLines, DatabaseBackup, FolderSearch, History, UserRound, Users, type LucideIcon } from "lucide-react";
import type { UserRole } from "../../features/auth";

export type SettingsSection = {
  to: "/settings/account" | "/settings/playback" | "/settings/library" | "/settings/members" | "/settings/operations" | "/settings/backups";
  label: string;
  icon: LucideIcon;
  /** 只有管理员看到；页面仍由路由里的 AdminGate 把关，后端也独立鉴权，这里只决定显示。 */
  adminOnly: boolean;
};

export const settingsSections: readonly SettingsSection[] = [
  { to: "/settings/account", label: "账号", icon: UserRound, adminOnly: false },
  { to: "/settings/playback", label: "播放", icon: AudioLines, adminOnly: false },
  { to: "/settings/library", label: "扫描目录", icon: FolderSearch, adminOnly: true },
  { to: "/settings/members", label: "成员", icon: Users, adminOnly: true },
  { to: "/settings/operations", label: "操作记录", icon: History, adminOnly: true },
  { to: "/settings/backups", label: "备份", icon: DatabaseBackup, adminOnly: true },
];

/** 当前角色在设置分栏里看到的栏目：成员只有“账号”与“播放”。 */
export function visibleSettingsSections(role: UserRole): SettingsSection[] {
  return settingsSections.filter((section) => !section.adminOnly || role === "admin");
}
