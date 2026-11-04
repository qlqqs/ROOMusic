import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { deferred } from "../test/deferred";
import { installFakeApi, installFakeSession } from "../test/fake-api";
import { FakeAudio } from "../test/fake-audio";
import { jsonResponse } from "../test/http";
import { renderApp } from "../test/render-app";
import { searchAlbumItem, searchAlbumPage, searchTrackItem, searchTrackPage } from "../test/search-data";

const albums = searchAlbumPage();
const tracks = searchTrackPage();
const firstAlbum = searchAlbumItem();
const firstTrack = searchTrackItem();
const path = "/api/v1/search";
type Kind = "albums" | "tracks";
const labels = { albums: "专辑", tracks: "曲目" };
const paramsOf = (url: string) => new URL(url, "http://test").searchParams;
const section = (type: Kind) => screen.getByRole("region", { name: labels[type] });

beforeEach(() => {
  vi.stubGlobal("Audio", class { constructor() { return new FakeAudio(); } });
  vi.spyOn(HTMLMediaElement.prototype, "canPlayType").mockReturnValue("probably");
  sessionStorage.clear();
});
afterEach(() => vi.restoreAllMocks());

function setup(distinctResults = false) {
  const api = installFakeApi();
  installFakeSession(api, { current: "listener" });
  const pageFor = (url: string) => {
    const params = paramsOf(url);
    const type = params.get("type");
    const page = Number(params.get("page"));
    const pageSize = Number(params.get("page_size"));
    const q = params.get("q") ?? "";
    let result = type === "albums" ? albums : tracks;
    if (distinctResults) {
      // 两个显式测试查询的各页有不同身份，不能靠只更新页码/标题蒙混通过。
      if (q !== "爱" && q !== "新词") throw new Error(`未登记查询：${q}`);
      const id = `00000000-0000-4000-8000-${String((q === "爱" ? 1000 : 2000) + page).padStart(12, "0")}`;
      result = type === "albums"
        ? searchAlbumPage({ items: [{ ...firstAlbum, group: { ...firstAlbum.group, id, representative: { ...firstAlbum.group.representative, title: `${q}的专辑第${page}页` } } }] })
        : searchTrackPage({ items: [{ ...firstTrack, track_id: id, title: `${q}的曲目第${page}页` }] });
    }
    return { ...result, query: q, pagination: { page, page_size: pageSize, total: 1000, total_relation: "gte" as const, has_more: true } };
  };
  const respond = (url: string) => jsonResponse(200, pageFor(url));
  api.on("GET", path, (request) => respond(request.path));
  const requests = (type: Kind) => api.requestsTo("GET", path).filter((request) => paramsOf(request.path).get("type") === type);
  return { api, respond, requests, pageFor };
}

async function loaded() {
  await screen.findByRole("table", { name: "曲目结果" });
  await screen.findByRole("link", { name: new RegExp(firstAlbum.group.representative.title) });
}

describe("搜索结果页", () => {
  it.each(["albums", "tracks"] as const)("%s 独立加载，另一栏结果不受阻塞", async (type) => {
    const { api, respond } = setup();
    const pending = deferred<Response>();
    api.on("GET", path, (request) => paramsOf(request.path).get("type") === type ? pending.promise : respond(request.path));
    renderApp({ path: "/search?q=爱" });
    expect(await screen.findByText(`正在查找${labels[type]}`)).toBeTruthy();
    const other = type === "albums" ? "tracks" : "albums";
    await waitFor(() => expect(within(section(other)).getByText("1,000+ " + (other === "albums" ? "张" : "首"))).toBeTruthy());
    expect(within(section(type)).queryByText(/没有找到/)).toBeNull();
    pending.resolve(respond(`${path}?type=${type}&q=爱&page=1&page_size=${type === "albums" ? 12 : 20}`));
    await loaded();
  });

  it.each(["albums", "tracks"] as const)("%s 失败仅重试本栏，另一栏保持结果且不重发", async (type) => {
    const { api, respond, requests } = setup();
    api.on("GET", path, (request) => paramsOf(request.path).get("type") === type
      ? jsonResponse(500, { error: { code: "internal_error", message: "读取失败" }, request_id: "search-column-error" }) : respond(request.path));
    renderApp({ path: "/search?q=爱" });
    expect((await screen.findByRole("alert")).textContent).toContain("search-column-error");
    const other = type === "albums" ? "tracks" : "albums";
    expect(within(section(other)).getByText("1,000+ " + (other === "albums" ? "张" : "首"))).toBeTruthy();
    expect(within(section(type)).queryByText(/没有找到/)).toBeNull();
    const otherRequests = requests(other).length;
    api.on("GET", path, (request) => respond(request.path));
    await userEvent.click(within(section(type)).getByRole("button", { name: "重试" }));
    await loaded();
    expect(requests(type)).toHaveLength(2);
    expect(requests(other)).toHaveLength(otherRequests);
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("直达 URL 恢复两栏，分别用 12/20 条；翻页只改本栏且不重新请求另一栏", async () => {
    const { requests } = setup(true);
    const { history } = renderApp({ path: "/search?q=爱&albums_page=2&tracks_page=3" });
    await screen.findByRole("link", { name: /爱的专辑第2页/ });
    await screen.findByText("爱的曲目第3页");
    expect(paramsOf(requests("albums")[0]?.path ?? "")).toEqual(new URLSearchParams({ type: "albums", q: "爱", page: "2", page_size: "12" }));
    expect(paramsOf(requests("tracks")[0]?.path ?? "")).toEqual(new URLSearchParams({ type: "tracks", q: "爱", page: "3", page_size: "20" }));
    await userEvent.click(within(screen.getByRole("navigation", { name: "专辑结果分页" })).getByRole("link", { name: "下一页" }));
    await waitFor(() => expect(requests("albums")).toHaveLength(2));
    expect(new URLSearchParams(history.location.search).get("albums_page")).toBe("3");
    expect(new URLSearchParams(history.location.search).get("tracks_page")).toBe("3");
    expect(requests("tracks")).toHaveLength(1);
    await screen.findByRole("link", { name: /爱的专辑第3页/ });
    expect(screen.queryByRole("link", { name: /爱的专辑第2页/ })).toBeNull();
    expect(screen.getByText("爱的曲目第3页")).toBeTruthy();
    expect(paramsOf(requests("albums").at(-1)?.path ?? "").get("page")).toBe("3");
    await userEvent.click(within(screen.getByRole("navigation", { name: "曲目结果分页" })).getByRole("link", { name: "上一页" }));
    await waitFor(() => expect(requests("tracks")).toHaveLength(2));
    expect(new URLSearchParams(history.location.search).get("albums_page")).toBe("3");
    expect(new URLSearchParams(history.location.search).get("tracks_page")).toBe("2");
    expect(requests("albums")).toHaveLength(2);
    await screen.findByText("爱的曲目第2页");
    expect(screen.queryByText("爱的曲目第3页")).toBeNull();
    expect(screen.getByRole("link", { name: /爱的专辑第3页/ })).toBeTruthy();
    expect(paramsOf(requests("tracks").at(-1)?.path ?? "").get("page")).toBe("2");
  });

  it.each([999, 1000])("eq 精确显示 %s 而不是下界，末页由 has_more 禁用下一页", async (total) => {
    const { api, pageFor } = setup();
    api.on("GET", path, (request) => {
      const response = pageFor(request.path);
      return jsonResponse(200, { ...response, pagination: { ...response.pagination, total, total_relation: "eq", has_more: false } });
    });
    renderApp({ path: "/search?q=爱&albums_page=84&tracks_page=50" });
    await loaded();
    expect(within(section("albums")).getByText(`${total.toLocaleString("en-US")} 张`)).toBeTruthy();
    expect(within(section("tracks")).getByText(`${total.toLocaleString("en-US")} 首`)).toBeTruthy();
    expect(screen.queryByText(/1,000\+/)).toBeNull();
    for (const type of ["albums", "tracks"] as const) expect(within(section(type)).queryByRole("link", { name: "下一页" })).toBeNull();
  });

  it("gte 的第 21 页及超过下界推算的第 51 页仍能继续，不显示虚假总页数", async () => {
    setup();
    const { history } = renderApp({ path: "/search?q=爱&tracks_page=21" });
    await loaded();
    await userEvent.click(within(section("tracks")).getByRole("link", { name: "下一页" }));
    await waitFor(() => expect(new URLSearchParams(history.location.search).get("tracks_page")).toBe("22"));
    act(() => history.push("/search?q=爱&tracks_page=51"));
    await screen.findByText("第 51 页");
    expect(within(section("tracks")).getByRole("link", { name: "下一页" }).getAttribute("href")).toContain("tracks_page=52");
    expect(screen.queryByText(/共\s*\d+\s*页/)).toBeNull();
  });

  it.each(["eq", "gte"] as const)("%s 越界空页不当作零结果，回首页只清本栏页码", async (relation) => {
    const { api, respond } = setup();
    api.on("GET", path, (request) => paramsOf(request.path).get("type") === "tracks" && paramsOf(request.path).get("page") === "99"
      ? jsonResponse(200, { ...tracks, items: [], pagination: { page: 99, page_size: 20, total: relation === "gte" ? 1000 : 1, total_relation: relation, has_more: false } }) : respond(request.path));
    const { history } = renderApp({ path: "/search?q=爱&albums_page=2&tracks_page=99" });
    await screen.findByText(/这一页没有结果/);
    expect(within(section("tracks")).queryByText("没有找到匹配的曲目。")).toBeNull();
    await userEvent.click(within(section("tracks")).getByRole("link", { name: "回到第一页" }));
    await loaded();
    const params = new URLSearchParams(history.location.search);
    expect(params.get("q")).toBe("爱");
    expect(params.get("albums_page")).toBe("2");
    expect(params.has("tracks_page")).toBe(false);
  });

  it("换词重置双页，前进后退恢复查询与页码，复制刷新后的地址可独立重建", async () => {
    const { requests } = setup(true);
    const app = renderApp({ path: "/search?q=爱&albums_page=2&tracks_page=3" });
    await screen.findByRole("link", { name: /爱的专辑第2页/ });
    await screen.findByText("爱的曲目第3页");
    const input = screen.getAllByRole("combobox")[0];
    if (input === undefined) throw new Error("缺少搜索框");
    await userEvent.clear(input);
    await userEvent.type(input, "新词{Enter}");
    await screen.findByRole("heading", { name: "搜索“新词”" });
    await waitFor(() => expect(requests("tracks").some((request) => paramsOf(request.path).get("q") === "新词")).toBe(true));
    const nextSearch = new URLSearchParams(app.history.location.search);
    expect(nextSearch.get("q")).toBe("新词");
    expect(nextSearch.has("albums_page")).toBe(false);
    expect(nextSearch.has("tracks_page")).toBe(false);
    await screen.findByRole("link", { name: /新词的专辑第1页/ });
    await screen.findByText("新词的曲目第1页");
    expect(screen.queryByRole("link", { name: /爱的专辑第2页/ })).toBeNull();
    expect(screen.queryByText("爱的曲目第3页")).toBeNull();
    for (const type of ["albums", "tracks"] as const) {
      const params = paramsOf(requests(type).at(-1)?.path ?? "");
      expect(params.get("q")).toBe("新词");
      expect(params.get("page")).toBe("1");
    }
    act(() => app.history.back());
    await screen.findByRole("heading", { name: "搜索“爱”" });
    await screen.findByText("第 2 页");
    await screen.findByText("第 3 页");
    await screen.findByRole("link", { name: /爱的专辑第2页/ });
    await screen.findByText("爱的曲目第3页");
    expect(screen.queryByRole("link", { name: /新词的专辑第1页/ })).toBeNull();
    expect(screen.queryByText("新词的曲目第1页")).toBeNull();
    act(() => app.history.forward());
    await screen.findByRole("heading", { name: "搜索“新词”" });
    await screen.findByRole("link", { name: /新词的专辑第1页/ });
    await screen.findByText("新词的曲目第1页");
    expect(screen.queryByRole("link", { name: /爱的专辑第2页/ })).toBeNull();
    expect(screen.queryByText("爱的曲目第3页")).toBeNull();
    const refreshedPath = app.history.location.href;
    app.view.unmount();
    app.runtime.dispose();
    const before = requests("tracks").length;
    renderApp({ path: refreshedPath });
    await screen.findByRole("link", { name: /新词的专辑第1页/ });
    await screen.findByText("新词的曲目第1页");
    await waitFor(() => expect(requests("tracks")).toHaveLength(before + 1));
    expect(paramsOf(requests("tracks").at(-1)?.path ?? "").get("q")).toBe("新词");
    expect(paramsOf(requests("tracks").at(-1)?.path ?? "").get("page")).toBe("1");
  });


  it.each([
    { field: "credit", role: "composer", original: "久石譲", note: "作曲：久石譲" },
    { field: "label", role: null, original: "Éditions 東京", note: "厂牌：Éditions 東京" },
    { field: "catalog_number", role: null, original: "ＳＲＣＬ-1234", note: "编号：ＳＲＣＬ-1234" },
  ] as const)("专辑卡片和曲目行都显示 $field 的命中原文，不以查询词替代", async ({ field, role, original, note }) => {
    const { api } = setup();
    api.on("GET", path, (request) => {
      const type = paramsOf(request.path).get("type");
      return jsonResponse(200, type === "albums"
        ? searchAlbumPage({ query: "查询别写法", items: [{ ...firstAlbum, match: { ...firstAlbum.match, field, role, original } }] })
        : searchTrackPage({ query: "查询别写法", items: [{ ...firstTrack, match: { ...firstTrack.match, field, role, original } }] }));
    });
    renderApp({ path: "/search?q=查询别写法" });
    await loaded();
    const card = within(section("albums")).getByRole("link", { name: new RegExp(firstAlbum.group.representative.title) });
    const row = within(section("tracks")).getByRole("row", { name: new RegExp(firstTrack.title) });
    expect(within(card).getByText(note)).toBeTruthy();
    expect(within(row).getByText(note)).toBeTruthy();
    expect(within(card).queryByText(/：查询别写法/)).toBeNull();
    expect(within(row).queryByText(/：查询别写法/)).toBeNull();
  });

  it("直接命中已显示的专辑与曲目标题，不再重复展示匹配说明", async () => {
    const { api } = setup();
    const albumTitle = "原样专辑标题";
    const trackTitle = "原样曲目标题";
    api.on("GET", path, (request) => jsonResponse(200, paramsOf(request.path).get("type") === "albums"
      ? searchAlbumPage({ items: [{ ...firstAlbum, group: { ...firstAlbum.group, representative: { ...firstAlbum.group.representative, title: albumTitle } }, match: { ...firstAlbum.match, field: "release_title", role: null, original: albumTitle } }] })
      : searchTrackPage({ items: [{ ...firstTrack, title: trackTitle, match: { ...firstTrack.match, field: "track_title", role: null, original: trackTitle } }] })));
    renderApp({ path: "/search?q=原样" });
    await screen.findByRole("link", { name: new RegExp(albumTitle) });
    await screen.findByText(trackTitle);
    const card = within(section("albums")).getByRole("link", { name: new RegExp(albumTitle) });
    const row = within(section("tracks")).getByRole("row", { name: new RegExp(trackTitle) });
    expect(within(card).getAllByText(albumTitle).filter((element) => element.closest('[aria-hidden="true"]') === null)).toHaveLength(1);
    expect(within(row).getAllByText(trackTitle)).toHaveLength(1);
    expect(within(card).queryByText(`专辑：${albumTitle}`)).toBeNull();
    expect(within(row).queryByText(`曲目：${trackTitle}`)).toBeNull();
  });

  it("曲目结果指向所在专辑的具体版本", async () => {
    setup();
    renderApp({ path: "/search?q=爱" });
    await loaded();
    const row = screen.getByRole("row", { name: new RegExp(firstTrack.title) });
    expect(within(row).getByRole("link").getAttribute("href")).toBe(`/albums/${firstTrack.group_id}?release=${firstTrack.release.id}`);
  });
});
