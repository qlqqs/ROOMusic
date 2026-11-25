import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import { decodeSubsonicConnection, decodeSubsonicSettings, type SubsonicConnectionDTO, type SubsonicSettingsDTO } from "./decoders";

export function fetchSubsonicConnection(signal?: AbortSignal): Promise<ApiResponse<SubsonicConnectionDTO>> {
  return apiRequest({ method: "GET", path: "/api/v1/me/subsonic", signal }, decodeSubsonicConnection);
}

/** 确认一次登录密码，服务器加密保存副本；密码不对 403 current_password_incorrect，输错太多 429，实例已关闭 409 subsonic_disabled。 */
export function confirmSubsonicPassword(password: string): Promise<ApiResponse<null>> {
  return apiRequest({ method: "POST", path: "/api/v1/me/subsonic/confirm-password", body: { password } }, () => null);
}

export function fetchSubsonicSettings(signal?: AbortSignal): Promise<ApiResponse<SubsonicSettingsDTO>> {
  return apiRequest({ method: "GET", path: "/api/v1/admin/subsonic", signal }, decodeSubsonicSettings);
}

/** 开关实例的 Subsonic 接口；关闭时服务器同时删除全部登录密码副本。返回改后的状态。 */
export function setSubsonicEnabled(enabled: boolean): Promise<ApiResponse<SubsonicSettingsDTO>> {
  return apiRequest({ method: "PUT", path: "/api/v1/admin/subsonic", body: { enabled } }, decodeSubsonicSettings);
}
