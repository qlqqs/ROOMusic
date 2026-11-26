import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import { decodeProxySettings, decodeProxyTest, type ProxyDraft, type ProxySettingsDTO, type ProxyTestDTO } from "./decoders";

const BASE = "/api/v1/admin/network/proxy" as const;

export function fetchProxySettings(signal?: AbortSignal): Promise<ApiResponse<ProxySettingsDTO>> {
  return apiRequest({ method: "GET", path: BASE, signal }, decodeProxySettings);
}

/** 保存代理；带上读到时的 version，别处改过返回 409。返回保存后的设置。 */
export function saveProxySettings(draft: ProxyDraft, version: number): Promise<ApiResponse<ProxySettingsDTO>> {
  return apiRequest({ method: "PUT", path: BASE, body: { ...draft, version } }, decodeProxySettings);
}

/** 按草稿测试一次（不必先保存）；password 省略时后端用已存的密码。 */
export function testProxySettings(draft: Omit<ProxyDraft, "enabled">): Promise<ApiResponse<ProxyTestDTO>> {
  return apiRequest({ method: "POST", path: `${BASE}/test`, body: draft }, decodeProxyTest);
}
