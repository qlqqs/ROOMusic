import type { Device, DeviceGroup, GroupMemberState, Zone } from "../features/player/zones/model";

// SlimProto 编组的合成设备与播放区（只给前端测试用）：编号 n 决定设备与播放区 ID，名称由调用方给出。

export const slimDeviceId = (n: number) => `00000000-0000-4000-8000-0000000a${String(n).padStart(4, "0")}`;
export const slimZoneId = (n: number) => `00000000-0000-4000-8000-0000000b${String(n).padStart(4, "0")}`;

/** 一台在线、没人占用、不在组里、音量可调的 SlimProto 设备。 */
export function slimDevice(n: number, name: string, overrides: Partial<Device> = {}): Device {
  return {
    deviceId: slimDeviceId(n), zoneId: slimZoneId(n), name, kind: "slimproto", online: true, lastSeenAt: Date.now(),
    volume: 0.5, muted: false, volumeControllable: true, outputSessionId: null,
    lan: { maxSampleRate: 48000, formatDeclared: true, occupiedBy: null, unavailableReason: null },
    group: null, syncDelayMs: 0,
    ...overrides,
  };
}

/** 设备自己的播放区：base 取替身服务端的空播放区，换上设备与播放区 ID。 */
export function slimZone(base: Zone, device: Device, overrides: Partial<Zone> = {}): Zone {
  return { ...base, zoneId: device.zoneId, deviceId: device.deviceId, ...overrides };
}

/** 播放区的组成员（不含主设备）。 */
export function groupOf(...members: readonly (readonly [Device | string, GroupMemberState])[]): Zone["group"] {
  return { members: members.map(([device, state]) => ({ deviceId: typeof device === "string" ? device : device.deviceId, state })) };
}

/** 主设备与成员各自的 Device.group。 */
export const leaderOf = (zone: Zone): DeviceGroup => ({ zoneId: zone.zoneId, role: "leader" });
export const memberOf = (zone: Zone): DeviceGroup => ({ zoneId: zone.zoneId, role: "member" });
