import { describe, expect, it } from "vitest";
import { decode as d, decodeErrorEnvelope } from "../../../shared/api";
import { listApiSampleNames, loadApiSample, sampleBody } from "../../../test/api-samples";
import { withValue, without } from "../../../test/mutate";
import {
  decodeContainingTrack,
  decodeFavoriteAlbumsPage,
  decodeFavoriteLookup,
  decodeFavoriteQueue,
  decodeFavoriteToggle,
  decodeFavoriteTracksPage,
  decodeItemMoved,
  decodeItemRemoved,
  decodeItemRestored,
  decodeItemsAdded,
  decodePlaylistCreated,
  decodePlaylistDeleted,
  decodePlaylistDetail,
  decodePlaylistItemsPage,
  decodePlaylistQueue,
  decodePlaylistRenamed,
  decodePlaylistsPage,
} from "./decoders";

type Contract = { status: number; decode: (input: unknown) => unknown };

// 显式登记每份样本，新增或删除样本都必须同步审查，不按文件名猜解码器。
const contracts: Record<string, Contract> = {
  "favorites/album-delete": { status: 200, decode: decodeFavoriteToggle },
  "favorites/album-put": { status: 200, decode: decodeFavoriteToggle },
  "favorites/albums-empty": { status: 200, decode: decodeFavoriteAlbumsPage },
  "favorites/albums-unavailable": { status: 200, decode: decodeFavoriteAlbumsPage },
  "favorites/albums": { status: 200, decode: decodeFavoriteAlbumsPage },
  "favorites/lookup-empty": { status: 200, decode: decodeFavoriteLookup },
  "favorites/lookup": { status: 200, decode: decodeFavoriteLookup },
  "favorites/queue-empty": { status: 200, decode: decodeFavoriteQueue },
  "favorites/queue-missing": { status: 200, decode: decodeFavoriteQueue },
  "favorites/queue": { status: 200, decode: decodeFavoriteQueue },
  "favorites/track-delete-repeated": { status: 200, decode: decodeFavoriteToggle },
  "favorites/track-delete": { status: 200, decode: decodeFavoriteToggle },
  "favorites/track-put-repeated": { status: 200, decode: decodeFavoriteToggle },
  "favorites/track-put": { status: 200, decode: decodeFavoriteToggle },
  "favorites/tracks-empty": { status: 200, decode: decodeFavoriteTracksPage },
  "favorites/tracks-missing": { status: 200, decode: decodeFavoriteTracksPage },
  "favorites/tracks-out-of-range": { status: 200, decode: decodeFavoriteTracksPage },
  "favorites/tracks": { status: 200, decode: decodeFavoriteTracksPage },
  "playlists/add-items-all-existing": { status: 200, decode: decodeItemsAdded },
  "playlists/add-items-input-duplicates": { status: 200, decode: decodeItemsAdded },
  "playlists/add-items": { status: 200, decode: decodeItemsAdded },
  "playlists/containing-track-empty": { status: 200, decode: decodeContainingTrack },
  "playlists/containing-track": { status: 200, decode: decodeContainingTrack },
  "playlists/create-replayed": { status: 201, decode: decodePlaylistCreated },
  "playlists/create-with-tracks": { status: 201, decode: decodePlaylistCreated },
  "playlists/create": { status: 201, decode: decodePlaylistCreated },
  "playlists/delete-replayed": { status: 200, decode: decodePlaylistDeleted },
  "playlists/delete": { status: 200, decode: decodePlaylistDeleted },
  "playlists/detail-over-limit": { status: 200, decode: decodePlaylistDetail },
  "playlists/detail": { status: 200, decode: decodePlaylistDetail },
  "playlists/items-empty": { status: 200, decode: decodePlaylistItemsPage },
  "playlists/items-missing": { status: 200, decode: decodePlaylistItemsPage },
  "playlists/items-next-page": { status: 200, decode: decodePlaylistItemsPage },
  "playlists/items-out-of-range": { status: 200, decode: decodePlaylistItemsPage },
  "playlists/items": { status: 200, decode: decodePlaylistItemsPage },
  "playlists/list-empty": { status: 200, decode: decodePlaylistsPage },
  "playlists/list-unknown-duration": { status: 200, decode: decodePlaylistsPage },
  "playlists/list": { status: 200, decode: decodePlaylistsPage },
  "playlists/move-before": { status: 200, decode: decodeItemMoved },
  "playlists/move-down": { status: 200, decode: decodeItemMoved },
  "playlists/move-first": { status: 200, decode: decodeItemMoved },
  "playlists/move-last": { status: 200, decode: decodeItemMoved },
  "playlists/move-noop": { status: 200, decode: decodeItemMoved },
  "playlists/move-up": { status: 200, decode: decodeItemMoved },
  "playlists/queue-empty": { status: 200, decode: decodePlaylistQueue },
  "playlists/queue-missing": { status: 200, decode: decodePlaylistQueue },
  "playlists/queue": { status: 200, decode: decodePlaylistQueue },
  "playlists/remove-item": { status: 200, decode: decodeItemRemoved },
  "playlists/rename-noop": { status: 200, decode: decodePlaylistRenamed },
  "playlists/rename": { status: 200, decode: decodePlaylistRenamed },
  "playlists/restore-after-previous": { status: 200, decode: decodeItemRestored },
  "playlists/restore-already-present": { status: 200, decode: decodeItemRestored },
  "playlists/restore-at-end": { status: 200, decode: decodeItemRestored },
  "playlists/restore-item": { status: 200, decode: decodeItemRestored },
  "playlists/restore-original-index": { status: 200, decode: decodeItemRestored },
  "playlists/restore-over-limit": { status: 200, decode: decodeItemRestored },
  "errors/personal-database-unavailable": { status: 503, decode: decodeErrorEnvelope },
  "errors/personal-favorites-changed": { status: 409, decode: decodeErrorEnvelope },
  "errors/personal-idempotency-conflict": { status: 409, decode: decodeErrorEnvelope },
  "errors/personal-invalid-input": { status: 400, decode: decodeErrorEnvelope },
  "errors/personal-invalid-json": { status: 400, decode: decodeErrorEnvelope },
  "errors/personal-invalid-pagination": { status: 400, decode: decodeErrorEnvelope },
  "errors/personal-method-not-allowed": { status: 405, decode: decodeErrorEnvelope },
  "errors/personal-not-found": { status: 404, decode: decodeErrorEnvelope },
  "errors/personal-origin-forbidden": { status: 403, decode: decodeErrorEnvelope },
  "errors/personal-origin-required": { status: 403, decode: decodeErrorEnvelope },
  "errors/personal-playlist-limit": { status: 409, decode: decodeErrorEnvelope },
  "errors/personal-playlist-track-limit": { status: 409, decode: decodeErrorEnvelope },
  "errors/personal-revision-conflict": { status: 409, decode: decodeErrorEnvelope },
  "errors/personal-unauthorized": { status: 401, decode: decodeErrorEnvelope },
};

const representativeContracts = [
  ["favorites/tracks", decodeFavoriteTracksPage],
  ["favorites/albums", decodeFavoriteAlbumsPage],
  ["favorites/track-put", decodeFavoriteToggle],
  ["favorites/lookup", decodeFavoriteLookup],
  ["favorites/queue", decodeFavoriteQueue],
  ["playlists/list", decodePlaylistsPage],
  ["playlists/create", decodePlaylistCreated],
  ["playlists/detail", decodePlaylistDetail],
  ["playlists/rename", decodePlaylistRenamed],
  ["playlists/delete", decodePlaylistDeleted],
  ["playlists/items", decodePlaylistItemsPage],
  ["playlists/add-items", decodeItemsAdded],
  ["playlists/remove-item", decodeItemRemoved],
  ["playlists/restore-item", decodeItemRestored],
  ["playlists/move-up", decodeItemMoved],
  ["playlists/queue", decodePlaylistQueue],
  ["playlists/containing-track", decodeContainingTrack],
] as const;

function first<T>(items: readonly T[]): T {
  const item = items[0];
  if (item === undefined) throw new Error("测试样本必须至少包含一项");
  return item;
}

const favoriteTracks = decodeFavoriteTracksPage(sampleBody("favorites/tracks"));
const favoriteAlbums = decodeFavoriteAlbumsPage(sampleBody("favorites/albums"));
const playlists = decodePlaylistsPage(sampleBody("playlists/list"));
const playlistItems = decodePlaylistItemsPage(sampleBody("playlists/items"));
const detail = decodePlaylistDetail(sampleBody("playlists/detail"));
const lookup = decodeFavoriteLookup(sampleBody("favorites/lookup"));
const containing = decodeContainingTrack(sampleBody("playlists/containing-track"));

// 固定的合成 UUID 带字母，既能生成互不重复的身份，也能验证大小写不改变身份。
function fixtureId(index: number): string {
  return `abcdef00-0000-4000-8000-${String(index + 1).padStart(12, "0")}`;
}

function favoriteTracksInput(size: number) {
  const item = first(favoriteTracks.items);
  return {
    ...favoriteTracks,
    items: Array.from({ length: size }, (_, index) => ({ ...item, track: { ...item.track, track_id: fixtureId(index) } })),
    pagination: { page: 1, page_size: 100, total: size },
  };
}

function favoriteAlbumsInput(size: number) {
  const item = first(favoriteAlbums.items);
  return {
    ...favoriteAlbums,
    items: Array.from({ length: size }, (_, index) => ({ ...item, album: { ...item.album, group_id: fixtureId(index) } })),
    pagination: { page: 1, page_size: 100, total: size },
  };
}

function playlistsInput(size: number) {
  return {
    ...playlists,
    items: Array.from({ length: size }, (_, index) => ({ ...first(playlists.items), id: fixtureId(index) })),
    pagination: { page: 1, page_size: 20, total: size },
  };
}

function playlistItemsInput(size: number, page = 1, pageSize = 100, total = (page - 1) * pageSize + size) {
  const start = (page - 1) * pageSize + 1;
  const item = first(playlistItems.items);
  return {
    ...playlistItems,
    playlist: { ...playlistItems.playlist, track_count: total, over_limit_count: Math.max(total - 10_000, 0) },
    items: Array.from({ length: size }, (_, index) => ({
      ...item,
      id: fixtureId(10_000 + start + index),
      position: start + index,
      track: { ...item.track, track_id: fixtureId(start + index) },
    })),
    pagination: { page, page_size: pageSize, total },
    page_after_item_id: size > 0 && start + size - 1 < total ? fixtureId(10_000 + start + size) : null,
  };
}

function lookupTracksInput(size: number) {
  return { ...lookup, tracks: Array.from({ length: size }, (_, index) => ({ ...first(lookup.tracks), track_id: fixtureId(index) })) };
}

function lookupAlbumsInput(size: number) {
  return { ...lookup, albums: Array.from({ length: size }, (_, index) => ({ ...first(lookup.albums), group_id: fixtureId(index) })) };
}

function containingInput(size: number) {
  return { ...containing, items: Array.from({ length: size }, (_, index) => ({ ...first(containing.items), playlist_id: fixtureId(index) })) };
}

function coverInput(size: number) {
  return {
    playlist: {
      ...detail.playlist,
      track_count: size,
      duration_seconds: size * 120,
      cover_groups: Array.from({ length: size }, (_, index) => ({ group_id: fixtureId(index), artwork: null })),
    },
  };
}

function queueInput(size: number, missing = 0, omitted = 0) {
  const track = first(favoriteTracks.items).track;
  return {
    items: Array.from({ length: size }, (_, index) => ({
      ...track,
      track_id: fixtureId(index),
      present: true,
    })),
    total: size + missing + omitted,
    missing_count: missing,
    eligible_count: size + omitted,
    returned_count: size,
    omitted_count: omitted,
    limit: 10_000,
    truncated: omitted > 0,
    revision: 0,
    playlist: { ...playlistItems.playlist, track_count: size + missing + omitted, over_limit_count: Math.max(size + missing + omitted - 10_000, 0) },
  };
}

function queueResponses(size: number) {
  const { playlist, revision, ...counts } = queueInput(size);
  return { favorites: { ...counts, revision }, playlist: { ...counts, playlist } };
}

describe("收藏与歌单真实接口样本", () => {
  it("全部相关样本与解码器登记一一对应，不遗漏或遗留样本", () => {
    const names = listApiSampleNames().filter((name) => name.startsWith("favorites/") || name.startsWith("playlists/") || name.startsWith("errors/personal-"));
    expect(names.length).toBeGreaterThan(0);
    expect(Object.keys(contracts).sort()).toEqual(names);
  });

  it.each(Object.entries(contracts))("%s：状态码正确，解码与 JSON 往返不丢字段", (name, contract) => {
    const sample = loadApiSample(name);
    expect(sample.status).toBe(contract.status);
    const decoded = contract.decode(sample.body);
    expect(decoded).toStrictEqual(sample.body);
    const serialized: unknown = JSON.parse(JSON.stringify(decoded));
    expect(serialized).toStrictEqual(sample.body);
    expect(contract.decode(serialized)).toStrictEqual(decoded);
  });

  it.each(representativeContracts)("%s：拒绝缺少任何顶层必填字段或非对象响应", (name, decode) => {
    const body = sampleBody(name);
    if (typeof body !== "object" || body === null) throw new Error("样本响应必须是对象");
    for (const field of Object.keys(body)) {
      expect(() => decode(without(body, [field])), `缺少 ${field}`).toThrow(d.DecodeError);
    }
    for (const input of [null, [], "成功", 200]) expect(() => decode(input)).toThrow(d.DecodeError);
  });

  it("超末页仍保留真实总数，缺失曲目保留摘要，不可用专辑没有代表版本", () => {
    const tracks = decodeFavoriteTracksPage(sampleBody("favorites/tracks-out-of-range"));
    const items = decodePlaylistItemsPage(sampleBody("playlists/items-out-of-range"));
    for (const page of [tracks, items]) {
      expect(page.items).toEqual([]);
      expect(page.pagination.total).toBeGreaterThan(0);
      expect(page.pagination.page).toBeGreaterThan(1);
    }
    const missing = decodeFavoriteTracksPage(sampleBody("favorites/tracks-missing")).items.find((item) => !item.track.present);
    expect(missing?.track.title).toBeTruthy();
    expect(missing?.track.release.id).toBeTruthy();
    const unavailable = first(decodeFavoriteAlbumsPage(sampleBody("favorites/albums-unavailable")).items);
    expect(unavailable.album).toMatchObject({ available: false, representative: null, release_count: 0 });
  });
});

describe("个人数据的枚举与字段关系", () => {
  it.each(["artist", "TRACK", "", null, 1])("拒绝非法收藏目标类别 %s", (kind) => {
    expect(() => decodeFavoriteToggle(withValue(sampleBody("favorites/track-put"), ["target_kind"], kind))).toThrow(d.DecodeError);
  });

  it.each([
    ["outcome", "added"], ["outcome", null], ["placement", "before"], ["placement", null],
    ["outcome", "already_present"], ["placement", "unchanged"],
  ])("拒绝非法或相互矛盾的撤销结果 %s=%s", (field, value) => {
    expect(() => decodeItemRestored(withValue(sampleBody("playlists/restore-item"), [field], value))).toThrow(d.DecodeError);
  });

  it.each([
    ["favorites/track-put", false], ["favorites/track-delete", true],
  ] as const)("%s：收藏状态必须与收藏时间一致", (name, favorited) => {
    expect(() => decodeFavoriteToggle(withValue(sampleBody(name), ["favorited"], favorited))).toThrow(d.DecodeError);
  });

  it.each([true, false])("专辑可见性为 %s 时，必须与代表版本一致", (available) => {
    const name = available ? "favorites/albums-unavailable" : "favorites/albums";
    expect(() => decodeFavoriteAlbumsPage(withValue(sampleBody(name), ["items", 0, "album", "available"], available))).toThrow(d.DecodeError);
  });

  it("不可用专辑必须没有可见版本，拒绝非零 release_count", () => {
    const invalid = withValue(sampleBody("favorites/albums-unavailable"), ["items", 0, "album", "release_count"], 1);
    expect(() => decodeFavoriteAlbumsPage(invalid)).toThrow(d.DecodeError);
  });

  it.each(["missing_count", "unknown_duration_count"])("歌单 %s 不得超过曲目总数，列表与详情都拒绝", (field) => {
    const invalid = { ...detail.playlist, [field]: detail.playlist.track_count + 1 };
    expect(() => decodePlaylistDetail({ playlist: invalid })).toThrow(d.DecodeError);
    expect(() => decodePlaylistsPage({ ...playlists, items: [invalid] })).toThrow(d.DecodeError);
  });

  it("撤销临时超出 10,000 首仍可解码，不截断真实计数", () => {
    const overLimit = decodePlaylistDetail(sampleBody("playlists/detail-over-limit"));
    expect(overLimit.playlist).toMatchObject({ track_count: 10_001, track_limit: 10_000, over_limit_count: 1 });
    const restored = decodeItemRestored(sampleBody("playlists/restore-over-limit"));
    expect(restored.playlist.track_count).toBeGreaterThan(10_000);
    expect(restored.playlist.over_limit_count).toBe(restored.playlist.track_count - 10_000);
  });

  it.each([
    ["playlists/detail", decodePlaylistDetail],
    ["playlists/create", decodePlaylistCreated],
    ["playlists/restore-over-limit", decodeItemRestored],
  ] as const)("%s：超限计数不能与 track_count 矛盾", (name, decode) => {
    expect(() => decode(withValue(sampleBody(name), ["playlist", "over_limit_count"], 7))).toThrow(d.DecodeError);
  });

  it.each([0, -1, 1.5, Number.MAX_SAFE_INTEGER + 1, "1"])("歌单版本与位置拒绝非法安全整数 %s", (value) => {
    expect(() => decodePlaylistDetail(withValue(detail, ["playlist", "revision"], value))).toThrow(d.DecodeError);
    expect(() => decodePlaylistItemsPage(withValue(playlistItems, ["items", 0, "position"], value))).toThrow(d.DecodeError);
  });

  it.each([-1, 1.5, Number.MAX_SAFE_INTEGER + 1, NaN, Infinity, "0"])("收藏版本与新增计数拒绝非法数值 %s", (value) => {
    expect(() => decodeFavoriteTracksPage(withValue(favoriteTracks, ["revision"], value))).toThrow(d.DecodeError);
    for (const field of ["added_count", "existing_count", "duplicate_count"]) {
      expect(() => decodeItemsAdded(withValue(sampleBody("playlists/add-items"), [field], value))).toThrow(d.DecodeError);
    }
  });

  it.each([-1, NaN, Infinity, "240"])("总时长拒绝负数、非有限数及错误类型 %s", (value) => {
    expect(() => decodePlaylistDetail(withValue(detail, ["playlist", "duration_seconds"], value))).toThrow(d.DecodeError);
  });

  it.each([false, 1, "true", null])("删除成功标志必须为布尔 true，不能为 %s", (value) => {
    expect(() => decodePlaylistDeleted(withValue(sampleBody("playlists/delete"), ["deleted"], value))).toThrow(d.DecodeError);
  });

  it("缺失 nullable 字段仍被拒绝，不能把缺字段当成 null", () => {
    for (const field of ["duration_seconds", "source", "release", "group_id"]) {
      expect(() => decodeFavoriteTracksPage(without(favoriteTracks, ["items", 0, "track", field]))).toThrow(d.DecodeError);
    }
    for (const field of ["codec", "bit_depth", "sample_rate", "channels", "bitrate"]) {
      expect(() => decodeFavoriteTracksPage(without(favoriteTracks, ["items", 0, "track", "source", field]))).toThrow(d.DecodeError);
    }
    expect(() => decodePlaylistItemsPage(without(playlistItems, ["page_after_item_id"]))).toThrow(d.DecodeError);
  });

  it("拒绝损坏的 UUID、时间与封面尺寸", () => {
    expect(() => decodeFavoriteTracksPage(withValue(favoriteTracks, ["items", 0, "track", "track_id"], "track-1"))).toThrow(d.DecodeError);
    expect(() => decodeFavoriteTracksPage(withValue(favoriteTracks, ["items", 0, "created_at"], "昨天"))).toThrow(d.DecodeError);
    expect(() => decodePlaylistDetail(withValue(detail, ["playlist", "cover_groups", 0, "artwork"], {
      resource_id: "cover.png", mime: "image/png", width: 0, height: 640,
    }))).toThrow(d.DecodeError);
  });
});

describe("有界数组与分页", () => {
  const arrays = [
    { name: "收藏曲目", maximum: 100, decode: decodeFavoriteTracksPage, input: favoriteTracksInput },
    { name: "收藏专辑", maximum: 100, decode: decodeFavoriteAlbumsPage, input: favoriteAlbumsInput },
    { name: "歌单列表", maximum: 20, decode: decodePlaylistsPage, input: playlistsInput },
    { name: "歌单条目", maximum: 100, decode: decodePlaylistItemsPage, input: playlistItemsInput },
    { name: "批量曲目状态", maximum: 10_000, decode: decodeFavoriteLookup, input: lookupTracksInput },
    { name: "批量专辑状态", maximum: 1_000, decode: decodeFavoriteLookup, input: lookupAlbumsInput },
    { name: "曲目所在歌单", maximum: 1_000, decode: decodeContainingTrack, input: containingInput },
    { name: "歌单封面", maximum: 4, decode: decodePlaylistDetail, input: coverInput },
  ];

  it.each(arrays)("$name：接受上限 $maximum，拒绝多一项且不截断", ({ maximum, decode, input }) => {
    const valid = input(maximum);
    expect(decode(valid)).toStrictEqual(valid);
    expect(() => decode(input(maximum + 1))).toThrow(d.DecodeError);
  });

  it.each([
    ["favorites/tracks", decodeFavoriteTracksPage, 100],
    ["favorites/albums", decodeFavoriteAlbumsPage, 100],
    ["playlists/list", decodePlaylistsPage, 20],
    ["playlists/items", decodePlaylistItemsPage, 100],
  ] as const)("%s：拒绝分页越界及 null 数组", (name, decode, maximum) => {
    const body = sampleBody(name);
    for (const [field, value] of [["page", 0], ["page", 1.5], ["page_size", 0], ["page_size", maximum + 1], ["total", -1], ["total", Number.MAX_SAFE_INTEGER + 1]] as const) {
      expect(() => decode(withValue(body, ["pagination", field], value))).toThrow(d.DecodeError);
    }
    expect(() => decode(withValue(body, ["items"], null))).toThrow(d.DecodeError);
  });
});

describe.each([
  ["收藏", decodeFavoriteQueue],
  ["歌单", decodePlaylistQueue],
] as const)("%s播放全部的计数合同", (_name, decode) => {
  it.each([0, 1, 1_000, 1_001, 10_000])("接受 %i 个不重复的可播放候选并保留完整统计", (size) => {
    const result = decode(queueInput(size, 2));
    expect(result.items).toHaveLength(size);
    expect(result).toMatchObject({ total: size + 2, missing_count: 2, eligible_count: size, returned_count: size, omitted_count: 0, truncated: false });
  });

  it("10,001 首可用曲目只返回前 10,000 首，缺失和省略分别计数", () => {
    const result = decode(queueInput(10_000, 3, 1));
    expect(result.items).toHaveLength(10_000);
    expect(result).toMatchObject({ total: 10_004, missing_count: 3, eligible_count: 10_001, returned_count: 10_000, omitted_count: 1, truncated: true });
  });

  it("拒绝返回 10,001 首，不静默截断", () => {
    expect(() => decode(queueInput(10_001))).toThrow(d.DecodeError);
  });

  it.each([
    ["returned_count", 0], ["total", 99], ["missing_count", 1],
    ["eligible_count", 3], ["omitted_count", 1], ["truncated", true],
    ["limit", 9_999], ["limit", 10_001], ["limit", "10000"],
  ])("拒绝彼此不一致的计数或上限：%s=%s", (field, value) => {
    expect(() => decode(withValue(queueInput(2, 2), [field], value))).toThrow(d.DecodeError);
  });

  it("拒绝少取候选却声称已完整处理的窗口", () => {
    const input = queueInput(2, 0, 1);
    expect(() => decode(input)).toThrow(d.DecodeError);
  });

  it("拒绝窗口内缺失和重复的曲目", () => {
    const input = queueInput(2);
    expect(() => decode(withValue(input, ["items", 0, "present"], false))).toThrow(d.DecodeError);
    expect(() => decode(withValue(input, ["items", 1, "track_id"], first(input.items).track_id))).toThrow(d.DecodeError);
  });
});

describe("专辑的可见性与版本数", () => {
  it.each([1, 2, 10])("可用专辑允许 %i 个版本并保留代表版本", (release_count) => {
    const input = withValue(favoriteAlbums, ["items", 0, "album", "release_count"], release_count);
    expect(decodeFavoriteAlbumsPage(input)).toStrictEqual(input);
  });

  it("不可用专辑的零版本与空代表版本是合法组合", () => {
    const input = sampleBody("favorites/albums-unavailable");
    expect(decodeFavoriteAlbumsPage(input)).toStrictEqual(input);
  });

  it("可用专辑即使有代表版本也不能声称零个可见版本", () => {
    const input = withValue(favoriteAlbums, ["items", 0, "album", "release_count"], 0);
    expect(() => decodeFavoriteAlbumsPage(input)).toThrow(d.DecodeError);
  });
});

const summaryCases = [
  { name: "歌单详情", decode: decodePlaylistDetail, input: (playlist: typeof detail.playlist) => ({ playlist }) },
  { name: "歌单列表", decode: decodePlaylistsPage, input: (playlist: typeof detail.playlist) => ({ items: [playlist], pagination: { page: 1, page_size: 20, total: 1 } }) },
];

describe.each(summaryCases)("$name的摘要计数关系", ({ decode, input }) => {
  it.each(["missing_count", "unknown_duration_count"] as const)("%s 为零或等于全部曲目数都合法", (field) => {
    for (const count of [0, detail.playlist.track_count]) {
      const summary = { ...detail.playlist, [field]: count };
      if (field === "unknown_duration_count" && count === summary.track_count) summary.duration_seconds = 0;
      const valid = input(summary);
      expect(decode(valid)).toStrictEqual(valid);
    }
  });

  it.each(["missing_count", "unknown_duration_count"] as const)("%s 超过总曲目数时独立拒绝", (field) => {
    const invalid = input({ ...detail.playlist, [field]: detail.playlist.track_count + 1 });
    expect(() => decode(invalid)).toThrow(d.DecodeError);
  });

  it("同一首可以同时缺失和时长未知，两类计数不是互斥的", () => {
    const valid = input({
      ...detail.playlist,
      missing_count: detail.playlist.track_count,
      unknown_duration_count: detail.playlist.track_count,
      duration_seconds: 0,
    });
    expect(decode(valid)).toStrictEqual(valid);
  });

  it.each([0, 1, 2, 4])("%i 首曲目允许相同数量的不同封面组，空歌单也合法", (count) => {
    const valid = input(coverInput(count).playlist);
    expect(decode(valid)).toStrictEqual(valid);
  });

  it("封面组未超过四个，但超过曲目数时仍拒绝", () => {
    const invalid = input({ ...coverInput(3).playlist, track_count: 2 });
    expect(() => decode(invalid)).toThrow(d.DecodeError);
  });

  it("空歌单不能保留一张封面组", () => {
    const invalid = input({ ...coverInput(1).playlist, track_count: 0, duration_seconds: 0 });
    expect(() => decode(invalid)).toThrow(d.DecodeError);
  });
});

const pageCases = [
  { name: "收藏曲目", decode: decodeFavoriteTracksPage, input: (page_size: number, total: number) => ({ ...favoriteTracksInput(2), pagination: { page: 1, page_size, total } }) },
  { name: "收藏专辑", decode: decodeFavoriteAlbumsPage, input: (page_size: number, total: number) => ({ ...favoriteAlbumsInput(2), pagination: { page: 1, page_size, total } }) },
  { name: "歌单列表", decode: decodePlaylistsPage, input: (page_size: number, total: number) => ({ ...playlistsInput(2), pagination: { page: 1, page_size, total } }) },
  { name: "歌单条目", decode: decodePlaylistItemsPage, input: (page_size: number, total: number) => playlistItemsInput(2, 1, page_size, total) },
];

describe.each(pageCases)("$name的一页条数关系", ({ decode, input }) => {
  it("当前页可以正好达到页大小与总数", () => {
    const valid = input(2, 2);
    expect(decode(valid)).toStrictEqual(valid);
  });

  it("一页不必等于总数，不能把分页数据误当成完整集合", () => {
    const valid = input(2, 5);
    expect(decode(valid)).toStrictEqual(valid);
  });

  it("末页不足页大小仍合法", () => {
    const valid = input(3, 2);
    expect(decode(valid)).toStrictEqual(valid);
  });

  it("items 没超过 total，但超过 page_size 时拒绝", () => {
    expect(() => decode(input(1, 2))).toThrow(d.DecodeError);
  });

  it("items 没超过 page_size，但超过 total 时拒绝", () => {
    expect(() => decode(input(2, 1))).toThrow(d.DecodeError);
  });
});

describe("歌单条目页的快照与下一页锚点", () => {
  it.each([
    { name: "空歌单", size: 0, page: 1, pageSize: 3, total: 0, positions: [], hasNext: false },
    { name: "第一页", size: 3, page: 1, pageSize: 3, total: 8, positions: [1, 2, 3], hasNext: true },
    { name: "中间页", size: 3, page: 2, pageSize: 3, total: 8, positions: [4, 5, 6], hasNext: true },
    { name: "不足一页的末页", size: 2, page: 3, pageSize: 3, total: 8, positions: [7, 8], hasNext: false },
    { name: "完整末页", size: 3, page: 2, pageSize: 3, total: 6, positions: [4, 5, 6], hasNext: false },
    { name: "超末页", size: 0, page: 4, pageSize: 3, total: 8, positions: [], hasNext: false },
  ])("$name：总数、连续位置和下一页锚点匹配", ({ size, page, pageSize, total, positions, hasNext }) => {
    const input = playlistItemsInput(size, page, pageSize, total);
    const decoded = decodePlaylistItemsPage(input);
    expect(decoded).toStrictEqual(input);
    expect(decoded.pagination.total).toBe(decoded.playlist.track_count);
    expect(decoded.items.map((item) => item.position)).toEqual(positions);
    expect(decoded.page_after_item_id !== null).toBe(hasNext);
  });

  it.each([7, 9])("分页 total=%i 与同快照 track_count=8 不一致时拒绝", (total) => {
    const invalid = withValue(playlistItemsInput(3, 2, 3, 8), ["pagination", "total"], total);
    expect(() => decodePlaylistItemsPage(invalid)).toThrow(d.DecodeError);
  });

  it.each([
    ["起点错误", 0, 1], ["中间缺号", 1, 6], ["序号重复", 1, 4], ["逆序", 2, 3],
  ] as const)("拒绝不符合页码的连续位置：%s", (_name, index, position) => {
    const invalid = withValue(playlistItemsInput(3, 2, 3, 8), ["items", index, "position"], position);
    expect(() => decodePlaylistItemsPage(invalid)).toThrow(d.DecodeError);
  });

  it.each([1, 2])("第 %i 页后面仍有条目时不能缺少下一页锚点", (page) => {
    const invalid = withValue(playlistItemsInput(3, page, 3, 8), ["page_after_item_id"], null);
    expect(() => decodePlaylistItemsPage(invalid)).toThrow(d.DecodeError);
  });

  it.each([
    ["空歌单", 0, 1, 0], ["末页", 2, 3, 8], ["超末页", 0, 4, 8],
  ] as const)("%s不能附带下一页锚点", (_name, size, page, total) => {
    const invalid = withValue(playlistItemsInput(size, page, 3, total), ["page_after_item_id"], fixtureId(99));
    expect(() => decodePlaylistItemsPage(invalid)).toThrow(d.DecodeError);
  });

  it("超末页不能带上超出总曲目数的条目，即使页内位置连续", () => {
    const invalid = playlistItemsInput(2, 3, 3, 5);
    expect(() => decodePlaylistItemsPage(invalid)).toThrow(d.DecodeError);
  });

  it("下一页锚点不能指向当前页已经返回的条目", () => {
    const input = playlistItemsInput(3, 1, 3, 8);
    const invalid = withValue(input, ["page_after_item_id"], first(input.items).id);
    expect(() => decodePlaylistItemsPage(invalid)).toThrow(d.DecodeError);
  });
});

describe("歌单队列与摘要的总数一致", () => {
  it.each([
    [0, 0], [0, 3], [2, 3], [10_000, 2],
  ] as const)("%i 首候选加 %i 首缺失与歌单总数一致", (size, missing) => {
    const decoded = decodePlaylistQueue(queueInput(size, missing));
    expect(decoded.total).toBe(size + missing);
    expect(decoded.total).toBe(decoded.playlist.track_count);
  });

  it.each([2, 4])("队列自身统计正确，但歌单 track_count=%i 与 total=3 不一致时拒绝", (track_count) => {
    const invalid = withValue(queueInput(2, 1), ["playlist", "track_count"], track_count);
    expect(() => decodePlaylistQueue(invalid)).toThrow(d.DecodeError);
  });
});

describe("歌单操作回执的位置与移除版本", () => {
  const removed = decodeItemRemoved(sampleBody("playlists/remove-item"));

  it.each([
    [0, 1], [2, 1], [2, 3],
  ] as const)("移除后剩 %i 首，移除前位置 %i 合法", (track_count, position) => {
    const input = { ...removed, playlist: { ...removed.playlist, track_count }, removed: { ...removed.removed, position } };
    expect(decodeItemRemoved(input)).toStrictEqual(input);
  });

  it.each([
    [0, 2], [2, 4],
  ] as const)("移除后剩 %i 首，不接受移除前位置 %i", (track_count, position) => {
    const input = { ...removed, playlist: { ...removed.playlist, track_count }, removed: { ...removed.removed, position } };
    expect(() => decodeItemRemoved(input)).toThrow(d.DecodeError);
  });

  it("undo 的移除版本与回执歌单版本相等时接受", () => {
    const input = { ...removed, playlist: { ...removed.playlist, revision: 7 }, undo: { ...removed.undo, removal_revision: 7 } };
    expect(decodeItemRemoved(input)).toStrictEqual(input);
  });

  it.each([6, 8])("移除后版本为 7 时，undo.removal_revision=%i 被拒绝", (removal_revision) => {
    const input = { ...removed, playlist: { ...removed.playlist, revision: 7 }, undo: { ...removed.undo, removal_revision } };
    expect(() => decodeItemRemoved(input)).toThrow(d.DecodeError);
  });

  describe.each([
    ["playlists/restore-item", decodeItemRestored, "item"],
    ["playlists/restore-already-present", decodeItemRestored, "item"],
    ["playlists/move-up", decodeItemMoved, "moved"],
    ["playlists/move-noop", decodeItemMoved, "moved"],
  ] as const)("%s", (name, decode, field) => {
    const input = withValue(sampleBody(name), ["playlist", "track_count"], 2);

    it.each([1, 2])("操作后的第 %i 首位于合法范围内", (position) => {
      const valid = withValue(input, [field, "position"], position);
      expect(decode(valid)).toStrictEqual(valid);
    });

    it("操作后的位置不能超过总曲目数", () => {
      const invalid = withValue(input, [field, "position"], 3);
      expect(() => decode(invalid)).toThrow(d.DecodeError);
    });

    it("空歌单不能返回一个仍存在的条目位置", () => {
      const invalid = withValue(withValue(input, ["playlist", "track_count"], 0), [field, "position"], 1);
      expect(() => decode(invalid)).toThrow(d.DecodeError);
    });
  });
});

const duplicateCases = [
  { name: "收藏曲目", decode: decodeFavoriteTracksPage, input: () => favoriteTracksInput(2), arrayPath: ["items"], idPath: ["track", "track_id"] },
  { name: "收藏专辑", decode: decodeFavoriteAlbumsPage, input: () => favoriteAlbumsInput(2), arrayPath: ["items"], idPath: ["album", "group_id"] },
  { name: "歌单列表", decode: decodePlaylistsPage, input: () => playlistsInput(2), arrayPath: ["items"], idPath: ["id"] },
  { name: "歌单条目身份", decode: decodePlaylistItemsPage, input: () => playlistItemsInput(2), arrayPath: ["items"], idPath: ["id"] },
  { name: "歌单曲目身份", decode: decodePlaylistItemsPage, input: () => playlistItemsInput(2), arrayPath: ["items"], idPath: ["track", "track_id"] },
  { name: "批量曲目状态", decode: decodeFavoriteLookup, input: () => lookupTracksInput(2), arrayPath: ["tracks"], idPath: ["track_id"] },
  { name: "批量专辑状态", decode: decodeFavoriteLookup, input: () => lookupAlbumsInput(2), arrayPath: ["albums"], idPath: ["group_id"] },
  { name: "曲目所在歌单", decode: decodeContainingTrack, input: () => containingInput(2), arrayPath: ["items"], idPath: ["playlist_id"] },
  { name: "歌单封面组", decode: decodePlaylistDetail, input: () => coverInput(2), arrayPath: ["playlist", "cover_groups"], idPath: ["group_id"] },
  { name: "收藏队列", decode: decodeFavoriteQueue, input: () => queueResponses(2).favorites, arrayPath: ["items"], idPath: ["track_id"] },
  { name: "歌单队列", decode: decodePlaylistQueue, input: () => queueResponses(2).playlist, arrayPath: ["items"], idPath: ["track_id"] },
];

describe.each(duplicateCases)("$name的批内唯一性", ({ decode, input, arrayPath, idPath }) => {
  it("不同 ID 的两项都保留，不按相同标题或元数据去重", () => {
    const valid = input();
    expect(decode(valid)).toStrictEqual(valid);
  });

  it.each([false, true])("同一 ID 重复时拒绝，第二项改成大写=%s", (uppercase) => {
    const id = fixtureId(0);
    const firstChanged = withValue(input(), [...arrayPath, 0, ...idPath], id);
    const invalid = withValue(firstChanged, [...arrayPath, 1, ...idPath], uppercase ? id.toUpperCase() : id);
    expect(() => decode(invalid)).toThrow(d.DecodeError);
  });
});
