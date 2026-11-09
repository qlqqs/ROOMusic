import { describe, expect, it } from "vitest";
import type { PlaybackFormatDTO } from "../api/decoders";
import { buildCapabilityTable, typeString, unplayableText, type BrowserProbe } from "./capability";

const formats: PlaybackFormatDTO[] = [
  { source_kind: "flac_vorbis_comment", codec: "flac", delivery: "original", mime_type: "audio/flac", codecs: null },
  // ALAC 由服务端无损转换成 FLAC：媒体类型描述浏览器拿到的表示。
  { source_kind: "m4a", codec: "alac", delivery: "derived", mime_type: "audio/flac", codecs: null },
  { source_kind: "m4a", codec: "aac", delivery: "original", mime_type: "audio/mp4", codecs: "mp4a.40.2" },
  { source_kind: "mp3_id3v2", codec: "mp3", delivery: "original", mime_type: "audio/mpeg", codecs: null },
];

/** 模拟 Chromium：能解 FLAC、AAC 与 MP3。 */
const chromium: BrowserProbe = { canPlayType: () => "probably" };

/** 模拟不能解 FLAC 的浏览器（直出的 MP3 与 AAC 照常）。 */
const withoutFlac: BrowserProbe = { canPlayType: (type) => (type.includes("flac") ? "" : "probably") };

describe("typeString", () => {
  it("没有 codecs 参数时只有媒体类型，有时写成 codecs=\"…\"；派生项是转换后的类型", () => {
    expect(typeString(formats[0] as PlaybackFormatDTO)).toBe("audio/flac");
    expect(typeString(formats[1] as PlaybackFormatDTO)).toBe("audio/flac");
    expect(typeString(formats[2] as PlaybackFormatDTO)).toBe('audio/mp4; codecs="mp4a.40.2"');
  });
});

describe("buildCapabilityTable", () => {
  it("格式表里且浏览器能解码的曲目可以播放", async () => {
    const table = await buildCapabilityTable(formats, chromium);
    expect(table.verdictOf({ sourceKind: "flac_vorbis_comment", codec: "flac" })).toEqual({ playable: true });
    expect(table.verdictOf({ sourceKind: "mp3_id3v2", codec: "mp3" })).toEqual({ playable: true });
  });

  it("ALAC 经转换后以 FLAC 交付：浏览器能解 FLAC 就可以播放，与 ALAC 本身能否被浏览器解码无关", async () => {
    const asked: string[] = [];
    const table = await buildCapabilityTable(formats, { canPlayType: (type) => (asked.push(type), "probably") });
    expect(table.verdictOf({ sourceKind: "m4a", codec: "alac" })).toEqual({ playable: true });
    expect(asked).not.toContain("audio/mp4; codecs=\"alac\"");
    expect(asked.filter((type) => type === "audio/flac")).toHaveLength(1);
  });

  it("canPlayType 返回空串时是“浏览器不能解码”：直出的 FLAC 与转换成 FLAC 的 ALAC 一起不能播放，其他格式不受影响", async () => {
    const table = await buildCapabilityTable(formats, withoutFlac);
    expect(table.verdictOf({ sourceKind: "flac_vorbis_comment", codec: "flac" })).toEqual({ playable: false, reason: "browser_cannot_decode" });
    expect(table.verdictOf({ sourceKind: "m4a", codec: "alac" })).toEqual({ playable: false, reason: "browser_cannot_decode" });
    expect(table.verdictOf({ sourceKind: "m4a", codec: "aac" })).toEqual({ playable: true });
  });

  it("没有派生项（后端关闭了派生）时 ALAC 不在格式表里：格式不能直出", async () => {
    const table = await buildCapabilityTable(formats.filter((format) => format.delivery === "original"), chromium);
    expect(table.verdictOf({ sourceKind: "m4a", codec: "alac" })).toEqual({ playable: false, reason: "format_not_direct" });
    expect(table.deliveryOf({ sourceKind: "m4a", codec: "alac" })).toBeNull();
  });

  it("deliveryOf：可播放曲目是直出还是转换；不能播放的是 null", async () => {
    const table = await buildCapabilityTable(formats, withoutFlac);
    expect(table.deliveryOf({ sourceKind: "m4a", codec: "aac" })).toBe("original");
    expect(table.deliveryOf({ sourceKind: "mp3_id3v2", codec: "mp3" })).toBe("original");
    // 浏览器不能解 FLAC：转换也没有意义。
    expect(table.deliveryOf({ sourceKind: "m4a", codec: "alac" })).toBeNull();
    const chromiumTable = await buildCapabilityTable(formats, chromium);
    expect(chromiumTable.deliveryOf({ sourceKind: "m4a", codec: "alac" })).toBe("derived");
    expect(chromiumTable.deliveryOf({ sourceKind: "flac_vorbis_comment", codec: "flac" })).toBe("original");
    expect(chromiumTable.deliveryOf({ sourceKind: "cue_virtual", codec: "flac" })).toBeNull();
    expect(chromiumTable.deliveryOf({ sourceKind: "wav_info", codec: "wav_extensible" })).toBeNull();
    expect(chromiumTable.deliveryOf({ sourceKind: "m4a", codec: null })).toBeNull();
  });

  it("CUE 虚拟轨无论编码如何都不能播放；不在格式表里的组合是“格式不能直出”", async () => {
    const table = await buildCapabilityTable(formats, chromium);
    expect(table.verdictOf({ sourceKind: "cue_virtual", codec: "flac" })).toEqual({ playable: false, reason: "cue_virtual" });
    expect(table.verdictOf({ sourceKind: "wav_info", codec: "wav_extensible" })).toEqual({ playable: false, reason: "format_not_direct" });
    expect(table.verdictOf({ sourceKind: "flac_vorbis_comment", codec: null })).toEqual({ playable: false, reason: "format_not_direct" });
    expect(table.verdictOf({ sourceKind: "flac_vorbis_comment", codec: "alac" })).toEqual({ playable: false, reason: "format_not_direct" });
  });

  it("格式表为空时没有任何曲目可以播放（包括服务端暂时拿不到的情形由调用方处理）", async () => {
    const table = await buildCapabilityTable([], chromium);
    expect(table.verdictOf({ sourceKind: "flac_vorbis_comment", codec: "flac" })).toEqual({ playable: false, reason: "format_not_direct" });
  });
});

describe("unplayableText", () => {
  it("每种原因有明确的文案，编码名取自曲目", () => {
    expect(unplayableText({ playable: false, reason: "cue_virtual" }, "flac")).toBe("CUE 分轨曲目需要切分，目前不能播放");
    expect(unplayableText({ playable: false, reason: "format_not_direct" }, "wav_extensible")).toBe("这种格式目前不能直接播放");
    expect(unplayableText({ playable: false, reason: "browser_cannot_decode" }, "alac")).toBe("当前浏览器不能解码 ALAC");
    expect(unplayableText({ playable: false, reason: "browser_cannot_decode" }, null)).toBe("当前浏览器不能解码这种编码");
  });
});
