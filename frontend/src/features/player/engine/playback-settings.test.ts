import { describe, expect, it } from "vitest";
import { memoryStorage } from "../../../test/memory-storage";
import { defaultPlaybackSettings, loadPlaybackSettings, PLAYBACK_SETTINGS_STORAGE_KEY, type LegacyPlaybackSettings } from "./playback-settings";

describe("旧版本的播放设置", () => {
  it("默认均衡自动、交叉渐变关闭且时长为 5 秒", () => {
    expect(loadPlaybackSettings(memoryStorage())).toEqual({ leveling: "auto", crossfade: false, crossfadeSeconds: 5 });
    expect(loadPlaybackSettings(null)).toEqual(defaultPlaybackSettings);
  });

  it.each(["off", "auto", "track", "album"] as const)("读回旧版本的 %s 均衡模式", (leveling) => {
    const settings: LegacyPlaybackSettings = { leveling, crossfade: true, crossfadeSeconds: 8 };
    const storage = memoryStorage({ [PLAYBACK_SETTINGS_STORAGE_KEY]: JSON.stringify({ version: 1, ...settings }) });
    expect(loadPlaybackSettings(storage)).toEqual(settings);
  });

  it("损坏或不支持的值回默认", () => {
    const storage = memoryStorage();
    storage.data.set(PLAYBACK_SETTINGS_STORAGE_KEY, "{");
    expect(loadPlaybackSettings(storage)).toEqual(defaultPlaybackSettings);
    storage.data.set(PLAYBACK_SETTINGS_STORAGE_KEY, JSON.stringify({ version: 1, leveling: "loud", crossfade: true, crossfadeSeconds: 5 }));
    expect(loadPlaybackSettings(storage)).toEqual(defaultPlaybackSettings);
  });
});
