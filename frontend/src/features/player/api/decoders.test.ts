import { describe, expect, it } from "vitest";
import { decode as d } from "../../../shared/api";
import { sampleBody } from "../../../test/api-samples";
import { withValue, without } from "../../../test/mutate";
import { decodePlaybackFormats, decodeTrackLyrics, decodeTrackPlayback, decodeTrackPreparing } from "./decoders";

const formats = sampleBody("playback/formats");
const playback = sampleBody("tracks/playback");
const derived = sampleBody("tracks/playback-derived");
const preparing = sampleBody("tracks/playback-preparing");

describe("播放 API decoder", () => {
  it("完整解码后端批准格式、直出与派生的预检样本、准备中样本，不丢字段", () => {
    expect(decodePlaybackFormats(formats)).toEqual(formats);
    expect(decodeTrackPlayback(playback)).toEqual(playback);
    expect(decodeTrackPlayback(derived)).toEqual(derived);
    expect(decodeTrackPreparing(preparing)).toEqual(preparing);
  });

  it("格式表同时有直出与派生项，派生项描述转换后的表示", () => {
    const items = decodePlaybackFormats(formats).items;
    expect(items.filter((item) => item.delivery === "original").length).toBeGreaterThan(0);
    expect(items.find((item) => item.delivery === "derived")).toMatchObject({ source_kind: "m4a", codec: "alac", mime_type: "audio/flac", codecs: null });
    expect(decodeTrackPlayback(derived).representation.kind).toBe("derived");
  });

  it.each([
    without(formats, ["items"]),
    without(formats, ["items", 0, "delivery"]),
    withValue(formats, ["items", 0, "delivery"], "transcoded"),
    withValue(formats, ["items", 0, "delivery"], null),
    withValue(formats, ["items", 0, "codec"], null),
    withValue(formats, ["items", 0, "mime_type"], "text/html"),
    withValue(formats, ["items", 0, "codecs"], 'flac"; x="y'),
    withValue(formats, ["items"], Array.from({ length: 33 }, () => decodePlaybackFormats(formats).items[0])),
    withValue(formats, ["items"], [decodePlaybackFormats(formats).items[0], decodePlaybackFormats(formats).items[0]]),
  ])("格式表拒绝无效字段、超长或重复组合 %#", (input) => {
    expect(() => decodePlaybackFormats(input)).toThrow(d.DecodeError);
  });

  it.each([
    without(playback, ["representation"]),
    withValue(playback, ["track_id"], "bad-id"),
    withValue(playback, ["representation", "kind"], "transcoded"),
    withValue(playback, ["representation", "url"], "https://evil.example/audio"),
    withValue(playback, ["representation", "url"], "/api/v1/tracks/00000000-0000-4000-8000-000000000001/audio?token=secret"),
    withValue(playback, ["representation", "url"], "/api/v1/tracks/00000000-0000-4000-8000-000000000002/audio"),
    withValue(playback, ["representation", "size_bytes"], -1),
    withValue(playback, ["representation", "size_bytes"], 1.5),
    withValue(playback, ["representation", "size_bytes"], Number.MAX_SAFE_INTEGER + 1),
    withValue(derived, ["representation", "kind"], "converted"),
    withValue(derived, ["representation", "url"], "/api/v1/tracks/00000000-0000-4000-8000-000000000001/audio?derived=1"),
  ])("预检拒绝不安全地址、身份错配与非法大小 %#", (input) => {
    expect(() => decodeTrackPlayback(input)).toThrow(d.DecodeError);
  });

  it.each([
    without(preparing, ["status"]),
    without(preparing, ["track_id"]),
    withValue(preparing, ["status"], "ready"),
    withValue(preparing, ["status"], "failed"),
    withValue(preparing, ["track_id"], "bad-id"),
    playback,
  ])("准备中响应只接受 preparing 状态与合法曲目标识 %#", (input) => {
    expect(() => decodeTrackPreparing(input)).toThrow(d.DecodeError);
  });
});

describe("歌词 API decoder", () => {
  const synced = sampleBody("tracks/lyrics-synced");
  const plain = sampleBody("tracks/lyrics-plain");
  const none = sampleBody("tracks/lyrics-none");

  it("完整解码同步、纯文本与没有歌词三种样本，不丢字段", () => {
    expect(decodeTrackLyrics(synced)).toEqual(synced);
    expect(decodeTrackLyrics(plain)).toEqual(plain);
    expect(decodeTrackLyrics(none)).toEqual(none);
  });

  it("同步歌词的每一行有开始时间，纯文本的每一行没有；没有歌词是 null", () => {
    const syncedLyrics = decodeTrackLyrics(synced).lyrics;
    expect(syncedLyrics?.kind).toBe("synced");
    expect(syncedLyrics?.lines.every((line) => typeof line.start_ms === "number")).toBe(true);
    // 样本里有间奏（空文本）与同一时间的双语两行。
    expect(syncedLyrics?.lines.some((line) => line.text === "")).toBe(true);
    const plainLyrics = decodeTrackLyrics(plain).lyrics;
    expect(plainLyrics?.kind).toBe("plain");
    expect(plainLyrics?.lines.every((line) => line.start_ms === null)).toBe(true);
    expect(decodeTrackLyrics(none).lyrics).toBeNull();
  });

  const line = (start_ms: number | null, text = "x") => ({ start_ms, text });
  const lyrics = (kind: string, lines: unknown[], source = "embedded") => ({ track_id: "00000000-0000-4000-8000-000000000001", lyrics: { kind, source, lines } });

  it.each([
    without(synced, ["lyrics"]),
    without(synced, ["track_id"]),
    withValue(synced, ["track_id"], "bad-id"),
    without(synced, ["lyrics", "kind"]),
    withValue(synced, ["lyrics", "kind"], "karaoke"),
    withValue(synced, ["lyrics", "source"], "file"),
    without(synced, ["lyrics", "source"]),
    without(synced, ["lyrics", "lines"]),
    withValue(synced, ["lyrics", "lines"], []),
    withValue(synced, ["lyrics", "lines", 0, "text"], null),
    withValue(synced, ["lyrics", "lines", 0, "text"], "x".repeat(2001)),
    withValue(synced, ["lyrics", "lines", 0], { text: "no start" }),
    withValue(synced, ["lyrics", "lines", 1, "start_ms"], -1),
    withValue(synced, ["lyrics", "lines", 1, "start_ms"], 1.5),
    withValue(synced, ["lyrics", "lines", 1, "start_ms"], "12000"),
    withValue(synced, ["lyrics", "lines", 1, "start_ms"], 24 * 60 * 60 * 1000 + 1),
    withValue(plain, ["lyrics", "lines", 0, "start_ms"], 0),
    withValue(plain, ["lyrics", "lines"], []),
    withValue(none, ["lyrics"], "no lyrics"),
  ])("拒绝缺字段、未知取值、越界与非法值 %#", (input) => {
    expect(() => decodeTrackLyrics(input)).toThrow(d.DecodeError);
  });

  it.each([
    ["同步歌词里有 null 开始时间", lyrics("synced", [line(1000), line(null)])],
    ["同步歌词的开始时间递减", lyrics("synced", [line(5000), line(4999)])],
    ["纯文本歌词里有数字开始时间", lyrics("plain", [line(null), line(0)])],
    ["超过 2000 行", lyrics("synced", Array.from({ length: 2001 }, (_, index) => line(index)))],
  ])("按形态检查开始时间并限制行数：%s", (_name, input) => {
    expect(() => decodeTrackLyrics(input)).toThrow(d.DecodeError);
  });

  it("相同的开始时间、间奏的空文本与恰好 2000 行都合法", () => {
    expect(() => decodeTrackLyrics(lyrics("synced", [line(0, ""), line(1000, "a"), line(1000, "b")]))).not.toThrow();
    expect(() => decodeTrackLyrics(lyrics("synced", Array.from({ length: 2000 }, (_, index) => line(index * 1000))))).not.toThrow();
    expect(() => decodeTrackLyrics(lyrics("plain", [line(null, "a"), line(null, ""), line(null, "b")]))).not.toThrow();
  });
});
