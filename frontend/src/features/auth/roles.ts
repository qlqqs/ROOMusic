import type { UserRole } from "./api/decoders";

/** 角色的显示名：身份区与成员列表共用，是同一个概念，不各写一份。 */
export const roleLabels: Readonly<Record<UserRole, string>> = { admin: "管理员", user: "成员" };
