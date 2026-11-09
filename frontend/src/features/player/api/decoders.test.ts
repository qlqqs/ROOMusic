import { describe, expect, it } from "vitest";
import { decode as d } from "../../../shared/api";
import { sampleBody } from "../../../test/api-samples";
import { withValue, without } from "../../../test/mutate";
import { decodePlaybackFormats, decodeTrackAnalysis, decodeTrackLyrics } from "./decoders";

const formats = sampleBody("playback/formats");

describe("播放 API decoder", () => {
  it("格式表只取来源类别与编码，新增取值不受前端枚举限制", () => {
    const items = decodePlaybackFormats(formats).items;
    expect(items.length).toBeGreaterThan(0);
    expect(Object.keys(items[0] ?? {}).sort()).toEqual(["codec", "source_kind"]);
    const input = withValue(withValue(formats, ["items", 0, "source_kind"], "future_source"), ["items", 0, "codec"], "future_codec");
    expect(decodePlaybackFormats(input).items[0]).toEqual({ source_kind: "future_source", codec: "future_codec" });
  });

  it.each([
    without(formats, ["items"]),
    withValue(formats, ["items", 0, "codec"], null),
    withValue(formats, ["items"], Array.from({ length: 65 }, () => decodePlaybackFormats(formats).items[0])),
    withValue(formats, ["items"], [decodePlaybackFormats(formats).items[0], decodePlaybackFormats(formats).items[0]]),
  ])("格式表拒绝无效字段、超长或重复组合 %#", (input) => {
    expect(() => decodePlaybackFormats(input)).toThrow(d.DecodeError);
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


describe("曲目分析 API decoder", () => {
  it.each(["done", "pending", "failed"] as const)("完整解码 %s 样本", (status) => {
    const sample = sampleBody(`tracks/analysis-${status}`);
    const decoded = decodeTrackAnalysis(sample);
    expect(decoded.status).toBe(status);
    if (status === "done") {
      expect(decoded.analysis?.peaks.length).toBeGreaterThan(0);
      expect(decoded.analysis?.peaks.length).toBe((sample as { analysis: { waveform: { points: number } } }).analysis.waveform.points);
    } else {
      expect(decoded.analysis).toBeNull();
      expect(decoded.album).toBeNull();
    }
  });
});


describe("曲目分析响度范围与采样峰值 decoder", () => {
  const done = sampleBody("tracks/analysis-done");

  it.each([
    [6.3, -1.2],
    [0, 0],
    [null, null],
  ] as const)("保留响度范围 %s 与采样峰值 %s", (loudnessRangeLu, samplePeakDbfs) => {
    const input = withValue(withValue(done, ["analysis", "loudness_range_lu"], loudnessRangeLu), ["analysis", "sample_peak_dbfs"], samplePeakDbfs);
    expect(decodeTrackAnalysis(input).analysis).toMatchObject({ loudnessRangeLu, samplePeakDbfs });
  });

  it.each(["loudness_range_lu", "sample_peak_dbfs"] as const)("%s 必须存在，且不接受字符串或布尔值", (field) => {
    expect(() => decodeTrackAnalysis(without(done, ["analysis", field]))).toThrow(d.DecodeError);
    for (const value of ["6.3", false]) {
      expect(() => decodeTrackAnalysis(withValue(done, ["analysis", field], value))).toThrow(d.DecodeError);
    }
  });
});
