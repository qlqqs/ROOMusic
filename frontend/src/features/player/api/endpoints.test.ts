import { describe, expect, it } from "vitest";
import { installFakeApi } from "../../../test/fake-api";
import { jsonResponse, sampleResponse } from "../../../test/http";
import { sampleBody } from "../../../test/api-samples";
import { withValue } from "../../../test/mutate";
import { fetchPlaybackFormats, fetchTrackLyrics } from "./endpoints";

const trackId = "00000000-0000-4000-8000-000000000001";

describe("播放 API 请求", () => {
  it("按合同请求格式表，解码后返回", async () => {
    const api = installFakeApi();
    api.on("GET", "/api/v1/playback/formats", () => sampleResponse("playback/formats"));
    expect((await fetchPlaybackFormats()).data.items.length).toBeGreaterThan(0);
    expect(api.requests).toHaveLength(1);
  });
});

describe("歌词 API 请求", () => {
  it("按合同请求歌词，解码后返回三种形态", async () => {
    const api = installFakeApi();
    for (const sample of ["tracks/lyrics-synced", "tracks/lyrics-plain", "tracks/lyrics-none"]) {
      api.on("GET", `/api/v1/tracks/${trackId}/lyrics`, () => sampleResponse(sample));
      expect((await fetchTrackLyrics(trackId)).data).toEqual(sampleBody(sample));
    }
    expect(api.requests.map((request) => `${request.method} ${request.path}`)).toEqual(Array(3).fill(`GET /api/v1/tracks/${trackId}/lyrics`));
  });

  it("响应必须是所请求的曲目，不显示别的曲目的歌词", async () => {
    const api = installFakeApi();
    api.on("GET", `/api/v1/tracks/${trackId}/lyrics`, () => jsonResponse(200, withValue(sampleBody("tracks/lyrics-synced"), ["track_id"], "00000000-0000-4000-8000-000000000002")));
    await expect(fetchTrackLyrics(trackId)).rejects.toMatchObject({ failure: { kind: "invalid_response" } });
  });

  it("404 与 401 作为错误返回，不当成“没有歌词”", async () => {
    const api = installFakeApi();
    api.on("GET", `/api/v1/tracks/${trackId}/lyrics`, () => sampleResponse("errors/not-found-track"));
    await expect(fetchTrackLyrics(trackId)).rejects.toMatchObject({ failure: { kind: "http", status: 404, code: "not_found" } });
  });

  it("非法曲目标识不发请求", () => {
    const api = installFakeApi();
    expect(() => fetchTrackLyrics("../secret")).toThrow();
    expect(api.requests).toEqual([]);
  });
});
