import { describe, expect, it } from "vitest";
import { decode as d } from "../../../shared/api";
import { sampleBody } from "../../../test/api-samples";
import { withValue, without } from "../../../test/mutate";
import { decodeHistoryQueue, decodeHistoryTracks, decodeTrackPlaysDeleted, decodePlaysCleared, decodeRecentReleases, decodeRecordedPlay } from "./decoders";

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

describe("按曲目汇总的历史响应", () => {
  const tracks = sampleBody("listening/history-tracks");
  const queue = sampleBody("listening/history-queue");

  it.each([
    ["history-tracks", decodeHistoryTracks],
    ["history-tracks-missing", decodeHistoryTracks],
    ["history-tracks-empty", decodeHistoryTracks],
    ["history-tracks-out-of-range", decodeHistoryTracks],
    ["history-queue", decodeHistoryQueue],
    ["history-queue-empty", decodeHistoryQueue],
    ["history-queue-truncated", decodeHistoryQueue],
    ["track-plays-deleted", decodeTrackPlaysDeleted],
    ["track-plays-deleted-empty", decodeTrackPlaysDeleted],
  ] as const)("真实样本 %s 的 body 往返不丢字段", (name, decode) => {
    const body = sampleBody(`listening/${name}`);
    expect(decode(body)).toEqual(body);
  });

  it("空页保留真实 total 与请求页码，missing 保留元数据，截断候选恰好 1,000 首", () => {
    const out = decodeHistoryTracks(sampleBody("listening/history-tracks-out-of-range"));
    expect(out.items).toEqual([]);
    expect(out.pagination.page).toBeGreaterThan(1);
    expect(out.pagination.total).toBeGreaterThan(0);
    expect(out.has_history).toBe(true);
    const missing = decodeHistoryTracks(sampleBody("listening/history-tracks-missing")).items.find((item) => !item.present);
    expect(missing?.release.id).toMatch(/^[0-9a-f-]{36}$/);
    expect(missing?.title).toBeTruthy();
    const truncated = decodeHistoryQueue(sampleBody("listening/history-queue-truncated"));
    expect(truncated.items).toHaveLength(1000);
    expect(truncated).toMatchObject({ limit: 1000, truncated: true, has_history: true });
    expect(truncated.total).toBeGreaterThan(1000);
  });

  it.each([
    "track_id", "last_played_at", "play_count", "present", "title", "artist",
    "duration_seconds", "group_id", "release", "source",
  ])("分页和队列都拒绝缺少条目字段 %s", (field) => {
    expect(() => decodeHistoryTracks(without(tracks, ["items", 0, field]))).toThrow(d.DecodeError);
    expect(() => decodeHistoryQueue(without(queue, ["items", 0, field]))).toThrow(d.DecodeError);
  });

  it.each(["source_kind", "codec", "bit_depth", "sample_rate", "channels", "bitrate"])("来源的 nullable 字段仍必须传入：%s", (field) => {
    expect(() => decodeHistoryTracks(without(tracks, ["items", 0, "source", field]))).toThrow(d.DecodeError);
  });

  it.each([
    ["play_count", 0], ["play_count", -1], ["play_count", 1.5], ["play_count", "2"],
    ["track_id", "track-1"], ["group_id", "../group"], ["present", "false"],
    ["last_played_at", "昨天"], ["title", null], ["artist", null],
    ["duration_seconds", -1], ["duration_seconds", NaN], ["duration_seconds", Infinity],
  ])("非法历史字段 %s=%s 被拒绝", (field, value) => {
    expect(() => decodeHistoryTracks(withValue(tracks, ["items", 0, field], value))).toThrow(d.DecodeError);
    expect(() => decodeHistoryQueue(withValue(queue, ["items", 0, field], value))).toThrow(d.DecodeError);
  });

  it.each([
    ["source_kind", ""], ["codec", 1], ["bit_depth", -1], ["sample_rate", 44100.5],
    ["channels", Infinity], ["bitrate", "128000"],
  ])("拒绝非法来源事实 %s", (field, value) => {
    expect(() => decodeHistoryTracks(withValue(tracks, ["items", 0, "source", field], value))).toThrow(d.DecodeError);
  });

  it.each([
    ["width", 0], ["height", -1],
  ])("沿用封面尺寸边界：拒绝 %s", (field, value) => {
    const artwork = { resource_id: "cover.png", mime: "image/png", width: 640, height: 480, [field]: value };
    expect(() => decodeHistoryTracks(withValue(tracks, ["items", 0, "release", "artwork"], artwork))).toThrow(d.DecodeError);
  });

  it.each(["items", "pagination", "has_history"])("分页拒绝缺少 %s", (field) => {
    expect(() => decodeHistoryTracks(without(tracks, [field]))).toThrow(d.DecodeError);
  });
  it.each(["items", "total", "limit", "truncated", "has_history"])("队列拒绝缺少 %s", (field) => {
    expect(() => decodeHistoryQueue(without(queue, [field]))).toThrow(d.DecodeError);
  });
  it.each([0, 999, 1001, "1000", null])("固定候选 limit 不能是 %s", (limit) => {
    expect(() => decodeHistoryQueue(withValue(queue, ["limit"], limit))).toThrow(d.DecodeError);
  });
  it.each([["page", 0], ["page", 1.5], ["page_size", 0], ["page_size", 101], ["total", -1]])("拒绝非法分页 %s=%s", (field, value) => {
    expect(() => decodeHistoryTracks(withValue(tracks, ["pagination", field], value))).toThrow(d.DecodeError);
  });

  it("分页允许 100 拒绝 101，候选允许 1,000 拒绝 1,001，不悄悄截断", () => {
    const item = decodeHistoryTracks(tracks).items[0];
    const items = (count: number) => Array.from({ length: count }, () => item);
    expect(decodeHistoryTracks(withValue(tracks, ["items"], items(100))).items).toHaveLength(100);
    expect(() => decodeHistoryTracks(withValue(tracks, ["items"], items(101)))).toThrow(d.DecodeError);
    expect(decodeHistoryQueue(withValue(queue, ["items"], items(1000))).items).toHaveLength(1000);
    expect(() => decodeHistoryQueue(withValue(queue, ["items"], items(1001)))).toThrow(d.DecodeError);
  });

  it.each([{}, { deleted: -1 }, { deleted: 0.5 }, { deleted: "1" }])("移除响应拒绝非法条数 %j", (value) => {
    expect(() => decodeTrackPlaysDeleted(value)).toThrow(d.DecodeError);
  });
});
