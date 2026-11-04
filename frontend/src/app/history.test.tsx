import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { listeningKeys } from "../features/listening";
import type { HistoryItemDTO } from "../features/listening/api/decoders";
import { formatDateTime } from "../shared/lib/time";
import { deferred } from "../test/deferred";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { FakeAudio } from "../test/fake-audio";
import { historyItem, historyPage, historyQueue } from "../test/history-data";
import { jsonResponse, sampleResponse } from "../test/http";
import { renderApp } from "../test/render-app";

const plays = "/api/v1/me/plays";
const tracks = `${plays}/tracks`;
const recent = `${plays}/recent-releases`;
const emptyText = "还没有播放记录。曲目听过一半就会记在这里。";
const first = historyItem(1);
const second = historyItem(2);
const row = (title: string) => screen.getByRole("row", { name: new RegExp(title) });

beforeEach(() => {
  vi.stubGlobal("Audio", class { constructor() { return new FakeAudio(); } });
  vi.spyOn(HTMLMediaElement.prototype, "canPlayType").mockReturnValue("probably");
  sessionStorage.clear();
});
afterEach(() => vi.restoreAllMocks());

function setup(items: HistoryItemDTO[] = [first, second]) {
  const api = installFakeApi();
  const session = installFakeSession(api, { current: "listener" });
  installOverviewApi(api);
  const state = { items: [...items] };
  api.on("GET", tracks, (request) => {
    const params = new URL(request.path, "http://test").searchParams;
    return jsonResponse(200, historyPage(state.items, Number(params.get("page")), Number(params.get("page_size"))));
  });
  api.on("GET", `${plays}/queue`, () => jsonResponse(200, historyQueue(state.items)));
  api.on("GET", recent, () => jsonResponse(200, {
    items: state.items.filter((item) => item.present).slice(0, 8).map((item) => ({ group_id: item.group_id, release: item.release, last_played_at: item.last_played_at, last_track: { id: item.track_id, title: item.title } })),
    has_history: state.items.length > 0,
  }));
  api.on("DELETE", /^\/api\/v1\/me\/plays\/tracks\//, (request) => {
    const id = request.path.split("/").at(-1);
    const deleted = state.items.find((item) => item.track_id === id)?.play_count ?? 0;
    state.items = state.items.filter((item) => item.track_id !== id);
    return jsonResponse(200, { deleted });
  });
  api.on("DELETE", plays, () => {
    const deleted = state.items.reduce((sum, item) => sum + item.play_count, 0);
    state.items = [];
    return jsonResponse(200, { deleted });
  });
  return { api, session, state };
}

async function openRemove(title = first.title) {
  await userEvent.click(within(row(title)).getByRole("button", { name: "更多" }));
  await userEvent.click(screen.getByRole("menuitem", { name: "从历史中移除" }));
  return screen.findByRole("dialog", { name: "从历史中移除这首曲目？" });
}

function seedCaches(runtime: ReturnType<typeof renderApp>["runtime"]) {
  const generation = runtime.session.currentGeneration();
  const mine = [listeningKeys.recentReleases(generation), listeningKeys.queue(generation), listeningKeys.trackPage(generation, { page: 2, pageSize: 50 })];
  const other = [listeningKeys.recentReleases(generation + 1), listeningKeys.queue(generation + 1), listeningKeys.trackPage(generation + 1, { page: 2, pageSize: 50 })];
  for (const key of [...mine, ...other]) runtime.queryClient.setQueryData(key, { status: 200, data: historyPage([first, second], 2) });
  return { mine, other };
}

describe("我的音乐历史页", () => {
  it("加载时不伪装为空态；空态说明计次规则，提供去专辑页的链接", async () => {
    const { api } = setup([]);
    const pending = deferred<Response>();
    api.on("GET", tracks, () => pending.promise);
    renderApp({ path: "/me/history" });
    expect(await screen.findByText("正在加载播放历史")).toBeTruthy();
    expect(screen.queryByText(emptyText)).toBeNull();
    pending.resolve(jsonResponse(200, historyPage([])));
    expect(await screen.findByText(emptyText, { exact: false })).toBeTruthy();
    expect(screen.getByRole("link", { name: "去专辑页挑一张来听" }).getAttribute("href")).toBe("/albums");
    expect(screen.queryByRole("button", { name: "清除全部历史" })).toBeNull();
    expect(screen.queryByRole("button", { name: "播放全部" })).toBeNull();
  });

  it("读取失败显示请求 ID，手动重试恢复，不把失败当作空历史", async () => {
    const { api } = setup();
    api.on("GET", tracks, () => jsonResponse(500, { error: { code: "internal_error", message: "历史读取失败" }, request_id: "history-error" }));
    renderApp({ path: "/me/history" });
    expect((await screen.findByRole("alert")).textContent).toContain("history-error");
    expect(screen.queryByText(emptyText)).toBeNull();
    api.on("GET", tracks, () => jsonResponse(200, historyPage([first])));
    await userEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByRole("row", { name: new RegExp(first.title) })).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("遵循服务端顺序显示次数、最近时间和当前发行链接，不提供次数排序", async () => {
    setup([second, first]);
    renderApp({ path: "/me/history" });
    const table = await screen.findByRole("table", { name: "播放历史" });
    expect(within(table).getAllByRole("rowheader").map((cell) => cell.textContent)).toEqual([`${second.title}${second.artist}`, `${first.title}${first.artist}`]);
    expect(within(row(first.title)).getByRole("link").getAttribute("href")).toBe(`/albums/${first.group_id}?release=${first.release.id}`);
    expect(within(row(first.title)).getByText(String(first.play_count))).toBeTruthy();
    expect(row(first.title).querySelector("time")?.textContent).toBe(formatDateTime(first.last_played_at));
    expect(row(first.title).querySelector("time")?.getAttribute("datetime")).toBe(first.last_played_at);
    expect(screen.queryByRole("button", { name: /排序/ })).toBeNull();
  });

  it("地址恢复第 2 页；翻页写地址、聚焦结果标题，回第一页不保留 page", async () => {
    const items = Array.from({ length: 51 }, (_, index) => historyItem(index + 1));
    const { api } = setup(items);
    const { history } = renderApp({ path: "/me/history?page=2" });
    expect(await screen.findByText("历史曲目 51", { exact: true })).toBeTruthy();
    expect(api.requestsTo("GET", tracks)[0]?.path).toBe(`${tracks}?page=2&page_size=50`);
    expect(screen.queryByText("历史曲目 1", { exact: true })).toBeNull();
    await userEvent.click(screen.getByRole("link", { name: "上一页" }));
    await waitFor(() => expect(new URLSearchParams(history.location.search).has("page")).toBe(false));
    expect(await screen.findByText("历史曲目 1", { exact: true })).toBeTruthy();
    expect(document.activeElement).toBe(screen.getByRole("heading", { name: "播放历史" }));
    await userEvent.click(screen.getByRole("link", { name: "下一页" }));
    await waitFor(() => expect(new URLSearchParams(history.location.search).get("page")).toBe("2"));
  });

  it("超末页保留地址并说明总数，回到第一页可恢复记录", async () => {
    setup();
    const { history } = renderApp({ path: "/me/history?page=9" });
    expect(await screen.findByText(/这一页没有记录。/)).toBeTruthy();
    expect(new URLSearchParams(history.location.search).get("page")).toBe("9");
    expect(screen.queryByText(emptyText)).toBeNull();
    await userEvent.click(screen.getByRole("link", { name: "回到第一页" }));
    expect(await screen.findByRole("row", { name: new RegExp(first.title) })).toBeTruthy();
    expect(new URLSearchParams(history.location.search).has("page")).toBe(false);
  });

  it.each(["0", "-1", "1.5", "bad"])("非法页码 %s 使用第一页", async (page) => {
    const { api } = setup();
    renderApp({ path: `/me/history?page=${page}` });
    await screen.findByRole("table");
    expect(api.requestsTo("GET", tracks)[0]?.path).toBe(`${tracks}?page=1&page_size=50`);
  });

  it("缺失行显示文件缺失且专辑不是链接：播放及入队禁用，移除仍可用；刷新后可恢复当前发行", async () => {
    const { state } = setup([{ ...first, present: false }]);
    const { runtime } = renderApp({ path: "/me/history" });
    await screen.findByText("文件缺失");
    expect(within(row(first.title)).getByRole("button", { name: `播放 ${first.title}` }).hasAttribute("disabled")).toBe(true);
    expect(within(row(first.title)).queryByRole("link")).toBeNull();
    expect(within(row(first.title)).getByText(first.release.title)).toBeTruthy();
    await userEvent.click(within(row(first.title)).getByRole("button", { name: "更多" }));
    for (const name of ["下一首播放", "加入队列"]) expect(screen.getByRole("menuitem", { name }).hasAttribute("disabled")).toBe(true);
    expect(screen.getByRole("menuitem", { name: "从历史中移除" }).hasAttribute("disabled")).toBe(false);
    await userEvent.keyboard("{Escape}");
    state.items = [{ ...first, group_id: second.group_id, release: second.release }];
    await act(async () => runtime.queryClient.invalidateQueries({ queryKey: listeningKeys.all(runtime.session.currentGeneration()) }));
    await waitFor(() => expect(screen.queryByText("文件缺失")).toBeNull());
    expect(within(row(first.title)).getByRole("link").getAttribute("href")).toBe(`/albums/${second.group_id}?release=${second.release.id}`);
    expect(within(row(first.title)).getByRole("button", { name: `播放 ${first.title}` }).hasAttribute("disabled")).toBe(false);
  });

  it("移除先确认全部次数和本人范围，取消不发请求；成功立即去掉行并失效本人的全部历史缓存", async () => {
    const { api } = setup();
    const { runtime } = renderApp({ path: "/me/history" });
    await screen.findByRole("table");
    const caches = seedCaches(runtime);
    let dialog = await openRemove();
    expect(within(dialog).getByText(/的播放记录会被移除，不能恢复。/)).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("button", { name: "取消" }));
    expect(api.requestsTo("DELETE", `${tracks}/${first.track_id}`)).toHaveLength(0);
    expect(row(first.title)).toBeTruthy();
    dialog = await openRemove();
    const refetch = deferred<Response>();
    api.on("GET", tracks, () => refetch.promise);
    await userEvent.click(within(dialog).getByRole("button", { name: "从历史中移除" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(screen.queryByRole("row", { name: new RegExp(first.title) })).toBeNull();
    expect(row(second.title)).toBeTruthy();
    expect(screen.getByText(`已从历史中移除《${first.title}》。`)).toBeTruthy();
    expect(document.activeElement).toBe(screen.getByRole("heading", { name: "播放历史" }));
    for (const key of caches.mine) expect(runtime.queryClient.getQueryState(key)?.isInvalidated).toBe(true);
    for (const key of caches.other) expect(runtime.queryClient.getQueryState(key)?.isInvalidated).toBe(false);
    expect(api.requestsTo("DELETE", `${tracks}/${first.track_id}`)).toHaveLength(1);
    refetch.resolve(jsonResponse(200, historyPage([second])));
    await userEvent.click(screen.getByRole("link", { name: "概览" }));
    const panel = await screen.findByRole("region", { name: "最近播放" });
    expect(await within(panel).findByRole("link", { name: new RegExp(second.release.title) })).toBeTruthy();
    expect(within(panel).queryByRole("link", { name: new RegExp(first.release.title) })).toBeNull();
  });

  it("移除失败保留行，允许用同一个 DELETE 重试", async () => {
    const { api } = setup();
    let attempts = 0;
    api.on("DELETE", `${tracks}/${first.track_id}`, () => ++attempts === 1 ? sampleResponse("errors/database-unavailable") : jsonResponse(200, { deleted: 3 }));
    renderApp({ path: "/me/history" });
    await screen.findByRole("table");
    const dialog = await openRemove();
    await userEvent.click(within(dialog).getByRole("button", { name: "从历史中移除" }));
    expect(await within(dialog).findByRole("alert")).toBeTruthy();
    expect(screen.getByText(first.title)).toBeTruthy();
    api.on("GET", tracks, () => jsonResponse(200, historyPage([second])));
    await userEvent.click(within(dialog).getByRole("button", { name: "重试移除" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(screen.queryByRole("row", { name: new RegExp(first.title) })).toBeNull();
    expect(attempts).toBe(2);
  });

  it("清除需确认：取消不请求，失败可重试；成功立即清空并只失效本人历史缓存", async () => {
    const { api } = setup();
    const { runtime } = renderApp({ path: "/me/history" });
    await screen.findByRole("table");
    const caches = seedCaches(runtime);
    await userEvent.click(screen.getByRole("button", { name: "清除全部历史" }));
    let dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("全部播放记录都会被清除，不能恢复。")).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("button", { name: "取消" }));
    expect(api.requestsTo("DELETE", plays)).toHaveLength(0);
    api.on("DELETE", plays, () => sampleResponse("errors/database-unavailable"));
    await userEvent.click(screen.getByRole("button", { name: "清除全部历史" }));
    dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "清除播放历史" }));
    expect(await within(dialog).findByRole("alert")).toBeTruthy();
    expect(screen.getByText(first.title)).toBeTruthy();
    api.on("DELETE", plays, () => jsonResponse(200, { deleted: 6 }));
    const refetch = deferred<Response>();
    api.on("GET", tracks, () => refetch.promise);
    await userEvent.click(within(dialog).getByRole("button", { name: "重试清除" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(screen.getByText("已清除你的播放历史。")).toBeTruthy();
    expect(screen.getByText(emptyText, { exact: false })).toBeTruthy();
    expect(screen.queryByRole("table")).toBeNull();
    for (const key of caches.mine) expect(runtime.queryClient.getQueryState(key)?.isInvalidated).toBe(true);
    for (const key of caches.other) expect(runtime.queryClient.getQueryState(key)?.isInvalidated).toBe(false);
    expect(api.requestsTo("DELETE", plays)).toHaveLength(2);
    refetch.resolve(jsonResponse(200, historyPage([])));
  });

  it("/me 替换到收藏页，页签顺序为收藏、歌单、历史，历史仍可达", async () => {
    setup();
    const { history } = renderApp({ path: "/me" });
    await waitFor(() => expect(history.location.pathname).toBe("/me/favorites"));
    expect(history.length).toBe(1);
    expect(screen.getByRole("link", { name: "我的音乐" }).getAttribute("href")).toBe("/me");
    const tabs = within(screen.getByRole("navigation", { name: "我的音乐" }));
    expect(tabs.getAllByRole("link").map((link) => link.textContent)).toEqual(["收藏", "歌单", "历史"]);
    expect(tabs.getByRole("link", { name: "收藏" }).getAttribute("aria-current")).toBe("page");
    expect(tabs.getByRole("link", { name: "历史" }).getAttribute("aria-current")).toBeNull();
    await userEvent.click(tabs.getByRole("link", { name: "历史" }));
    await screen.findByRole("table", { name: "播放历史" });
    expect(history.location.pathname).toBe("/me/history");
    expect(tabs.getByRole("link", { name: "历史" }).getAttribute("aria-current")).toBe("page");
  });

  it("旧用户在途读取被取消，迟到历史不渲染到新会话", async () => {
    const { api, session } = setup();
    const pending = deferred<Response>();
    let reads = 0;
    api.on("GET", tracks, () => ++reads === 1 ? pending.promise : jsonResponse(200, historyPage([second])));
    const { runtime } = renderApp({ path: "/me/history" });
    await screen.findByText("正在加载播放历史");
    const oldGeneration = runtime.session.currentGeneration();
    const signal = api.requestsTo("GET", tracks)[0]?.signal;
    session.current = "curator";
    await act(async () => runtime.session.recheck());
    await screen.findByRole("row", { name: new RegExp(second.title) });
    expect(signal?.aborted).toBe(true);
    await act(async () => pending.resolve(jsonResponse(200, historyPage([first]))));
    expect(row(second.title)).toBeTruthy();
    expect(screen.queryByRole("row", { name: new RegExp(first.title) })).toBeNull();
    expect(runtime.queryClient.getQueriesData({ queryKey: listeningKeys.all(oldGeneration) })).toEqual([]);
  });

  it.each(["remove", "clear"] as const)("%s 的迟到成功不能改变下一位用户的历史或显示上一人的提示", async (command) => {
    const { api, session, state } = setup();
    const { runtime } = renderApp({ path: "/me/history" });
    await screen.findByRole("table");
    const oldGeneration = runtime.session.currentGeneration();
    const pending = deferred<Response>();
    api.on("DELETE", command === "remove" ? `${tracks}/${first.track_id}` : plays, () => pending.promise);
    if (command === "remove") await openRemove();
    else await userEvent.click(screen.getByRole("button", { name: "清除全部历史" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: command === "remove" ? "从历史中移除" : "清除播放历史" }));
    await waitFor(() => expect(api.requestsTo("DELETE", command === "remove" ? `${tracks}/${first.track_id}` : plays)).toHaveLength(1));
    state.items = [second];
    session.current = "curator";
    await act(async () => runtime.session.recheck());
    await screen.findByRole("row", { name: new RegExp(second.title) });
    expect(runtime.session.currentGeneration()).not.toBe(oldGeneration);
    await act(async () => pending.resolve(jsonResponse(200, { deleted: 3 })));
    expect(row(second.title)).toBeTruthy();
    expect(screen.queryByText(/已从历史中移除|已清除你的播放历史/)).toBeNull();
    expect(screen.queryByRole("row", { name: new RegExp(first.title) })).toBeNull();
  });
});
