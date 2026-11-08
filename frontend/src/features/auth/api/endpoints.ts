import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import {
  decodeLoggedOut,
  decodePasswordChanged,
  decodeSetupStatus,
  decodeViewer,
  type SetupStatusDTO,
  type ViewerDTO,
} from "./decoders";

export type Credentials = { username: string; password: string };

// 初始化状态、初始化、登录与当前会话都用 session: "none"：它们的 401 或失败由会话控制器按自己的语义处理，
// 不能当作“已登录的会话失效”再触发一次会话边界回调。

export function fetchSetupStatus(signal?: AbortSignal): Promise<ApiResponse<SetupStatusDTO>> {
  return apiRequest({ method: "GET", path: "/api/v1/setup/status", signal, session: "none" }, decodeSetupStatus);
}

export function fetchCurrentViewer(signal?: AbortSignal): Promise<ApiResponse<ViewerDTO>> {
  return apiRequest({ method: "GET", path: "/api/v1/auth/me", signal, session: "none" }, decodeViewer);
}

export function postSetupAdmin(credentials: Credentials): Promise<ApiResponse<ViewerDTO>> {
  return apiRequest({ method: "POST", path: "/api/v1/setup/admin", body: credentials, session: "none" }, decodeViewer);
}

export function postLogin(credentials: Credentials): Promise<ApiResponse<ViewerDTO>> {
  return apiRequest({ method: "POST", path: "/api/v1/auth/login", body: credentials, session: "none" }, decodeViewer);
}

export function postLogout(): Promise<ApiResponse<{ logged_out: true }>> {
  return apiRequest({ method: "POST", path: "/api/v1/auth/logout", body: {}, session: "none" }, decodeLoggedOut);
}

/** 管理员改自己的密码不需要当前密码：currentPassword 为 null，请求体里不带 current_password。 */
export type PasswordChange = { currentPassword: string | null; newPassword: string };

/**
 * 修改自己的密码。保持默认的 session: "required"：真正的 401 unauthorized 照常结束会话；
 * 当前密码不对是 403 current_password_incorrect，只拒绝这次操作。“再输入一次”只在前端比较，不传给后端。
 */
export function changeOwnPassword(input: PasswordChange): Promise<ApiResponse<{ password_changed: true }>> {
  return apiRequest(
    {
      method: "POST",
      path: "/api/v1/auth/password",
      body: input.currentPassword === null ? { new_password: input.newPassword } : { current_password: input.currentPassword, new_password: input.newPassword },
    },
    decodePasswordChanged,
  );
}
