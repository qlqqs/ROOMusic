import { describe, expect, it, vi } from "vitest";
import type { PlaybackFormatDTO } from "../api/decoders";
import { buildCapabilityTable, typeString, unplayableText, type BrowserProbe } from "./capability";

const formats: PlaybackFormatDTO[] = [
  { source_kind: "flac_vorbis_comment", codec: "flac", mime_type: "audio/flac", codecs: null },
  { source_kind: "m4a", codec: "alac", mime_type: "audio/mp4", codecs: "alac" },
  { source_kind: "mp3_id3v2", codec: "mp3", mime_type: "audio/mpeg", codecs: null },
];

/** 模拟 Chromium：能解 FLAC 与 MP3，不能解 ALAC。 */
const chromium: BrowserProbe = {
  canPlayType: (type) => (type.includes("alac") ? "" : "probably"),
};

describe("typeString", () => {
  it("没有 codecs 参数时只有媒体类型，有时写成 codecs=\"…\"", () => {
    expect(typeString(formats[0] as PlaybackFormatDTO)).toBe("audio/flac");
    expect(typeString(formats[1] as PlaybackFormatDTO)).toBe('audio/mp4; codecs="alac"');
  });
});

describe("buildCapabilityTable", () => {
  it("格式表里且浏览器能解码的曲目可以播放", async () => {
    const table = await buildCapabilityTable(formats, chromium);
    expect(table.verdictOf({ sourceKind: "flac_vorbis_comment", codec: "flac" })).toEqual({ playable: true });
    expect(table.verdictOf({ sourceKind: "mp3_id3v2", codec: "mp3" })).toEqual({ playable: true });
  });

  it("canPlayType 返回空串时是“浏览器不能解码”，与 Safari 能解的 ALAC 区分开", async () => {
    const chromiumTable = await buildCapabilityTable(formats, chromium);
    expect(chromiumTable.verdictOf({ sourceKind: "m4a", codec: "alac" })).toEqual({ playable: false, reason: "browser_cannot_decode" });
    const safariTable = await buildCapabilityTable(formats, { canPlayType: () => "maybe" });
    expect(safariTable.verdictOf({ sourceKind: "m4a", codec: "alac" })).toEqual({ playable: true });
  });

  it("CUE 虚拟轨无论编码如何都不能播放；不在格式表里的组合是“格式不能直出”", async () => {
    const table = await buildCapabilityTable(formats, chromium);
    expect(table.verdictOf({ sourceKind: "cue_virtual", codec: "flac" })).toEqual({ playable: false, reason: "cue_virtual" });
    expect(table.verdictOf({ sourceKind: "wav_info", codec: "wav_extensible" })).toEqual({ playable: false, reason: "format_not_direct" });
    expect(table.verdictOf({ sourceKind: "flac_vorbis_comment", codec: null })).toEqual({ playable: false, reason: "format_not_direct" });
    expect(table.verdictOf({ sourceKind: "flac_vorbis_comment", codec: "alac" })).toEqual({ playable: false, reason: "format_not_direct" });
  });

  it("decodingInfo 报告 supported 为 false 时不能播放，即使 canPlayType 说可以", async () => {
    const decodingSupported = vi.fn(async (type: string) => !type.includes("flac"));
    const table = await buildCapabilityTable(formats, { canPlayType: () => "probably", decodingSupported });
    expect(table.verdictOf({ sourceKind: "flac_vorbis_comment", codec: "flac" })).toEqual({ playable: false, reason: "browser_cannot_decode" });
    expect(table.verdictOf({ sourceKind: "mp3_id3v2", codec: "mp3" })).toEqual({ playable: true });
    expect(decodingSupported).toHaveBeenCalledWith("audio/flac");
  });

  it("decodingInfo 拒绝或抛错时只看 canPlayType", async () => {
    const rejecting = await buildCapabilityTable(formats, { canPlayType: () => "probably", decodingSupported: () => Promise.reject(new Error("拒绝")) });
    expect(rejecting.verdictOf({ sourceKind: "flac_vorbis_comment", codec: "flac" })).toEqual({ playable: true });
    const throwing = await buildCapabilityTable(formats, {
      canPlayType: () => "probably",
      decodingSupported: () => {
        throw new Error("抛错");
      },
    });
    expect(throwing.verdictOf({ sourceKind: "mp3_id3v2", codec: "mp3" })).toEqual({ playable: true });
  });

  it("canPlayType 为空时不再询问 decodingInfo", async () => {
    const decodingSupported = vi.fn(async () => true);
    const table = await buildCapabilityTable(formats, { canPlayType: () => "", decodingSupported });
    expect(table.verdictOf({ sourceKind: "mp3_id3v2", codec: "mp3" })).toEqual({ playable: false, reason: "browser_cannot_decode" });
    expect(decodingSupported).not.toHaveBeenCalled();
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
