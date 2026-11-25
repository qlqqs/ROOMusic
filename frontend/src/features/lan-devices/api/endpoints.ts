import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import { decodeAirPlayFormat, decodeCastFormat, decodeLanDevice, decodeLanDeviceGrants, decodeLanDevices, decodeLanDeviceSettings, decodeSlimProtoFormat, type AirPlayFormatDTO, type CastFormatDTO, type LanDeviceDTO, type LanDeviceSettingsChange, type LanDeviceSettingsDTO, type SlimProtoFormatDTO } from "./decoders";

const BASE = "/api/v1/admin/upnp" as const;
const udnPath = (udn: string) => `/api/v1/admin/upnp/devices/${encodeURIComponent(udn)}` as const;

export function fetchLanDeviceSettings(signal?: AbortSignal): Promise<ApiResponse<LanDeviceSettingsDTO>> {
  return apiRequest({ method: "GET", path: `${BASE}/settings`, signal }, decodeLanDeviceSettings);
}

/** 改开关（至少一项）；返回改后的设置。 */
export function updateLanDeviceSettings(change: LanDeviceSettingsChange): Promise<ApiResponse<LanDeviceSettingsDTO>> {
  return apiRequest({ method: "PUT", path: `${BASE}/settings`, body: change }, decodeLanDeviceSettings);
}

export function fetchLanDevices(signal?: AbortSignal): Promise<ApiResponse<{ items: LanDeviceDTO[] }>> {
  return apiRequest({ method: "GET", path: `${BASE}/devices`, signal }, decodeLanDevices);
}

/** 手动添加：UPnP 填设备的局域网 IP 或同主机的 HTTP 描述地址；Google Cast 与 AirPlay 填局域网 IP（可带端口）。地址不合格时 400 invalid_address。 */
export function addLanDevice(kind: LanDeviceDTO["kind"], address: string): Promise<ApiResponse<LanDeviceDTO>> {
  return apiRequest({ method: "POST", path: `${BASE}/devices`, body: { kind, address } }, decodeLanDevice);
}

/** 设定一台 Google Cast 设备的编码与采样率上限；不合格时 400 invalid_input。 */
export function putCastFormat(udn: string, format: CastFormatDTO): Promise<ApiResponse<CastFormatDTO>> {
  return apiRequest({ method: "PUT", path: `${udnPath(udn)}/format`, body: format }, decodeCastFormat);
}

/** 移除手动添加的设备（同时清掉它的账号分配）。 */
export function removeLanDevice(udn: string): Promise<ApiResponse<null>> {
  return apiRequest({ method: "DELETE", path: udnPath(udn) }, () => null);
}

/** 全量替换一台设备可用的账号（共享关闭时生效）。 */
export function putLanDeviceGrants(udn: string, userIds: readonly string[]): Promise<ApiResponse<{ user_ids: string[] }>> {
  return apiRequest({ method: "PUT", path: `${udnPath(udn)}/grants`, body: { user_ids: userIds } }, decodeLanDeviceGrants);
}

/** 设定一台 AirPlay 设备的音质；不合格时 400 invalid_input。 */
export function putAirPlayFormat(udn: string, format: AirPlayFormatDTO): Promise<ApiResponse<AirPlayFormatDTO>> {
  return apiRequest({ method: "PUT", path: `${udnPath(udn)}/format`, body: format }, decodeAirPlayFormat);
}

/** 设定一台 SlimProto 播放器的音质：采样率上限（null 跟随播放器）与音量方式；超过播放器声明或不合格时 400 invalid_input。 */
export function putSlimProtoFormat(udn: string, format: SlimProtoFormatDTO): Promise<ApiResponse<SlimProtoFormatDTO>> {
  return apiRequest({ method: "PUT", path: `${udnPath(udn)}/format`, body: format }, decodeSlimProtoFormat);
}

/** Apple TV 等需要屏幕 PIN 的设备：开始配对后设备屏幕显示 PIN。 */
export function startAirPlayPairing(udn: string): Promise<ApiResponse<null>> {
  return apiRequest({ method: "POST", path: `${udnPath(udn)}/pair/start` }, () => null);
}

/** 提交屏幕上的 PIN；过期时 409 pairing_expired，要重新开始。 */
export function finishAirPlayPairing(udn: string, pin: string): Promise<ApiResponse<null>> {
  return apiRequest({ method: "POST", path: `${udnPath(udn)}/pair/finish`, body: { pin } }, () => null);
}
