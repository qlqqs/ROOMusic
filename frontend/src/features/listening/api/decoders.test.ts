import { describe, expect, it } from "vitest";
import { decode as d } from "../../../shared/api";
import { sampleBody } from "../../../test/api-samples";
import { withValue, without } from "../../../test/mutate";
import { decodePlaysCleared, decodeRecentReleases, decodeRecordedPlay } from "./decoders";

describe("decodeRecordedPlay", () => {
  const recorded = sampleBody("listening/play-recorded");

  it("解码记录响应，保留全部字段", () => {
    expect(decodeRecordedPlay(recorded)).toEqual(recorded);
  });

  it.each([
    ["缺少 id", without(recorded, ["id"])],
    ["缺少 track_id", without(recorded, ["track_id"])],
    ["缺少 played_at", without(recorded, ["played_at"])],
    ["id 不是 UUID", withValue(recorded, ["id"], "play-1")],
    ["track_id 不是 UUID", withValue(recorded, ["track_id"], 7)],
    ["played_at 不是 RFC 3339 时间", withValue(recorded, ["played_at"], "yesterday")],
    ["played_at 不是字符串", withValue(recorded, ["played_at"], 1_700_000_000)],
  ])("拒绝：%s", (_name, input) => {
    expect(() => decodeRecordedPlay(input)).toThrow(d.DecodeError);
  });
});

describe("decodeRecentReleases", () => {
  const recent = sampleBody("listening/recent-releases");
  const empty = sampleBody("listening/recent-releases-empty");

  it("解码空历史与有数据的历史，保留全部字段", () => {
    expect(decodeRecentReleases(empty)).toEqual({ items: [], has_history: false });
    expect(decodeRecentReleases(recent)).toEqual(recent);
  });

  it("每一项带发行摘要（含封面）、最后播放的时间与曲目", () => {
    const [item] = decodeRecentReleases(recent).items;
    expect(item?.release).toMatchObject({
      title: "Sample Symphonies",
      album_artist: "Sample Orchestra",
      track_count: 3,
      artwork: { mime: "image/webp", width: 640, height: 480 },
    });
    expect(item?.last_track.title).toBe("Allegro");
    expect(item?.group_id).toMatch(/^[0-9a-f-]{36}$/);
  });

  it("没有封面的发行摘要同样可以解码", () => {
    const withoutCover = withValue(recent, ["items", 0, "release", "artwork"], null);
    expect(decodeRecentReleases(withoutCover).items[0]?.release.artwork).toBeNull();
  });

  it("有记录但当前都不显示：items 为空而 has_history 为真", () => {
    const hidden = { items: [], has_history: true };
    expect(decodeRecentReleases(hidden)).toEqual(hidden);
  });

  it("最多 8 项", () => {
    const first = (recent as { items: unknown[] }).items[0];
    const many = (count: number) => withValue(recent, ["items"], Array.from({ length: count }, () => structuredClone(first)));
    expect(decodeRecentReleases(many(8)).items).toHaveLength(8);
    expect(() => decodeRecentReleases(many(9))).toThrow(d.DecodeError);
  });

  it.each([
    ["缺少 items", without(recent, ["items"])],
    ["缺少 has_history", without(recent, ["has_history"])],
    ["has_history 是字符串", withValue(recent, ["has_history"], "true")],
    ["items 是 null", withValue(recent, ["items"], null)],
    ["缺少 group_id", without(recent, ["items", 0, "group_id"])],
    ["group_id 不是 UUID", withValue(recent, ["items", 0, "group_id"], "group-1")],
    ["缺少发行摘要", without(recent, ["items", 0, "release"])],
    ["发行摘要缺少标题", without(recent, ["items", 0, "release", "title"])],
    ["封面缺少宽度", without(recent, ["items", 0, "release", "artwork", "width"])],
    ["封面高度为 0", withValue(recent, ["items", 0, "release", "artwork", "height"], 0)],
    ["缺少 last_played_at", without(recent, ["items", 0, "last_played_at"])],
    ["last_played_at 不是时间", withValue(recent, ["items", 0, "last_played_at"], "2026-13-40")],
    ["缺少 last_track", without(recent, ["items", 0, "last_track"])],
    ["last_track 缺少标题", without(recent, ["items", 0, "last_track", "title"])],
    ["last_track.id 不是 UUID", withValue(recent, ["items", 0, "last_track", "id"], "track-1")],
  ])("拒绝：%s", (_name, input) => {
    expect(() => decodeRecentReleases(input)).toThrow(d.DecodeError);
  });
});

describe("decodePlaysCleared", () => {
  const cleared = sampleBody("listening/plays-cleared");

  it("解码删除的条数，保留全部字段", () => {
    expect(decodePlaysCleared(cleared)).toEqual(cleared);
    expect(decodePlaysCleared({ deleted: 0 })).toEqual({ deleted: 0 });
  });

  it.each([
    ["缺少 deleted", {}],
    ["deleted 为负", { deleted: -1 }],
    ["deleted 是小数", { deleted: 1.5 }],
    ["deleted 是字符串", { deleted: "1" }],
  ])("拒绝：%s", (_name, input) => {
    expect(() => decodePlaysCleared(input)).toThrow(d.DecodeError);
  });
});
