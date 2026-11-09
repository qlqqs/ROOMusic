import { describe, expect, it } from "vitest";
import { browserCapabilities } from "./capabilities";

const hls = "application/vnd.apple.mpegurl";
const longStreams = [
  { codec: "flac", mime_type: "audio/flac", lossless: true, max_sample_rate: 192_000, max_bit_depth: 24 },
  { codec: "wav", mime_type: "audio/wav", lossless: true, max_sample_rate: 192_000, max_bit_depth: 24 },
  { codec: "opus", mime_type: 'audio/ogg; codecs="opus"', lossless: false, max_sample_rate: null, max_bit_depth: null },
  { codec: "aac", mime_type: "audio/aac", lossless: false, max_sample_rate: null, max_bit_depth: null },
];

function element(readonlyVolume: boolean, unsupported?: string) {
  let volume = 1;
  return {
    get volume() { return volume; },
    set volume(value: number) { if (!readonlyVolume) volume = value; },
    canPlayType: (type: string) => type === unsupported ? "" : "maybe",
  };
}

describe("HLS 能力声明", () => {
  it("两项 HLS 都可探测时只声明两项 HLS，与音量能否写入无关", () => {
    for (const readonlyVolume of [true, false]) {
      const capabilities = browserCapabilities(element(readonlyVolume));
      expect(capabilities.volume).toBe(readonlyVolume ? "fixed" : "number");
      expect(capabilities.stream_formats).toEqual([
        { codec: "alac", mime_type: hls, lossless: true, max_sample_rate: 192_000, max_bit_depth: 24 },
        { codec: "aac", mime_type: hls, lossless: false, max_sample_rate: 48_000, max_bit_depth: null },
      ]);
    }
  });

  it("HLS 探测不全时按原长流表及探测结果声明", () => {
    for (const [readonlyVolume, unsupported] of [
      [true, hls],
      [false, 'audio/mp4; codecs="alac"'],
      [true, 'audio/mp4; codecs="mp4a.40.2"'],
    ] as const) {
      const capabilities = browserCapabilities(element(readonlyVolume, unsupported));
      expect(capabilities.volume).toBe(readonlyVolume ? "fixed" : "number");
      expect(capabilities.stream_formats).toEqual(longStreams.filter((candidate) => candidate.mime_type !== unsupported));
    }
  });
});
