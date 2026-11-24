import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import { decodeLanDevice, decodeLanDeviceGrants, decodeLanDevices, decodeLanDeviceSettings, type LanDeviceDTO, type LanDeviceSettingsDTO } from "./decoders";

const BASE = "/api/v1/admin/upnp" as const;
const udnPath = (udn: string) => `/api/v1/admin/upnp/devices/${encodeURIComponent(udn)}` as const;

export function fetchLanDeviceSettings(signal?: AbortSignal): Promise<ApiResponse<LanDeviceSettingsDTO>> {
  return apiRequest({ method: "GET", path: `${BASE}/settings`, signal }, decodeLanDeviceSettings);
}

/** 改开关（至少一项）；返回改后的设置。 */
export function updateLanDeviceSettings(change: Partial<LanDeviceSettingsDTO>): Promise<ApiResponse<LanDeviceSettingsDTO>> {
  return apiRequest({ method: "PUT", path: `${BASE}/settings`, body: change }, decodeLanDeviceSettings);
}

export function fetchLanDevices(signal?: AbortSignal): Promise<ApiResponse<{ items: LanDeviceDTO[] }>> {
  return apiRequest({ method: "GET", path: `${BASE}/devices`, signal }, decodeLanDevices);
}

/** 手动添加：设备的局域网 IP，或同主机的 HTTP 描述地址。地址不合格时 400 invalid_address。 */
export function addLanDevice(address: string): Promise<ApiResponse<LanDeviceDTO>> {
  return apiRequest({ method: "POST", path: `${BASE}/devices`, body: { address } }, decodeLanDevice);
}

/** 移除手动添加的设备（同时清掉它的账号分配）。 */
export function removeLanDevice(udn: string): Promise<ApiResponse<null>> {
  return apiRequest({ method: "DELETE", path: udnPath(udn) }, () => null);
}

/** 全量替换一台设备可用的账号（共享关闭时生效）。 */
export function putLanDeviceGrants(udn: string, userIds: readonly string[]): Promise<ApiResponse<{ user_ids: string[] }>> {
  return apiRequest({ method: "PUT", path: `${udnPath(udn)}/grants`, body: { user_ids: userIds } }, decodeLanDeviceGrants);
}
