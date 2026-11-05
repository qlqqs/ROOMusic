import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ArtistNames } from "../features/catalog/components/artist-names";
import { catalogStateKeys } from "../features/catalog/query-keys";
import { decodeArtistResults } from "../features/search/api/decoders";
import { sampleBody } from "../test/api-samples";
import { artistCatalogState, artistDataOf, artistTestId, primaryArtistId } from "../test/artist-data";
import { installCatalogApi } from "../test/fake-catalog";
import { deferred } from "../test/deferred";
import { installArtistsApi, installFakeApi, installFakeSession } from "../test/fake-api";
import { FakeAudio } from "../test/fake-audio";
import { jsonResponse, sampleResponse } from "../test/http";
import { renderApp } from "../test/render-app";
import { searchAlbumItem, searchAlbumPage, searchArtistPage, searchTrackItem, searchTrackPage } from "../test/search-data";

beforeEach(() => { vi.stubGlobal("Audio", class { constructor() { return new FakeAudio(); } }); });
function setup() {
  const api = installFakeApi();
  installFakeSession(api, { current: "curator" });
  const data = installArtistsApi(api, artistDataOf());
  api.on("GET", "/api/v1/catalog/status", () => jsonResponse(200, artistCatalogState(data)));
  return { api, data };
}
const id = primaryArtistId();

describe("艺人名字链接与回退", () => {
  it("就绪但没有引用时仍用原文字；普通专辑卡的署名永远不是嵌套链接", async () => {
    const { api, data } = setup();
    const album = data.albums.get(id)?.[0];
    if (!album) throw new Error("缺少专辑样本");
    installCatalogApi(api, { groups: [album.group] });
    renderApp({ path: "/albums" });
    const card = await screen.findByRole("link", { name: /海边夜曲 邓丽君/ });
    expect(within(card).getByText("邓丽君")).toBeTruthy();
    expect(within(card).queryByRole("link")).toBeNull();
    expect(card.getAttribute("href")).toBe(`/albums/${album.group.id}`);
  });

  it("ready但引用为空的曲目原样显示署名，不伪造艺人链接", async () => {
    const { data } = setup();
    const first = data.tracks.get(`${id}:track_artist`)?.[0];
    if (!first) throw new Error("缺少曲目");
    first.artist = "无法解析的原始署名";
    first.artist_refs = [];
    renderApp({ path: `/artists/${id}` });
    const cell = within(await screen.findByRole("row", { name: /曲目1-1/ })).getByRole("rowheader");
    expect(within(cell).getByText("无法解析的原始署名")).toBeTruthy();
    expect(within(cell).queryByRole("link")).toBeNull();
    expect(cell.textContent).not.toContain("整理");
  });

  it("状态未知时只有原文，不需要路由、不添加整理说明", () => {
    render(<p data-testid="signature"><ArtistNames text="原始署名 / 第二位" refs={[{ artist_id: id, name: "链接文字", display_name: "主名" }]} /></p>);
    expect(screen.getByTestId("signature").textContent).toBe("原始署名 / 第二位");
    expect(screen.queryByRole("link")).toBeNull();
  });

  it("ready每位艺人使用原署名和正确ID，多位间斜线；preparing退回原文不解释", async () => {
    const { api, data } = setup();
    const tracks = data.tracks.get(`${id}:track_artist`);
    const first = tracks?.[0];
    if (!first) throw new Error("缺少曲目");
    first.artist = "原始多人署名";
    first.artist_refs = [{ artist_id: id, name: "邓麗君", display_name: "邓丽君" }, { artist_id: artistTestId(2), name: "第二位", display_name: "第二主名" }];
    const { runtime } = renderApp({ path: `/artists/${id}` });
    const row = await screen.findByRole("row", { name: /曲目1-1/ });
    const cell = within(row).getByRole("rowheader");
    expect(within(cell).getByRole("link", { name: "邓麗君" }).getAttribute("href")).toBe(`/artists/${id}`);
    expect(within(cell).getByRole("link", { name: "第二位" }).getAttribute("href")).toBe(`/artists/${artistTestId(2)}`);
    expect(cell.textContent).toContain("邓麗君 / 第二位");
    expect(api.requestsTo("GET", "/api/v1/catalog/status")).toHaveLength(1);
    data.state = "preparing";
    await act(async () => runtime.queryClient.invalidateQueries({ queryKey: catalogStateKeys.current(runtime.session.currentGeneration()) }));
    await waitFor(() => expect(within(cell).queryByRole("link")).toBeNull());
    expect(cell.textContent).toContain("原始多人署名");
    expect(cell.textContent).not.toContain("整理");
  });

  it.each([true, false])("曲目署名空白时名字和引用同一回退（专辑艺人存在=%s）", async (hasAlbumArtist) => {
    const { data } = setup();
    const first = data.tracks.get(`${id}:track_artist`)?.[0];
    if (!first) throw new Error("缺少曲目");
    first.artist = "  ";
    first.artist_refs = [{ artist_id: artistTestId(55), name: "不应出现", display_name: "不应出现" }];
    first.release.album_artist = hasAlbumArtist ? "专辑署名" : null;
    first.release.artist = "发行署名";
    first.release.album_artist_refs = [{ artist_id: artistTestId(56), name: "专辑署名", display_name: "专辑主名" }];
    first.release.artist_refs = [{ artist_id: artistTestId(57), name: "发行署名", display_name: "发行主名" }];
    renderApp({ path: `/artists/${id}` });
    const cell = within(await screen.findByRole("row", { name: /曲目1-1/ })).getByRole("rowheader");
    expect(within(cell).getByRole("link", { name: hasAlbumArtist ? "专辑署名" : "发行署名" }).getAttribute("href")).toBe(`/artists/${artistTestId(hasAlbumArtist ? 56 : 57)}`);
    expect(within(cell).queryByText("不应出现")).toBeNull();
  });
});

describe("I5 艺人入口能力状态", () => {
  it.each(["ready", "preparing", "unavailable", "unknown", "failed"] as const)("%s 下侧栏与收藏分类同时显示或隐藏", async (state) => {
    const { api, data } = setup();
    if (state === "unknown") {
      const pending = deferred<Response>();
      api.on("GET", "/api/v1/catalog/status", () => pending.promise);
    } else if (state === "failed") api.on("GET", "/api/v1/catalog/status", () => sampleResponse("errors/database-unavailable"));
    else data.state = state;
    const { runtime } = renderApp({ path: "/me/favorites" });
    runtime.queryClient.setDefaultOptions({ queries: { retry: false } });
    const nav = await screen.findByRole("navigation", { name: "主导航" });
    const categories = await screen.findByRole("navigation", { name: "收藏分类" });
    if (state === "ready" || state === "preparing") {
      expect(await within(nav).findByRole("link", { name: "艺人" })).toBeTruthy();
      expect(within(nav).getByRole("link", { name: "可能是同一位" })).toBeTruthy();
      expect(within(categories).getByRole("link", { name: "艺人" })).toBeTruthy();
    } else {
      await screen.findByText("还没有收藏的专辑。");
      expect(within(nav).queryByRole("link", { name: "艺人" })).toBeNull();
      expect(within(nav).queryByRole("link", { name: "可能是同一位" })).toBeNull();
      expect(within(categories).queryByRole("link", { name: "艺人" })).toBeNull();
    }
  });
});

describe("全局搜索的艺人栏", () => {
  function searchSetup() {
    const fixture = setup();
    const alias = decodeArtistResults(sampleBody("search/artists-alias-match")).items[0];
    if (!alias) throw new Error("缺少别名命中样本");
    const album = searchAlbumItem();
    const track = searchTrackItem();
    // 每页身份与内容不同，集合总数、has_more 和返回窗口严格由实际请求计算。
    const artists = Array.from({ length: 37 }, (_, index) => {
      const name = index === 0 ? alias.artist.name : `搜索艺人 ${index + 1}`;
      const artistId = artistTestId(1000 + index);
      return { ...alias, artist: { ...alias.artist, id: artistId, name }, match: index === 0 ? { ...alias.match, artist_id: artistId } : { ...alias.match, field: "artist_name" as const, artist_id: artistId, original: name, alias: null } };
    });
    const albums = Array.from({ length: 37 }, (_, index) => ({ ...album, group: { ...album.group, id: artistTestId(2000 + index), representative: { ...album.group.representative, title: `搜索专辑 ${index + 1}` } } }));
    const tracks = Array.from({ length: 61 }, (_, index) => ({ ...track, track_id: artistTestId(3000 + index), title: `搜索曲目 ${index + 1}` }));
    fixture.api.on("GET", "/api/v1/search", (request) => {
      const params = new URL(request.path, "http://test").searchParams;
      const page = Number(params.get("page"));
      const pageSize = Number(params.get("page_size"));
      if (!Number.isInteger(page) || page < 1 || !Number.isInteger(pageSize) || pageSize < 1) throw new Error("搜索必须明确发送合法分页");
      const windowOf = <T,>(items: T[]) => ({ items: items.slice((page - 1) * pageSize, page * pageSize), pagination: { page, page_size: pageSize, total: items.length, total_relation: "eq" as const, has_more: page * pageSize < items.length } });
      const options = { query: params.get("q") ?? "", catalog_state: artistCatalogState(fixture.data) };
      switch (params.get("type")) {
        case "artists": return jsonResponse(200, searchArtistPage({ ...options, ...windowOf(artists) }));
        case "albums": return jsonResponse(200, searchAlbumPage({ ...options, ...windowOf(albums) }));
        case "tracks": return jsonResponse(200, searchTrackPage({ ...options, ...windowOf(tracks) }));
        default: throw new Error("未登记搜索类型");
      }
    });
    const requests = (kind: "artists" | "albums" | "tracks") => fixture.api.requestsTo("GET", "/api/v1/search").filter((request) => new URL(request.path, "http://test").searchParams.get("type") === kind);
    return { ...fixture, requests, artists, albums, tracks };
  }
  it("ready显示别名解释，artists_page只请求自己的新页，另两栏内容与请求数不变", async () => {
    const { requests } = searchSetup();
    const { history } = renderApp({ path: "/search?q=Teresa&albums_page=2&tracks_page=3" });
    const section = await screen.findByRole("region", { name: "艺人" });
    expect(await within(section).findByText("别名：Teresa Teng")).toBeTruthy();
    const albumSection = screen.getByRole("region", { name: "专辑" });
    const trackSection = screen.getByRole("region", { name: "曲目" });
    await within(albumSection).findByRole("link", { name: /^搜索专辑 13 / });
    await within(trackSection).findByRole("row", { name: /搜索曲目 41/ });
    const albumTitles = () => within(albumSection).getAllByRole("link", { name: /^搜索专辑 / }).map((link) => link.textContent);
    const trackTitles = () => within(trackSection).getAllByRole("rowheader").map((cell) => cell.textContent);
    const albumsBefore = albumTitles();
    const tracksBefore = trackTitles();
    expect(albumsBefore).toHaveLength(12);
    expect(tracksBefore).toHaveLength(20);
    expect(within(section).getAllByRole("link").filter((link) => link.getAttribute("href")?.startsWith("/artists/"))).toHaveLength(12);
    expect(requests("artists")).toHaveLength(1);
    expect(requests("albums")).toHaveLength(1);
    expect(requests("tracks")).toHaveLength(1);
    expect(new URL(requests("artists")[0]?.path ?? "", "http://test").searchParams.get("page_size")).toBe("12");
    const otherReads = { albums: requests("albums").length, tracks: requests("tracks").length };
    await userEvent.click(within(section).getByRole("link", { name: "下一页" }));
    await within(section).findByRole("link", { name: "搜索艺人 13" });
    expect(within(section).getByRole("link", { name: "搜索艺人 24" })).toBeTruthy();
    expect(within(section).queryByRole("link", { name: "搜索艺人 12" })).toBeNull();
    expect(within(section).queryByText("别名：Teresa Teng")).toBeNull();
    expect(history.location.search).toContain("artists_page=2");
    expect(history.location.search).toContain("albums_page=2");
    expect(history.location.search).toContain("tracks_page=3");
    expect(requests("artists")).toHaveLength(2);
    expect(new URL(requests("artists").at(-1)?.path ?? "", "http://test").searchParams.get("page")).toBe("2");
    expect(requests("albums")).toHaveLength(otherReads.albums);
    expect(requests("tracks")).toHaveLength(otherReads.tracks);
    expect(albumTitles()).toEqual(albumsBefore);
    expect(trackTitles()).toEqual(tracksBefore);
  });
  it("preparing不请求艺人搜索、不显示栏或整理说明，专辑曲目照常", async () => {
    const { api, data } = searchSetup();
    data.state = "preparing";
    renderApp({ path: "/search?q=Teresa" });
    await screen.findByRole("region", { name: "曲目" });
    await waitFor(() => expect(api.requestsTo("GET", "/api/v1/search")).toHaveLength(2));
    expect(api.requestsTo("GET", "/api/v1/search").some((request) => request.path.includes("type=artists"))).toBe(false);
    expect(screen.queryByRole("region", { name: "艺人" })).toBeNull();
    expect(screen.queryByText(/艺人信息正在整理/)).toBeNull();
  });
});
