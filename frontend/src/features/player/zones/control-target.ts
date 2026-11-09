// 控制目标（design.md §5.3、合同 §8）：正在控制的播放区存在 localStorage，同一浏览器的标签页共用（storage 事件同步）。
// 没有或无效时是本机播放区。有效 = 播放区还在、设备在线，并且正在播放或停下不到 15 分钟；刚选中的目标从选中时算起，
// 不会因为它早就停了而一选就退回本机。
import { decode as d } from "../../../shared/api";
import type { StorageLike } from "../engine/persistence";
import type { Device, Zone } from "./model";

export const CONTROL_TARGET_IDLE_MS = 15 * 60_000;

export type ControlTarget = { zoneId: string; selectedAt: number };

export function controlTargetStorageKey(userId: string): string {
  return `roomusic:control-target:v1:${userId}`;
}

const storedTargetDecoder = d.defineDecoder(d.object({ version: d.literal(1), zoneId: d.uuid(), selectedAt: d.number() }));

/** 存着的控制目标；没有、损坏或存储不可用时是 null。 */
export function readControlTarget(storage: StorageLike | null, userId: string): ControlTarget | null {
  try {
    const text = storage?.getItem(controlTargetStorageKey(userId)) ?? null;
    if (text === null) return null;
    const stored = storedTargetDecoder(JSON.parse(text));
    return { zoneId: stored.zoneId, selectedAt: stored.selectedAt };
  } catch {
    return null;
  }
}

export function writeControlTarget(storage: StorageLike | null, userId: string, target: ControlTarget | null): void {
  try {
    if (target === null) storage?.removeItem(controlTargetStorageKey(userId));
    else storage?.setItem(controlTargetStorageKey(userId), JSON.stringify({ version: 1, ...target }));
  } catch {
    // 存不下就只在这个标签页里记着。
  }
}

/** 目标还能用：设备在线，正在播放，或停下（从停下与选中两者中较晚的时刻算）不超过 15 分钟。 */
export function isUsableTarget(target: ControlTarget, zone: Zone | undefined, device: Device | undefined, serverNow: number, now: number): boolean {
  if (zone === undefined || device === undefined || !device.online) return false;
  if (zone.intent === "play") return true;
  const pausedFor = zone.pausedAt === null ? 0 : serverNow - zone.pausedAt;
  return Math.min(pausedFor, now - target.selectedAt) <= CONTROL_TARGET_IDLE_MS;
}
