import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import {
  decodeCreatedMember,
  decodeMemberDisabledState,
  decodeMemberList,
  decodePasswordReset,
  decodeSessionsRevoked,
  type CreatedMemberDTO,
  type MemberDisabledStateDTO,
  type MemberListDTO,
  type PasswordResetDTO,
  type SessionsRevokedDTO,
} from "./decoders";

export function fetchMembers(signal?: AbortSignal): Promise<ApiResponse<MemberListDTO>> {
  return apiRequest({ method: "GET", path: "/api/v1/users", signal }, decodeMemberList);
}

// 成员的写操作没有幂等键，也没有 revision：同一个请求发两次就执行两次。所以这里的写请求从不自动重试，
// 结果不确定时由界面刷新列表让用户核对。

export type NewMember = { username: string; password: string };

/** 新建成员（201）。后端只能创建 `user` 角色，不能创建管理员。 */
export function createMember(input: NewMember): Promise<ApiResponse<CreatedMemberDTO>> {
  return apiRequest({ method: "POST", path: "/api/v1/users", body: input }, decodeCreatedMember);
}

/** 停用（disabled: true，同时撤销其全部会话）或重新启用（false）。停用最后一个启用的管理员返回 409 last_admin。 */
export function setMemberDisabled(id: string, disabled: boolean): Promise<ApiResponse<MemberDisabledStateDTO>> {
  return apiRequest({ method: "PATCH", path: `/api/v1/users/${id}`, body: { disabled } }, decodeMemberDisabledState);
}

/** 撤销该成员的全部会话；成员不存在时返回 404 not_found。 */
export function revokeMemberSessions(id: string): Promise<ApiResponse<SessionsRevokedDTO>> {
  return apiRequest({ method: "POST", path: `/api/v1/users/${id}/sessions/revoke` }, decodeSessionsRevoked);
}

/**
 * 替成员设一个新密码，并撤销该成员的全部会话；停用的成员重置后仍是停用。
 * 对自己调用返回 403 self_password_reset（改自己的密码要走“修改密码”，验证当前密码）。
 */
export function resetMemberPassword(id: string, password: string): Promise<ApiResponse<PasswordResetDTO>> {
  return apiRequest({ method: "POST", path: `/api/v1/users/${id}/password`, body: { password } }, decodePasswordReset);
}
