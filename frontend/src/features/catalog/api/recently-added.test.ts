import { describe, expect, it } from "vitest";
import { decode as d } from "../../../shared/api";
import { sampleBody } from "../../../test/api-samples";
import { jsonResponse, stubFetch, sampleResponse } from "../../../test/http";
import { withValue, without } from "../../../test/mutate";
import { parseAlbumSearch } from "../album-search";
import { catalogKeys } from "../query-keys";
import { decodeRecentlyAdded, decodeReleaseGroupAddedPage, decodeReleaseGroupPage } from "./decoders";
import { fetchRecentlyAdded, fetchReleaseGroupPage } from "./endpoints";

const recent = sampleBody("scan-feedback/recent-one");

describe("最近新增解码与查询合同", () => {
  it("固定最多八项；空列表可解码，九项拒绝", () => {
    expect(decodeRecentlyAdded(sampleBody("scan-feedback/recent-empty")).items).toEqual([]);
    const eight = decodeRecentlyAdded(sampleBody("scan-feedback/recent-eight"));
    expect(eight.items).toHaveLength(8);
    expect(() => decodeRecentlyAdded({ items: [...eight.items, eight.items[0]] })).toThrow(d.DecodeError);
  });

  it.each([null, "", "昨天", 0])("拒绝非法 added_at=%s", (value) => {
    expect(() => decodeRecentlyAdded(withValue(recent, ["items", 0, "added_at"], value))).toThrow(d.DecodeError);
  });

  it("最近新增和时间排序必须带 added_at，普通标题列表仍兼容旧服务", () => {
    expect(() => decodeRecentlyAdded(without(recent, ["items", 0, "added_at"]))).toThrow(d.DecodeError);
    const legacy = without(sampleBody("release-groups/list-one"), ["items", 0, "added_at"]);
    expect(() => decodeReleaseGroupPage(legacy)).not.toThrow();
    expect(() => decodeReleaseGroupAddedPage(legacy)).toThrow(d.DecodeError);
    expect(() => decodeRecentlyAdded({})).toThrow(d.DecodeError);
    expect(() => decodeRecentlyAdded(without(recent, ["items", 0, "representative"]))).toThrow(d.DecodeError);
  });

  it("最近新增端点不发分页参数，按时间列表同时发送搜索、筛选和页码", async () => {
    const requests = stubFetch((request) => request.path.includes("recently-added") ? sampleResponse("scan-feedback/recent-one") : sampleResponse("scan-feedback/sort-added"));
    await fetchRecentlyAdded();
    await fetchReleaseGroupPage({ page: 2, pageSize: 48, attentionRequired: true, search: "贝多芬", sort: "added_at_desc" });
    expect(requests[0]?.path).toBe("/api/v1/release-groups/recently-added");
    const query = new URL(requests[1]?.path ?? "", "http://localhost").searchParams;
    expect(Object.fromEntries(query)).toEqual({ page: "2", page_size: "48", attention: "required", q: "贝多芬", sort: "added_at_desc" });
  });

  it("实际端点拒绝缺少加入时间的响应，标题列表仍兼容旧形状", async () => {
    const legacyPage = without(sampleBody("release-groups/list-one"), ["items", 0, "added_at"]);
    const legacyRecent = without(recent, ["items", 0, "added_at"]);
    stubFetch((request) => jsonResponse(200, request.path.includes("recently-added") ? legacyRecent : legacyPage));
    const params = { page: 1, pageSize: 48, attentionRequired: false };
    await expect(fetchReleaseGroupPage(params)).resolves.toMatchObject({ status: 200, data: legacyPage });
    await expect(fetchReleaseGroupPage({ ...params, sort: "added_at_desc" })).rejects.toMatchObject({ failure: { kind: "invalid_response", status: 200 } });
    await expect(fetchRecentlyAdded()).rejects.toMatchObject({ failure: { kind: "invalid_response", status: 200 } });
  });

  it("默认标题排序不发送 sort，缓存与最近新增排序分开并带身份代次", async () => {
    const requests = stubFetch(() => sampleResponse("scan-feedback/sort-title"));
    await fetchReleaseGroupPage({ page: 1, pageSize: 48, attentionRequired: false });
    expect(requests[0]?.path).toBe("/api/v1/release-groups?page=1&page_size=48");
    expect(catalogKeys.albumPage(1, {})).toEqual(catalogKeys.albumPage(1, parseAlbumSearch({ sort: "title" })));
    expect(catalogKeys.albumPage(1, {})).not.toEqual(catalogKeys.albumPage(1, { sort: "added_at_desc" }));
    expect(catalogKeys.recentlyAdded(1).slice(0, catalogKeys.all(1).length)).toEqual(catalogKeys.all(1));
    expect(catalogKeys.recentlyAdded(1)).not.toEqual(catalogKeys.recentlyAdded(2));
  });
});
