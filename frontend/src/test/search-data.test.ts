import { describe, expect, it } from "vitest";
import { fetchCatalogStatus } from "../features/catalog/api/endpoints";
import { fetchAlbumResults, fetchTrackResults } from "../features/search/api/endpoints";
import { sampleBody } from "./api-samples";
import { installFakeApi, installFakeSearch, installFakeSession } from "./fake-api";
import { sampleResponse } from "./http";
import { respondToSearch, searchDataOf, searchRequestKey, searchTrackPage } from "./search-data";

describe("显式搜索替身", () => {
  it("未登记的查询返回404，不把不存在的结果伪装成成功空页", () => {
    const data = searchDataOf();
    expect(respondToSearch(data, "/api/v1/search", new URLSearchParams("q=爱&type=tracks")).status).toBe(404);
    expect(respondToSearch(data, "/api/v1/search/suggestions", new URLSearchParams("q=爱")).status).toBe(404);
  });
  it("参数顺序与默认值等价，词、类别、页码、页长和建议limit互相隔离", () => {
    const data = searchDataOf();
    const path = "/api/v1/search";
    const query = new URLSearchParams("q=爱&type=tracks");
    const body = searchTrackPage();
    data.responses.set(searchRequestKey(path, query), { status: 200, body });
    expect(respondToSearch(data, path, new URLSearchParams("page_size=50&type=tracks&q=爱&page=1"))).toEqual({ status: 200, body });
    for (const [key, value] of [["q", "別词"], ["type", "albums"], ["page", "2"], ["page_size", "1"], ["unexpected", "1"]]) {
      const changed = new URLSearchParams(query);
      changed.set(key ?? "", value ?? "");
      expect(respondToSearch(data, path, changed).status).toBe(404);
    }
    const suggest = "/api/v1/search/suggestions";
    data.responses.set(searchRequestKey(suggest, new URLSearchParams("q=爱")), { status: 200, body: sampleBody("search/suggestions") });
    expect(respondToSearch(data, suggest, new URLSearchParams("q=爱&limit=5")).status).toBe(200);
    expect(respondToSearch(data, suggest, new URLSearchParams("q=爱&limit=8")).status).toBe(404);
  });
  it("每次回应返回副本，不让消费方修改登记的数据", () => {
    const data = searchDataOf();
    const params = new URLSearchParams("q=爱&type=tracks");
    const response = { status: 200, body: searchTrackPage() };
    data.responses.set(searchRequestKey("/api/v1/search", params), response);
    expect(respondToSearch(data, "/api/v1/search", params)).not.toBe(response);
    expect(respondToSearch(data, "/api/v1/search", params).body).not.toBe(response.body);
  });
  it("Vitest 默认status ready且受会话保护，搜索需显式登记，status可覆盖", async () => {
    const api = installFakeApi();
    const session = installFakeSession(api);
    await expect(fetchCatalogStatus()).rejects.toMatchObject({ failure: { status: 401 } });
    session.current = "listener";
    expect((await fetchCatalogStatus()).data).toEqual(sampleBody("search/catalog-status-ready"));
    await expect(fetchTrackResults({ q: "爱", page: 1, pageSize: 50 })).rejects.toMatchObject({ failure: { status: 404 } });
    const data = installFakeSearch(api);
    data.responses.set(searchRequestKey("/api/v1/search", new URLSearchParams("q=爱&type=tracks")), { status: 200, body: searchTrackPage() });
    expect((await fetchTrackResults({ q: "爱", page: 1, pageSize: 50 })).status).toBe(200);
    await expect(fetchAlbumResults({ q: "爱", page: 1, pageSize: 50 })).rejects.toMatchObject({ failure: { status: 404 } });
    api.on("GET", "/api/v1/catalog/status", () => sampleResponse("search/catalog-status-legacy"));
    expect((await fetchCatalogStatus()).data).toEqual(sampleBody("search/catalog-status-legacy"));
  });
});
