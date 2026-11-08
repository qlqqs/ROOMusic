// 播放设置（音量均衡与交叉渐变）：设备偏好，与音量一样存在 localStorage，不含私有数据，不随会话清除。
// 带版本与边界校验：损坏、未知版本与存储不可用都回到默认值，写失败不声称“已保存”。
import { decode as d } from "../../../shared/api";
import type { StorageLike } from "./persistence";

export const PLAYBACK_SETTINGS_STORAGE_KEY = "roomusic:playback-settings:v1";

export const levelingModes = ["off", "auto", "track", "album"] as const;
export type LevelingMode = (typeof levelingModes)[number];

export const crossfadeDurations = [3, 5, 8, 12] as const;
export type CrossfadeSeconds = (typeof crossfadeDurations)[number];

export type PlaybackSettings = { leveling: LevelingMode; crossfade: boolean; crossfadeSeconds: CrossfadeSeconds };

export const defaultPlaybackSettings: PlaybackSettings = { leveling: "auto", crossfade: false, crossfadeSeconds: 5 };

const settingsDecoder = d.defineDecoder(
  d.object({
    version: d.literal(1),
    leveling: d.oneOf(levelingModes),
    crossfade: d.boolean(),
    crossfadeSeconds: d.integer({ min: 1, max: 60 }),
  }),
);

function isCrossfadeSeconds(value: number): value is CrossfadeSeconds {
  return (crossfadeDurations as readonly number[]).includes(value);
}

export function loadPlaybackSettings(storage: StorageLike | null): PlaybackSettings {
  if (storage === null) return defaultPlaybackSettings;
  try {
    const text = storage.getItem(PLAYBACK_SETTINGS_STORAGE_KEY);
    if (text === null) return defaultPlaybackSettings;
    const saved = settingsDecoder(JSON.parse(text));
    return { leveling: saved.leveling, crossfade: saved.crossfade, crossfadeSeconds: isCrossfadeSeconds(saved.crossfadeSeconds) ? saved.crossfadeSeconds : defaultPlaybackSettings.crossfadeSeconds };
  } catch {
    return defaultPlaybackSettings;
  }
}

export function savePlaybackSettings(storage: StorageLike | null, settings: PlaybackSettings): boolean {
  if (storage === null) return false;
  try {
    storage.setItem(PLAYBACK_SETTINGS_STORAGE_KEY, JSON.stringify({ version: 1, ...settings }));
    return true;
  } catch {
    return false;
  }
}
