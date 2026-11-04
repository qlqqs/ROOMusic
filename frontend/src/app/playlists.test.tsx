import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { decodePlaylistDetail, type ItemMovedDTO, type ItemRefDTO, type ItemRestoredDTO, type PlaylistItemDTO, type PlaylistStateDTO, type PlaylistSummaryDTO } from "../features/personal/api/decoders";
import { personalKeys } from "../features/personal/query-keys";
import { decode as d } from "../shared/api";
import { sampleBody } from "../test/api-samples";
import { deferred } from "../test/deferred";
import { installFakeApi, installFakeSession } from "../test/fake-api";
import { FakeAudio } from "../test/fake-audio";
import { historyItem } from "../test/history-data";
import { jsonResponse, type RecordedRequest } from "../test/http";
import { renderApp } from "../test/render-app";

const base = decodePlaylistDetail(sampleBody("playlists/detail")).playlist;
const playlistsPath = "/api/v1/me/playlists";
const playlistPath = `${playlistsPath}/${base.id}`;
const itemsPath = `${playlistPath}/items`;
const pagePath = `/me/playlists/${base.id}`;
const undoToken = "00000000-0000-4000-8000-00000000e001";
const decodeLookup = d.defineDecoder(d.object({ track_ids: d.array(d.uuid(), { max: 10_000 }), group_ids: d.array(d.uuid(), { max: 1_000 }) }));
const decodeMove = d.defineDecoder(d.object({ expected_revision: d.integer({ min: 1 }), action: d.oneOf(["up", "down", "first", "last"] as const) }));
const itemOf = (index: number): PlaylistItemDTO => ({ id: `00000000-0000-4000-8000-d${String(index).padStart(11, "0")}`, position: index, added_at: base.created_at, track: historyItem(index, { title: `歌单曲目 ${index}` }) });
const itemRef = (item: PlaylistItemDTO): ItemRefDTO => ({ id: item.id, track_id: item.track.track_id, position: item.position, added_at: item.added_at });
const reindex = (items: readonly PlaylistItemDTO[]) => items.map((item, index) => ({ ...item, position: index + 1 }));
const first = itemOf(1);
const second = itemOf(2);
const third = itemOf(3);

beforeEach(() => {
  sessionStorage.clear();
  vi.stubGlobal("Audio", class { constructor() { return new FakeAudio(); } });
  vi.spyOn(HTMLMediaElement.prototype, "canPlayType").mockReturnValue("probably");
});
afterEach(() => vi.restoreAllMocks());

/** 本文件只关心页面如何消费服务端位置与回执，不复制后端的撤销选位算法。 */
function setup(count = 3) {
  const api = installFakeApi();
  installFakeSession(api, { current: "listener" });
  const state = { items: Array.from({ length: count }, (_, index) => itemOf(index + 1)), revision: 4, name: base.name, total: null as number | null, deleted: false };
  const playlist = (): PlaylistStateDTO => ({ id: base.id, name: state.name, revision: state.revision, track_count: state.total ?? state.items.length, track_limit: 10_000, over_limit_count: Math.max((state.total ?? state.items.length) - 10_000, 0), created_at: base.created_at, updated_at: base.updated_at });
  const summary = (): PlaylistSummaryDTO => ({ ...base, ...playlist(), cover_groups: [], duration_seconds: state.items.reduce((sum, item) => sum + (item.track.duration_seconds ?? 0), 0), unknown_duration_count: state.items.filter((item) => item.track.duration_seconds === null).length, missing_count: state.items.filter((item) => !item.track.present).length });
  const readItems = (request: RecordedRequest): Response => {
    const params = new URL(request.path, "http://test").searchParams;
    const page = Number(params.get("page") ?? 1);
    const size = Number(params.get("page_size") ?? 50);
    if (params.has("revision") && Number(params.get("revision")) !== state.revision) return jsonResponse(409, { error: { code: "revision_conflict", message: "歌单已改变" }, request_id: "playlist-page-conflict" });
    return jsonResponse(200, { playlist: playlist(), items: state.items.slice((page - 1) * size, page * size), pagination: { page, page_size: size, total: state.total ?? state.items.length }, page_after_item_id: state.items[page * size]?.id ?? null });
  };
  api.on("GET", playlistPath, () => jsonResponse(200, { playlist: summary() }));
  api.on("GET", itemsPath, readItems);
  api.on("GET", playlistsPath, () => jsonResponse(200, { items: state.deleted ? [] : [summary()], pagination: { page: 1, page_size: 20, total: state.deleted ? 0 : 1 } }));
  api.on("POST", "/api/v1/me/favorites/lookup", (request) => {
    const ids = decodeLookup(request.body);
    return jsonResponse(200, { revision: 0, tracks: ids.track_ids.map((track_id) => ({ track_id, favorited: false })), albums: ids.group_ids.map((group_id) => ({ group_id, favorited: false })) });
  });
  const move = (id: string, action: "up" | "down" | "first" | "last"): ItemMovedDTO => {
    const from = state.items.findIndex((item) => item.id === id);
    const target = action === "first" ? 0 : action === "last" ? state.items.length - 1 : action === "up" ? Math.max(0, from - 1) : Math.min(state.items.length - 1, from + 1);
    const moving = state.items[from];
    if (moving === undefined) throw new Error("测试条目不存在");
    const next = state.items.filter((item) => item.id !== id);
    next.splice(target, 0, moving);
    state.items = reindex(next);
    state.revision += 1;
    return { playlist: playlist(), moved: { ...itemRef(moving), position: target + 1 }, changed: from !== target };
  };
  api.on("POST", /^\/api\/v1\/me\/playlists\/[^/]+\/items\/[^/]+\/move$/, (request) => {
    const body = decodeMove(request.body);
    if (body.expected_revision !== state.revision) return jsonResponse(409, { error: { code: "revision_conflict", message: "歌单已改变" }, request_id: "playlist-move-conflict" });
    return jsonResponse(200, move(request.path.split("/").at(-2) ?? "", body.action));
  });
  api.on("DELETE", /^\/api\/v1\/me\/playlists\/[^/]+\/items\/[^/]+$/, (request) => {
    const item = state.items.find((candidate) => candidate.id === request.path.split("/").at(-1));
    if (item === undefined) throw new Error("测试移除目标不存在");
    expect(request.body).toEqual({ expected_revision: state.revision });
    state.items = reindex(state.items.filter((candidate) => candidate.id !== item.id));
    state.revision += 1;
    return jsonResponse(200, { playlist: playlist(), removed: itemRef(item), undo: { token: undoToken, removal_revision: state.revision } });
  });
  api.on("DELETE", playlistPath, (request) => {
    expect(request.body).toEqual({ expected_revision: state.revision });
    state.deleted = true;
    return jsonResponse(200, { playlist_id: base.id, deleted: true });
  });
  return { api, state, playlist, summary, move, readItems };
}

function rowOf(title: string) {
  const row = screen.getByText(title, { exact: true }).closest("li");
  if (row === null) throw new Error(`缺少曲目行：${title}`);
  return within(row);
}
function titles() {
  return screen.getAllByRole("button", { name: /^拖动《/ }).map((button) => button.getAttribute("aria-label"));
}
async function openActions(title: string) {
  await screen.findByText(title, { exact: true });
  await userEvent.click(rowOf(title).getByRole("button", { name: "更多" }));
}
async function remove(title: string) {
  await openActions(title);
  await userEvent.click(screen.getByRole("menuitem", { name: "从歌单移除" }));
  await screen.findByText(`已移除《${title}》`);
  await waitFor(() => expect(screen.queryByText(title, { exact: true })).toBeNull());
}

describe("歌单详情的顺序、冲突与稳定重试", () => {
  it("四个菜单移动在整个歌单的首尾禁用，中间行均可用", async () => {
    setup();
    renderApp({ path: pagePath });
    for (const [title, disabled] of [[first.track.title, ["上移", "移到最前"]], [second.track.title, []], [third.track.title, ["下移", "移到最后"]]] as const) {
      await openActions(title);
      for (const name of ["上移", "下移", "移到最前", "移到最后"]) expect(screen.getByRole("menuitem", { name }).hasAttribute("disabled")).toBe(disabled.some((label) => label === name));
      await userEvent.keyboard("{Escape}");
    }
  });

  it("仅一首的歌单四个移动项全部禁用，移除仍可用", async () => {
    setup(1);
    renderApp({ path: pagePath });
    await openActions(first.track.title);
    for (const name of ["上移", "下移", "移到最前", "移到最后"]) expect(screen.getByRole("menuitem", { name }).hasAttribute("disabled")).toBe(true);
    expect(screen.getByRole("menuitem", { name: "从歌单移除" }).hasAttribute("disabled")).toBe(false);
  });

  it.each([
    ["上移", "up", [2, 1, 3]],
    ["下移", "down", [1, 3, 2]],
    ["移到最前", "first", [2, 1, 3]],
    ["移到最后", "last", [1, 3, 2]],
  ] as const)("菜单%s发送动作与当前 revision，重读显示服务端顺序", async (label, action, order) => {
    const { api } = setup();
    const { runtime } = renderApp({ path: pagePath });
    await openActions(second.track.title);
    await userEvent.click(screen.getByRole("menuitem", { name: label }));
    await screen.findByText(new RegExp(`《${second.track.title}》已移到第`));
    await waitFor(() => expect(titles()).toEqual(order.map((index) => `拖动《歌单曲目 ${index}》调整顺序`)));
    const request = api.requestsTo("POST", `${itemsPath}/${second.id}/move`)[0];
    expect(request?.body).toEqual({ expected_revision: 4, action });
    expect(request?.headers.get("Idempotency-Key")).toMatch(/^[0-9a-f-]{36}$/);
    await act(async () => { await runtime.queryClient.invalidateQueries({ queryKey: personalKeys.playlists(runtime.session.currentGeneration()) }); });
    expect(titles()).toEqual(order.map((index) => `拖动《歌单曲目 ${index}》调整顺序`));
  });

  it("已提交但响应丢失后，重试保持同键同请求，不拿重读的新 revision 重建意图", async () => {
    const { api, move } = setup();
    let receipt: ItemMovedDTO | null = null;
    api.on("POST", `${itemsPath}/${first.id}/move`, () => {
      if (receipt === null) {
        receipt = move(first.id, "down");
        throw new TypeError("测试：服务端已提交，响应在返回途中丢失");
      }
      return jsonResponse(200, receipt);
    });
    renderApp({ path: pagePath });
    await openActions(first.track.title);
    await userEvent.click(screen.getByRole("menuitem", { name: "下移" }));
    await screen.findByRole("alert");
    await waitFor(() => expect(titles()).toEqual([2, 1, 3].map((index) => `拖动《歌单曲目 ${index}》调整顺序`)));
    await userEvent.click(screen.getByRole("button", { name: "重试" }));
    await screen.findByText(`《${first.track.title}》已移到第 2 首。`);
    const requests = api.requestsTo("POST", `${itemsPath}/${first.id}/move`);
    expect(requests).toHaveLength(2);
    expect(requests[0]?.body).toEqual({ expected_revision: 4, action: "down" });
    expect(requests[1]?.body).toEqual(requests[0]?.body);
    expect(requests[0]?.headers.get("Idempotency-Key")).toMatch(/^[0-9a-f-]{36}$/);
    expect(requests[1]?.headers.get("Idempotency-Key")).toBe(requests[0]?.headers.get("Idempotency-Key"));
    expect(titles()).toEqual([2, 1, 3].map((index) => `拖动《歌单曲目 ${index}》调整顺序`));
  });

  it("409 显示冲突提示并重读，不自动将旧移动套用到新顺序", async () => {
    const { api, state } = setup();
    api.on("POST", `${itemsPath}/${first.id}/move`, () => {
      state.items = reindex([third, first, second]);
      state.revision = 8;
      return jsonResponse(409, { error: { code: "revision_conflict", message: "版本不一致" }, request_id: "move-conflict" });
    });
    renderApp({ path: pagePath });
    await openActions(first.track.title);
    await userEvent.click(screen.getByRole("menuitem", { name: "下移" }));
    expect((await screen.findByRole("alert")).textContent).toContain("歌单刚在别处改过，已重新读取，请再试一次。");
    await waitFor(() => expect(titles()).toEqual([3, 1, 2].map((index) => `拖动《歌单曲目 ${index}》调整顺序`)));
    expect(api.requestsTo("POST", `${itemsPath}/${first.id}/move`)).toHaveLength(1);
    expect(api.requestsTo("GET", playlistPath).length).toBeGreaterThan(1);
    expect(api.requestsTo("GET", itemsPath).length).toBeGreaterThan(1);
    expect(screen.queryByRole("button", { name: "重试" })).toBeNull();
  });

  it("分页遇到 409 丢掉旧 revision 后重读，末页菜单仍按全歌单位置禁用", async () => {
    const { api, state } = setup(51);
    renderApp({ path: pagePath });
    await screen.findByText(first.track.title, { exact: true });
    state.revision = 9;
    await userEvent.click(screen.getByRole("link", { name: "下一页" }));
    await screen.findByText("歌单曲目 51", { exact: true });
    const reads = api.requestsTo("GET", itemsPath).filter((request) => new URL(request.path, "http://test").searchParams.get("page") === "2");
    expect(reads.map((request) => new URL(request.path, "http://test").searchParams.get("revision"))).toEqual(["4", null]);
    await openActions("歌单曲目 51");
    expect(screen.getByRole("menuitem", { name: "上移" }).hasAttribute("disabled")).toBe(false);
    expect(screen.getByRole("menuitem", { name: "移到最前" }).hasAttribute("disabled")).toBe(false);
    expect(screen.getByRole("menuitem", { name: "下移" }).hasAttribute("disabled")).toBe(true);
    expect(screen.getByRole("menuitem", { name: "移到最后" }).hasAttribute("disabled")).toBe(true);
  });
});

describe("歌单移除与服务端撤销位置", () => {
  it("移除无需确认，成功后焦点到标题；撤销只发 token 并按服务端位置恢复", async () => {
    const { api, state, playlist } = setup();
    api.on("POST", `${playlistPath}/restore`, (request) => {
      expect(request.body).toEqual({ token: undoToken });
      state.items = reindex([third, second, first]);
      state.revision += 1;
      return jsonResponse(200, { playlist: playlist(), outcome: "restored", item: { ...itemRef(second), position: 2 }, placement: "after_previous" });
    });
    renderApp({ path: pagePath });
    await remove(second.track.title);
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(screen.getByRole("heading", { name: base.name }));
    expect(api.requestsTo("DELETE", `${itemsPath}/${second.id}`)).toHaveLength(1);
    await userEvent.click(screen.getByRole("button", { name: "撤销" }));
    await screen.findByText(`已放回《${second.track.title}》，在第 2 首。`);
    await waitFor(() => expect(titles()).toEqual([3, 2, 1].map((index) => `拖动《歌单曲目 ${index}》调整顺序`)));
    expect(api.requestsTo("POST", `${playlistPath}/restore`)[0]?.body).toEqual({ token: undoToken });
    expect(screen.queryByRole("button", { name: "撤销" })).toBeNull();
  });

  it("移除后外部改序与聚焦重读不使撤销过期，服务端放到第 51 首时自动换页", async () => {
    const { api, state, playlist } = setup(51);
    api.on("POST", `${playlistPath}/restore`, (request) => {
      expect(request.body).toEqual({ token: undoToken });
      state.items = reindex([...state.items, second]);
      state.revision += 1;
      return jsonResponse(200, { playlist: playlist(), outcome: "restored", item: { ...itemRef(second), position: 51 }, placement: "after_previous" });
    });
    const { history, runtime } = renderApp({ path: pagePath });
    await remove(second.track.title);
    state.items = reindex([...state.items].reverse());
    state.revision += 1;
    await act(async () => { window.dispatchEvent(new Event("focus")); });
    await waitFor(() => expect(runtime.queryClient.isFetching()).toBe(0));
    expect(screen.getByRole("button", { name: "撤销" }).hasAttribute("disabled")).toBe(false);
    await userEvent.click(screen.getByRole("button", { name: "撤销" }));
    await screen.findByText(`已放回《${second.track.title}》，在第 51 首。`);
    await waitFor(() => expect(new URLSearchParams(history.location.search).get("page")).toBe("2"));
    await screen.findByText(second.track.title, { exact: true });
    expect(titles()).toEqual([`拖动《${second.track.title}》调整顺序`]);
    expect(api.requestsTo("POST", `${playlistPath}/restore`)).toHaveLength(1);
    const lastRead = api.requestsTo("GET", itemsPath).at(-1);
    expect(new URL(lastRead?.path ?? "", "http://test").searchParams.get("revision")).toBeNull();
  });

  it.each([
    ["restored", "original", 2],
    ["restored", "original_index", 2],
    ["restored", "end", 3],
    ["already_present", "unchanged", 1],
  ] as const)("撤销结果 %s/%s 尊重服务端位置，不在本地重复插入", async (outcome, placement, position) => {
    const { api, state, playlist } = setup();
    api.on("POST", `${playlistPath}/restore`, () => {
      const next = state.items.filter((item) => item.id !== second.id);
      next.splice(position - 1, 0, second);
      state.items = reindex(next);
      state.revision += 1;
      const result: ItemRestoredDTO = { playlist: playlist(), outcome, placement, item: { ...itemRef(second), position } };
      return jsonResponse(200, result);
    });
    renderApp({ path: pagePath });
    await remove(second.track.title);
    await userEvent.click(screen.getByRole("button", { name: "撤销" }));
    await screen.findByText(outcome === "already_present" ? `《${second.track.title}》已在歌单里。` : `已放回《${second.track.title}》，在第 ${position} 首。`);
    await waitFor(() => expect(titles()[position - 1]).toBe(`拖动《${second.track.title}》调整顺序`));
    expect(titles()).toHaveLength(3);
    expect(screen.getAllByText(second.track.title, { exact: true })).toHaveLength(1);
  });

  it("最近一次移除覆盖旧撤销按钮，恢复请求只带最新 token", async () => {
    const { api, state, playlist } = setup();
    const latestToken = "00000000-0000-4000-8000-00000000e002";
    api.on("DELETE", `${itemsPath}/${third.id}`, () => {
      state.items = reindex(state.items.filter((item) => item.id !== third.id));
      state.revision += 1;
      return jsonResponse(200, { playlist: playlist(), removed: { ...itemRef(third), position: 2 }, undo: { token: latestToken, removal_revision: state.revision } });
    });
    api.on("POST", `${playlistPath}/restore`, () => {
      state.items = reindex([first, third]);
      state.revision += 1;
      return jsonResponse(200, { playlist: playlist(), outcome: "restored", item: { ...itemRef(third), position: 2 }, placement: "original" });
    });
    renderApp({ path: pagePath });
    await remove(second.track.title);
    await remove(third.track.title);
    expect(screen.queryByText(`已移除《${second.track.title}》`)).toBeNull();
    expect(screen.getAllByRole("button", { name: "撤销" })).toHaveLength(1);
    await userEvent.click(screen.getByRole("button", { name: "撤销" }));
    await screen.findByText(`已放回《${third.track.title}》，在第 2 首。`);
    expect(api.requestsTo("POST", `${playlistPath}/restore`)[0]?.body).toEqual({ token: latestToken });
  });
});

describe("歌单的容量、删除与表单", () => {
  it("暂时超限显示准确提示，不禁移动和移除", async () => {
    const { state } = setup(51);
    state.total = 10_003;
    renderApp({ path: pagePath });
    await screen.findByText("歌单比上限多 3 首，先移除一些才能再添加。");
    await openActions(second.track.title);
    for (const name of ["上移", "下移", "移到最前", "移到最后", "从歌单移除"]) expect(screen.getByRole("menuitem", { name }).hasAttribute("disabled")).toBe(false);
  });

  it("删除使用准确不可恢复文案，取消不发请求，确认后返回歌单空态", async () => {
    const { api } = setup();
    const { history } = renderApp({ path: pagePath });
    await screen.findByText(first.track.title, { exact: true });
    await userEvent.click(screen.getByRole("button", { name: "删除歌单" }));
    let dialog = await screen.findByRole("dialog", { name: "删除歌单？" });
    expect(within(dialog).getByText(`歌单《${base.name}》会被删除，不能恢复。`)).toBeTruthy();
    expect(api.requestsTo("DELETE", playlistPath)).toHaveLength(0);
    await userEvent.click(within(dialog).getByRole("button", { name: "取消" }));
    expect(api.requestsTo("DELETE", playlistPath)).toHaveLength(0);
    await userEvent.click(screen.getByRole("button", { name: "删除歌单" }));
    dialog = await screen.findByRole("dialog", { name: "删除歌单？" });
    await userEvent.click(within(dialog).getByRole("button", { name: "删除歌单" }));
    await screen.findByText("还没有歌单。");
    expect(history.location.pathname).toBe("/me/playlists");
    expect(api.requestsTo("DELETE", playlistPath)).toHaveLength(1);
    expect(api.requestsTo("DELETE", playlistPath)[0]?.body).toEqual({ expected_revision: 4 });
  });

  it("曲目缺失仍可排序与移除，但播放和专辑链接不可用", async () => {
    const { state } = setup();
    state.items = state.items.map((item) => item.id === second.id ? { ...item, track: { ...item.track, present: false } } : item);
    renderApp({ path: pagePath });
    await screen.findByText("文件缺失");
    expect(rowOf(second.track.title).queryByRole("link")).toBeNull();
    expect(screen.getByText(second.track.title, { exact: true }).closest("li")?.hasAttribute("data-missing")).toBe(true);
    expect(rowOf(second.track.title).getByRole("button", { name: `播放 ${second.track.title}` }).hasAttribute("disabled")).toBe(true);
    await openActions(second.track.title);
    for (const name of ["上移", "下移", "从歌单移除"]) expect(screen.getByRole("menuitem", { name }).hasAttribute("disabled")).toBe(false);
    for (const name of ["下一首播放", "加入队列"]) expect(screen.getByRole("menuitem", { name }).hasAttribute("disabled")).toBe(true);
  });

  it("新建的输入校验按码点计数，空白、101 个码点与控制字符均不发请求", async () => {
    const { api } = setup();
    renderApp({ path: "/me/playlists" });
    await userEvent.click(await screen.findByRole("button", { name: "新建歌单" }));
    const dialog = await screen.findByRole("dialog", { name: "新建歌单" });
    const input = within(dialog).getByRole("textbox", { name: "名字" });
    for (const value of ["   ", "🎵".repeat(101), "含\u0001控制字符"]) {
      fireEvent.change(input, { target: { value } });
      await userEvent.click(within(dialog).getByRole("button", { name: "新建" }));
      expect(input.getAttribute("aria-invalid")).toBe("true");
      expect(api.requestsTo("POST", playlistsPath)).toHaveLength(0);
    }
  });

  it("新建 100 个补充平面码点去掉首尾空白，提交中禁止重复，成功进入新歌单", async () => {
    const { api, state, playlist } = setup(0);
    const pending = deferred<Response>();
    const name = "🎵".repeat(100);
    api.on("POST", playlistsPath, () => pending.promise);
    const { history } = renderApp({ path: "/me/playlists" });
    await userEvent.click(await screen.findByRole("button", { name: "新建歌单" }));
    const dialog = await screen.findByRole("dialog", { name: "新建歌单" });
    fireEvent.change(within(dialog).getByRole("textbox", { name: "名字" }), { target: { value: ` ${name} ` } });
    await userEvent.click(within(dialog).getByRole("button", { name: "新建" }));
    const submitting = within(dialog).getByRole("button", { name: "正在新建…" });
    expect(submitting.getAttribute("aria-disabled")).toBe("true");
    await userEvent.click(submitting);
    expect(api.requestsTo("POST", playlistsPath)).toHaveLength(1);
    expect(api.requestsTo("POST", playlistsPath)[0]?.body).toEqual({ name });
    state.name = name;
    await act(async () => { pending.resolve(jsonResponse(201, { playlist: playlist(), added_count: 0, existing_count: 0, duplicate_count: 0 })); });
    await screen.findByRole("heading", { name });
    expect(history.location.pathname).toBe(pagePath);
  });

  it("创建达到 1,000 个上限时显示明确提示，不伪装成功或关闭对话框", async () => {
    const { api } = setup();
    api.on("POST", playlistsPath, () => jsonResponse(409, { error: { code: "playlist_limit_reached", message: "已到上限" }, request_id: "playlist-capacity" }));
    renderApp({ path: "/me/playlists" });
    await userEvent.click(await screen.findByRole("button", { name: "新建歌单" }));
    const dialog = await screen.findByRole("dialog", { name: "新建歌单" });
    await userEvent.type(within(dialog).getByRole("textbox", { name: "名字" }), "满额后的新歌单");
    await userEvent.click(within(dialog).getByRole("button", { name: "新建" }));
    expect((await within(dialog).findByRole("alert")).textContent).toContain("歌单已经有 1,000 个了，先删掉一些再新建。");
    expect(screen.getByRole("dialog", { name: "新建歌单" })).toBeTruthy();
    expect(api.requestsTo("POST", playlistsPath)).toHaveLength(1);
  });
});
