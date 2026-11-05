import { describe, expect, it } from "vitest";
import { decode as d } from "../../../shared/api";
import { listApiSampleNames, sampleBody } from "../../../test/api-samples";
import { withValue, without } from "../../../test/mutate";
import { decodeAlbumResults, decodeSearchPagination, decodeSearchSuggestions, decodeTrackResults } from "./decoders";

describe("搜索响应 decoder", () => {
  const albums = sampleBody("search/albums");
  const tracks = sampleBody("search/tracks");
  const suggestions = sampleBody("search/suggestions");
  it.each(listApiSampleNames().filter((name) => /^search\/(albums|tracks|results|suggestions)/.test(name)))("真实样本 %s 完整往返", (name) => {
    const body = sampleBody(name);
    const decoder = name.includes("suggestions") ? decodeSearchSuggestions : typeof body === "object" && body !== null && "type" in body && body.type === "albums" ? decodeAlbumResults : decodeTrackResults;
    expect(decoder(body)).toEqual(body);
  });
  it.each([
    ["query", undefined], ["type", "artists"], ["catalog_state", null], ["items", null], ["pagination", null],
  ])("拒绝非法顶层字段 %s", (field, value) => {
    expect(() => decodeTrackResults(withValue(tracks, [String(field)], value))).toThrow(d.DecodeError);
  });
  it.each(["kind", "field", "role", "original", "artist_id", "alias", "release_id", "track_id"])("Match 必传字段 %s 不可省略", (field) => {
    expect(() => decodeTrackResults(without(tracks, ["items", 0, "match", field]))).toThrow(d.DecodeError);
  });
  it.each([
    ["kind", "fuzzy"], ["field", "path"], ["role", "singer"], ["original", null],
    ["artist_id", "bad-id"], ["alias", 1], ["release_id", "bad-id"], ["track_id", "bad-id"],
  ])("拒绝非法 Match.%s", (field, value) => {
    expect(() => decodeTrackResults(withValue(tracks, ["items", 0, "match", String(field)], value))).toThrow(d.DecodeError);
  });
  it("拒绝错误的类别、身份及安全音频事实", () => {
    expect(() => decodeAlbumResults(tracks)).toThrow(d.DecodeError);
    expect(() => decodeTrackResults(albums)).toThrow(d.DecodeError);
    expect(() => decodeAlbumResults(withValue(albums, ["items", 0, "matched_release_id"], "bad-id"))).toThrow(d.DecodeError);
    for (const field of ["codec", "bit_depth", "sample_rate", "channels", "bitrate", "source_kind"]) {
      expect(() => decodeTrackResults(without(tracks, ["items", 0, "source", field]))).toThrow(d.DecodeError);
    }
  });
  it("结果上限100、建议每栏上限8，超限整体拒绝而不截断", () => {
    const track = decodeTrackResults(tracks).items[0];
    const album = decodeAlbumResults(albums).items[0];
    expect(decodeTrackResults(withValue(tracks, ["items"], Array(100).fill(track))).items).toHaveLength(100);
    expect(() => decodeTrackResults(withValue(tracks, ["items"], Array(101).fill(track)))).toThrow(d.DecodeError);
    expect(() => decodeAlbumResults(withValue(albums, ["items"], Array(101).fill(album)))).toThrow(d.DecodeError);
    for (const [kind, item] of [["tracks", track], ["albums", album]] as const) {
      expect(decodeSearchSuggestions(withValue(suggestions, [kind, "items"], Array(8).fill(item)))[kind].items).toHaveLength(8);
      expect(() => decodeSearchSuggestions(withValue(suggestions, [kind, "items"], Array(9).fill(item)))).toThrow(d.DecodeError);
    }
  });
  it.each(["artists", "albums", "tracks"])("建议 %s 的 items/has_more 必传", (kind) => {
    for (const field of ["items", "has_more"]) expect(() => decodeSearchSuggestions(without(suggestions, [kind, field]))).toThrow(d.DecodeError);
  });
  it("拒绝缺少摘要和匹配字段的艺人建议", () => {
    expect(() => decodeSearchSuggestions(withValue(suggestions, ["artists", "items"], [{}]))).toThrow(d.DecodeError);
  });
  it.each([
    ["query", "爱".repeat(67)], ["original", "爱".repeat(1366)],
  ])("按 UTF-8 字节预算拒绝超长 %s", (field, value) => {
    const path = field === "query" ? ["query"] : ["items", 0, "match", "original"];
    expect(() => decodeTrackResults(withValue(tracks, path, value))).toThrow(d.DecodeError);
  });
  it.each([
    ["不可用艺人却被列为可搜索", ["artists", "albums", "tracks"]],
    ["重复类别", ["albums", "albums"]],
  ])("拒绝建议能力非法组合：%s", (_label, available) => {
    expect(() => decodeSearchSuggestions(withValue(withValue(suggestions, ["catalog_state", "artists_state"], "unavailable"), ["available_types"], available))).toThrow(d.DecodeError);
  });
});

describe("搜索专用分页", () => {
  const base = { page: 1, page_size: 50, total: 0, total_relation: "eq", has_more: false };
  it.each([
    base,
    { ...base, total: 999, has_more: true },
    { ...base, page: 20, total: 1000 },
    { ...base, total: 1000, total_relation: "gte", has_more: true },
    { ...base, page: 21, total: 1000, total_relation: "gte" },
    { ...base, page: 99, total: 1000, total_relation: "gte" },
  ])("接受精确数和阈值后页面 %j", (page) => expect(decodeSearchPagination(page)).toEqual(page));
  it.each([
    ["page", 0], ["page", 1.5], ["page_size", 0], ["page_size", 101], ["total", -1], ["total", 1001],
    ["total_relation", "approximate"], ["has_more", null], ["has_more", "true"],
  ])("拒绝非法 %s=%s", (field, value) => {
    expect(() => decodeSearchPagination({ ...base, [String(field)]: value })).toThrow(d.DecodeError);
  });
  it.each(Object.keys(base))("分页必传字段 %s", (field) => expect(() => decodeSearchPagination(without(base, [field]))).toThrow(d.DecodeError));
  it.each([0, 999])("gte 下界不能是 %i", (total) => {
    expect(() => decodeSearchPagination({ ...base, total_relation: "gte", total })).toThrow(d.DecodeError);
  });
  it.each([
    { ...base, has_more: true },
    { ...base, total: 100, has_more: false },
    { ...base, page: 20, total: 1000, has_more: true },
    { ...base, total: 1000, total_relation: "gte", has_more: false },
  ])("拒绝计数与 has_more 矛盾 %j", (page) => {
    expect(() => decodeSearchPagination(page)).toThrow(d.DecodeError);
  });
});
