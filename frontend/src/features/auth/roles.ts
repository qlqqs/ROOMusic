import type { UserRole } from "./api/decoders";

/** 角色的显示名：侧栏用户按钮、账号栏与成员列表共用，是同一个概念，不各写一份。 */
export const roleLabels: Readonly<Record<UserRole, string>> = { admin: "管理员", user: "成员" };
