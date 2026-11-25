// 播放区端点（合同 §3–§5）。所有路径都在 /api/v1/me/playback 下，作用于已鉴权的用户；命令不幂等，调用方不自动重试。
import type { PeqWire } from "../peq/document";
import { apiRequest, apiStream, decode as d, type ApiResponse } from "../../../shared/api";
import type { PlayMode } from "../engine/queue";
import {
  decodeClaimResult,
  decodeCommandResult,
  decodeDeviceResult,
  decodeGroupMemberResult,
  decodeGroupVolumeResult,
  decodeRegisterResult,
  decodeReportResult,
  decodeSnapshot,
  decodeZoneQueue,
  type ClaimResult,
  type CommandResult,
  type DeviceResult,
  type GroupVolumeResult,
  type RegisterResult,
  type ReportResult,
} from "./decoders";
import type { ZoneApi } from "./controller";
import type { Capabilities, CrossfadeSeconds, LevelingMode, SampleRateFilter, SampleRateStrategy, Snapshot, StreamQuality, ZoneQueue } from "./model";

const BASE = "/api/v1/me/playback";

const idOf = d.defineDecoder(d.uuid());

/** 设置的部分更新（合同 §3.3 set_settings 与登记时的初值），键名与协议一致。 */
export type SettingsPatch = Partial<{
  leveling: LevelingMode;
  gapless: boolean;
  crossfade: boolean;
  crossfade_seconds: CrossfadeSeconds;
  quality: StreamQuality;
  headroom: { enabled: boolean; db: number; clip_indicator: boolean };
  sample_rate: { strategy: SampleRateStrategy; custom_rate: number | null; filter: SampleRateFilter };
  downmix: { enabled: boolean };
  peq: PeqWire;
  convolution: { enabled: boolean; ir_id: string | null; before_peq: boolean };
}>;

/** 合同 §3.3 的命令。 */
export type ZoneCommand =
  | { command: "play_tracks"; track_ids: readonly string[]; start_index: number }
  | { command: "play_next"; track_ids: readonly string[] }
  | { command: "enqueue"; track_ids: readonly string[] }
  | { command: "play_entry"; entry_id: string }
  | { command: "remove_entry"; entry_id: string }
  | { command: "move_entry"; entry_id: string; to_index: number }
  | { command: "clear_queue" }
  | { command: "set_mode"; mode: PlayMode }
  | { command: "play" }
  | { command: "pause" }
  | { command: "stop" }
  | { command: "next" }
  | { command: "previous" }
  | { command: "seek"; position_seconds: number }
  | { command: "set_settings"; settings: SettingsPatch }
  | { command: "retry" }
  | { command: "transfer"; target_zone_id: string }
  | {
      command: "import";
      entries: readonly { entry_id: string; track_id: string }[];
      current_entry_id: string | null;
      mode: PlayMode;
      position_seconds: number;
      history: readonly string[];
    };

export function fetchSnapshot(signal?: AbortSignal): Promise<ApiResponse<Snapshot>> {
  return apiRequest({ method: "GET", path: BASE, signal }, decodeSnapshot);
}

export function fetchZoneQueue(zoneId: string, signal?: AbortSignal): Promise<ApiResponse<ZoneQueue>> {
  return apiRequest({ method: "GET", path: `${BASE}/zones/${idOf(zoneId)}/queue`, signal }, decodeZoneQueue);
}

export type RegisterBody = { name: string; kind: "browser"; volume: number; muted: boolean; settings: SettingsPatch };

export function registerDevice(body: RegisterBody): Promise<ApiResponse<RegisterResult>> {
  return apiRequest({ method: "POST", path: `${BASE}/devices`, body }, decodeRegisterResult);
}

export function renameDevice(deviceId: string, name: string): Promise<ApiResponse<DeviceResult>> {
  return apiRequest({ method: "PATCH", path: `${BASE}/devices/${idOf(deviceId)}`, body: { name } }, decodeDeviceResult);
}

export function deleteDevice(deviceId: string): Promise<ApiResponse<null>> {
  return apiRequest({ method: "DELETE", path: `${BASE}/devices/${idOf(deviceId)}` }, () => null);
}

export function putDeviceVolume(deviceId: string, volume: number, muted: boolean): Promise<ApiResponse<DeviceResult>> {
  return apiRequest({ method: "PUT", path: `${BASE}/devices/${idOf(deviceId)}/volume`, body: { volume, muted } }, decodeDeviceResult);
}

/** 选中一台局域网设备开始用它（M8 U3）：别人正在用时 409 device_busy，takeover 为 true 时接管（原播放区暂停）。 */
export function activateDevice(deviceId: string, takeover: boolean): Promise<ApiResponse<DeviceResult>> {
  return apiRequest({ method: "POST", path: `${BASE}/devices/${idOf(deviceId)}/activate`, body: { takeover } }, decodeDeviceResult);
}

/** 局域网设备的采样率上限：44100、48000、96000 或 192000。 */
export function putDeviceSampleRate(deviceId: string, rate: number): Promise<ApiResponse<DeviceResult>> {
  return apiRequest({ method: "PUT", path: `${BASE}/devices/${idOf(deviceId)}/sample-rate`, body: { max_sample_rate: rate } }, decodeDeviceResult);
}

/** 把一台 SlimProto 设备加入播放区所在的组（主设备须正在用）：别人在用时 409 device_busy，takeover 为 true 时接管。 */
export function addGroupMember(zoneId: string, deviceId: string, takeover: boolean): Promise<ApiResponse<RegisterResult>> {
  return apiRequest({ method: "POST", path: `${BASE}/zones/${idOf(zoneId)}/group/members`, body: { device_id: deviceId, takeover } }, decodeGroupMemberResult);
}

/** 把设备移出播放组；它回到自己的播放区（暂停）。 */
export function removeGroupMember(zoneId: string, deviceId: string): Promise<ApiResponse<RegisterResult>> {
  return apiRequest({ method: "DELETE", path: `${BASE}/zones/${idOf(zoneId)}/group/members/${idOf(deviceId)}` }, decodeGroupMemberResult);
}

/** 组音量：服务端按各台原来的比例缩放组内能调音量的设备，最响的一台到 volume。 */
export function putGroupVolume(zoneId: string, volume: number): Promise<ApiResponse<GroupVolumeResult>> {
  return apiRequest({ method: "PUT", path: `${BASE}/zones/${idOf(zoneId)}/group/volume`, body: { volume } }, decodeGroupVolumeResult);
}

/** SlimProto 设备的同步微调，毫秒，−500…500。 */
export function putSyncDelay(deviceId: string, ms: number): Promise<ApiResponse<DeviceResult>> {
  return apiRequest({ method: "PUT", path: `${BASE}/devices/${idOf(deviceId)}/sync-delay`, body: { sync_delay_ms: ms } }, decodeDeviceResult);
}

export function sendZoneCommand(zoneId: string, command: ZoneCommand): Promise<ApiResponse<CommandResult>> {
  return apiRequest({ method: "POST", path: `${BASE}/zones/${idOf(zoneId)}/commands`, body: command }, decodeCommandResult);
}

export type ClaimBody = {
  protocol: 1;
  capabilities: Capabilities;
  takeover: boolean;
  playing: { entry_id: string; position_seconds: number } | null;
};

export function claimOutput(deviceId: string, body: ClaimBody): Promise<ApiResponse<ClaimResult>> {
  return apiRequest({ method: "POST", path: `${BASE}/devices/${idOf(deviceId)}/output`, body }, decodeClaimResult);
}

export type ReportBody = {
  kind: "status" | "ended" | "fault";
  stream_seq: number | null;
  stream_seconds: number;
  status: "loading" | "playing" | "paused" | "buffering";
  fault: "autoplay_blocked" | "stream_lost" | null;
};

export function postOutputReport(sessionId: string, body: ReportBody): Promise<ApiResponse<ReportResult>> {
  return apiRequest({ method: "POST", path: `${BASE}/outputs/${idOf(sessionId)}/reports`, body }, decodeReportResult);
}

/** 释放输出会话。页面卸载时也要发出，所以带 keepalive；结果不重要（会话不存在同样 204）。 */
export function releaseOutput(sessionId: string, body: { stream_seq: number | null; stream_seconds: number }): void {
  apiRequest({ method: "POST", path: `${BASE}/outputs/${idOf(sessionId)}/release`, body, keepalive: true }, () => null).catch(() => undefined);
}

/** 打开事件流；输出端标签页带上自己的输出会话，流连着即视为在线。 */
export function openEventStream(outputSessionId: string | null, signal: AbortSignal): Promise<Response> {
  const query = outputSessionId === null ? "" : `?output_session=${idOf(outputSessionId)}`;
  return apiStream({ path: `${BASE}/events${query}`, signal });
}

/** 控制器用的真实端点。 */
export const browserZoneApi: ZoneApi = {
  fetchSnapshot: async () => (await fetchSnapshot()).data,
  fetchZoneQueue: async (zoneId) => (await fetchZoneQueue(zoneId)).data,
  registerDevice: async (body) => (await registerDevice(body)).data,
  sendZoneCommand: async (zoneId, command) => (await sendZoneCommand(zoneId, command)).data,
  putDeviceVolume: async (deviceId, volume, muted) => (await putDeviceVolume(deviceId, volume, muted)).data,
  renameDevice: async (deviceId, name) => (await renameDevice(deviceId, name)).data,
  activateDevice: async (deviceId, takeover) => (await activateDevice(deviceId, takeover)).data,
  putDeviceSampleRate: async (deviceId, rate) => (await putDeviceSampleRate(deviceId, rate)).data,
  deleteDevice: async (deviceId) => {
    await deleteDevice(deviceId);
  },
  addGroupMember: async (zoneId, deviceId, takeover) => (await addGroupMember(zoneId, deviceId, takeover)).data,
  removeGroupMember: async (zoneId, deviceId) => (await removeGroupMember(zoneId, deviceId)).data,
  putGroupVolume: async (zoneId, volume) => (await putGroupVolume(zoneId, volume)).data,
  putSyncDelay: async (deviceId, ms) => (await putSyncDelay(deviceId, ms)).data,
  claimOutput: async (deviceId, body) => (await claimOutput(deviceId, body)).data,
  postOutputReport: async (sessionId, body) => (await postOutputReport(sessionId, body)).data,
  releaseOutput,
  openEventStream,
};
