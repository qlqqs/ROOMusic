import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { TrackSummaryDTO } from "../features/catalog";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { FakeAudio } from "../test/fake-audio";
import { jsonResponse, type RecordedRequest } from "../test/http";
import { personalTrack } from "../test/personal-data";
import { renderApp } from "../test/render-app";

const ratingsPath = "/api/v1/me/ratings";

beforeEach(() => {
  vi.stubGlobal("Audio", class { constructor() { return new FakeAudio(); } });
  vi.spyOn(HTMLMediaElement.prototype, "canPlayType").mockReturnValue("probably");
  sessionStorage.clear();
});
afterEach(() => vi.restoreAllMocks());

type Rated = { track: TrackSummaryDTO; rating: number; ratedAt: string };

/** 评分的服务端替身：列表按 min_rating 与 sort 筛选排序，写入改同一份数据，lookup 也读它。 */
function setup(initial: { index: number; rating: number; ratedAt: string }[]) {
  const api = installFakeApi();
  installFakeSession(api, { current: "listener" });
  installOverviewApi(api);
  const state = new Map<string, Rated>(initial.map(({ index, rating, ratedAt }) => {
    const track = personalTrack(index, { title: `评分曲目 ${index}` });
    return [track.track_id, { track, rating, ratedAt }];
  }));
  const tracks = [...state.values()].map((item) => item.track);
  const listed = (request: RecordedRequest) => {
    const params = new URL(request.path, "http://test").searchParams;
    const min = Number(params.get("min_rating") ?? "1");
    const sort = params.get("sort") ?? "rating";
    return [...state.values()]
      .filter((item) => item.rating >= min)
      .sort((a, b) => (sort === "rating" ? b.rating - a.rating || b.ratedAt.localeCompare(a.ratedAt) : b.ratedAt.localeCompare(a.ratedAt)));
  };
  api.on("GET", `${ratingsPath}/tracks`, (request) => {
    const items = listed(request);
    return jsonResponse(200, { items: items.map((item) => ({ track: item.track, rating: item.rating, rated_at: item.ratedAt })), pagination: { page: 1, page_size: 48, total: items.length } });
  });
  api.on("GET", `${ratingsPath}/tracks/queue`, (request) => {
    const items = listed(request).map((item) => item.track);
    return jsonResponse(200, { items, total: items.length, missing_count: 0, eligible_count: items.length, returned_count: items.length, omitted_count: 0, limit: 10_000, truncated: false });
  });
  api.on("POST", `${ratingsPath}/lookup`, (request) => {
    const body = request.body as { track_ids?: string[] };
    return jsonResponse(200, { tracks: (body.track_ids ?? []).map((id) => ({ track_id: id, rating: state.get(id)?.rating ?? null })), albums: [], artists: [] });
  });
  let clock = 0;
  for (const method of ["PUT", "DELETE"]) {
    api.on(method, /^\/api\/v1\/me\/ratings\/tracks\/[^/]+$/, (request) => {
      const id = request.path.split("/").at(-1) ?? "";
      const item = state.get(id);
      if (item === undefined) return jsonResponse(404, { error: { code: "not_found", message: "不存在" }, request_id: "test" });
      const rating = request.method === "PUT" ? (request.body as { rating: number }).rating : null;
      clock += 1;
      const ratedAt = `2026-11-25T12:00:${String(clock).padStart(2, "0")}Z`;
      if (rating === null) state.delete(id);
      else state.set(id, { ...item, rating, ratedAt });
      return jsonResponse(200, { target_kind: "track", target_id: id, rating, rated_at: rating === null ? null : ratedAt });
    });
  }
  return { api, tracks };
}

const three = [
  { index: 1, rating: 5, ratedAt: "2026-11-20T00:00:00Z" },
  { index: 2, rating: 4, ratedAt: "2026-11-24T00:00:00Z" },
  { index: 3, rating: 2, ratedAt: "2026-11-25T00:00:00Z" },
];

const titles = () => screen.queryAllByRole("rowheader").map((cell) => cell.textContent);

describe("我的评分", () => {
  it("地址里的筛选与排序：读屏能读出当前筛选，列表按它请求并排序", async () => {
    const { api } = setup(three);
    renderApp({ path: "/me/ratings?min=4&sort=recent" });
    const filter = await screen.findByRole("radiogroup", { name: "筛选" });
    expect(within(filter).getByRole("radio", { name: "4 星及以上" }).getAttribute("aria-checked")).toBe("true");
    expect(screen.getByRole("button", { name: "排序：最近评分" })).toBeTruthy();
    await waitFor(() => expect(titles()).toEqual(["评分曲目 2", "评分曲目 1"]));
    const query = new URL(api.requestsTo("GET", `${ratingsPath}/tracks`).at(-1)?.path ?? "", "http://test").searchParams;
    expect(query.get("min_rating")).toBe("4");
    expect(query.get("sort")).toBe("recent");
  });

  it("只用键盘切换筛选、排序与栏，状态写进地址；再点当前那颗星回到全部", async () => {
    setup(three);
    const { history } = renderApp({ path: "/me/ratings" });
    const user = userEvent.setup();
    await waitFor(() => expect(titles()).toEqual(["评分曲目 1", "评分曲目 2", "评分曲目 3"]));
    const all = screen.getByRole("radio", { name: "全部评过分的" });
    all.focus();
    await user.keyboard("{ArrowRight}{ArrowRight}");
    await waitFor(() => expect(history.location.search).toContain("min=3"));
    expect(document.activeElement).toBe(screen.getByRole("radio", { name: "3 星及以上" }));
    await waitFor(() => expect(titles()).toEqual(["评分曲目 1", "评分曲目 2"]));
    await user.keyboard(" ");
    await waitFor(() => expect(history.location.search).not.toContain("min="));
    const sort = screen.getByRole("button", { name: "排序：按评分" });
    sort.focus();
    await user.keyboard("{Enter}");
    await waitFor(() => expect(history.location.search).toContain("sort=recent"));
    await waitFor(() => expect(titles()).toEqual(["评分曲目 3", "评分曲目 2", "评分曲目 1"]));
    const albumsTab = within(screen.getByRole("navigation", { name: "评分分类" })).getByRole("link", { name: "专辑" });
    albumsTab.focus();
    await user.keyboard("{Enter}");
    await waitFor(() => expect(history.location.search).toContain("tab=albums"));
    expect(history.location.search).toContain("sort=recent");
  });

  it("在列表里改评分：这一项按新值重新排位或被筛掉", async () => {
    setup(three);
    renderApp({ path: "/me/ratings?min=4" });
    const user = userEvent.setup();
    await waitFor(() => expect(titles()).toEqual(["评分曲目 1", "评分曲目 2"]));
    const stars = await screen.findByRole("slider", { name: "评分《评分曲目 2》" });
    await waitFor(() => expect(stars.getAttribute("aria-valuetext")).toBe("4 星"));
    stars.focus();
    await user.keyboard("2");
    await waitFor(() => expect(titles()).toEqual(["评分曲目 1"]));
  });

  it("筛选后为空：说明这个星级以上没有，并给“看全部评过分的”", async () => {
    setup([{ index: 3, rating: 2, ratedAt: "2026-11-25T00:00:00Z" }]);
    renderApp({ path: "/me/ratings?min=5" });
    expect(await screen.findByText("没有 5 星及以上的曲目")).toBeTruthy();
    expect(screen.getByRole("link", { name: "看全部评过分的" }).getAttribute("href")).toBe("/me/ratings");
  });

  it("一条评分都没有：说明在哪里打分", async () => {
    setup([]);
    renderApp({ path: "/me/ratings" });
    expect(await screen.findByText("还没有评过分的曲目")).toBeTruthy();
  });

  it("“播放全部”按当前筛选与排序读取候选并开始播放", async () => {
    const { api } = setup(three);
    renderApp({ path: "/me/ratings?min=4&sort=recent" });
    const user = userEvent.setup();
    const play = await screen.findByRole("button", { name: "播放全部" });
    await waitFor(() => expect(play.hasAttribute("disabled")).toBe(false));
    play.focus();
    await user.keyboard("{Enter}");
    await waitFor(() => expect(api.requestsTo("GET", `${ratingsPath}/tracks/queue`)).toHaveLength(1));
    const query = new URL(api.requestsTo("GET", `${ratingsPath}/tracks/queue`)[0]?.path ?? "", "http://test").searchParams;
    expect(query.get("min_rating")).toBe("4");
    expect(query.get("sort")).toBe("recent");
  });

  it("侧栏“我的音乐”在“我的收藏”之后有“我的评分”", async () => {
    setup([]);
    renderApp({ path: "/me/ratings" });
    const [nav] = await screen.findAllByRole("navigation", { name: "主导航" });
    if (nav === undefined) throw new Error("没有主导航");
    const labels = within(nav).getAllByRole("link").map((link) => link.textContent?.trim());
    expect(labels.indexOf("我的评分")).toBe(labels.indexOf("我的收藏") + 1);
  });
});
