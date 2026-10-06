import { describe, expect, it } from "vitest";
import { decode as d } from "../../../shared/api";
import { sampleBody } from "../../../test/api-samples";
import { withValue, without } from "../../../test/mutate";
import { decodePlaybackFormats, decodeTrackPlayback } from "./decoders";

const formats = sampleBody("playback/formats");
const playback = sampleBody("tracks/playback");

describe("播放 API decoder", () => {
  it("完整解码后端批准格式与预检样本，不丢字段", () => {
    expect(decodePlaybackFormats(formats)).toEqual(formats);
    expect(decodeTrackPlayback(playback)).toEqual(playback);
  });

  it.each([
    without(formats, ["items"]),
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
  ])("预检拒绝不安全地址、身份错配与非法大小 %#", (input) => {
    expect(() => decodeTrackPlayback(input)).toThrow(d.DecodeError);
  });
});
