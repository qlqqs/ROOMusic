import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createSessionController, SessionProvider, ViewerScopeProvider } from "../features/auth";
import { decodePlaylistCreated } from "../features/personal/api/decoders";
import { PlayerProvider, usePlayer, type PlayerTrack } from "../features/player";
import { sampleBody } from "../test/api-samples";
import { deferred } from "../test/deferred";
import { installFakeApi, installFakeSession } from "../test/fake-api";
import { playerHarness } from "../test/fake-zones";
import { jsonResponse } from "../test/http";
import { playerTrack } from "../test/player-data";
import { distinctTrackIds, SaveQueueAsPlaylist } from "./player-personal-actions";

const dispose: (() => void)[] = [];
afterEach(() => {
  for (const cleanup of dispose.splice(0)) cleanup();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

const createPath = "/api/v1/me/playlists";
const createdSample = decodePlaylistCreated(sampleBody("playlists/create-with-tracks"));
function track(index: number, overrides: Partial<PlayerTrack> = {}): PlayerTrack {
  return playerTrack(String(index), { trackId: `00000000-0000-4000-8000-${String(index).padStart(12, "0")}`, ...overrides });
}
function created(name: string, count: number) {
  return { ...createdSample, playlist: { ...createdSample.playlist, name, track_count: count }, added_count: count, existing_count: 0, duplicate_count: 0 };
}

// 和 player 的中立插槽一样，直接订阅真实控制器，传入有重复条目的当前队列，不模拟保存业务。
function QueueActions() {
  const { snapshot } = usePlayer();
  return <SaveQueueAsPlaylist tracks={snapshot.queue.entries.map((entry) => entry.track)} />;
}

async function setup(tracks: readonly PlayerTrack[]) {
  const api = installFakeApi();
  installFakeSession(api, { current: "listener" });
  const controller = createSessionController({ channel: null });
  await controller.start();
  const state = controller.getSnapshot();
  if (state.status !== "authenticated") throw new Error("会话未就绪");
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const player = playerHarness();
  const { engine } = player;
  engine.enqueue(tracks);
  dispose.push(() => { engine.endSession(); controller.dispose(); client.clear(); });
  render(<QueryClientProvider client={client}><SessionProvider controller={controller}>
    <ViewerScopeProvider viewer={state.viewer} generation={state.generation}>
      <PlayerProvider userId={state.viewer.id} controller={engine}><QueueActions /></PlayerProvider>
    </ViewerScopeProvider>
  </SessionProvider></QueryClientProvider>);
  await player.queued(tracks.length);
  return { api, engine, player };
}
async function openSave() {
  await userEvent.click(screen.getByRole("button", { name: "存为歌单" }));
  return screen.findByRole("dialog", { name: "存为歌单" });
}

// 默认名按本地日期而不是 UTC 日期；只固定 Date，HTTP、Query 与用户交互仍使用真实异步调度。
function setLocalDay(day = 4) {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(new Date(2026, 10, day, 23, 59, 0));
}

describe("队列存为歌单", () => {
  it("只按 trackId 去重并保留第一次出现的顺序，同名异轨仍保留，不改动输入", () => {
    const a = track(8, { title: "同名" });
    const b = track(3, { title: "同名" });
    const duplicate = { ...a, title: "后来改过的显示名" };
    const tracks = Object.freeze([a, b, duplicate, track(5), b]);
    expect(distinctTrackIds(tracks)).toEqual([a.trackId, b.trackId, track(5).trackId]);
    expect(tracks).toEqual([a, b, duplicate, track(5), b]);
    expect(distinctTrackIds([])).toEqual([]);
  });

  it("默认名是点击当天的本地日期，取消不会发送创建请求", async () => {
    setLocalDay();
    const { api, engine } = await setup([track(1)]);
    const before = engine.getSnapshot().queue;
    const dialog = await openSave();
    expect(within(dialog).getByRole("textbox", { name: "名字" }).getAttribute("value")).toBe("队列 2026-11-04");
    expect(api.requestsTo("POST", createPath)).toHaveLength(0);
    await userEvent.click(within(dialog).getByRole("button", { name: "取消" }));
    expect(screen.queryByRole("dialog", { name: "存为歌单" })).toBeNull();
    expect(engine.getSnapshot().queue).toBe(before);
    expect(api.requestsTo("POST", createPath)).toHaveLength(0);
  });

  it("按真实队列首次顺序将名字与去重曲目一次新建，携带幂等键且不改变正在播放的队列", async () => {
    setLocalDay();
    const tracks = [track(9), track(2), track(9), track(7), track(2)];
    const { api, engine, player } = await setup(tracks);
    api.on("POST", createPath, () => jsonResponse(201, created("队列 2026-11-04", 3)));
    act(() => engine.play());
    const audio = await player.sounding(23);
    const before = engine.getSnapshot().queue;
    const plays = audio.playCalls;
    const dialog = await openSave();
    await userEvent.click(within(dialog).getByRole("button", { name: "存为歌单" }));
    expect(await screen.findByText("已存为《队列 2026-11-04》，共 3 首。")).toBeTruthy();
    expect(screen.queryByRole("dialog", { name: "存为歌单" })).toBeNull();
    const requests = api.requestsTo("POST", createPath);
    expect(requests).toHaveLength(1);
    expect(requests[0]?.body).toEqual({ name: "队列 2026-11-04", track_ids: [track(9).trackId, track(2).trackId, track(7).trackId] });
    expect(requests[0]?.headers.get("Idempotency-Key")).toMatch(/^[0-9a-f-]{36}$/i);
    expect(requests[0]?.init.credentials).toBe("same-origin");
    expect(api.requests.filter((request) => request.method === "POST").map((request) => request.path)).toEqual([createPath]);
    expect(engine.getSnapshot().queue).toBe(before);
    expect(engine.getSnapshot().queue.entries).toHaveLength(5);
    expect(engine.getSnapshot().status).toBe("playing");
    expect(audio.currentTime).toBe(23);
    expect(audio.playCalls).toBe(plays);
  });

  it("随机模式且当前项不在队首时仍按完整列表顺序保存，不只保存待播部分", async () => {
    setLocalDay();
    const tracks = [track(8), track(3), track(8), track(5)];
    const { api, engine, player } = await setup(tracks);
    api.on("POST", createPath, () => jsonResponse(201, created("队列 2026-11-04", 3)));
    act(() => {
      engine.setMode("shuffle");
      const last = engine.getSnapshot().queue.entries.at(-1);
      if (last === undefined) throw new Error("测试队列缺少末项");
      engine.playEntry(last.entryId);
    });
    await player.sounding();
    expect(engine.getSnapshot().current?.track.trackId).toBe(track(5).trackId);
    const before = engine.getSnapshot().queue;
    const dialog = await openSave();
    await userEvent.click(within(dialog).getByRole("button", { name: "存为歌单" }));
    expect(await screen.findByText("已存为《队列 2026-11-04》，共 3 首。")).toBeTruthy();
    expect(api.requestsTo("POST", createPath)[0]?.body).toEqual({ name: "队列 2026-11-04", track_ids: [track(8).trackId, track(3).trackId, track(5).trackId] });
    expect(engine.getSnapshot().queue).toBe(before);
    expect(engine.getSnapshot().status).toBe("playing");
  });

  it("存为歌单不按播放能力筛选，不支持的曲目与未知时长也按身份保存", async () => {
    setLocalDay();
    const cue = track(1);
    cue.source = { ...cue.source, sourceKind: "cue_virtual" };
    const unknown = track(2, { durationSeconds: null });
    unknown.source = { ...unknown.source, codec: "unknown-codec" };
    const { api } = await setup([cue, unknown, cue]);
    api.on("POST", createPath, () => jsonResponse(201, created("队列 2026-11-04", 2)));
    const dialog = await openSave();
    await userEvent.click(within(dialog).getByRole("button", { name: "存为歌单" }));
    expect(await screen.findByText("已存为《队列 2026-11-04》，共 2 首。")).toBeTruthy();
    expect(api.requestsTo("POST", createPath)[0]?.body).toEqual({ name: "队列 2026-11-04", track_ids: [cue.trackId, unknown.trackId] });
    expect(api.requestsTo("GET", "/api/v1/playback/formats")).toHaveLength(0);
  });

  it("打开后队列重排、追加或跨日不改变此次快照，取消后重开才读取新队列与日期", async () => {
    setLocalDay();
    const { api, engine } = await setup([track(8), track(3), track(8)]);
    const first = await openSave();
    act(() => {
      const second = engine.getSnapshot().queue.entries[1];
      if (second === undefined) throw new Error("测试队列缺少第二项");
      engine.moveEntry(second.entryId, -1);
      engine.enqueue([track(5)]);
    });
    vi.setSystemTime(new Date(2026, 10, 5, 0, 1, 0));
    api.on("POST", createPath, () => jsonResponse(201, created("队列 2026-11-04", 2)));
    await userEvent.click(within(first).getByRole("button", { name: "存为歌单" }));
    expect(await screen.findByText("已存为《队列 2026-11-04》，共 2 首。")).toBeTruthy();
    expect(api.requestsTo("POST", createPath)[0]?.body).toEqual({ name: "队列 2026-11-04", track_ids: [track(8).trackId, track(3).trackId] });

    const second = await openSave();
    expect(within(second).getByRole("textbox", { name: "名字" }).getAttribute("value")).toBe("队列 2026-11-05");
    await userEvent.click(within(second).getByRole("button", { name: "取消" }));
    const third = await openSave();
    api.on("POST", createPath, () => jsonResponse(201, created("队列 2026-11-05", 3)));
    await userEvent.click(within(third).getByRole("button", { name: "存为歌单" }));
    expect(await screen.findByText("已存为《队列 2026-11-05》，共 3 首。")).toBeTruthy();
    const requests = api.requestsTo("POST", createPath);
    expect(requests).toHaveLength(2);
    expect(requests[1]?.body).toEqual({ name: "队列 2026-11-05", track_ids: [track(3).trackId, track(8).trackId, track(5).trackId] });
    expect(requests[1]?.headers.get("Idempotency-Key")).not.toBe(requests[0]?.headers.get("Idempotency-Key"));
  });

  it("允许修改默认名，提交规范化名字以及快照中的曲目，不发送条目 ID 或播放位置", async () => {
    const { api, engine } = await setup([track(4), track(1), track(4)]);
    api.on("POST", createPath, () => jsonResponse(201, created("我的夜曲", 2)));
    const dialog = await openSave();
    const name = within(dialog).getByRole("textbox", { name: "名字" });
    await userEvent.clear(name);
    await userEvent.type(name, "  我的夜曲  ");
    const before = engine.getSnapshot().queue;
    await userEvent.click(within(dialog).getByRole("button", { name: "存为歌单" }));
    expect(await screen.findByText("已存为《我的夜曲》，共 2 首。")).toBeTruthy();
    expect(api.requestsTo("POST", createPath)[0]?.body).toEqual({ name: "我的夜曲", track_ids: [track(4).trackId, track(1).trackId] });
    expect(engine.getSnapshot().queue).toBe(before);
  });

  it("提交中不重复创建、不能取消，等回执后才显示保存成功", async () => {
    setLocalDay();
    const { api, engine } = await setup([track(1), track(2)]);
    const pending = deferred<Response>();
    api.on("POST", createPath, () => pending.promise);
    const before = engine.getSnapshot().queue;
    const dialog = await openSave();
    await userEvent.click(within(dialog).getByRole("button", { name: "存为歌单" }));
    const busy = await within(dialog).findByRole("button", { name: "正在新建…" });
    await userEvent.click(busy);
    await userEvent.click(within(dialog).getByRole("button", { name: "取消" }));
    expect(api.requestsTo("POST", createPath)).toHaveLength(1);
    expect(screen.getByRole("dialog", { name: "存为歌单" })).toBe(dialog);
    expect(screen.getByRole("status").textContent).toBe("");
    expect(engine.getSnapshot().queue).toBe(before);
    await act(async () => pending.resolve(jsonResponse(201, created("队列 2026-11-04", 2))));
    expect(await screen.findByText("已存为《队列 2026-11-04》，共 2 首。")).toBeTruthy();
    expect(api.requestsTo("POST", createPath)).toHaveLength(1);
  });

  it("创建结果不确定时保留同一请求与幂等键，重试不读取后续变化的队列", async () => {
    setLocalDay();
    const { api, engine } = await setup([track(6), track(1), track(6)]);
    api.on("POST", createPath, () => { throw new TypeError("合成响应丢失"); });
    const dialog = await openSave();
    await userEvent.click(within(dialog).getByRole("button", { name: "存为歌单" }));
    expect(await within(dialog).findByRole("alert")).toBeTruthy();
    const first = api.requestsTo("POST", createPath)[0];
    expect(first?.body).toEqual({ name: "队列 2026-11-04", track_ids: [track(6).trackId, track(1).trackId] });
    act(() => { engine.enqueue([track(9)]); });
    api.on("POST", createPath, () => jsonResponse(201, created("队列 2026-11-04", 2)));
    await userEvent.click(within(dialog).getByRole("button", { name: "重试" }));
    expect(await screen.findByText("已存为《队列 2026-11-04》，共 2 首。")).toBeTruthy();
    const requests = api.requestsTo("POST", createPath);
    expect(requests).toHaveLength(2);
    expect(requests[1]?.body).toEqual(first?.body);
    expect(requests[1]?.headers.get("Idempotency-Key")).toBe(first?.headers.get("Idempotency-Key"));
    await waitFor(() => expect(engine.getSnapshot().queue.entries.map((entry) => entry.track.trackId)).toEqual([track(6).trackId, track(1).trackId, track(6).trackId, track(9).trackId]));
  });

  it("空白名字不能提交，保留原队列并提示修正", async () => {
    const { api, engine } = await setup([track(1)]);
    const before = engine.getSnapshot().queue;
    const dialog = await openSave();
    await userEvent.clear(within(dialog).getByRole("textbox", { name: "名字" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "存为歌单" }));
    await waitFor(() => expect(within(dialog).getByText("请输入名字。")).toBeTruthy());
    expect(api.requestsTo("POST", createPath)).toHaveLength(0);
    expect(engine.getSnapshot().queue).toBe(before);
  });
});
