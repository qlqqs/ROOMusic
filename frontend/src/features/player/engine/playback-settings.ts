// 旧版本的播放设置（音量均衡与交叉渐变，localStorage 的设备偏好）：只在登记设备时读作播放区的初值，不再写入，也不删除。
// 带版本与边界校验：损坏、未知版本与存储不可用都回到默认值。播放设置现在归播放区（服务端），见 zones/model.ts。
import { decode as d } from "../../../shared/api";
import { crossfadeDurations, levelingModes, type CrossfadeSeconds, type LevelingMode } from "../zones/model";
import type { StorageLike } from "./persistence";

export const PLAYBACK_SETTINGS_STORAGE_KEY = "roomusic:playback-settings:v1";

export type LegacyPlaybackSettings = { leveling: LevelingMode; crossfade: boolean; crossfadeSeconds: CrossfadeSeconds };

export const defaultPlaybackSettings: LegacyPlaybackSettings = { leveling: "auto", crossfade: false, crossfadeSeconds: 5 };

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

export function loadPlaybackSettings(storage: StorageLike | null): LegacyPlaybackSettings {
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
