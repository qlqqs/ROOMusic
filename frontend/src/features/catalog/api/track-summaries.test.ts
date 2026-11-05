import { describe, expect, it } from "vitest";
import { decodeErrorEnvelope } from "../../../shared/api";
import { loadApiSample, sampleBody } from "../../../test/api-samples";
import { withValue, without } from "../../../test/mutate";
import { decodeTrackSummaries } from "./track-summary";

const base = () => {
  const item = decodeTrackSummaries(sampleBody("artists/adjudication/tracks-summaries")).items[0];
  if (item === undefined) throw new Error("批量摘要真实样本应有曲目");
  return item;
};

describe("I4 批量曲目摘要解码（正式真实 handler 样本）", () => {
  it.each(["tracks-summaries", "tracks-summaries-after-merge", "tracks-summaries-preparing", "tracks-summaries-unknown"])("%s 完整往返", (name) => {
    const sample = loadApiSample(`artists/adjudication/${name}`);
    expect(sample.status).toBe(200);
    expect(decodeTrackSummaries(sample.body)).toEqual(sample.body);
  });
  it("真实摘要保留输入顺序和 missing（含全部缺失的发行），不按 present 丢项", () => {
    const body = decodeTrackSummaries(sampleBody("artists/adjudication/tracks-summaries"));
    expect(body.items.map((item) => item.title)).toEqual(["曲目1-3", "曲目2-1", "曲目1-1", "曲目5-1"]);
    expect(body.items.map((item) => item.present)).toEqual([false, true, true, false]);
    expect(body.items[3]?.release.track_count).toBe(0);
  });
  it("请求空数组是 400 invalid_input；未知 ID 的成功响应允许空 items", () => {
    const invalidRequest = loadApiSample("artists/adjudication/tracks-summaries-empty");
    expect(invalidRequest.status).toBe(400);
    const error = decodeErrorEnvelope(invalidRequest.body);
    expect(error).toEqual(invalidRequest.body);
    expect(error.error.code).toBe("invalid_input");
    expect(() => decodeTrackSummaries(invalidRequest.body)).toThrow();
    const unknown = loadApiSample("artists/adjudication/tracks-summaries-unknown");
    expect(unknown.status).toBe(200);
    expect(decodeTrackSummaries(unknown.body)).toEqual({ items: [] });
  });
  it("整理中仍返回摘要，曲目与发行的艺人引用都明确为空数组", () => {
    const { items } = decodeTrackSummaries(sampleBody("artists/adjudication/tracks-summaries-preparing"));
    expect(items).toHaveLength(1);
    for (const item of items) {
      expect(item.artist_refs).toEqual([]);
      expect(item.release.artist_refs).toEqual([]);
      expect(item.release.album_artist_refs).toEqual([]);
    }
  });
  // 以下以真实响应为基底构造边界/畸形反例，不冒充后端生成的样本。
  it("最多 1000 项，1001 项拒绝", () => {
    const items = Array.from({ length: 1001 }, (_, n) => ({ ...base(), track_id: `a0000000-0000-4000-8000-${n.toString(16).padStart(12, "0")}` }));
    expect(decodeTrackSummaries({ items: items.slice(0, 1000) }).items).toHaveLength(1000);
    expect(() => decodeTrackSummaries({ items })).toThrow();
  });
  it("同一 ID 大小写不同仍视为重复，不把后一个悄悄覆盖前一个", () => {
    const item = { ...base(), track_id: "a0000000-0000-4000-8000-0000000000ab" };
    expect(() => decodeTrackSummaries({ items: [item, { ...item, track_id: item.track_id.toUpperCase() }] })).toThrow();
  });
  it.each(["artist_refs", "present", "release", "source"])("缺少 %s 不容忍为旧的半摘要", (key) => {
    expect(() => decodeTrackSummaries({ items: [without(base(), [key])] })).toThrow();
  });
  it("非法艺人 ID 和 null 引用数组都拒绝", () => {
    expect(() => decodeTrackSummaries({ items: [withValue(base(), ["artist_refs"], null)] })).toThrow();
    expect(() => decodeTrackSummaries({ items: [withValue(base(), ["artist_refs"], [{ artist_id: "bad", name: "甲", display_name: "甲" }])] })).toThrow();
  });
});
