import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { FavoriteAlbumDTO, FavoriteTrackDTO, FavoriteTracksPageDTO } from "../features/personal/api/decoders";
import { personalKeys } from "../features/personal/query-keys";
import { decode as d, type ApiResponse } from "../shared/api";
import { albumGroup } from "../test/catalog-data";
import { deferred } from "../test/deferred";
import { installFakeApi, installFakeSession } from "../test/fake-api";
import { FakeAudio } from "../test/fake-audio";
import { historyItem } from "../test/history-data";
import { jsonResponse, type RecordedRequest } from "../test/http";
import { renderApp } from "../test/render-app";

const favorites = "/api/v1/me/favorites";
const tracksPath = `${favorites}/tracks`;
const albumsPath = `${favorites}/albums`;
const createdAt = "2026-11-04T08:30:00Z";
const decodeLookupInput = d.defineDecoder(d.object({ track_ids: d.array(d.uuid(), { max: 10_000 }), group_ids: d.array(d.uuid(), { max: 1_000 }) }));
const track = (index: number, present = true): FavoriteTrackDTO => ({ track: historyItem(index, { title: `收藏曲目 ${index}`, present }), created_at: createdAt });
function album(index: number, available = true): FavoriteAlbumDTO {
  const group = albumGroup(index, { title: `收藏专辑 ${index}`, artwork: false });
  return { album: { group_id: group.id, available, title: group.representative.title, artist: group.representative.artist, release_count: available ? 1 : 0, representative: available ? group.representative : null }, created_at: createdAt };
}
const first = track(1);
const second = track(2);
const row = (title: string) => screen.getByRole("row", { name: new RegExp(title) });
const heart = (title: string) => screen.getByRole("button", { name: `收藏《${title}》` });

beforeEach(() => {
  sessionStorage.clear();
  vi.stubGlobal("Audio", class { constructor() { return new FakeAudio(); } });
  vi.spyOn(HTMLMediaElement.prototype, "canPlayType").mockReturnValue("probably");
});
afterEach(() => vi.restoreAllMocks());

/** 只服务本文件的有状态替身；未登记路由仍由 installFakeApi 返回 404。 */
function setup(initialTracks: FavoriteTrackDTO[] = [first, second], initialAlbums: FavoriteAlbumDTO[] = []) {
  const api = installFakeApi();
  installFakeSession(api, { current: "listener" });
  const state = { tracks: [...initialTracks], albums: [...initialAlbums], revision: 7 };
  const list = (kind: "tracks" | "albums", request: RecordedRequest): Response => {
    const params = new URL(request.path, "http://test").searchParams;
    const page = Number(params.get("page") ?? 1);
    const size = Number(params.get("page_size") ?? 48);
    if (params.has("revision") && Number(params.get("revision")) !== state.revision) {
      return jsonResponse(409, { error: { code: "favorites_changed", message: "收藏已改变" }, request_id: "favorites-conflict" });
    }
    return jsonResponse(200, { items: state[kind].slice((page - 1) * size, page * size), pagination: { page, page_size: size, total: state[kind].length }, revision: state.revision });
  };
  api.on("GET", tracksPath, (request) => list("tracks", request));
  api.on("GET", albumsPath, (request) => list("albums", request));
  api.on("POST", `${favorites}/lookup`, (request) => {
    const input = decodeLookupInput(request.body);
    return jsonResponse(200, {
      revision: state.revision,
      artists: [],
      tracks: input.track_ids.map((track_id) => ({ track_id, favorited: state.tracks.some((item) => item.track.track_id === track_id) })),
      albums: input.group_ids.map((group_id) => ({ group_id, favorited: state.albums.some((item) => item.album.group_id === group_id) })),
    });
  });
  for (const method of ["PUT", "DELETE"]) {
    api.on(method, /^\/api\/v1\/me\/favorites\/(tracks|albums)\/[^/]+$/, (request) => {
      const id = request.path.split("/").at(-1);
      const isTrack = request.path.includes("/tracks/");
      const favorited = method === "PUT";
      if (isTrack) {
        const item = initialTracks.find((candidate) => candidate.track.track_id === id);
        if (item === undefined) return jsonResponse(404, { error: { code: "not_found", message: "曲目不存在" } });
        state.tracks = state.tracks.filter((candidate) => candidate.track.track_id !== id);
        if (favorited) state.tracks.unshift(item);
      } else {
        const item = initialAlbums.find((candidate) => candidate.album.group_id === id);
        if (item === undefined) return jsonResponse(404, { error: { code: "not_found", message: "专辑不存在" } });
        state.albums = state.albums.filter((candidate) => candidate.album.group_id !== id);
        if (favorited) state.albums.unshift(item);
      }
      state.revision += 1;
      return jsonResponse(200, { target_kind: isTrack ? "track" : "album", target_id: id, favorited, created_at: favorited ? createdAt : null, revision: state.revision });
    });
  }
  return { api, state, list };
}

async function cancel(title: string) {
  await waitFor(() => expect(heart(title).getAttribute("aria-pressed")).toBe("true"));
  await userEvent.click(heart(title));
  await waitFor(() => {
    expect(heart(title).getAttribute("aria-pressed")).toBe("false");
    expect(heart(title).hasAttribute("disabled")).toBe(false);
  });
}

function visibleTitles() {
  return within(screen.getByRole("table", { name: "收藏的曲目" })).getAllByRole("rowheader").map((cell) => cell.textContent);
}

describe("收藏页的保留行与服务端列表", () => {
  it("取消后后台重读与窗口聚焦都保留原槽，缓存仍只含真实收藏，并可点回", async () => {
    const { api } = setup();
    const { runtime } = renderApp({ path: "/me/favorites?tab=tracks" });
    await screen.findByRole("table", { name: "收藏的曲目" });
    const originalOrder = visibleTitles();
    await cancel(first.track.title);
    await screen.findByText("共 1 首曲目");
    const generation = runtime.session.currentGeneration();
    await act(async () => { await runtime.queryClient.invalidateQueries({ queryKey: personalKeys.favorites(generation) }); });
    expect(visibleTitles()).toEqual(originalOrder);
    const cached = runtime.queryClient.getQueryData<ApiResponse<FavoriteTracksPageDTO>>(personalKeys.favoriteList(generation, "track", { page: 1, pageSize: 48, revision: null }));
    expect(cached?.data.items.map((item) => item.track.track_id)).toEqual([second.track.track_id]);
    const reads = api.requestsTo("GET", tracksPath).length;
    await act(async () => { window.dispatchEvent(new Event("focus")); });
    await waitFor(() => expect(api.requestsTo("GET", tracksPath).length).toBeGreaterThan(reads));
    await waitFor(() => expect(runtime.queryClient.isFetching({ queryKey: personalKeys.favorites(generation) })).toBe(0));
    expect(visibleTitles()).toEqual(originalOrder);
    expect(heart(first.track.title).getAttribute("aria-pressed")).toBe("false");
    await userEvent.click(heart(first.track.title));
    await screen.findByText("共 2 首曲目");
    expect(visibleTitles()).toEqual(originalOrder);
    expect(heart(first.track.title).getAttribute("aria-pressed")).toBe("true");
  });

  it("专辑取消后聚焦重读仍保留无链接的不可用卡片", async () => {
    const unavailable = album(1, false);
    const { api } = setup([], [unavailable]);
    const { runtime } = renderApp({ path: "/me/favorites" });
    await screen.findByText("暂时不可用");
    await cancel(unavailable.album.title);
    await screen.findByText("共 0 张专辑");
    const reads = api.requestsTo("GET", albumsPath).length;
    await act(async () => { window.dispatchEvent(new Event("focus")); });
    await waitFor(() => expect(api.requestsTo("GET", albumsPath).length).toBeGreaterThan(reads));
    await waitFor(() => expect(runtime.queryClient.isFetching()).toBe(0));
    const card = heart(unavailable.album.title).closest("li");
    if (card === null) throw new Error("缺少专辑卡片");
    expect(card.getAttribute("data-available")).toBe("false");
    expect(within(card).queryByRole("link")).toBeNull();
    expect(heart(unavailable.album.title).getAttribute("aria-pressed")).toBe("false");
  });

  it("只有取消、没有新收藏时不显示刷新，重新进入后清掉保留行", async () => {
    setup();
    const { runtime, view } = renderApp({ path: "/me/favorites?tab=tracks" });
    await screen.findByRole("table", { name: "收藏的曲目" });
    await cancel(first.track.title);
    await screen.findByText("共 1 首曲目");
    await waitFor(() => expect(runtime.queryClient.isFetching()).toBe(0));
    expect(row(first.track.title)).toBeTruthy();
    expect(heart(first.track.title).getAttribute("aria-pressed")).toBe("false");
    expect(screen.queryByRole("button", { name: "刷新" })).toBeNull();
    expect(screen.queryByText("列表有更新。", { exact: false })).toBeNull();
    view.unmount();
    runtime.dispose();
    renderApp({ path: "/me/favorites?tab=tracks" });
    await screen.findByRole("table", { name: "收藏的曲目" });
    expect(screen.queryByText(first.track.title, { exact: true })).toBeNull();
    expect(row(second.track.title)).toBeTruthy();
  });

  it("服务端新增时显示列表有更新，不挤入旧顺序；刷新后新增出现且已取消项消失", async () => {
    const { state } = setup();
    const { runtime } = renderApp({ path: "/me/favorites?tab=tracks" });
    await screen.findByRole("table", { name: "收藏的曲目" });
    await cancel(first.track.title);
    await screen.findByText("共 1 首曲目");
    state.tracks.unshift(track(3));
    state.revision += 1;
    await act(async () => { await runtime.queryClient.invalidateQueries({ queryKey: personalKeys.favorites(runtime.session.currentGeneration()) }); });
    expect(await screen.findByText("列表有更新。", { exact: false })).toBeTruthy();
    expect(screen.queryByText("收藏曲目 3", { exact: true })).toBeNull();
    expect(row(first.track.title)).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "刷新" }));
    expect(await screen.findByText("收藏曲目 3", { exact: true })).toBeTruthy();
    expect(screen.queryByText(first.track.title, { exact: true })).toBeNull();
    expect(visibleTitles()).toEqual(["收藏曲目 3曲目艺人 3", "收藏曲目 2曲目艺人 2"]);
    expect(screen.queryByText("列表有更新。", { exact: false })).toBeNull();
  });

  it("换栏再回曲目丢弃已取消行", async () => {
    setup();
    const { history } = renderApp({ path: "/me/favorites?tab=tracks" });
    await screen.findByRole("table", { name: "收藏的曲目" });
    await cancel(first.track.title);
    await screen.findByText("共 1 首曲目");
    const tabs = within(screen.getByRole("navigation", { name: "收藏分类" }));
    await userEvent.click(tabs.getByRole("link", { name: "专辑" }));
    await screen.findByText("还没有收藏的专辑。", { exact: false });
    expect(new URLSearchParams(history.location.search).has("tab")).toBe(false);
    await userEvent.click(tabs.getByRole("link", { name: "曲目" }));
    expect(await screen.findByText(second.track.title, { exact: true })).toBeTruthy();
    expect(screen.queryByText(first.track.title, { exact: true })).toBeNull();
  });

  it("离开收藏页签再回来时丢弃保留行，不把展示快照跨页面复用", async () => {
    const { api } = setup();
    api.on("GET", "/api/v1/me/playlists", () => jsonResponse(200, { items: [], pagination: { page: 1, page_size: 20, total: 0 } }));
    renderApp({ path: "/me/favorites?tab=tracks" });
    await screen.findByRole("table", { name: "收藏的曲目" });
    await cancel(first.track.title);
    await screen.findByText("共 1 首曲目");
    await userEvent.click(within(screen.getByRole("navigation", { name: "我的音乐" })).getByRole("link", { name: "歌单" }));
    await screen.findByText("还没有歌单。");
    await userEvent.click(within(screen.getByRole("navigation", { name: "我的音乐" })).getByRole("link", { name: "收藏" }));
    await screen.findByText("还没有收藏的专辑。", { exact: false });
    await userEvent.click(within(screen.getByRole("navigation", { name: "收藏分类" })).getByRole("link", { name: "曲目" }));
    await screen.findByText(second.track.title, { exact: true });
    expect(screen.queryByText(first.track.title, { exact: true })).toBeNull();
  });

  it("换页再返回丢弃已取消行，携带当前 revision，聚焦结果标题", async () => {
    const { api } = setup(Array.from({ length: 50 }, (_, index) => track(index + 1)));
    const { history } = renderApp({ path: "/me/favorites?tab=tracks" });
    await screen.findByRole("table", { name: "收藏的曲目" });
    await cancel(first.track.title);
    await screen.findByText("共 49 首曲目");
    await userEvent.click(screen.getByRole("link", { name: "下一页" }));
    await screen.findByText("收藏曲目 50", { exact: true });
    expect(new URLSearchParams(history.location.search).get("page")).toBe("2");
    expect(document.activeElement).toBe(screen.getByRole("heading", { name: "收藏的曲目" }));
    expect(api.requestsTo("GET", tracksPath).some((request) => request.path === `${tracksPath}?page=2&page_size=48&revision=8`)).toBe(true);
    await userEvent.click(screen.getByRole("link", { name: "上一页" }));
    await screen.findByText(second.track.title, { exact: true });
    expect(screen.queryByText(first.track.title, { exact: true })).toBeNull();
    expect(new URLSearchParams(history.location.search).has("page")).toBe(false);
  });

  it("翻页遇到 favorites_changed 丢掉携带版本，重新读取目标页而非重复旧 revision", async () => {
    const { api, state } = setup(Array.from({ length: 50 }, (_, index) => track(index + 1)));
    renderApp({ path: "/me/favorites?tab=tracks" });
    await screen.findByRole("table", { name: "收藏的曲目" });
    state.revision = 9;
    await userEvent.click(screen.getByRole("link", { name: "下一页" }));
    await screen.findByText("收藏曲目 49", { exact: true });
    const reads = api.requestsTo("GET", tracksPath).filter((request) => new URL(request.path, "http://test").searchParams.get("page") === "2");
    expect(reads.map((request) => new URL(request.path, "http://test").searchParams.get("revision"))).toEqual(["7", null]);
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.queryByText(first.track.title, { exact: true })).toBeNull();
  });

  it("缺失曲目标记灰态、没有专辑链接且不能播放，但可以取消并再次收藏", async () => {
    setup([track(1, false)]);
    renderApp({ path: "/me/favorites?tab=tracks" });
    await screen.findByText("文件缺失");
    const missingRow = row(first.track.title);
    expect(within(missingRow).queryByRole("link")).toBeNull();
    expect(within(missingRow).getByRole("button", { name: `播放 ${first.track.title}` }).hasAttribute("disabled")).toBe(true);
    // jsdom 不测最终颜色；这里守住灰态样式挂点，实际颜色由浏览器验收负责。
    expect(within(missingRow).getByText(first.track.title, { exact: true }).parentElement?.className).toContain("missing");
    await userEvent.click(within(missingRow).getByRole("button", { name: "更多操作" }));
    for (const name of ["下一首播放", "加入队列"]) expect(screen.getByRole("menuitem", { name }).hasAttribute("disabled")).toBe(true);
    expect(screen.getByRole("menuitem", { name: "添加到歌单…" }).hasAttribute("disabled")).toBe(false);
    await userEvent.keyboard("{Escape}");
    await cancel(first.track.title);
    await screen.findByText("共 0 首曲目");
    await userEvent.click(heart(first.track.title));
    await screen.findByText("共 1 首曲目");
    expect(heart(first.track.title).getAttribute("aria-pressed")).toBe("true");
  });

  it("加载中和读取失败不冒充空态，手动重试能恢复", async () => {
    const { api, list } = setup();
    const pending = deferred<Response>();
    api.on("GET", tracksPath, () => pending.promise);
    renderApp({ path: "/me/favorites?tab=tracks" });
    await screen.findByText("正在加载收藏的曲目");
    expect(screen.queryByText("还没有收藏的曲目。", { exact: false })).toBeNull();
    await act(async () => { pending.resolve(jsonResponse(500, { error: { code: "internal_error", message: "读取失败" }, request_id: "favorites-read-error" })); });
    expect((await screen.findByRole("alert")).textContent).toContain("favorites-read-error");
    api.on("GET", tracksPath, (request) => list("tracks", request));
    await userEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByRole("table", { name: "收藏的曲目" })).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
  });
});
