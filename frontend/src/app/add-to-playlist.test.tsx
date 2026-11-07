import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { decodePlaylistDetail, type ItemsAddedDTO, type PlaylistSummaryDTO } from "../features/personal/api/decoders";
import { decode as d } from "../shared/api";
import { sampleBody } from "../test/api-samples";
import { albumGroup, mediaOf } from "../test/catalog-data";
import { deferred } from "../test/deferred";
import { installFakeApi, installFakeSession } from "../test/fake-api";
import { FakeAudio } from "../test/fake-audio";
import { installCatalogApi } from "../test/fake-catalog";
import { jsonResponse, type RecordedRequest } from "../test/http";
import { renderApp } from "../test/render-app";

const base = decodePlaylistDetail(sampleBody("playlists/detail")).playlist;
const playlistsPath = "/api/v1/me/playlists";
const group = albumGroup(1, { title: "加入歌单用的专辑", track_count: 3, medium_count: 1 });
const tracks = mediaOf(group.representative).flatMap((medium) => medium.tracks);
const trackIds = tracks.map((track) => track.id);
const firstTrack = tracks[0];
if (firstTrack === undefined) throw new Error("专辑夹具必须含至少一首曲目");
const singleId = firstTrack.id;
const singleTitle = firstTrack.title;
const decodeLookup = d.defineDecoder(d.object({ track_ids: d.array(d.uuid(), { max: 10_000 }), group_ids: d.array(d.uuid(), { max: 1_000 }) }));
const playlistOf = (index: number, trackCount = 2): PlaylistSummaryDTO => ({ ...base, id: `00000000-0000-4000-8000-e${String(index).padStart(11, "0")}`, name: `备选歌单 ${index}`, revision: 4, track_count: trackCount, over_limit_count: Math.max(trackCount - 10_000, 0), cover_groups: [] });
const first = playlistOf(1);
const second = playlistOf(2);

beforeEach(() => {
  sessionStorage.clear();
  vi.stubGlobal("Audio", class { constructor() { return new FakeAudio(); } });
  vi.spyOn(HTMLMediaElement.prototype, "canPlayType").mockReturnValue("probably");
});
afterEach(() => vi.restoreAllMocks());

/** 专辑头部和行菜单都走真实 app 组合；只替换 HTTP，不替换对话框和 hooks。 */
function setup(initial: PlaylistSummaryDTO[] = [first, second]) {
  const api = installFakeApi();
  installFakeSession(api, { current: "listener" });
  installCatalogApi(api, { groups: [group] });
  const state = { playlists: [...initial], containing: new Set<string>() };
  const readPlaylists = (request: RecordedRequest): Response => {
    const params = new URL(request.path, "http://test").searchParams;
    const page = Number(params.get("page") ?? 1);
    const size = Number(params.get("page_size") ?? 20);
    return jsonResponse(200, { items: state.playlists.slice((page - 1) * size, page * size), pagination: { page, page_size: size, total: state.playlists.length } });
  };
  api.on("GET", playlistsPath, readPlaylists);
  api.on("GET", /^\/api\/v1\/me\/playlists\/containing-track\/[^/]+$/, (request) => jsonResponse(200, { track_id: request.path.split("/").at(-1), items: state.playlists.filter((playlist) => state.containing.has(playlist.id)).map((playlist) => ({ playlist_id: playlist.id, revision: playlist.revision })) }));
  api.on("POST", "/api/v1/me/favorites/lookup", (request) => {
    const ids = decodeLookup(request.body);
    return jsonResponse(200, { revision: 0, tracks: ids.track_ids.map((track_id) => ({ track_id, favorited: false })), albums: ids.group_ids.map((group_id) => ({ group_id, favorited: false })) });
  });
  return { api, state, readPlaylists };
}
async function openSingle() {
  await screen.findByText(singleTitle, { exact: true });
  const row = screen.getByText(singleTitle, { exact: true }).closest("li");
  if (row === null) throw new Error("专辑曲目行不存在");
  await userEvent.click(within(row).getByRole("button", { name: "更多操作" }));
  await userEvent.click(screen.getByRole("menuitem", { name: "添加到歌单…" }));
  return screen.findByRole("dialog", { name: "添加到歌单" });
}
async function openAlbum() {
  await userEvent.click(await screen.findByRole("button", { name: "添加到歌单…" }));
  return screen.findByRole("dialog", { name: "添加到歌单" });
}
/** 批量反馈必须出现在发起操作的专辑头部；混合结果分别报告新增与已存在数量。 */
async function albumFeedback() {
  await waitFor(() => expect(screen.queryByRole("dialog", { name: "添加到歌单" })).toBeNull());
  const source = screen.getByRole("button", { name: "添加到歌单…" }).parentElement;
  if (source === null) throw new Error("缺少专辑头部加入入口");
  return within(source).getByRole("status").textContent;
}
function choice(dialog: HTMLElement, name: string) {
  const option = within(dialog).getByText(name, { exact: true }).closest("li");
  if (option === null) throw new Error(`缺少歌单选项：${name}`);
  return within(option).getByRole("button");
}
const itemPath = (id: string) => `${playlistsPath}/${id}/items`;

describe("添加到歌单：真实专辑头部与曲目行入口", () => {
  it("单曲查 membership，已有的标已在歌单里并禁用；另一个歌单可加入", async () => {
    const { api, state } = setup();
    state.containing.add(first.id);
    api.on("POST", itemPath(second.id), () => jsonResponse(200, { playlist: { ...second, track_count: 3, revision: 5 }, added_count: 1, existing_count: 0, duplicate_count: 0 }));
    renderApp({ path: `/albums/${group.id}` });
    const dialog = await openSingle();
    await within(dialog).findByText("2 首 · 已在歌单里");
    expect(api.requestsTo("GET", `${playlistsPath}/containing-track/${singleId}`)).toHaveLength(1);
    expect(choice(dialog, first.name).hasAttribute("disabled")).toBe(true);
    await userEvent.click(choice(dialog, first.name));
    expect(api.requestsTo("POST", itemPath(first.id))).toHaveLength(0);
    expect(choice(dialog, second.name).hasAttribute("disabled")).toBe(false);
    await userEvent.click(choice(dialog, second.name));
    await screen.findByText(`已加入《${second.name}》。`);
    expect(screen.queryByRole("dialog", { name: "添加到歌单" })).toBeNull();
    expect(api.requestsTo("POST", itemPath(second.id))[0]?.body).toEqual({ expected_revision: 4, track_ids: [singleId] });
  });

  it("批量加入传当前版本全部曲目，原处反馈加入和已存在数量，不查单曲 membership", async () => {
    const { api } = setup();
    api.on("POST", itemPath(first.id), () => jsonResponse(200, { playlist: { ...first, track_count: 4, revision: 5 }, added_count: 2, existing_count: 1, duplicate_count: 0 }));
    renderApp({ path: `/albums/${group.id}` });
    const dialog = await openAlbum();
    await within(dialog).findByText(first.name);
    expect(within(dialog).getByText(`《${group.representative.title}》的 3 首`)).toBeTruthy();
    await userEvent.click(choice(dialog, first.name));
    expect(await albumFeedback()).toBe(`《${first.name}》：已加入 2 首，1 首已在歌单里。`);
    expect(api.requestsTo("POST", itemPath(first.id))[0]?.body).toEqual({ expected_revision: 4, track_ids: trackIds });
    expect(api.requestsTo("GET", /^\/api\/v1\/me\/playlists\/containing-track\//)).toHaveLength(0);
  });

  it("批量全部已存在时显示简短提示，不报告零首新增", async () => {
    const { api } = setup([playlistOf(1, 3)]);
    api.on("POST", itemPath(first.id), () => jsonResponse(200, { playlist: playlistOf(1, 3), added_count: 0, existing_count: 3, duplicate_count: 0 }));
    renderApp({ path: `/albums/${group.id}` });
    const dialog = await openAlbum();
    await within(dialog).findByText(first.name);
    await userEvent.click(choice(dialog, first.name));
    expect(await albumFeedback()).toBe(`这些都已在《${first.name}》里了。`);
    expect(api.requestsTo("POST", itemPath(first.id))).toHaveLength(1);
  });

  it("批量全部新增时报告 3 首新增，省略零首已存在", async () => {
    const { api } = setup();
    api.on("POST", itemPath(first.id), () => jsonResponse(200, { playlist: { ...first, track_count: 5, revision: 5 }, added_count: 3, existing_count: 0, duplicate_count: 0 }));
    renderApp({ path: `/albums/${group.id}` });
    const dialog = await openAlbum();
    await within(dialog).findByText(first.name);
    await userEvent.click(choice(dialog, first.name));
    expect(await albumFeedback()).toBe(`《${first.name}》：已加入 3 首。`);
    expect(api.requestsTo("POST", itemPath(first.id))).toHaveLength(1);
    expect(api.requestsTo("POST", itemPath(first.id))[0]?.body).toEqual({ expected_revision: 4, track_ids: trackIds });
  });

  it.each([10_000, 10_001])("歌单已有 %i 首时标已满且不可选", async (count) => {
    const { api } = setup([playlistOf(1, count), second]);
    renderApp({ path: `/albums/${group.id}` });
    const dialog = await openAlbum();
    await within(dialog).findByText(first.name);
    expect(choice(dialog, first.name).textContent).toContain("已满");
    expect(choice(dialog, first.name).hasAttribute("disabled")).toBe(true);
    expect(choice(dialog, second.name).hasAttribute("disabled")).toBe(false);
    await userEvent.click(choice(dialog, first.name));
    expect(api.requestsTo("POST", itemPath(first.id))).toHaveLength(0);
  });

  it("服务端拒绝整批超上限时显示原因，不截断请求，不关闭对话框", async () => {
    const { api } = setup([playlistOf(1, 9999)]);
    api.on("POST", itemPath(first.id), () => jsonResponse(409, { error: { code: "playlist_track_limit_reached", message: "容量不足" }, request_id: "add-capacity" }));
    renderApp({ path: `/albums/${group.id}` });
    const dialog = await openAlbum();
    await within(dialog).findByText(first.name);
    await userEvent.click(choice(dialog, first.name));
    expect((await within(dialog).findByRole("alert")).textContent).toContain("歌单最多 10,000 首，放不下这些了。");
    expect(screen.getByRole("dialog", { name: "添加到歌单" })).toBeTruthy();
    expect(api.requestsTo("POST", itemPath(first.id))[0]?.body).toEqual({ expected_revision: 4, track_ids: trackIds });
    expect(api.requestsTo("POST", itemPath(first.id))).toHaveLength(1);
  });

  it("对话框新建并加入只发一个原子请求，不先建空歌单再逐首加入", async () => {
    const { api } = setup([]);
    api.on("POST", playlistsPath, () => jsonResponse(201, { playlist: { ...first, name: "旅途", revision: 1, track_count: 3 }, added_count: 3, existing_count: 0, duplicate_count: 0 }));
    renderApp({ path: `/albums/${group.id}` });
    const dialog = await openAlbum();
    await within(dialog).findByText("还没有歌单。");
    await userEvent.click(within(dialog).getByRole("button", { name: "新建歌单" }));
    await userEvent.type(within(dialog).getByRole("textbox", { name: "名字" }), " 旅途 ");
    await userEvent.click(within(dialog).getByRole("button", { name: "新建并加入" }));
    await screen.findByText("已新建《旅途》，加入了 3 首。");
    expect(api.requestsTo("POST", playlistsPath)).toHaveLength(1);
    expect(api.requestsTo("POST", playlistsPath)[0]?.body).toEqual({ name: "旅途", track_ids: trackIds });
    expect(api.requestsTo("POST", /^\/api\/v1\/me\/playlists\/[^/]+\/items$/)).toHaveLength(0);
    expect(api.requestsTo("POST", playlistsPath)[0]?.headers.get("Idempotency-Key")).toMatch(/^[0-9a-f-]{36}$/);
  });

  it("新建并加入遇到结果不确定时，重试保留同一个名字、曲目顺序和幂等键", async () => {
    const { api } = setup([]);
    let attempts = 0;
    api.on("POST", playlistsPath, () => {
      attempts += 1;
      if (attempts === 1) throw new TypeError("测试：新建并加入已提交但响应丢失");
      return jsonResponse(201, { playlist: { ...first, name: "稳定重试", revision: 1, track_count: 3 }, added_count: 3, existing_count: 0, duplicate_count: 0 });
    });
    renderApp({ path: `/albums/${group.id}` });
    const dialog = await openAlbum();
    await within(dialog).findByText("还没有歌单。");
    await userEvent.click(within(dialog).getByRole("button", { name: "新建歌单" }));
    await userEvent.type(within(dialog).getByRole("textbox", { name: "名字" }), "稳定重试");
    await userEvent.click(within(dialog).getByRole("button", { name: "新建并加入" }));
    await within(dialog).findByRole("alert");
    expect(api.requestsTo("POST", playlistsPath)).toHaveLength(1);
    await userEvent.click(within(dialog).getByRole("button", { name: "重试" }));
    await screen.findByText("已新建《稳定重试》，加入了 3 首。");
    const requests = api.requestsTo("POST", playlistsPath);
    expect(requests).toHaveLength(2);
    expect(requests[0]?.body).toEqual({ name: "稳定重试", track_ids: trackIds });
    expect(requests[1]?.body).toEqual(requests[0]?.body);
    expect(requests[0]?.headers.get("Idempotency-Key")).toMatch(/^[0-9a-f-]{36}$/);
    expect(requests[1]?.headers.get("Idempotency-Key")).toBe(requests[0]?.headers.get("Idempotency-Key"));
    expect(api.requestsTo("POST", /^\/api\/v1\/me\/playlists\/[^/]+\/items$/)).toHaveLength(0);
  });

  it("显示更多歌单追加下一页，保留第一页且不重复，可选择后续歌单", async () => {
    const { api } = setup(Array.from({ length: 21 }, (_, index) => playlistOf(index + 1)));
    const last = playlistOf(21);
    api.on("POST", itemPath(last.id), () => jsonResponse(200, { playlist: { ...last, revision: 5, track_count: 5 }, added_count: 3, existing_count: 0, duplicate_count: 0 }));
    renderApp({ path: `/albums/${group.id}` });
    const dialog = await openAlbum();
    const list = await within(dialog).findByRole("list", { name: "歌单" });
    expect(within(list).getAllByRole("listitem")).toHaveLength(20);
    expect(within(dialog).queryByText(last.name)).toBeNull();
    await userEvent.click(within(dialog).getByRole("button", { name: "显示更多歌单" }));
    await within(dialog).findByText(last.name);
    expect(within(list).getAllByRole("listitem")).toHaveLength(21);
    expect(within(dialog).getAllByText(first.name)).toHaveLength(1);
    expect(within(dialog).queryByRole("button", { name: "显示更多歌单" })).toBeNull();
    expect(api.requestsTo("GET", playlistsPath).map((request) => request.path)).toEqual([`${playlistsPath}?page=1&page_size=20`, `${playlistsPath}?page=2&page_size=20`]);
    await userEvent.click(choice(dialog, last.name));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "添加到歌单" })).toBeNull());
    expect(api.requestsTo("POST", itemPath(last.id))[0]?.body).toEqual({ expected_revision: 4, track_ids: trackIds });
  });

  it("加入已提交但响应丢失，列表刷新后重试仍复用原键、原 revision 和原曲目快照", async () => {
    const { api, state } = setup();
    let receipt: ItemsAddedDTO | null = null;
    api.on("POST", itemPath(first.id), () => {
      if (receipt === null) {
        state.playlists = [{ ...first, revision: 5, track_count: 5 }, second];
        receipt = { playlist: { ...first, revision: 5, track_count: 5 }, added_count: 3, existing_count: 0, duplicate_count: 0 };
        throw new TypeError("测试：提交后响应丢失");
      }
      return jsonResponse(200, receipt);
    });
    renderApp({ path: `/albums/${group.id}` });
    const dialog = await openAlbum();
    await within(dialog).findByText(first.name);
    await userEvent.click(choice(dialog, first.name));
    await within(dialog).findByRole("alert");
    await waitFor(() => expect(choice(dialog, first.name).textContent).toContain("5 首"));
    expect(api.requestsTo("POST", itemPath(first.id))).toHaveLength(1);
    await userEvent.click(within(dialog).getByRole("button", { name: "重试" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "添加到歌单" })).toBeNull());
    const requests = api.requestsTo("POST", itemPath(first.id));
    expect(requests).toHaveLength(2);
    expect(requests[0]?.body).toEqual({ expected_revision: 4, track_ids: trackIds });
    expect(requests[1]?.body).toEqual(requests[0]?.body);
    expect(requests[1]?.headers.get("Idempotency-Key")).toBe(requests[0]?.headers.get("Idempotency-Key"));
    expect(requests[0]?.headers.get("Idempotency-Key")).toMatch(/^[0-9a-f-]{36}$/);
  });

  it("409 显示提示并重读，用户重新选择才用新 revision 与新幂等键", async () => {
    const { api, state } = setup();
    let attempt = 0;
    api.on("POST", itemPath(first.id), () => {
      attempt += 1;
      if (attempt === 1) {
        state.playlists = [{ ...first, revision: 9, track_count: 4 }, second];
        return jsonResponse(409, { error: { code: "revision_conflict", message: "歌单已改变" }, request_id: "picker-conflict" });
      }
      return jsonResponse(200, { playlist: { ...first, revision: 10, track_count: 7 }, added_count: 3, existing_count: 0, duplicate_count: 0 });
    });
    renderApp({ path: `/albums/${group.id}` });
    const dialog = await openAlbum();
    await within(dialog).findByText(first.name);
    await userEvent.click(choice(dialog, first.name));
    expect((await within(dialog).findByRole("alert")).textContent).toContain("歌单刚在别处改过，已重新读取，请再试一次。");
    await waitFor(() => expect(choice(dialog, first.name).textContent).toContain("4 首"));
    expect(api.requestsTo("POST", itemPath(first.id))).toHaveLength(1);
    await userEvent.click(choice(dialog, first.name));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "添加到歌单" })).toBeNull());
    const requests = api.requestsTo("POST", itemPath(first.id));
    expect(requests[1]?.body).toEqual({ expected_revision: 9, track_ids: trackIds });
    expect(requests[1]?.headers.get("Idempotency-Key")).not.toBe(requests[0]?.headers.get("Idempotency-Key"));
  });

  it("提交中点击同一选项不重复提交，其他选项、新建与取消不可用，Esc 不关闭", async () => {
    const { api } = setup();
    const pending = deferred<Response>();
    api.on("POST", itemPath(first.id), () => pending.promise);
    renderApp({ path: `/albums/${group.id}` });
    const dialog = await openAlbum();
    await within(dialog).findByText(first.name);
    await userEvent.click(choice(dialog, first.name));
    expect(choice(dialog, first.name).getAttribute("aria-busy")).toBe("true");
    await userEvent.click(choice(dialog, first.name));
    expect(api.requestsTo("POST", itemPath(first.id))).toHaveLength(1);
    expect(choice(dialog, second.name).hasAttribute("disabled")).toBe(true);
    for (const name of ["新建歌单", "取消"]) expect(within(dialog).getByRole("button", { name }).hasAttribute("disabled")).toBe(true);
    fireEvent(dialog, new Event("cancel", { cancelable: true }));
    expect(screen.getByRole("dialog", { name: "添加到歌单" })).toBe(dialog);
    await act(async () => { pending.resolve(jsonResponse(200, { playlist: { ...first, revision: 5, track_count: 5 }, added_count: 3, existing_count: 0, duplicate_count: 0 })); });
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "添加到歌单" })).toBeNull());
  });

  it("加载和读取失败都不伪装没有歌单；重试恢复后可取消且不写数据", async () => {
    const { api, readPlaylists } = setup();
    const pending = deferred<Response>();
    api.on("GET", playlistsPath, () => pending.promise);
    renderApp({ path: `/albums/${group.id}` });
    const dialog = await openAlbum();
    await within(dialog).findByText("正在读取歌单");
    expect(within(dialog).queryByText("还没有歌单。")).toBeNull();
    await act(async () => { pending.resolve(jsonResponse(500, { error: { code: "internal_error", message: "读取失败" }, request_id: "picker-read-error" })); });
    expect((await within(dialog).findByRole("alert")).textContent).toContain("picker-read-error");
    expect(within(dialog).queryByText("还没有歌单。")).toBeNull();
    api.on("GET", playlistsPath, readPlaylists);
    await userEvent.click(within(dialog).getByRole("button", { name: "重试" }));
    await within(dialog).findByText(first.name);
    await userEvent.click(within(dialog).getByRole("button", { name: "取消" }));
    expect(screen.queryByRole("dialog", { name: "添加到歌单" })).toBeNull();
    expect(api.requestsTo("POST", /^\/api\/v1\/me\/playlists/)).toHaveLength(0);
  });
});
