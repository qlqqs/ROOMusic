import { describe, expect, it } from "vitest";
import { addedText, overLimitText, playlistMessages, playlistMetaText } from "./playlist-text";

describe("歌单时长的三种显示状态", () => {
  it.each([
    [12, 4_800, "12 首 · 1 小时 20 分钟"],
    [1, 3_600, "1 首 · 1 小时"],
    [3, 2_700, "3 首 · 45 分钟"],
    [1, 42, "1 首 · 42 秒"],
    [1, 42.6, "1 首 · 43 秒"],
    [0, 0, "0 首"],
  ])("全部时长已知：%i 首、%s 秒显示 %s", (track_count, duration_seconds, expected) => {
    expect(playlistMetaText({ track_count, duration_seconds, unknown_duration_count: 0 })).toBe(expected);
  });

  it.each([
    [4_800, "12 首 · 1 小时 20 分钟以上"],
    [4_859.9, "12 首 · 1 小时 20 分钟以上"],
    [3_599.9, "12 首 · 59 分钟以上"],
    [3_600, "12 首 · 1 小时以上"],
    [119.9, "12 首 · 1 分钟以上"],
    [59.9, "12 首 · 59 秒以上"],
    [0.9, "12 首 · 0 秒以上"],
  ])("部分时长未知：已知 %s 秒向下取整为 %s", (duration_seconds, expected) => {
    expect(playlistMetaText({ track_count: 12, duration_seconds, unknown_duration_count: 1 })).toBe(expected);
  });

  it.each([1, 12, 10_001])("全部时长未知：%i 首只显示曲目数", (track_count) => {
    const expected = track_count === 10_001 ? "10,001 首" : `${track_count} 首`;
    expect(playlistMetaText({ track_count, duration_seconds: 0, unknown_duration_count: track_count })).toBe(expected);
  });

  it("没有可展示的已知时长时不伪称总时长为零", () => {
    expect(playlistMetaText({ track_count: 2, duration_seconds: 0, unknown_duration_count: 1 })).toBe("2 首");
  });

  it("撤销后超过容量仍显示完整曲目数与时长", () => {
    expect(playlistMetaText({ track_count: 10_001, duration_seconds: 4_800, unknown_duration_count: 1 })).toBe("10,001 首 · 1 小时 20 分钟以上");
  });
});

describe("歌单加入与超限提示", () => {
  it("单曲加入写明目标歌单", () => {
    expect(addedText("晚间聆听", { added_count: 1, existing_count: 0 }, true)).toBe("已加入《晚间聆听》。");
  });

  it("批量加入分别报告新增和已经存在的数量", () => {
    expect(addedText("晚间聆听", { added_count: 12, existing_count: 3 }, false)).toBe("《晚间聆听》：已加入 12 首，3 首已在歌单里。");
  });

  it("批量全部新增时省略零首已在歌单里的提示", () => {
    expect(addedText("晚间聆听", { added_count: 2, existing_count: 0 }, false)).toBe("《晚间聆听》：已加入 2 首。");
  });

  it("数量使用千位分隔，不把已存在数量加进新增数量", () => {
    expect(addedText("大歌单", { added_count: 1_234, existing_count: 2_345 }, false)).toBe("《大歌单》：已加入 1,234 首，2,345 首已在歌单里。");
  });

  it("单曲已存在时保留简短提示", () => {
    expect(addedText("晚间聆听", { added_count: 0, existing_count: 1 }, true)).toBe("已在《晚间聆听》里了。");
  });

  it.each([
    [1, "已在《晚间聆听》里了。"],
    [3, "这些都已在《晚间聆听》里了。"],
  ] as const)("批量全部已存在时省略零首新增，按 %i 首选择简短提示", (existing_count, expected) => {
    expect(addedText("晚间聆听", { added_count: 0, existing_count }, false)).toBe(expected);
  });

  it("临时超限提示真实超出数量及恢复新增的办法", () => {
    expect(overLimitText(1)).toBe("歌单比上限多 1 首，先移除一些才能再添加。");
    expect(overLimitText(1_234)).toBe("歌单比上限多 1,234 首，先移除一些才能再添加。");
  });

  it("容量与版本冲突有具体可操作的提示", () => {
    expect(playlistMessages.playlist_limit_reached).toBe("歌单已经有 1,000 个了，先删掉一些再新建。");
    expect(playlistMessages.playlist_track_limit_reached).toBe("歌单最多 10,000 首，放不下这些了。");
    expect(playlistMessages.revision_conflict).toBe("歌单刚在别处改过，已重新读取，请再试一次。");
  });
});
