import { describe, expect, it } from "vitest";
import { sampleBody } from "../../../test/api-samples";
import { jsonResponse, sampleResponse, stubFetch } from "../../../test/http";
import { fetchAlbumResults, fetchSearchSuggestions, fetchTrackResults } from "./endpoints";

describe("搜索 HTTP 边界", () => {
  it("建议编码字面查询、每栏limit，沿用取消信号与同源无缓存", async () => {
    const calls = stubFetch(() => sampleResponse("search/suggestions"));
    const signal = new AbortController().signal;
    const result = await fetchSearchSuggestions({ q: "爱 & %_~", limit: 8, signal });
    expect(result).toEqual({ status: 200, data: sampleBody("search/suggestions") });
    expect(calls).toHaveLength(1);
    const call = calls[0];
    const url = new URL(call?.path ?? "", "http://localhost");
    expect(url.pathname).toBe("/api/v1/search/suggestions");
    expect(Object.fromEntries(url.searchParams)).toEqual({ q: "爱 & %_~", limit: "8" });
    expect(call?.signal).toBe(signal);
    expect(call?.init).toMatchObject({ method: "GET", credentials: "same-origin", cache: "no-store" });
  });
  it.each([
    ["albums", fetchAlbumResults, "search/albums"],
    ["tracks", fetchTrackResults, "search/results-capped-page-21"],
  ] as const)("%s 独立请求第21页，不把total阈值当页码上限", async (type, fetchPage, sample) => {
    const calls = stubFetch(() => sampleResponse(sample));
    const signal = new AbortController().signal;
    expect(await fetchPage({ q: "爱", page: 21, pageSize: 50, signal })).toEqual({ status: 200, data: sampleBody(sample) });
    expect(calls).toHaveLength(1);
    expect(Object.fromEntries(new URL(calls[0]?.path ?? "", "http://localhost").searchParams)).toEqual({ type, q: "爱", page: "21", page_size: "50" });
    expect(calls[0]?.signal).toBe(signal);
  });
  it("非法响应变成 invalid_response，不能以原始JSON进入缓存", async () => {
    stubFetch(() => jsonResponse(200, { items: [], pagination: null }));
    await expect(fetchTrackResults({ q: "爱", page: 1, pageSize: 50 })).rejects.toMatchObject({ failure: { kind: "invalid_response" } });
  });
  it("服务503沿用真实安全错误信封", async () => {
    stubFetch(() => sampleResponse("search/errors/database-unavailable"));
    await expect(fetchAlbumResults({ q: "爱", page: 1, pageSize: 50 })).rejects.toMatchObject({ failure: { kind: "http", status: 503, code: "database_unavailable" } });
  });
});
