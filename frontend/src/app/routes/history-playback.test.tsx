import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createSessionController, SessionProvider, ViewerScopeProvider } from "../../features/auth";
import { PlayerProvider, MAX_QUEUE_ENTRIES } from "../../features/player";
import { FavoriteStatusScope } from "../../features/personal";
import { deferred } from "../../test/deferred";
import { installFakeApi, installFakeSession } from "../../test/fake-api";
import { playerHarness } from "../../test/fake-zones";
import { historyItem, historyQueue } from "../../test/history-data";
import { jsonResponse } from "../../test/http";
import { playerTrack } from "../../test/player-data";
import { HistoryPlayAll, HistoryTrackActions } from "./history-playback";
import { playerTrackOfSummary as playerTrackOfHistoryItem } from "./track-playback";

const dispose: (() => void)[] = [];
afterEach(() => { for (const cleanup of dispose.splice(0)) cleanup(); vi.restoreAllMocks(); });

async function setup() {
  const api = installFakeApi();
  const session = installFakeSession(api, { current: "listener" });
  const controller = createSessionController({ channel: null });
  await controller.start();
  const state = controller.getSnapshot();
  if (state.status !== "authenticated") throw new Error("会话未就绪");
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const player = playerHarness();
  const { engine } = player;
  dispose.push(() => { engine.endSession(); controller.dispose(); client.clear(); });
  const view = (children: ReactNode, trackIds: readonly string[] = []) => render(<QueryClientProvider client={client}><SessionProvider controller={controller}>
    <ViewerScopeProvider viewer={state.viewer} generation={state.generation}><PlayerProvider userId={state.viewer.id} controller={engine}><FavoriteStatusScope trackIds={trackIds}>{children}</FavoriteStatusScope></PlayerProvider></ViewerScopeProvider>
  </SessionProvider></QueryClientProvider>);
  return { api, session, controller, engine, player, view };
}

const queueIds = (engine: ReturnType<typeof playerHarness>["engine"]) => engine.getSnapshot().queue.entries.map((entry) => entry.track.trackId);
async function menu(name: string) {
  await userEvent.click(screen.getByRole("button", { name: "更多操作" }));
  await userEvent.click(screen.getByRole("menuitem", { name }));
}
async function ready(name: string) {
  await waitFor(() => expect(screen.getByRole("button", { name }).hasAttribute("disabled")).toBe(false));
}

// 这里用真正的控制器与输出端（服务端与音频元素是替身）验证队列与出声，不把入队动作换成永远成功的桩。
describe("历史页播放动作", () => {
  it("映射保留身份、当前版本、来源事实与封面", () => {
    const item = historyItem(1);
    item.release.artwork = { resource_id: "cover.png", mime: "image/png", width: 640, height: 480 };
    expect(playerTrackOfHistoryItem(item)).toEqual({
      trackId: item.track_id, releaseId: item.release.id, groupId: item.group_id,
      title: item.title, artist: item.artist, artistRefs: item.artist_refs, albumTitle: item.release.title,
      albumArtist: item.release.album_artist ?? item.release.artist, durationSeconds: 120, artworkId: "cover.png",
      source: { sourceKind: "flac_vorbis_comment", codec: "flac", bitDepth: 16, sampleRate: 44100, channels: 2, bitrate: null },
    });
  });

  it("空白标题写（无标题），空白艺人退回专辑艺人；album_artist 与 artwork 为 null 时分别退回发行艺人与空封面", () => {
    const item = historyItem(1, { title: " \t", artist: "\n ", duration_seconds: null });
    item.release.album_artist = "专辑署名";
    expect(playerTrackOfHistoryItem(item)).toMatchObject({ title: "（无标题）", artist: "专辑署名", albumArtist: "专辑署名", durationSeconds: null, artworkId: null });
    item.release.album_artist = null;
    item.release.artist = "发行署名";
    expect(playerTrackOfHistoryItem(item)).toMatchObject({ artist: "发行署名", albumArtist: "发行署名", artworkId: null });
  });

  it("行内播放替换旧队列，只放入这一首并开始播放", async () => {
    const { engine, player, view } = await setup();
    const item = historyItem(1);
    view(<HistoryTrackActions item={item} onRemove={() => undefined} />, [item.track_id]);
    act(() => { engine.enqueue([playerTrack("old-1"), playerTrack("old-2")]); });
    await player.queued(2);
    await ready(`播放 ${item.title}`);
    await userEvent.click(screen.getByRole("button", { name: `播放 ${item.title}` }));
    await waitFor(() => expect(queueIds(engine)).toEqual([item.track_id]));
    await player.sounding();
    expect(engine.getSnapshot().current?.track.trackId).toBe(item.track_id);
  });

  it.each(["下一首播放", "加入队列"])("%s 保持当前曲目、位置和媒体播放不变，插入位置正确", async (action) => {
    const { engine, player, view } = await setup();
    const item = historyItem(1);
    const old = [playerTrack("old-1"), playerTrack("old-2")];
    view(<HistoryTrackActions item={item} onRemove={() => undefined} />, [item.track_id]);
    act(() => { engine.playTracks(old); });
    const audio = await player.sounding(12);
    const current = engine.getSnapshot().current;
    const stream = engine.getSnapshot().zone?.stream;
    await ready(`播放 ${item.title}`);
    await menu(action);
    await waitFor(() => expect(queueIds(engine)).toEqual(action === "下一首播放" ? [old[0]?.trackId, item.track_id, old[1]?.trackId] : [old[0]?.trackId, old[1]?.trackId, item.track_id]));
    expect(engine.getSnapshot().current).toEqual(current);
    expect(engine.getSnapshot().status).toBe("playing");
    expect(engine.getSnapshot().zone?.stream).toBe(stream);
    expect(audio.currentTime).toBe(12);
    expect(audio.paused).toBe(false);
    expect(screen.getByRole("status").textContent).toBe(action === "下一首播放" ? "已设为下一首播放。" : "已加入队列。");
  });

  it.each(["下一首播放", "加入队列"])("队列满时 %s 提示且整队不变，不截断", async (action) => {
    const { engine, player, view } = await setup();
    const item = historyItem(1);
    view(<HistoryTrackActions item={item} onRemove={() => undefined} />, [item.track_id]);
    act(() => { engine.enqueue(Array.from({ length: MAX_QUEUE_ENTRIES }, () => playerTrack("old"))); });
    await player.queued(MAX_QUEUE_ENTRIES);
    const sent = player.server.commands.length;
    await ready(`播放 ${item.title}`);
    await menu(action);
    expect(screen.getByRole("status").textContent).toBe("队列已满，请先移除一些条目。");
    expect(player.server.commands).toHaveLength(sent);
    expect(engine.getSnapshot().queue.entries).toHaveLength(MAX_QUEUE_ENTRIES);
  });

  it("播放全部按候选顺序过滤缺失与不可播项，提示准确加入/跳过数，每次读取最新候选", async () => {
    const { api, engine, view } = await setup();
    const items = [historyItem(3), historyItem(2, { present: false }), historyItem(1), historyItem(4, { source: { ...historyItem(4).source, source_kind: "cue_virtual" } })];
    api.on("GET", "/api/v1/me/plays/queue", () => jsonResponse(200, historyQueue(items)));
    view(<HistoryPlayAll />);
    await ready("播放全部");
    await userEvent.click(screen.getByRole("button", { name: "播放全部" }));
    expect(await screen.findByText("已选择 2 首，跳过 2 首不可播放的曲目。")).toBeTruthy();
    await waitFor(() => expect(queueIds(engine)).toEqual([items[0]?.track_id, items[2]?.track_id]));
    api.on("GET", "/api/v1/me/plays/queue", () => jsonResponse(200, historyQueue([historyItem(5)])));
    await userEvent.click(screen.getByRole("button", { name: "播放全部" }));
    expect(await screen.findByText("已选择 1 首。")).toBeTruthy();
    await waitFor(() => expect(queueIds(engine)).toEqual([historyItem(5).track_id]));
    expect(api.requestsTo("GET", "/api/v1/me/plays/queue")).toHaveLength(2);
  });

  it("超过 1,000 首只处理前 1,000 个候选，截断数量不算跳过，不向更早曲目补位", async () => {
    const { api, engine, view } = await setup();
    const items = Array.from({ length: 1001 }, (_, index) => historyItem(index + 1, { present: index !== 0 }));
    api.on("GET", "/api/v1/me/plays/queue", () => jsonResponse(200, historyQueue(items)));
    view(<HistoryPlayAll />);
    await ready("播放全部");
    await userEvent.click(screen.getByRole("button", { name: "播放全部" }));
    expect(await screen.findByText("已选择 999 首，跳过 1 首不可播放的曲目。历史太长，只取了最近的 1,000 首。")).toBeTruthy();
    await waitFor(() => expect(queueIds(engine)).toEqual(items.slice(1, 1000).map((item) => item.track_id)));
  });

  it.each([false, true])("候选全不可播放时队列不变（truncated=%s）", async (truncated) => {
    const { api, engine, player, view } = await setup();
    const items = Array.from({ length: truncated ? 1001 : 2 }, (_, index) => historyItem(index + 1, { present: false }));
    api.on("GET", "/api/v1/me/plays/queue", () => jsonResponse(200, historyQueue(items)));
    view(<HistoryPlayAll />);
    act(() => { engine.enqueue([playerTrack("old")]); });
    await player.queued(1);
    const before = engine.getSnapshot().queue;
    await ready("播放全部");
    await userEvent.click(screen.getByRole("button", { name: "播放全部" }));
    expect(await screen.findByText(truncated ? "这 1,000 首都不能播放，队列没有变。历史太长，只取了最近的 1,000 首。" : "这 2 首都不能播放，队列没有变。")).toBeTruthy();
    expect(engine.getSnapshot().queue).toBe(before);
  });

  it("候选读取失败可重试，不改变既有队列", async () => {
    const { api, engine, player, view } = await setup();
    api.on("GET", "/api/v1/me/plays/queue", () => jsonResponse(500, { error: { code: "internal_error", message: "读取失败" }, request_id: "queue-failed" }));
    view(<HistoryPlayAll />);
    act(() => { engine.enqueue([playerTrack("old")]); });
    await player.queued(1);
    const before = engine.getSnapshot().queue;
    await ready("播放全部");
    await userEvent.click(screen.getByRole("button", { name: "播放全部" }));
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(engine.getSnapshot().queue).toBe(before);
    api.on("GET", "/api/v1/me/plays/queue", () => jsonResponse(200, historyQueue([historyItem(1)])));
    await userEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByText("已选择 1 首。")).toBeTruthy();
  });

  it("读取候选期间会话切换，旧候选迟到后不能入队", async () => {
    const { api, controller, session, engine, player, view } = await setup();
    const pending = deferred<Response>();
    api.on("GET", "/api/v1/me/plays/queue", () => pending.promise);
    view(<HistoryPlayAll />);
    await ready("播放全部");
    await userEvent.click(screen.getByRole("button", { name: "播放全部" }));
    await waitFor(() => expect(api.requestsTo("GET", "/api/v1/me/plays/queue")).toHaveLength(1));
    session.current = "curator";
    await act(async () => controller.recheck());
    await act(async () => pending.resolve(jsonResponse(200, historyQueue([historyItem(1)]))));
    expect(player.server.commands).toEqual([]);
    expect(queueIds(engine)).toEqual([]);
    expect(screen.queryByText("已选择 1 首。")).toBeNull();
  });
});
