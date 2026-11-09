// 浏览器设备的身份与登记（合同 §8）：设备 ID 由服务端在登记时生成，按账号存在 localStorage，同一浏览器的标签页共用；
// 首次登记用 Web Lock 串行，两个同时打开的标签页不会各登记一台。退出登录不删除设备 ID。
import { decode as d } from "../../../shared/api";
import { loadVolume, type StorageLike } from "../engine/persistence";
import { loadPlaybackSettings } from "../engine/playback-settings";
import type { RegisterBody } from "./api";

export function deviceStorageKey(userId: string): string {
  return `roomusic:playback-device:v1:${userId}`;
}

const storedDeviceDecoder = d.defineDecoder(d.object({ version: d.literal(1), deviceId: d.uuid() }));

/** 存着的设备 ID；没有、损坏或存储不可用时是 null。 */
export function readDeviceId(storage: StorageLike | null, userId: string): string | null {
  try {
    const text = storage?.getItem(deviceStorageKey(userId)) ?? null;
    return text === null ? null : storedDeviceDecoder(JSON.parse(text)).deviceId;
  } catch {
    return null;
  }
}

export function writeDeviceId(storage: StorageLike | null, userId: string, deviceId: string): void {
  try {
    storage?.setItem(deviceStorageKey(userId), JSON.stringify({ version: 1, deviceId }));
  } catch {
    // 存不下就每次打开重新登记一台；不影响这一次播放。
  }
}

/** 按 User-Agent 生成“系统 · 浏览器”（如“iPhone · Safari”）；同名由服务端追加序号。 */
export function deviceNameOf(userAgent: string, maxTouchPoints = 0): string {
  const system = /iPhone/.test(userAgent) ? "iPhone"
    : /iPad/.test(userAgent) || (/Macintosh/.test(userAgent) && maxTouchPoints > 1) ? "iPad"
    : /Android/.test(userAgent) ? "Android"
    : /CrOS/.test(userAgent) ? "ChromeOS"
    : /Macintosh|Mac OS X/.test(userAgent) ? "Mac"
    : /Windows/.test(userAgent) ? "Windows"
    : /Linux/.test(userAgent) ? "Linux"
    : "设备";
  const browser = /Edg(?:e|A|iOS)?\//.test(userAgent) ? "Edge"
    : /OPR\/|Opera/.test(userAgent) ? "Opera"
    : /Firefox\/|FxiOS\//.test(userAgent) ? "Firefox"
    : /Chrome\/|CriOS\//.test(userAgent) ? "Chrome"
    : /Safari\//.test(userAgent) ? "Safari"
    : "浏览器";
  return `${system} · ${browser}`;
}

/** 登记的请求体：名字按 UA 生成；旧版本的音量与播放设置（设备偏好）只在这里读作初值，不再写入，也不删除。 */
export function registerBodyOf(localStorage: StorageLike | null, userAgent: string, maxTouchPoints: number): RegisterBody {
  const volume = loadVolume(localStorage);
  const settings = loadPlaybackSettings(localStorage);
  return {
    name: deviceNameOf(userAgent, maxTouchPoints),
    kind: "browser",
    volume: volume.volume,
    muted: volume.muted,
    settings: { leveling: settings.leveling, crossfade: settings.crossfade, crossfade_seconds: settings.crossfadeSeconds },
  };
}

/** Web Locks 的最小形状；浏览器没有时为 null（每个标签页直接执行）。 */
export type LockManagerLike = {
  request: (name: string, options: { ifAvailable?: boolean; signal?: AbortSignal }, callback: (lock: unknown) => Promise<unknown>) => Promise<unknown>;
};

export function browserLocks(): LockManagerLike | null {
  return typeof navigator !== "undefined" && "locks" in navigator && navigator.locks !== undefined ? (navigator.locks as unknown as LockManagerLike) : null;
}

/** 在账号的登记锁里执行：没有 Web Locks 时直接执行。 */
export function withRegistrationLock<T>(locks: LockManagerLike | null, userId: string, task: () => Promise<T>): Promise<T> {
  if (locks === null) return task();
  return locks.request(`roomusic:device-registration:${userId}`, {}, task) as Promise<T>;
}
