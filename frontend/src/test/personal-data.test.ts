import type { BrowserContext, Route } from "@playwright/test";
import { describe, expect, it, vi } from "vitest";
import { installApiMock } from "../../e2e/support/api-mock";
import * as d from "../features/personal/api/decoders";
import { apiRequest } from "../shared/api";
import { albumGroup, albumGroups, catalogDataOf, groupDetailOf, releaseDetailOf, type ApiResult } from "./catalog-data";
import { installFakeApi, installFakeSession } from "./fake-api";
import { installPersonalApi } from "./fake-personal";
import {
  PERSONAL_ADMIN_ID, PERSONAL_LISTENER_ID, personalAlbumOf, personalDataOf, personalTrack, personalTracksOf, personalUserOf,
  respondToPersonal, seedFavoriteAlbums, seedFavoriteTracks, seedPersonalAlbums, seedPersonalTracks, seedPlaylist, type PersonalRequest,
} from "./personal-data";

const userId = PERSONAL_LISTENER_ID;
const otherId = "00000000-0000-4000-8000-000000000004";
const unknownId = "00000000-0000-4000-8000-ffffffffffff";
const favorites = "/api/v1/me/favorites";
const playlists = "/api/v1/me/playlists";
const key = (index: number) => `00000000-0000-4000-8000-80000000${String(index).padStart(4, "0")}`;

function decoded<T>(response: ApiResult, decoder: (body: unknown) => T, status = 200): T {
  expect(response.status).toBe(status);
  const value = decoder(response.body);
  expect(value).toEqual(response.body);
  return value;
}
function rejected(response: ApiResult, status: number, code: string): void {
  expect(response).toMatchObject({ status, body: { error: { code }, request_id: "sample-request" } });
}
function setup(count = 3) {
  const catalog = catalogDataOf([albumGroup(1, { track_count: count })]);
  const data = personalDataOf(catalog);
  const tracks = personalTracksOf(catalog);
  let nextKey = 0;
  const call = (method: string, path: string, body?: unknown, options: { user?: string | null; key?: string | null } = {}) => respondToPersonal(data, options.user === undefined ? userId : options.user, {
    method, path, body, idempotencyKey: options.key === undefined ? key(++nextKey) : options.key,
  });
  const playlist = (ids = tracks.map((track) => track.track_id)) => seedPlaylist(data, userId, { name: "测试歌单", trackIds: ids });
  return { data, catalog, tracks, call, playlist };
}

describe("个人数据共享替身：默认接线与会话", () => {
  it("默认空收藏、空歌单与 lookup 明确成功，陌生 UUID 的状态为 false", () => {
    const { call } = setup();
    expect(decoded(call("GET", `${favorites}/albums`), d.decodeFavoriteAlbumsPage)).toMatchObject({ items: [], revision: 0, pagination: { total: 0 } });
    expect(decoded(call("GET", `${favorites}/tracks`), d.decodeFavoriteTracksPage).items).toEqual([]);
    expect(decoded(call("GET", playlists), d.decodePlaylistsPage).items).toEqual([]);
    expect(decoded(call("POST", `${favorites}/lookup`, { track_ids: [unknownId], group_ids: [unknownId] }), d.decodeFavoriteLookup)).toMatchObject({ tracks: [{ track_id: unknownId, favorited: false }], albums: [{ group_id: unknownId, favorited: false }] });
    expect(decoded(call("GET", `${favorites}/tracks/queue`), d.decodeFavoriteQueue).returned_count).toBe(0);
  });

  it("未登录先 401；未知路由 404，静态路由不吞掉错误方法", () => {
    const { call } = setup();
    rejected(call("POST", `${favorites}/lookup`, null, { user: null }), 401, "unauthorized");
    rejected(call("GET", `${favorites}/misspelled`), 404, "not_found");
    rejected(call("GET", `${playlists}/${unknownId}/misspelled`), 404, "not_found");
    rejected(call("DELETE", `${favorites}/tracks/queue`), 405, "method_not_allowed");
    rejected(call("POST", `${playlists}/containing-track/${unknownId}`), 405, "method_not_allowed");
  });

  it("installFakeSession 的默认空 API 与登出状态同步，不需要每页补 lookup", async () => {
    const api = installFakeApi();
    const session = installFakeSession(api, { current: "listener" });
    const request = () => apiRequest({ path: `${favorites}/lookup`, method: "POST", body: { track_ids: [unknownId] } }, d.decodeFavoriteLookup);
    expect((await request()).status).toBe(200);
    expect((await apiRequest({ path: `${favorites}/albums`, method: "GET" }, d.decodeFavoriteAlbumsPage)).data.items).toEqual([]);
    session.current = null;
    await expect(request()).rejects.toMatchObject({ failure: { kind: "http", status: 401 } });
    expect(api.requestsTo("POST", `${favorites}/lookup`)).toHaveLength(2);
  });

  it("显式 installPersonalApi 绑定当前 catalog 与用户；api.on 仍可覆盖故障", async () => {
    const api = installFakeApi();
    const session = installFakeSession(api, { current: "listener" });
    const catalog = catalogDataOf([albumGroup(3, { track_count: 2 })]);
    const data = installPersonalApi(api, { session, catalog });
    const track = personalTracksOf(catalog)[0];
    if (track === undefined) throw new Error("没有曲目种子");
    seedFavoriteTracks(data, userId, [track.track_id]);
    const request = () => apiRequest({ method: "GET", path: `${favorites}/tracks` }, d.decodeFavoriteTracksPage);
    expect((await request()).data.items[0]?.track.track_id).toBe(track.track_id);
    session.current = "curator";
    expect((await request()).data.items).toEqual([]);
    session.current = "listener";
    expect((await request()).data.items).toHaveLength(1);
    api.on("GET", `${favorites}/tracks`, () => { throw new Error("模拟丢响应"); });
    await expect(request()).rejects.toMatchObject({ failure: { kind: "network" } });
  });

  it("Playwright 适配器不启动浏览器也能按 mock.session 隔离、写入与刷新读取", async () => {
    const register = vi.fn<(pattern: string, handler: (route: Route) => Promise<void>) => Promise<void>>().mockResolvedValue(undefined);
    const mock = await installApiMock({ route: register } as unknown as BrowserContext, { signedInAs: "listener" });
    const handler = register.mock.calls[0]?.[1];
    if (handler === undefined) throw new Error("没有登记受控后端");
    const send = async (request: PersonalRequest) => {
      const fulfill = vi.fn<(options: { status: number; body: string }) => Promise<void>>().mockResolvedValue(undefined);
      const route = { request: () => ({ method: () => request.method, url: () => `http://localhost${request.path}`, postData: () => request.body === undefined ? null : JSON.stringify(request.body), postDataJSON: () => request.body === undefined ? null : request.body, headers: () => ({ "idempotency-key": request.idempotencyKey ?? "" }) }), fulfill } as unknown as Route;
      await handler(route);
      const reply = fulfill.mock.calls[0]?.[0];
      if (reply === undefined) throw new Error("没有响应");
      return { status: reply.status, body: JSON.parse(reply.body) as unknown };
    };
    const track = personalTracksOf(mock)[0];
    if (track === undefined) throw new Error("没有 catalog 曲目");
    const put = { method: "PUT", path: `${favorites}/tracks/${track.track_id}` };
    decoded(await send(put), d.decodeFavoriteToggle);
    rejected(await send({ ...put, method: "DELETE", body: null }), 400, "invalid_input");
    expect(decoded(await send({ method: "GET", path: `${favorites}/tracks` }), d.decodeFavoriteTracksPage).items).toHaveLength(1);
    expect(decoded(await send({ ...put, method: "DELETE" }), d.decodeFavoriteToggle).favorited).toBe(false);
    expect(decoded(await send({ method: "GET", path: `${favorites}/tracks` }), d.decodeFavoriteTracksPage).items).toEqual([]);
    decoded(await send(put), d.decodeFavoriteToggle);
    const created = decoded(await send({ method: "POST", path: playlists, idempotencyKey: key(1), body: { name: "浏览器歌单", track_ids: [track.track_id] } }), d.decodePlaylistCreated, 201);
    mock.session = "curator";
    expect(decoded(await send({ method: "GET", path: `${favorites}/tracks` }), d.decodeFavoriteTracksPage).items).toEqual([]);
    rejected(await send({ method: "GET", path: `${playlists}/${created.playlist.id}` }), 404, "not_found");
    mock.session = "listener";
    expect(decoded(await send({ method: "GET", path: `${playlists}/${created.playlist.id}/items` }), d.decodePlaylistItemsPage).items[0]?.track.track_id).toBe(track.track_id);
    rejected(await send({ method: "GET", path: "/api/v1/me/unregistered" }), 404, "not_found");
    mock.expireSession();
    rejected(await send(put), 401, "unauthorized");
  });
});

describe("共享收藏状态", () => {
  it("曲目和专辑的 PUT/DELETE 幂等，未知稳定身份 PUT 404、DELETE 成功", () => {
    const { catalog, tracks, call } = setup();
    const track = tracks[0];
    const group = catalog.groups[0];
    if (track === undefined || group === undefined) throw new Error("没有种子");
    for (const [kind, id] of [["tracks", track.track_id], ["albums", group.id]]) {
      const path = `${favorites}/${kind}/${id}`;
      const put = decoded(call("PUT", path), d.decodeFavoriteToggle);
      expect(put.favorited).toBe(true);
      expect(decoded(call("PUT", path), d.decodeFavoriteToggle)).toEqual(put);
      const removed = decoded(call("DELETE", path), d.decodeFavoriteToggle);
      expect(removed).toMatchObject({ favorited: false, created_at: null, revision: put.revision + 1 });
      expect(decoded(call("DELETE", path), d.decodeFavoriteToggle)).toEqual(removed);
      rejected(call("PUT", `${favorites}/${kind}/${unknownId}`), 404, "not_found");
      expect(decoded(call("DELETE", `${favorites}/${kind}/${unknownId}`), d.decodeFavoriteToggle).favorited).toBe(false);
    }
  });

  it("lookup 按首次出现去重、规范化大小写，既有状态仅归本人", () => {
    const { data, tracks, catalog, call } = setup();
    const track = tracks[0];
    const album = catalog.groups[0];
    if (track === undefined || album === undefined) throw new Error("没有种子");
    seedFavoriteTracks(data, userId, [track]);
    seedFavoriteAlbums(data, userId, [album]);
    const body = { track_ids: [unknownId, track.track_id.toUpperCase(), track.track_id], group_ids: [album.id, album.id] };
    const lookup = decoded(call("POST", `${favorites}/lookup`, body), d.decodeFavoriteLookup);
    expect(lookup.tracks).toEqual([{ track_id: unknownId, favorited: false }, { track_id: track.track_id, favorited: true }]);
    expect(lookup.albums).toEqual([{ group_id: album.id, favorited: true }]);
    expect(decoded(call("POST", `${favorites}/lookup`, body, { user: PERSONAL_ADMIN_ID }), d.decodeFavoriteLookup).tracks.every((track) => !track.favorited)).toBe(true);
  });

  it("时间倒序、并列 ID 升序，分页 total、越界空页与旧版本冲突", () => {
    const { data, tracks, call } = setup(4);
    seedFavoriteTracks(data, userId, [...tracks].reverse(), "2026-11-04T00:00:00Z");
    const first = decoded(call("GET", `${favorites}/tracks?page_size=2`), d.decodeFavoriteTracksPage);
    expect(first.items.map((item) => item.track.track_id)).toEqual(tracks.slice(0, 2).map((track) => track.track_id));
    const last = decoded(call("GET", `${favorites}/tracks?page=2&page_size=2&revision=${first.revision}`), d.decodeFavoriteTracksPage);
    expect(last.items.map((item) => item.track.track_id)).toEqual(tracks.slice(2).map((track) => track.track_id));
    expect(decoded(call("GET", `${favorites}/tracks?page=9&page_size=2`), d.decodeFavoriteTracksPage)).toMatchObject({ items: [], pagination: { total: 4 } });
    call("DELETE", `${favorites}/tracks/${tracks[0]?.track_id}`);
    rejected(call("GET", `${favorites}/tracks?revision=${first.revision}`), 409, "favorites_changed");
    call("PUT", `${favorites}/tracks/${tracks[0]?.track_id}`);
    expect(decoded(call("GET", `${favorites}/tracks`), d.decodeFavoriteTracksPage).items[0]?.track.track_id).toBe(tracks[0]?.track_id);
  });

  it("missing 和不可用专辑仍可再次收藏，列表不伪造可见发行", () => {
    const { data, tracks, catalog, call } = setup();
    const original = tracks[0];
    const group = catalog.groups[0];
    if (original === undefined || group === undefined) throw new Error("没有种子");
    seedPersonalTracks(data, [{ ...original, present: false }]);
    seedPersonalAlbums(data, [{ ...personalAlbumOf(group), available: false, release_count: 0, representative: null }]);
    for (const path of [`${favorites}/tracks/${original.track_id}`, `${favorites}/albums/${group.id}`]) {
      decoded(call("PUT", path), d.decodeFavoriteToggle);
      decoded(call("DELETE", path), d.decodeFavoriteToggle);
      decoded(call("PUT", path), d.decodeFavoriteToggle);
    }
    expect(decoded(call("GET", `${favorites}/tracks`), d.decodeFavoriteTracksPage).items[0]?.track.present).toBe(false);
    expect(decoded(call("GET", `${favorites}/albums`), d.decodeFavoriteAlbumsPage).items[0]?.album).toMatchObject({ available: false, release_count: 0, representative: null });
  });

  it("catalog 身份与页面一致，归组/隐藏发行后的读取不依赖旧摘要缓存", () => {
    const { data, tracks, catalog, call } = setup();
    const group = catalog.groups[0];
    const first = tracks[0];
    if (group === undefined || first === undefined) throw new Error("没有种子");
    seedFavoriteTracks(data, userId, [first.track_id]);
    seedFavoriteAlbums(data, userId, [group.id]);
    const newGroup = albumGroup(2);
    const moved = releaseDetailOf(group.representative);
    moved.title = "扫描后的标题";
    catalog.releases.set(moved.id, moved);
    catalog.groupDetails.set(newGroup.id, groupDetailOf(newGroup, [moved]));
    catalog.groupDetails.set(group.id, { ...groupDetailOf(group), releases: [] });
    const read = decoded(call("GET", `${favorites}/tracks`), d.decodeFavoriteTracksPage).items[0]?.track;
    expect(read).toMatchObject({ track_id: first.track_id, group_id: newGroup.id, release: { id: moved.id, title: "扫描后的标题" } });
    expect(decoded(call("GET", `${favorites}/albums`), d.decodeFavoriteAlbumsPage).items[0]?.album.available).toBe(false);
    catalog.hiddenReleases.add(moved.id);
    expect(decoded(call("GET", `${favorites}/tracks`), d.decodeFavoriteTracksPage).items[0]?.track.present).toBe(false);
  });

  it("只有 catalog 合成详情的曲目隐藏后仍保留稳定身份和缺失状态", () => {
    const { data, catalog, tracks, call } = setup();
    const track = tracks[0];
    if (track === undefined) throw new Error("没有曲目种子");
    seedFavoriteTracks(data, userId, [track.track_id]);
    catalog.hiddenReleases.add(track.release.id);
    expect(decoded(call("GET", `${favorites}/tracks`), d.decodeFavoriteTracksPage).items[0]?.track).toMatchObject({ track_id: track.track_id, present: false });
    expect(decoded(call("GET", `${favorites}/tracks/queue`), d.decodeFavoriteQueue)).toMatchObject({ total: 1, missing_count: 1, returned_count: 0 });
  });

  it("两类播放全部先过滤 missing，再取 10,000，并精确返回省略数", () => {
    const { data, call } = setup(0);
    const tracks = Array.from({ length: 10_006 }, (_, index) => personalTrack(index + 1, { present: index !== 0 && index !== 10_004 }));
    seedFavoriteTracks(data, userId, tracks);
    const playlist = seedPlaylist(data, userId, { name: "超限撤销种子", trackIds: tracks.map((track) => track.track_id) });
    const favoriteQueue = decoded(call("GET", `${favorites}/tracks/queue`), d.decodeFavoriteQueue);
    const playlistQueue = decoded(call("GET", `${playlists}/${playlist.state.id}/queue`), d.decodePlaylistQueue);
    for (const queue of [favoriteQueue, playlistQueue]) {
      expect(queue).toMatchObject({ total: 10_006, missing_count: 2, eligible_count: 10_004, returned_count: 10_000, omitted_count: 4, limit: 10_000, truncated: true });
      expect(queue.items[0]?.track_id).toBe(tracks[1]?.track_id);
      expect(queue.items.at(-1)?.track_id).toBe(tracks[10_000]?.track_id);
    }
  });
});

describe("共享歌单状态与幂等命令", () => {
  it("新建并加入为一次原子请求，重复计数、重放与规范化参数稳定", () => {
    const { call, tracks } = setup();
    const ids = tracks.map((track) => track.track_id);
    const body = { name: "  我的歌单  ", track_ids: [ids[0], ids[1], ids[1]] };
    const first = decoded(call("POST", playlists, body, { key: key(50) }), d.decodePlaylistCreated, 201);
    expect(first).toMatchObject({ added_count: 2, existing_count: 0, duplicate_count: 1, playlist: { name: "我的歌单", revision: 1, track_count: 2 } });
    const same = call("POST", playlists, { track_ids: body.track_ids.map((id) => id?.toUpperCase()), name: "我的歌单" }, { key: key(50) });
    expect(same.body).toEqual(first);
    expect(decoded(call("GET", playlists), d.decodePlaylistsPage).pagination.total).toBe(1);
    rejected(call("POST", playlists, { ...body, track_ids: [ids[1], ids[0], ids[1]] }, { key: key(50) }), 409, "idempotency_conflict");
    rejected(call("POST", playlists, { name: "无部分成功", track_ids: [ids[0], unknownId] }), 404, "not_found");
    expect(decoded(call("GET", playlists), d.decodePlaylistsPage).pagination.total).toBe(1);
  });

  it("删除后仍可重放原创建/删除回执，不复活歌单", () => {
    const { call } = setup();
    const body = { name: "可删除" };
    const created = decoded(call("POST", playlists, body, { key: key(30) }), d.decodePlaylistCreated, 201);
    const path = `${playlists}/${created.playlist.id}`;
    const deleted = decoded(call("DELETE", path, { expected_revision: 1 }, { key: key(31) }), d.decodePlaylistDeleted);
    expect(call("POST", playlists, body, { key: key(30) }).body).toEqual(created);
    expect(call("DELETE", path, { expected_revision: 1 }, { key: key(31) }).body).toEqual(deleted);
    rejected(call("GET", path), 404, "not_found");
    expect(decoded(call("GET", playlists), d.decodePlaylistsPage).pagination.total).toBe(0);
  });

  it("改名与重复加入 no-op 不增版本，但过期 no-op 仍冲突", () => {
    const { call, playlist, tracks } = setup();
    const p = playlist();
    const path = `${playlists}/${p.state.id}`;
    const renamed = decoded(call("PATCH", path, { expected_revision: 1, name: "改名" }), d.decodePlaylistRenamed);
    expect(renamed).toMatchObject({ changed: true, playlist: { revision: 2 } });
    expect(decoded(call("PATCH", path, { expected_revision: 2, name: "改名" }), d.decodePlaylistRenamed)).toMatchObject({ changed: false, playlist: { revision: 2 } });
    rejected(call("PATCH", path, { expected_revision: 1, name: "改名" }), 409, "revision_conflict");
    const added = decoded(call("POST", `${path}/items`, { expected_revision: 2, track_ids: [tracks[0]?.track_id, tracks[0]?.track_id] }), d.decodeItemsAdded);
    expect(added).toMatchObject({ added_count: 0, existing_count: 1, duplicate_count: 1, playlist: { revision: 2 } });
    rejected(call("POST", `${path}/items`, { expected_revision: 1, track_ids: [tracks[0]?.track_id] }), 409, "revision_conflict");
    rejected(call("GET", `${path}/items?revision=1`), 409, "revision_conflict");
    expect(renamed.playlist.name).toBe("改名");
  });

  it("批量加入分别计算 added/existing/duplicate，失败不能留下部分条目", () => {
    const { call, playlist, tracks } = setup();
    const p = playlist([tracks[0]?.track_id ?? ""]);
    const path = `${playlists}/${p.state.id}/items`;
    const input = [tracks[0]?.track_id, tracks[1]?.track_id, tracks[1]?.track_id];
    const added = decoded(call("POST", path, { expected_revision: 1, track_ids: input }), d.decodeItemsAdded);
    expect(added).toMatchObject({ added_count: 1, existing_count: 1, duplicate_count: 1, playlist: { track_count: 2, revision: 2 } });
    rejected(call("POST", path, { expected_revision: 2, track_ids: [tracks[2]?.track_id, unknownId] }), 404, "not_found");
    expect(p.items.map((item) => item.track_id)).toEqual(input.slice(0, 2));
    expect(p.state.revision).toBe(2);
  });

  it("列表按修改时间倒序、20 项分页、membership 随增删变化", () => {
    const { data, tracks, call } = setup();
    const seeded = Array.from({ length: 21 }, (_, index) => seedPlaylist(data, userId, { name: `歌单 ${index}`, trackIds: index === 0 ? [tracks[0]?.track_id ?? ""] : [] }));
    const first = decoded(call("GET", playlists), d.decodePlaylistsPage);
    expect(first).toMatchObject({ pagination: { total: 21, page_size: 20 } });
    expect(first.items.map((item) => item.id)).toEqual([...seeded].reverse().slice(0, 20).map((item) => item.state.id));
    expect(decoded(call("GET", `${playlists}?page=2`), d.decodePlaylistsPage).items[0]?.id).toBe(seeded[0]?.state.id);
    expect(decoded(call("GET", `${playlists}/containing-track/${tracks[0]?.track_id}`), d.decodeContainingTrack).items).toEqual([{ playlist_id: seeded[0]?.state.id, revision: 1 }]);
    call("PATCH", `${playlists}/${seeded[0]?.state.id}`, { expected_revision: 1, name: "最近修改" });
    expect(decoded(call("GET", playlists), d.decodePlaylistsPage).items[0]?.name).toBe("最近修改");
    call("DELETE", `${playlists}/${seeded[0]?.state.id}`, { expected_revision: 2 });
    expect(decoded(call("GET", `${playlists}/containing-track/${tracks[0]?.track_id}`), d.decodeContainingTrack).items).toEqual([]);
  });

  it("摘要完整统计未知时长/missing，封面为前四不同组且保留无封面组", () => {
    const { data, call } = setup(0);
    const groups = albumGroups(5, (index) => ({ artwork: index !== 1 }));
    const tracks = groups.map((group, index) => personalTrack(index + 1, { group_id: group.id, release: group.representative, duration_seconds: index === 1 ? null : 120, present: index !== 2 }));
    const firstGroup = groups[0];
    if (firstGroup === undefined) throw new Error("没有专辑种子");
    const extra = personalTrack(6, { group_id: firstGroup.id, release: firstGroup.representative, duration_seconds: 0 });
    seedPersonalTracks(data, [...tracks, extra]);
    const p = seedPlaylist(data, userId, { name: "统计", trackIds: [tracks[0]?.track_id ?? "", extra.track_id, ...tracks.slice(1).map((track) => track.track_id)] });
    const summary = decoded(call("GET", `${playlists}/${p.state.id}`), d.decodePlaylistDetail).playlist;
    expect(summary).toMatchObject({ duration_seconds: 480, unknown_duration_count: 2, missing_count: 1, track_count: 6 });
    expect(summary.cover_groups.map((cover) => cover.group_id)).toEqual(groups.slice(0, 4).map((group) => group.id));
    expect(summary.cover_groups[0]?.artwork).toBeNull();
    expect(decoded(call("GET", `${playlists}/${p.state.id}/queue`), d.decodePlaylistQueue)).toMatchObject({ total: 6, missing_count: 1, returned_count: 5, omitted_count: 0 });
  });

  it("用户/管理员不可读写对方歌单、条目、撤销和收据，同键跨用户独立", () => {
    const { call, tracks } = setup();
    const body = { name: "本人", track_ids: [tracks[0]?.track_id] };
    const created = decoded(call("POST", playlists, body, { key: key(70) }), d.decodePlaylistCreated, 201);
    const path = `${playlists}/${created.playlist.id}`;
    const item = decoded(call("GET", `${path}/items`), d.decodePlaylistItemsPage).items[0];
    const removed = decoded(call("DELETE", `${path}/items/${item?.id}`, { expected_revision: 1 }), d.decodeItemRemoved);
    for (const stranger of [otherId, PERSONAL_ADMIN_ID]) {
      for (const suffix of ["", "/items", "/queue"]) rejected(call("GET", path + suffix, undefined, { user: stranger }), 404, "not_found");
      rejected(call("PATCH", path, { expected_revision: 2, name: "越权" }, { user: stranger }), 404, "not_found");
      rejected(call("DELETE", `${path}/items/${item?.id}`, { expected_revision: 2 }, { user: stranger }), 404, "not_found");
      rejected(call("POST", `${path}/restore`, { token: removed.undo.token }, { user: stranger }), 404, "not_found");
      const own = decoded(call("POST", playlists, body, { key: key(70), user: stranger }), d.decodePlaylistCreated, 201);
      expect(own.playlist.id).not.toBe(created.playlist.id);
    }
  });

  it("每人 1,000 个歌单，失败不消耗幂等键，删除后可按原请求创建", () => {
    const { data, call } = setup(0);
    for (let i = 0; i < 1000; i += 1) seedPlaylist(data, userId, { name: `配额 ${i}` });
    rejected(call("POST", playlists, { name: "第 1001 个" }, { key: key(80) }), 409, "playlist_limit_reached");
    const first = personalUserOf(data, userId).playlists.values().next().value;
    if (first === undefined) throw new Error("没有歌单种子");
    call("DELETE", `${playlists}/${first.state.id}`, { expected_revision: 1 });
    decoded(call("POST", playlists, { name: "第 1001 个" }, { key: key(80) }), d.decodePlaylistCreated, 201);
    expect(personalUserOf(data, userId).playlists.size).toBe(1000);
  });
});

describe("移动与撤销", () => {
  it("四种菜单移动与 before 全局排序、端点 no-op、旧版本冲突", () => {
    const { call, playlist } = setup(5);
    const p = playlist();
    const original = p.items.map((item) => item.id);
    const moving = original[2];
    const path = `${playlists}/${p.state.id}`;
    const move = (action: string, beforeId?: string | null) => call("POST", `${path}/items/${moving}/move`, { expected_revision: p.state.revision, action, ...(beforeId === undefined ? {} : { before_item_id: beforeId }) });
    expect(decoded(move("up"), d.decodeItemMoved).moved.position).toBe(2);
    expect(decoded(move("down"), d.decodeItemMoved).moved.position).toBe(3);
    expect(decoded(move("first"), d.decodeItemMoved).moved.position).toBe(1);
    expect(decoded(move("first"), d.decodeItemMoved).changed).toBe(false);
    expect(decoded(move("last"), d.decodeItemMoved).moved.position).toBe(5);
    expect(decoded(move("down"), d.decodeItemMoved).changed).toBe(false);
    expect(decoded(move("before", original[1]), d.decodeItemMoved).moved.position).toBe(2);
    expect(decoded(move("before", moving), d.decodeItemMoved).changed).toBe(false);
    expect(decoded(move("before", null), d.decodeItemMoved).moved.position).toBe(5);
    rejected(call("POST", `${path}/items/${moving}/move`, { expected_revision: 1, action: "last" }), 409, "revision_conflict");
    rejected(move("before", unknownId), 404, "not_found");
    expect(p.items.map((item) => item.position)).toEqual([1, 2, 3, 4, 5]);
    expect(new Set(p.items.map((item) => item.id)).size).toBe(5);
  });

  it("分页给下一页锚点，拖到当前页末不是移到整个歌单末", () => {
    const { call, playlist } = setup(6);
    const p = playlist();
    const path = `${playlists}/${p.state.id}`;
    const page = decoded(call("GET", `${path}/items?page_size=2`), d.decodePlaylistItemsPage);
    expect(page.page_after_item_id).toBe(p.items[2]?.id);
    const moved = decoded(call("POST", `${path}/items/${p.items[0]?.id}/move`, { expected_revision: 1, action: "before", before_item_id: page.page_after_item_id }), d.decodeItemMoved);
    expect(moved.moved.position).toBe(2);
    const last = decoded(call("GET", `${path}/items?page=3&page_size=2`), d.decodePlaylistItemsPage);
    expect(last.items.map((item) => item.position)).toEqual([5, 6]);
    expect(last.page_after_item_id).toBeNull();
    expect(decoded(call("GET", `${path}/items?page=4&page_size=2`), d.decodePlaylistItemsPage)).toMatchObject({ items: [], pagination: { total: 6 } });
  });

  it("移除后原位恢复原身份/时间，重复 token 用新键仍回放首次结果", () => {
    const { call, playlist } = setup();
    const p = playlist();
    const original = structuredClone(p.items);
    const path = `${playlists}/${p.state.id}`;
    const removed = decoded(call("DELETE", `${path}/items/${p.items[1]?.id}`, { expected_revision: 1 }), d.decodeItemRemoved);
    expect(removed.removed).toEqual(original[1]);
    const restored = decoded(call("POST", `${path}/restore`, { token: removed.undo.token }), d.decodeItemRestored);
    expect(restored).toMatchObject({ outcome: "restored", placement: "original", item: original[1] });
    expect(p.items).toEqual(original);
    call("PATCH", path, { expected_revision: p.state.revision, name: "恢复后改名" });
    expect(decoded(call("POST", `${path}/restore`, { token: removed.undo.token }), d.decodeItemRestored)).toEqual(restored);
    expect(p.items).toHaveLength(3);
  });

  it("并发移动前驱后撤销放在前驱当前后方，超过当前页返回全局位置", () => {
    const { call, playlist } = setup(55);
    const p = playlist();
    const path = `${playlists}/${p.state.id}`;
    const predecessor = p.items[0]?.id;
    const removed = decoded(call("DELETE", `${path}/items/${p.items[1]?.id}`, { expected_revision: 1 }), d.decodeItemRemoved);
    call("POST", `${path}/items/${predecessor}/move`, { expected_revision: 2, action: "last" });
    const restored = decoded(call("POST", `${path}/restore`, { token: removed.undo.token }), d.decodeItemRestored);
    expect(restored).toMatchObject({ placement: "after_previous", item: { position: 55 } });
    expect(p.items.at(-2)?.id).toBe(predecessor);
  });

  it.each([false, true])("前驱消失按原序号恢复或退到末尾（末尾=%s）", (atEnd) => {
    const { call, playlist } = setup(4);
    const p = playlist();
    const path = `${playlists}/${p.state.id}`;
    const original = [...p.items];
    const removed = decoded(call("DELETE", `${path}/items/${original[2]?.id}`, { expected_revision: 1 }), d.decodeItemRemoved);
    call("DELETE", `${path}/items/${original[1]?.id}`, { expected_revision: p.state.revision });
    if (atEnd) call("DELETE", `${path}/items/${original[3]?.id}`, { expected_revision: p.state.revision });
    const restored = decoded(call("POST", `${path}/restore`, { token: removed.undo.token }), d.decodeItemRestored);
    expect(restored).toMatchObject({ placement: atEnd ? "end" : "original_index", item: { position: atEnd ? 2 : 3 } });
  });

  it("已经重新加入时不重复或移动；前驱重新加入换条目 ID 仍按曲目找到它", () => {
    const { call, playlist } = setup();
    const p = playlist();
    const path = `${playlists}/${p.state.id}`;
    const original = [...p.items];
    const removed = decoded(call("DELETE", `${path}/items/${original[1]?.id}`, { expected_revision: 1 }), d.decodeItemRemoved);
    call("DELETE", `${path}/items/${original[0]?.id}`, { expected_revision: p.state.revision });
    call("POST", `${path}/items`, { expected_revision: p.state.revision, track_ids: [original[0]?.track_id] });
    const restored = decoded(call("POST", `${path}/restore`, { token: removed.undo.token }), d.decodeItemRestored);
    expect(restored).toMatchObject({ placement: "after_previous", item: { position: 3 } });
    const removedAgain = decoded(call("DELETE", `${path}/items/${original[1]?.id}`, { expected_revision: p.state.revision }), d.decodeItemRemoved);
    call("POST", `${path}/items`, { expected_revision: p.state.revision, track_ids: [original[1]?.track_id] });
    const newItem = p.items.at(-1);
    const revision = p.state.revision;
    const present = decoded(call("POST", `${path}/restore`, { token: removedAgain.undo.token }), d.decodeItemRestored);
    expect(present).toMatchObject({ outcome: "already_present", placement: "unchanged", item: newItem, playlist: { revision } });
  });

  it("满额移除、添加后仍能撤销到 10,001；超限期间普通新增整批拒绝", () => {
    const { data, call } = setup(0);
    const tracks = Array.from({ length: 10_002 }, (_, index) => personalTrack(index + 1));
    seedPersonalTracks(data, tracks);
    const p = seedPlaylist(data, userId, { name: "容量", trackIds: tracks.slice(0, 10_000).map((track) => track.track_id) });
    const path = `${playlists}/${p.state.id}`;
    const removed = decoded(call("DELETE", `${path}/items/${p.items[0]?.id}`, { expected_revision: 1 }), d.decodeItemRemoved);
    decoded(call("POST", `${path}/items`, { expected_revision: 2, track_ids: [tracks[10_000]?.track_id] }), d.decodeItemsAdded);
    const restored = decoded(call("POST", `${path}/restore`, { token: removed.undo.token }), d.decodeItemRestored);
    expect(restored.playlist).toMatchObject({ track_count: 10_001, over_limit_count: 1 });
    rejected(call("POST", `${path}/items`, { expected_revision: p.state.revision, track_ids: [tracks[10_001]?.track_id] }), 409, "playlist_track_limit_reached");
    expect(decoded(call("POST", `${path}/items`, { expected_revision: p.state.revision, track_ids: [tracks[0]?.track_id] }), d.decodeItemsAdded).added_count).toBe(0);
    expect(decoded(call("GET", path), d.decodePlaylistDetail).playlist.track_count).toBe(10_001);
    expect(decoded(call("GET", `${path}/queue`), d.decodePlaylistQueue).omitted_count).toBe(1);
  });
});

describe("必要的协议输入校验", () => {
  it.each([
    ["GET", `${favorites}/tracks?page=0`, undefined, "invalid_pagination"],
    ["GET", `${favorites}/tracks?page_size=101`, undefined, "invalid_pagination"],
    ["GET", `${playlists}?page_size=21`, undefined, "invalid_pagination"],
    ["GET", `${favorites}/tracks?page=9007199254740991&page_size=100`, undefined, "invalid_pagination"],
    ["GET", `${favorites}/tracks?revision=-1`, undefined, "invalid_input"],
    ["GET", `${favorites}/tracks?revision=9007199254740992`, undefined, "invalid_input"],
    ["GET", `${favorites}/tracks?page=1&page=2`, undefined, "invalid_input"],
    ["GET", `${favorites}/tracks?user_id=${otherId}`, undefined, "invalid_input"],
    ["GET", `${playlists}?revision=0`, undefined, "invalid_input"],
    ["POST", `${favorites}/lookup`, { track_ids: null }, "invalid_input"],
    ["POST", `${favorites}/lookup`, { group_ids: ["bad"] }, "invalid_input"],
    ["POST", `${favorites}/lookup`, { user_id: otherId }, "invalid_input"],
    ["POST", `${favorites}/lookup`, { track_ids: Array.from({ length: 10_001 }, () => unknownId) }, "invalid_input"],
    ["POST", `${favorites}/lookup`, { group_ids: Array.from({ length: 1_001 }, () => unknownId) }, "invalid_input"],
    ["PUT", `${favorites}/tracks/${unknownId}`, { favorited: true }, "invalid_input"],
    ["POST", playlists, { name: "空数组以外的 null", track_ids: null }, "invalid_input"],
    ["POST", playlists, { name: "无效数组", track_ids: Array.from({ length: 10_001 }, () => unknownId) }, "invalid_input"],
  ])("%s %s 拒绝不符合合同的输入", (method, path, body, code) => {
    const { call } = setup();
    rejected(call(method as string, path as string, body), 400, code as string);
  });

  it.each(["", "   ", "\n", "含\u0001控制字符", "含\u0085控制字符", "字".repeat(101), "🎵".repeat(101)])("拒绝无效名字（%s）", (name) => {
    const { call } = setup();
    rejected(call("POST", playlists, { name }), 400, "invalid_input");
  });

  it("名字按 100 个 Unicode 码点而不是 UTF-16 长度，允许重名", () => {
    const { call } = setup();
    const name = "🎵".repeat(100);
    const first = decoded(call("POST", playlists, { name: ` \n${name}\t ` }, { key: ` ${key(89)} ` }), d.decodePlaylistCreated, 201);
    const second = decoded(call("POST", playlists, { name }), d.decodePlaylistCreated, 201);
    expect(first.playlist.name).toBe(name);
    expect(first.playlist.id).not.toBe(second.playlist.id);
  });

  it("歌单写命令要求 UUID 幂等键、精确版本及合法锚点形状", () => {
    const { call, playlist } = setup();
    rejected(call("POST", playlists, { name: "缺键" }, { key: null }), 400, "invalid_input");
    rejected(call("POST", playlists, { name: "坏键" }, { key: "bad" }), 400, "invalid_input");
    const p = playlist();
    const path = `${playlists}/${p.state.id}`;
    for (const revision of [undefined, null, 0, -1, 1.5, Number.MAX_SAFE_INTEGER + 1, "1"]) rejected(call("PATCH", path, { name: "无效版本", expected_revision: revision }), 400, "invalid_input");
    for (const body of [
      { expected_revision: 1, action: "before" },
      { expected_revision: 1, action: "before", before_item_id: "bad" },
      { expected_revision: 1, action: "up", before_item_id: null },
      { expected_revision: 1, action: "sideways" },
    ]) rejected(call("POST", `${path}/items/${p.items[0]?.id}/move`, body), 400, "invalid_input");
    rejected(call("POST", `${path}/restore`, { token: unknownId, expected_revision: 1 }), 400, "invalid_input");
    rejected(call("POST", `${path}/items`, { expected_revision: 1, track_ids: [] }), 400, "invalid_input");
    expect(p.state.revision).toBe(1);
  });
});
