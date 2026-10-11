import { describe, expect, it } from "vitest";
import { installFakeApi } from "../../../test/fake-api";
import { jsonResponse, sampleResponse } from "../../../test/http";
import { sampleBody } from "../../../test/api-samples";
import { withValue } from "../../../test/mutate";
import { fetchPlaybackFormats, fetchTrackLyrics, fetchTrackPlayback } from "./endpoints";

const trackId = "00000000-0000-4000-8000-000000000001";

describe("播放 API 请求", () => {
  it("按合同请求格式表与预检，解码后返回", async () => {
    const api = installFakeApi();
    api.on("GET", "/api/v1/playback/formats", () => sampleResponse("playback/formats"));
    api.on("GET", `/api/v1/tracks/${trackId}/playback`, () => sampleResponse("tracks/playback"));
    expect((await fetchPlaybackFormats()).data).toEqual(sampleBody("playback/formats"));
    expect((await fetchTrackPlayback(trackId)).data).toEqual({ state: "ready", playback: sampleBody("tracks/playback") });
    expect(api.requests).toHaveLength(2);
  });

  it("200 的派生表示与 202 的“正在准备”分别解码为 ready 与 preparing，并带回状态码", async () => {
    const api = installFakeApi();
    api.on("GET", `/api/v1/tracks/${trackId}/playback`, () => sampleResponse("tracks/playback-derived"));
    const derived = await fetchTrackPlayback(trackId);
    expect(derived.status).toBe(200);
    expect(derived.data).toEqual({ state: "ready", playback: sampleBody("tracks/playback-derived") });
    api.on("GET", `/api/v1/tracks/${trackId}/playback`, () => sampleResponse("tracks/playback-preparing"));
    const waiting = await fetchTrackPlayback(trackId);
    expect(waiting.status).toBe(202);
    expect(waiting.data).toEqual({ state: "preparing" });
  });

  it.each([
    ["200 带“正在准备”正文", 200, "tracks/playback-preparing"],
    ["202 带表示正文", 202, "tracks/playback"],
    ["201 带表示正文", 201, "tracks/playback"],
    ["200 与 202 以外的成功状态", 203, "tracks/playback-derived"],
  ])("状态码与响应体不符按无效响应处理，不会被当作“再等一轮”：%s", async (_name, status, sample) => {
    const api = installFakeApi();
    api.on("GET", `/api/v1/tracks/${trackId}/playback`, () => jsonResponse(status, sampleBody(sample)));
    await expect(fetchTrackPlayback(trackId)).rejects.toMatchObject({ failure: { kind: "invalid_response", status } });
  });

  it("202 也必须是所请求的曲目", async () => {
    const api = installFakeApi();
    api.on("GET", `/api/v1/tracks/${trackId}/playback`, () => jsonResponse(202, { track_id: "00000000-0000-4000-8000-000000000002", status: "preparing" }));
    await expect(fetchTrackPlayback(trackId)).rejects.toMatchObject({ failure: { kind: "invalid_response", status: 202 } });
  });

  it("预检不能返回另一首曲目；保持 invalid_response 分类", async () => {
    const api = installFakeApi();
    const otherId = "00000000-0000-4000-8000-000000000002";
    const wrong = withValue(withValue(sampleBody("tracks/playback"), ["track_id"], otherId), ["representation", "url"], `/api/v1/tracks/${otherId}/audio`);
    api.on("GET", `/api/v1/tracks/${trackId}/playback`, () => jsonResponse(200, wrong));
    await expect(fetchTrackPlayback(trackId)).rejects.toMatchObject({ failure: { kind: "invalid_response" } });
  });

  it("非法曲目标识不发请求", () => {
    const api = installFakeApi();
    expect(() => fetchTrackPlayback("../secret")).toThrow();
    expect(api.requests).toEqual([]);
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
