import { describe, expect, it } from "vitest";
import { decode as d } from "../../../shared/api";
import { sampleBody } from "../../../test/api-samples";
import { withValue, without } from "../../../test/mutate";
import { decodePlaybackFormats, decodeTrackPlayback, decodeTrackPreparing } from "./decoders";

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
