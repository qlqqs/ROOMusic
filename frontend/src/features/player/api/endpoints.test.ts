import { describe, expect, it } from "vitest";
import { installFakeApi } from "../../../test/fake-api";
import { jsonResponse, sampleResponse } from "../../../test/http";
import { sampleBody } from "../../../test/api-samples";
import { withValue } from "../../../test/mutate";
import { fetchPlaybackFormats, fetchTrackPlayback } from "./endpoints";

const trackId = "00000000-0000-4000-8000-000000000001";

describe("播放 API 请求", () => {
  it("按合同请求格式表与预检，解码后返回", async () => {
    const api = installFakeApi();
    api.on("GET", "/api/v1/playback/formats", () => sampleResponse("playback/formats"));
    api.on("GET", `/api/v1/tracks/${trackId}/playback`, () => sampleResponse("tracks/playback"));
    expect((await fetchPlaybackFormats()).data).toEqual(sampleBody("playback/formats"));
    expect((await fetchTrackPlayback(trackId)).data).toEqual(sampleBody("tracks/playback"));
    expect(api.requests).toHaveLength(2);
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
