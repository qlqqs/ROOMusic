import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { decodeArtistSplitPreview } from "../features/governance/api/decoders";
import type { ApiResponse } from "../shared/api";
import { decodeFavoriteArtistsPage, type FavoriteArtistsPageDTO } from "../features/personal/api/decoders";
import { personalKeys } from "../features/personal/query-keys";
import { catalogStateKeys } from "../features/catalog/query-keys";
import { artistCatalogState, artistDataOf, artistTestId, primaryArtistId, respondToArtistFavorites, respondToArtists, seedArtist } from "../test/artist-data";
import { installArtistsApi, installFakeApi, installFakeSession } from "../test/fake-api";
import { installArtistFavoritesApi } from "../test/fake-artists";
import { installPersonalApi } from "../test/fake-personal";
import { FakeAudio } from "../test/fake-audio";
import { historyItem, historyPage } from "../test/history-data";
import { jsonResponse } from "../test/http";
import { renderApp } from "../test/render-app";

beforeEach(() => { vi.stubGlobal("Audio", class { constructor() { return new FakeAudio(); } }); });
const id = primaryArtistId();
function setup() {
  const api = installFakeApi();
  const session = installFakeSession(api, { current: "listener" });
  const personal = installPersonalApi(api, { session });
  const data = installArtistsApi(api, artistDataOf());
  installArtistFavoritesApi(api, data, session, personal);
  api.on("GET", "/api/v1/catalog/status", () => jsonResponse(200, artistCatalogState(data)));
  const favorite = (artistId: string) => respondToArtistFavorites(data, "PUT", new URL(`/api/v1/me/favorites/artists/${artistId}`, "http://test"), null, session.accounts.listener?.id);
  return { api, data, favorite };
}

describe("艺人收藏列表与epoch", () => {
  it.each([
    { label: "三身份收藏时间交错", latest: "2026-11-04T00:00:03Z" },
    { label: "合并后同时间按canonical ID打平", latest: "2026-11-04T00:00:02Z" },
  ])("$label：页面遵循服务端去重排序，不按首次锚点或Map插入顺序", async ({ latest }) => {
    const { data, favorite } = setup();
    const sourceId = "00000000-0000-4000-8000-000000000002";
    const thirdId = "00000000-0000-4000-8000-000000000003";
    favorite(id);
    const entry = [...data.favorites.entries()][0];
    if (!entry) throw new Error("缺少收藏用户");
    const [userId, user] = entry;
    // 三个身份：A早、C中、B晚，B并入A。特意先插入C，避免同时间用插入顺序掩盖ID打平错误。
    user.anchors = new Map([[thirdId, "2026-11-04T00:00:02Z"], [id, "2026-11-04T00:00:01Z"], [sourceId, latest]]);
    user.revision = 3;
    expect(respondToArtists(data, "POST", new URL(`/api/v1/artists/${id}/merge`, "http://test"), { expected_revision: 1, source_artist_id: sourceId, source_expected_revision: 1 }, artistTestId(710)).status).toBe(200);
    // 只核对测试替身及前端消费此响应的顺序；这不是后端收藏事务/排序验收。
    const response = respondToArtistFavorites(data, "GET", new URL("/api/v1/me/favorites/artists?page=1&page_size=48", "http://test"), null, userId);
    const page = decodeFavoriteArtistsPage(response.body);
    expect(page.pagination.total).toBe(2);
    expect(page.items.map((item) => item.artist.id)).toEqual([id, thirdId]);
    expect(page.items[0]?.created_at).toBe(latest);
    expect([...user.anchors.entries()]).toEqual([[thirdId, "2026-11-04T00:00:02Z"], [id, "2026-11-04T00:00:01Z"], [sourceId, latest]]);
    renderApp({ path: "/me/favorites?tab=artists" });
    await screen.findByRole("link", { name: "邓丽君" });
    const links = within(screen.getByRole("region", { name: "收藏的艺人" })).getAllByRole("link").filter((link) => link.getAttribute("href")?.startsWith("/artists/"));
    expect(links.map((link) => link.getAttribute("href"))).toEqual([`/artists/${id}`, `/artists/${thirdId}`]);
    expect(links.map((link) => link.textContent)).toEqual(["邓丽君", "同名艺人"]);
    expect(screen.queryByRole("link", { name: "Teresa Teng" })).toBeNull();
  });

  it("收藏两个身份后合并只显示一个，恢复旧身份后原收藏重新出现", async () => {
    const { data, favorite } = setup();
    const sourceId = "00000000-0000-4000-8000-000000000002";
    favorite(id);
    favorite(sourceId);
    const result = respondToArtists(data, "POST", new URL(`/api/v1/artists/${id}/merge`, "http://test"), { expected_revision: 1, source_artist_id: sourceId, source_expected_revision: 1 }, artistTestId(700));
    expect(result.status).toBe(200);
    const { runtime } = renderApp({ path: "/me/favorites?tab=artists" });
    await screen.findByRole("link", { name: "邓丽君" });
    expect(screen.queryByRole("link", { name: "Teresa Teng" })).toBeNull();
    expect(screen.getByText("共 1 位艺人")).toBeTruthy();
    const source = data.mergedSources.get(id)?.[0];
    if (!source) throw new Error("缺少合并来源");
    const scope = { mode: "aliases", name_key_ids: source.name_key_ids };
    const preview = decodeArtistSplitPreview(respondToArtists(data, "POST", new URL(`/api/v1/artists/${id}/split-preview`, "http://test"), scope).body);
    const restored = respondToArtists(data, "POST", new URL(`/api/v1/artists/${id}/split`, "http://test"), { ...scope, expected_revision: preview.revision, basis: preview.basis, target: { kind: "restore", artist_id: sourceId, expected_revision: source.revision } }, artistTestId(701));
    expect(restored.status).toBe(200);
    data.epoch++;
    await act(async () => runtime.queryClient.invalidateQueries({ queryKey: catalogStateKeys.current(runtime.session.currentGeneration()) }));
    await userEvent.click(await screen.findByRole("button", { name: "刷新" }));
    expect(await screen.findByRole("link", { name: "Teresa Teng" })).toBeTruthy();
    expect(screen.getByRole("link", { name: "邓丽君" })).toBeTruthy();
    expect(screen.getByText("共 2 位艺人")).toBeTruthy();
  });

  it("取消收藏和窗口聚焦重读只更新真实缓存，保留原槽位可回点；离开后才移除", async () => {
    const { api, data, favorite } = setup();
    const detail = data.details.get(id);
    if (!detail) throw new Error("缺少艺人");
    detail.artist.available = false;
    const firstId = "00000000-0000-4000-8000-000000000003";
    const lastId = "00000000-0000-4000-8000-000000000002";
    favorite(lastId);
    favorite(id);
    favorite(firstId);
    const { runtime } = renderApp({ path: "/me/favorites?tab=artists" });
    const button = await screen.findByRole("button", { name: "收藏“邓丽君”" });
    await waitFor(() => expect(button.getAttribute("aria-pressed")).toBe("true"));
    const region = screen.getByRole("region", { name: "收藏的艺人" });
    const visibleTitles = () => within(region).getAllByRole("link").filter((link) => link.getAttribute("href")?.startsWith("/artists/")).map((link) => link.textContent);
    const originalOrder = ["同名艺人", "邓丽君", "Teresa Teng"];
    expect(visibleTitles()).toEqual(originalOrder);
    const originalSlot = button.closest("li");
    expect(originalSlot).not.toBeNull();
    expect(within(region).getAllByRole("listitem")[1]).toBe(originalSlot);
    expect(screen.getByText("暂时不可用")).toBeTruthy();
    const generation = runtime.session.currentGeneration();
    const cacheKey = personalKeys.favoriteList(generation, "artist", { page: 1, pageSize: 48, revision: null });
    const cachedIds = () => runtime.queryClient.getQueryData<ApiResponse<FavoriteArtistsPageDTO>>(cacheKey)?.data.items.map((item) => item.artist.id);
    const settled = () => waitFor(() => expect(runtime.queryClient.isFetching({ queryKey: personalKeys.favorites(generation) })).toBe(0));
    await userEvent.click(button);
    await screen.findByText("共 2 位艺人");
    await settled();
    expect(button.getAttribute("aria-pressed")).toBe("false");
    expect(cachedIds()).toEqual([firstId, lastId]);
    expect(visibleTitles()).toEqual(originalOrder);
    const reads = api.requestsTo("GET", "/api/v1/me/favorites/artists").length;
    await act(async () => { window.dispatchEvent(new Event("focus")); });
    await waitFor(() => expect(api.requestsTo("GET", "/api/v1/me/favorites/artists").length).toBeGreaterThan(reads));
    await settled();
    expect(cachedIds()).toEqual([firstId, lastId]);
    expect(visibleTitles()).toEqual(originalOrder);
    expect(within(region).getAllByRole("listitem")[1]).toBe(originalSlot);
    expect(button.getAttribute("aria-pressed")).toBe("false");
    // 重新收藏会变成服务端最新一条，但本页保留原来的中间槽位，不突然跳到列表顶部。
    await userEvent.click(button);
    await screen.findByText("共 3 位艺人");
    await settled();
    expect(cachedIds()).toEqual([id, firstId, lastId]);
    expect(button.getAttribute("aria-pressed")).toBe("true");
    expect(visibleTitles()).toEqual(originalOrder);
    expect(within(region).getAllByRole("listitem")[1]).toBe(originalSlot);
    expect(api.requestsTo("PUT", `/api/v1/me/favorites/artists/${id}`)).toHaveLength(1);
    await userEvent.click(button);
    await screen.findByText("共 2 位艺人");
    await settled();
    const tabs = screen.getByRole("navigation", { name: "收藏分类" });
    await userEvent.click(within(tabs).getByRole("link", { name: "专辑" }));
    await screen.findByText("还没有收藏的专辑。");
    await userEvent.click(within(tabs).getByRole("link", { name: "艺人" }));
    await screen.findByRole("link", { name: "同名艺人" });
    expect(screen.queryByRole("link", { name: "邓丽君" })).toBeNull();
    expect(cachedIds()).toEqual([firstId, lastId]);
  });

  it("翻页携带集合版本，409 favorites_changed重读当前页且不混入旧行", async () => {
    const { api, data, favorite } = setup();
    const base = data.details.get(id)?.artist;
    if (!base) throw new Error("缺少艺人");
    for (let i = 1; i <= 49; i++) { const artist = seedArtist(data, { ...base, id: artistTestId(i), name: `收藏艺人${i}` }); favorite(artist.artist.id); }
    renderApp({ path: "/me/favorites?tab=artists" });
    await screen.findByRole("link", { name: "收藏艺人49" });
    favorite(id);
    await userEvent.click(within(screen.getByRole("navigation", { name: "收藏的艺人分页" })).getByRole("link", { name: "下一页" }));
    await screen.findByRole("link", { name: "收藏艺人1" });
    const reads = api.requestsTo("GET", "/api/v1/me/favorites/artists").map((request) => request.path);
    expect(reads.some((path) => path.includes("page=2") && path.includes("revision=49"))).toBe(true);
    expect(reads.at(-1)).toContain("page=2");
    expect(reads.at(-1)).not.toContain("revision=");
    expect(screen.queryByRole("link", { name: "收藏艺人49" })).toBeNull();
  });

  it("整理中的收藏栏不是空态，epoch发布后自动重读并显示收藏", async () => {
    const { data, favorite, api } = setup();
    favorite(id);
    data.state = "preparing";
    const { runtime } = renderApp({ path: "/me/favorites?tab=artists" });
    runtime.queryClient.setDefaultOptions({ queries: { retry: false } });
    await screen.findByText("艺人信息正在整理，请稍后再试。");
    expect(screen.queryByText("还没有收藏的艺人。")).toBeNull();
    data.state = "ready";
    data.epoch++;
    await act(async () => runtime.queryClient.invalidateQueries({ queryKey: catalogStateKeys.current(runtime.session.currentGeneration()) }));
    expect(await screen.findByRole("link", { name: "邓丽君" })).toBeTruthy();
    expect(api.requestsTo("GET", "/api/v1/me/favorites/artists").length).toBeGreaterThanOrEqual(2);
  });

  it("epoch变大重读艺人详情与收藏lookup，不改变地址", async () => {
    const { api, data } = setup();
    const { runtime, history } = renderApp({ path: `/artists/${id}` });
    await screen.findByRole("heading", { name: "邓丽君", level: 1 });
    await waitFor(() => expect(api.requestsTo("POST", "/api/v1/me/favorites/lookup").length).toBeGreaterThan(0));
    const lookupBefore = api.requestsTo("POST", "/api/v1/me/favorites/lookup").length;
    const detail = data.details.get(id);
    if (!detail) throw new Error("缺少艺人");
    detail.artist.name = "发布后的主名";
    data.epoch++;
    await act(async () => runtime.queryClient.invalidateQueries({ queryKey: catalogStateKeys.current(runtime.session.currentGeneration()) }));
    await screen.findByRole("heading", { name: "发布后的主名", level: 1 });
    expect(api.requestsTo("GET", `/api/v1/artists/${id}`).length).toBeGreaterThanOrEqual(2);
    expect(api.requestsTo("POST", "/api/v1/me/favorites/lookup").length).toBeGreaterThan(lookupBefore);
    expect(history.location.pathname).toBe(`/artists/${id}`);
  });

  it("epoch变大让历史重新读取新的艺人署名", async () => {
    const { data, api } = setup();
    api.on("GET", "/api/v1/me/plays/tracks", () => jsonResponse(200, historyPage([historyItem(1, { artist: data.epoch === 1 ? "历史旧名" : "历史新名", artist_refs: [{ artist_id: id, name: data.epoch === 1 ? "历史旧名" : "历史新名", display_name: "主名" }] })])));
    const { runtime } = renderApp({ path: "/me/history" });
    await screen.findByRole("link", { name: "历史旧名" });
    data.epoch++;
    await act(async () => runtime.queryClient.invalidateQueries({ queryKey: catalogStateKeys.current(runtime.session.currentGeneration()) }));
    expect(await screen.findByRole("link", { name: "历史新名" })).toBeTruthy();
    expect(api.requestsTo("GET", "/api/v1/me/plays/tracks")).toHaveLength(2);
  });
});
