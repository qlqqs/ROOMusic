import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createSessionController, SessionProvider, ViewerScopeProvider } from "../../features/auth";
import type { TrackSummaryDTO } from "../../features/catalog";
import type { PersonalQueueSource } from "../../features/personal";
import { decodeFavoriteQueue, decodePlaylistQueue, type FavoriteQueueDTO, type PlaylistQueueDTO } from "../../features/personal/api/decoders";
import { MAX_QUEUE_ENTRIES, PlayerProvider } from "../../features/player";
import { sampleBody } from "../../test/api-samples";
import { deferred } from "../../test/deferred";
import { installFakeApi, installFakeSession } from "../../test/fake-api";
import { playerHarness } from "../../test/fake-zones";
import { jsonResponse, sampleResponse } from "../../test/http";
import { playerTrack } from "../../test/player-data";
import { PersonalPlayAll } from "./personal-playback";

const dispose: (() => void)[] = [];
afterEach(() => {
  for (const cleanup of dispose.splice(0)) cleanup();
  vi.restoreAllMocks();
});

const favoriteSample = decodeFavoriteQueue(sampleBody("favorites/queue"));
const playlistSample = decodePlaylistQueue(sampleBody("playlists/queue"));
const playlistId = playlistSample.playlist.id;
const sources: { name: string; source: PersonalQueueSource; path: string }[] = [
  { name: "收藏", source: { kind: "favorites" }, path: "/api/v1/me/favorites/tracks/queue" },
  { name: "歌单", source: { kind: "playlist", playlistId }, path: `/api/v1/me/playlists/${playlistId}/queue` },
];

function track(index: number, unsupported = false): TrackSummaryDTO {
  const base = favoriteSample.items[0];
  if (base === undefined) throw new Error("收藏队列样本缺少曲目");
  return {
    ...base,
    track_id: `00000000-0000-4000-8000-${String(index).padStart(12, "0")}`,
    title: `合成曲目 ${index}`,
    source: unsupported ? { ...base.source, codec: "unknown" } : { ...base.source },
  };
}

// 这里只构造已经由服务端限量的响应窗口；后端如何过滤、取前一万首仍由其集成测试证明。
function queueResponse(source: PersonalQueueSource, items: TrackSummaryDTO[], missing = 0, omitted = 0): FavoriteQueueDTO | PlaylistQueueDTO {
  const total = items.length + missing + omitted;
  const counts = {
    items, total, missing_count: missing, eligible_count: items.length + omitted,
    returned_count: items.length, omitted_count: omitted, limit: 10_000 as const, truncated: omitted > 0,
  };
  return source.kind === "favorites"
    ? { ...counts, revision: favoriteSample.revision }
    : { ...counts, playlist: { ...playlistSample.playlist, id: source.playlistId, track_count: total, over_limit_count: Math.max(total - 10_000, 0) } };
}

async function setup() {
  const api = installFakeApi();
  const session = installFakeSession(api, { current: "listener" });
  const controller = createSessionController({ channel: null });
  await controller.start();
  const state = controller.getSnapshot();
  if (state.status !== "authenticated") throw new Error("会话未就绪");
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const player = playerHarness();
  const { engine } = player;
  dispose.push(() => { engine.endSession(); controller.dispose(); client.clear(); });
  const view = (children: ReactNode) => render(children, {
    wrapper: ({ children: content }) => (
      <QueryClientProvider client={client}><SessionProvider controller={controller}>
        <ViewerScopeProvider viewer={state.viewer} generation={state.generation}>
          <PlayerProvider userId={state.viewer.id} controller={engine}>{content}</PlayerProvider>
        </ViewerScopeProvider>
      </SessionProvider></QueryClientProvider>
    ),
  });
  return { api, session, controller, engine, player, view };
}

type Harness = ReturnType<typeof playerHarness>;
const queueIds = (engine: Harness["engine"]) => engine.getSnapshot().queue.entries.map((entry) => entry.track.trackId);
async function ready() {
  await waitFor(() => expect(screen.getByRole("button", { name: "播放全部" }).hasAttribute("disabled")).toBe(false));
}
/** 旧队列在放、位置 17 秒；记下此刻的队列、当前项、流与已发出的命令数。 */
async function startOldPlayback(player: Harness) {
  act(() => { player.engine.playTracks([playerTrack("旧队列甲"), playerTrack("旧队列乙")]); });
  const audio = await player.sounding(17);
  const snapshot = player.engine.getSnapshot();
  return { audio, queue: snapshot.queue, current: snapshot.current, stream: snapshot.zone?.stream, commands: player.server.commands.length };
}
/** 旧的播放没有被动过：没有新命令，队列、当前项与流不变，元素还在原位置出声。 */
function expectOldPlayback(player: Harness, before: Awaited<ReturnType<typeof startOldPlayback>>) {
  const snapshot = player.engine.getSnapshot();
  expect(player.server.commands).toHaveLength(before.commands);
  expect(snapshot.queue).toBe(before.queue);
  expect(snapshot.current).toEqual(before.current);
  expect(snapshot.zone?.stream).toBe(before.stream);
  expect(snapshot.status).toBe("playing");
  expect(before.audio.currentTime).toBe(17);
  expect(before.audio.paused).toBe(false);
}

// 经真实请求层、decoder、能力表与播放器控制器；只替换 HTTP、播放区服务端与音频设备边界。
describe.each(sources)("$name的播放全部", ({ source, path }) => {
  it("不预取，点击时读取最新候选并按服务端顺序一次替换队列，元素出声后才是播放中", async () => {
    const { api, engine, player, view } = await setup();
    const items = [track(9), track(2), track(7)];
    api.on("GET", path, () => jsonResponse(200, queueResponse(source, items)));
    view(<PersonalPlayAll source={source} />);
    await ready();
    expect(api.requestsTo("GET", path)).toHaveLength(0);
    act(() => { engine.enqueue([playerTrack("旧曲目")]); });
    const replace = vi.spyOn(engine, "playTracks");
    await userEvent.click(screen.getByRole("button", { name: "播放全部" }));
    expect(await screen.findByText("已选择 3 首。")).toBeTruthy();
    await waitFor(() => expect(queueIds(engine)).toEqual(items.map((item) => item.track_id)));
    expect(replace).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(engine.getSnapshot().status).toBe("loading"));
    await player.sounding();
    expect(engine.getSnapshot().current?.track.trackId).toBe(items[0]?.track_id);

    const latest = [track(4)];
    api.on("GET", path, () => jsonResponse(200, queueResponse(source, latest)));
    await userEvent.click(screen.getByRole("button", { name: "播放全部" }));
    expect(await screen.findByText("已选择 1 首。")).toBeTruthy();
    await waitFor(() => expect(queueIds(engine)).toEqual(latest.map((item) => item.track_id)));
    expect(api.requestsTo("GET", path)).toHaveLength(2);
    expect(replace).toHaveBeenCalledTimes(2);
    expect(api.requestsTo("GET", path).every((request) => request.path === path && request.body === undefined)).toBe(true);
  });

  it("文件缺失与窗口内不支持分别计数，不混作省略数", async () => {
    const { api, engine, view } = await setup();
    // 同时覆盖 CUE 与服务端格式表里没有两种拒绝；可播与否与浏览器无关，MP3 照常放。
    const mp3 = track(3);
    mp3.source = { ...mp3.source, source_kind: "mp3_id3v2", codec: "mp3" };
    const unknown = track(4);
    unknown.source = { ...unknown.source, codec: "unknown-codec" };
    const items = [track(5), track(2, true), mp3, unknown, track(1)];
    api.on("GET", path, () => jsonResponse(200, queueResponse(source, items, 4)));
    view(<PersonalPlayAll source={source} />);
    await ready();
    await userEvent.click(screen.getByRole("button", { name: "播放全部" }));
    expect(await screen.findByText("已选择 3 首。跳过了4 首文件缺失、2 首不能在这里播放。")).toBeTruthy();
    await waitFor(() => expect(queueIds(engine)).toEqual([track(5).track_id, mp3.track_id, track(1).track_id]));
    expect(screen.getByRole("status").textContent).not.toContain("超出");
  });

  it("一万首候选可完整替换已经满额的旧队列，不把替换当作追加", async () => {
    const { api, engine, view } = await setup();
    const items = Array.from({ length: 10_000 }, (_, index) => track(index + 1));
    api.on("GET", path, () => jsonResponse(200, queueResponse(source, items)));
    view(<PersonalPlayAll source={source} />);
    act(() => { expect(engine.enqueue(Array.from({ length: MAX_QUEUE_ENTRIES }, () => playerTrack("旧曲目")))).toBe("added"); });
    const before = engine.getSnapshot().queue;
    await ready();
    await userEvent.click(screen.getByRole("button", { name: "播放全部" }));
    expect(await screen.findByText("已选择 10,000 首。")).toBeTruthy();
    expect(engine.getSnapshot().queue).not.toBe(before);
    await waitFor(() => expect(queueIds(engine)).toEqual(items.map((item) => item.track_id)));
    expect(engine.getSnapshot().queue.entries).toHaveLength(10_000);
    expect(screen.getByRole("status").textContent).not.toContain("队列已满");
  });

  it("超过上限只消费前一万首窗口，缺失、不支持、省略三组数字独立，不另请求补位", async () => {
    const { api, engine, view } = await setup();
    const items = Array.from({ length: 10_000 }, (_, index) => track(index + 1, index === 0 || index === 9999));
    api.on("GET", path, () => jsonResponse(200, queueResponse(source, items, 3, 7)));
    view(<PersonalPlayAll source={source} />);
    act(() => { engine.enqueue(Array.from({ length: MAX_QUEUE_ENTRIES }, () => playerTrack("旧曲目"))); });
    await ready();
    const requestsBefore = api.requests.length;
    await userEvent.click(screen.getByRole("button", { name: "播放全部" }));
    expect(await screen.findByText("已选择 9,998 首。跳过了3 首文件缺失、2 首不能在这里播放。另有 7 首超出 10,000 首的上限，这次没有放进来。")).toBeTruthy();
    await waitFor(() => expect(queueIds(engine)).toEqual(items.slice(1, -1).map((item) => item.track_id)));
    expect(api.requests.slice(requestsBefore).map((request) => request.path)).toEqual([path]);
  });

  it.each([
    { name: "空列表", items: [] as TrackSummaryDTO[], missing: 0, message: "还没有曲目，队列没有变。" },
    { name: "全部缺失", items: [] as TrackSummaryDTO[], missing: 3, message: "没有能播放的曲目，队列没有变。跳过了3 首文件缺失。" },
    { name: "全部不支持", items: [track(1, true), track(2, true)], missing: 0, message: "没有能播放的曲目，队列没有变。跳过了2 首不能在这里播放。" },
    { name: "缺失加不支持", items: [track(1, true)], missing: 2, message: "没有能播放的曲目，队列没有变。跳过了2 首文件缺失、1 首不能在这里播放。" },
  ])("$name时提示准确且旧队列、当前项和真实播放进度不变", async ({ items, missing, message }) => {
    const { api, player, view } = await setup();
    api.on("GET", path, () => jsonResponse(200, queueResponse(source, items, missing)));
    view(<PersonalPlayAll source={source} />);
    const before = await startOldPlayback(player);
    await ready();
    await userEvent.click(screen.getByRole("button", { name: "播放全部" }));
    expect(await screen.findByText(message)).toBeTruthy();
    expectOldPlayback(player, before);
  });

  it("整个一万首窗口都不支持时不会拿省略的曲目补位，也不清空旧队列", async () => {
    const { api, player, view } = await setup();
    const items = Array.from({ length: 10_000 }, (_, index) => track(index + 1, true));
    api.on("GET", path, () => jsonResponse(200, queueResponse(source, items, 2, 1)));
    view(<PersonalPlayAll source={source} />);
    const before = await startOldPlayback(player);
    await ready();
    await userEvent.click(screen.getByRole("button", { name: "播放全部" }));
    expect(await screen.findByText("没有能播放的曲目，队列没有变。跳过了2 首文件缺失、10,000 首不能在这里播放。另有 1 首超出 10,000 首的上限，这次没有放进来。")).toBeTruthy();
    expectOldPlayback(player, before);
    expect(api.requestsTo("GET", path)).toHaveLength(1);
  });

  it.each(["服务端错误", "网络中断", "损坏响应", "超出窗口"])("%s不改变旧队列，可以重新读取后重试", async (failure) => {
    const { api, engine, player, view } = await setup();
    api.on("GET", path, () => {
      if (failure === "网络中断") throw new TypeError("合成网络中断");
      if (failure === "服务端错误") return sampleResponse("errors/personal-database-unavailable");
      if (failure === "超出窗口") return jsonResponse(200, queueResponse(source, Array.from({ length: 10_001 }, (_, index) => track(index + 1))));
      return jsonResponse(200, { ...queueResponse(source, [track(1)]), returned_count: 2 });
    });
    view(<PersonalPlayAll source={source} />);
    const before = await startOldPlayback(player);
    await ready();
    await userEvent.click(screen.getByRole("button", { name: "播放全部" }));
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(screen.getByText("没能读取曲目")).toBeTruthy();
    expectOldPlayback(player, before);
    expect(api.requestsTo("GET", path)).toHaveLength(1);
    api.on("GET", path, () => jsonResponse(200, queueResponse(source, [track(8)])));
    await userEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByText("已选择 1 首。")).toBeTruthy();
    await waitFor(() => expect(queueIds(engine)).toEqual([track(8).track_id]));
    expect(screen.queryByRole("alert")).toBeNull();
    expect(api.requestsTo("GET", path)).toHaveLength(2);
  });

  it("读取期间重复点击被忽略，旧播放不变，到达后只替换一次", async () => {
    const { api, engine, player, view } = await setup();
    const pending = deferred<Response>();
    api.on("GET", path, () => pending.promise);
    view(<PersonalPlayAll source={source} />);
    const before = await startOldPlayback(player);
    const replace = vi.spyOn(engine, "playTracks");
    await ready();
    await userEvent.click(screen.getByRole("button", { name: "播放全部" }));
    const busy = await screen.findByRole("button", { name: "正在读取…" });
    await userEvent.click(busy);
    expect(api.requestsTo("GET", path)).toHaveLength(1);
    expectOldPlayback(player, before);
    expect(replace).not.toHaveBeenCalled();
    await act(async () => pending.resolve(jsonResponse(200, queueResponse(source, [track(3)]))));
    expect(await screen.findByText("已选择 1 首。")).toBeTruthy();
    expect(replace).toHaveBeenCalledTimes(1);
  });

  it("能力表未就绪或失败时不读候选，重试能力检查后才允许播放", async () => {
    const { api, engine, player, view } = await setup();
    const formats = deferred<Response>();
    api.on("GET", "/api/v1/playback/formats", () => formats.promise);
    api.on("GET", path, () => jsonResponse(200, queueResponse(source, [track(1)])));
    view(<PersonalPlayAll source={source} />);
    act(() => { engine.enqueue([playerTrack("旧曲目")]); });
    await player.queued(1);
    const before = engine.getSnapshot().queue;
    expect(screen.getByRole("button", { name: "播放全部" }).hasAttribute("disabled")).toBe(true);
    await userEvent.click(screen.getByRole("button", { name: "播放全部" }));
    expect(api.requestsTo("GET", path)).toHaveLength(0);
    await act(async () => formats.resolve(sampleResponse("errors/personal-database-unavailable")));
    expect(await screen.findByText("无法检查播放能力，请重试。")).toBeTruthy();
    expect(engine.getSnapshot().queue).toBe(before);
    expect(screen.getByRole("button", { name: "播放全部" }).hasAttribute("disabled")).toBe(true);
    api.on("GET", "/api/v1/playback/formats", () => sampleResponse("playback/formats"));
    await userEvent.click(screen.getByRole("button", { name: "重试检查" }));
    await ready();
    expect(api.requestsTo("GET", path)).toHaveLength(0);
    await userEvent.click(screen.getByRole("button", { name: "播放全部" }));
    expect(await screen.findByText("已选择 1 首。")).toBeTruthy();
  });

  it.each(["换账号", "退出", "同账号重新登录"])("读取期间%s，迟到成功不能再次入队或公告成功", async (change) => {
    const { api, session, controller, engine, player, view } = await setup();
    const pending = deferred<Response>();
    api.on("GET", path, () => pending.promise);
    view(<PersonalPlayAll source={source} />);
    act(() => { engine.enqueue([playerTrack("旧曲目")]); });
    await player.queued(1);
    const replace = vi.spyOn(engine, "playTracks");
    await ready();
    await userEvent.click(screen.getByRole("button", { name: "播放全部" }));
    await waitFor(() => expect(api.requestsTo("GET", path)).toHaveLength(1));
    const generation = controller.currentGeneration();
    session.current = change === "换账号" ? "curator" : null;
    await act(async () => controller.recheck());
    if (change === "同账号重新登录") {
      session.current = "listener";
      await act(async () => controller.recheck());
    }
    expect(controller.currentGeneration()).not.toBe(generation);
    // 会话边界本身必须清空旧账号队列；断言迟到结果不能改变清理后的状态，也不能调用真实引擎。
    const afterSession = engine.getSnapshot().queue;
    expect(afterSession.entries).toHaveLength(0);
    await act(async () => pending.resolve(jsonResponse(200, queueResponse(source, [track(1)]))));
    await waitFor(() => expect(screen.queryByRole("button", { name: "正在读取…" })).toBeNull());
    expect(engine.getSnapshot().queue).toBe(afterSession);
    expect(replace).not.toHaveBeenCalled();
    expect(screen.getByRole("status").textContent).toBe("");
  });

  it("离开当前页面后迟到候选不替换仍在播放的队列", async () => {
    const { api, engine, player, view } = await setup();
    const pending = deferred<Response>();
    api.on("GET", path, () => pending.promise);
    const rendered = view(<PersonalPlayAll source={source} />);
    const before = await startOldPlayback(player);
    await ready();
    await userEvent.click(screen.getByRole("button", { name: "播放全部" }));
    // 只卸载页面按钮，保留外壳中的 PlayerProvider，模拟播放中离开收藏或歌单页。
    rendered.rerender(null);
    const afterUnmount = engine.getSnapshot().queue;
    const replace = vi.spyOn(engine, "playTracks");
    await act(async () => pending.resolve(jsonResponse(200, queueResponse(source, [track(1)]))));
    expect(afterUnmount).toBe(before.queue);
    expectOldPlayback(player, before);
    expect(replace).not.toHaveBeenCalled();
  });
});
