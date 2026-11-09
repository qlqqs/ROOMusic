import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createSessionController, SessionProvider, ViewerScopeProvider } from "../features/auth";
import { PlayerProvider } from "../features/player";
import { MAX_QUEUE_ENTRIES } from "../features/player/engine/queue";
import type { SearchTrackItemDTO } from "../features/search";
import { deferred } from "../test/deferred";
import { installFakeApi, installFakeSession, type FakeApi } from "../test/fake-api";
import { playerHarness } from "../test/fake-zones";
import { jsonResponse, sampleResponse } from "../test/http";
import { playerTrack } from "../test/player-data";
import { searchTrackItem } from "../test/search-data";
import { SearchPlaybackNotice, SearchTrackActions } from "./routes/search-playback";
import { TrackFavoritesScope } from "./routes/track-favorites-scope";

const cleanups: (() => void)[] = [];
afterEach(() => { for (const cleanup of cleanups.splice(0)) cleanup(); vi.restoreAllMocks(); });

async function setup(item: SearchTrackItemDTO = searchTrackItem(), configure?: (api: FakeApi) => void) {
  const api = installFakeApi();
  installFakeSession(api, { current: "listener" });
  configure?.(api);
  const controller = createSessionController({ channel: null });
  await controller.start();
  const state = controller.getSnapshot();
  if (state.status !== "authenticated") throw new Error("应已登录");
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const player = playerHarness();
  const { engine } = player;
  cleanups.push(() => { engine.endSession(); controller.dispose(); client.clear(); });
  render(<QueryClientProvider client={client}><SessionProvider controller={controller}>
    <ViewerScopeProvider viewer={state.viewer} generation={state.generation}>
      <PlayerProvider userId={state.viewer.id} controller={engine}>
        <TrackFavoritesScope tracks={[item]}><SearchPlaybackNotice /><SearchTrackActions item={item} /></TrackFavoritesScope>
      </PlayerProvider>
    </ViewerScopeProvider>
  </SessionProvider></QueryClientProvider>);
  return { api, player, engine, item };
}

async function playable() {
  await waitFor(() => expect(screen.getByRole("button", { name: /^播放 / }).hasAttribute("disabled")).toBe(false));
}
async function menu(name: string) {
  await userEvent.click(screen.getByRole("button", { name: "更多操作" }));
  await userEvent.click(screen.getByRole("menuitem", { name }));
}
/** 先放一个 count 首的旧队列（列表循环），出声到 17 秒；返回在放的元素。 */
async function start(player: ReturnType<typeof playerHarness>, count = 2) {
  act(() => { player.engine.playTracks(Array.from({ length: count }, (_, index) => playerTrack(String(index)))); player.engine.setMode("repeat_all"); });
  const audio = await player.sounding(17);
  await waitFor(() => expect(player.engine.getSnapshot().queue.mode).toBe("repeat_all"));
  await player.queued(count);
  return audio;
}

describe("搜索曲目播放动作", () => {
  it("播放只替换为这一首，摘要完整映射并保留队列模式", async () => {
    const { item, engine, player } = await setup();
    await playable();
    await start(player);
    await userEvent.click(screen.getByRole("button", { name: `播放 ${item.title}` }));
    await waitFor(() => expect(engine.getSnapshot().current?.track.trackId).toBe(item.track_id));
    expect(player.server.commands.at(-1)).toEqual({ command: "play_tracks", track_ids: [item.track_id], start_index: 0 });
    const queue = engine.getSnapshot().queue;
    expect(queue.entries).toHaveLength(1);
    expect(queue.mode).toBe("repeat_all");
    expect(queue.entries[0]?.track).toEqual({
      trackId: item.track_id, releaseId: item.release.id, groupId: item.group_id,
      title: item.title, artist: item.artist, artistRefs: item.artist_refs, albumTitle: item.release.title, albumArtist: item.release.album_artist ?? item.release.artist,
      durationSeconds: item.duration_seconds, artworkId: item.release.artwork?.resource_id ?? null,
      source: { sourceKind: item.source.source_kind, codec: item.source.codec, bitDepth: item.source.bit_depth, sampleRate: item.source.sample_rate, channels: item.source.channels, bitrate: item.source.bitrate },
    });
  });

  it.each(["下一首播放", "加入队列"])("%s 不打断当前曲目、进度、模式或原条目身份", async (action) => {
    const { engine, player, item } = await setup();
    await playable();
    const audio = await start(player);
    const before = engine.getSnapshot();
    const sources = [...audio.sources];
    const playCalls = audio.playCalls;
    const pauseCalls = audio.pauseCalls;
    await menu(action);
    await waitFor(() => expect(engine.getSnapshot().queue.entries.map((entry) => entry.track.trackId)).toEqual(action === "下一首播放"
      ? ["track-0", item.track_id, "track-1"] : ["track-0", "track-1", item.track_id]));
    const after = engine.getSnapshot();
    expect(after.current?.entryId).toBe(before.current?.entryId);
    expect(after.queue.entries.filter((entry) => entry.track.trackId.startsWith("track-"))).toEqual(before.queue.entries);
    expect(after.queue.mode).toBe("repeat_all");
    expect(after.status).toBe("playing");
    expect(engine.getProgress().positionSeconds).toBe(17);
    expect(audio.sources).toEqual(sources);
    expect(audio.playCalls).toBe(playCalls);
    expect(audio.pauseCalls).toBe(pauseCalls);
    expect(screen.getByRole("status").textContent).toBe(action === "下一首播放" ? "已设为下一首播放。" : "已加入队列。");
  });

  it.each(["下一首播放", "加入队列"])("队列满时 %s 给提示，不截断或清空任何已有条目", async (action) => {
    const { engine, player } = await setup();
    await playable();
    const audio = await start(player, MAX_QUEUE_ENTRIES);
    const queue = engine.getSnapshot().queue;
    const sources = [...audio.sources];
    const sent = player.server.commands.length;
    await menu(action);
    expect(screen.getByRole("status").textContent).toBe("队列已满，请先移除一些条目。");
    expect(player.server.commands).toHaveLength(sent);
    expect(engine.getSnapshot().queue).toBe(queue);
    expect(engine.getSnapshot().queue.entries).toHaveLength(MAX_QUEUE_ENTRIES);
    expect(engine.getProgress().positionSeconds).toBe(17);
    expect(audio.sources).toEqual(sources);
  });

  it.each([
    { kind: "missing", reason: "文件缺失，不能播放" },
    { kind: "cue", reason: "CUE 分轨曲目需要切分，目前不能播放" },
    { kind: "unknown", reason: "这种格式目前不能播放" },
  ])("$kind 不可播，原因与禁用播放按钮关联且两个入队动作也禁用", async ({ kind, reason }) => {
    const item = searchTrackItem();
    if (kind === "missing") item.present = false;
    if (kind === "cue") item.source.source_kind = "cue_virtual";
    if (kind === "unknown") item.source.codec = "unknown";
    const { engine } = await setup(item);
    await screen.findByText(reason);
    const button = screen.getByRole("button", { name: /^播放 / });
    expect(button.hasAttribute("disabled")).toBe(true);
    expect(document.getElementById(button.getAttribute("aria-describedby") ?? "")?.textContent).toBe(reason);
    await userEvent.click(screen.getByRole("button", { name: "更多操作" }));
    for (const action of ["下一首播放", "加入队列"]) expect(screen.getByRole("menuitem", { name: action }).hasAttribute("disabled")).toBe(true);
    expect(engine.getSnapshot().queue.entries).toHaveLength(0);
  });

  it("能力尚未读到时不把 present 当作可播；读取失败可在曲目栏重试", async () => {
    const pending = deferred<Response>();
    const { api } = await setup(searchTrackItem(), (fake) => fake.on("GET", "/api/v1/playback/formats", () => pending.promise));
    expect(screen.getByRole("button", { name: /^播放 / }).hasAttribute("disabled")).toBe(true);
    expect(screen.getByText("正在检查播放能力")).toBeTruthy();
    await act(async () => pending.resolve(jsonResponse(500, { error: { code: "internal_error", message: "读取失败" }, request_id: "formats-error" })));
    await screen.findByText("无法检查播放能力");
    expect(screen.getByRole("alert").textContent).toContain("formats-error");
    expect(screen.getByText("无法检查播放能力，请在曲目列表上方重试。")).toBeTruthy();
    expect(screen.getByRole("button", { name: /^播放 / }).hasAttribute("disabled")).toBe(true);
    api.on("GET", "/api/v1/playback/formats", () => sampleResponse("playback/formats"));
    await userEvent.click(screen.getByRole("button", { name: "重试检查" }));
    await playable();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(api.requestsTo("GET", "/api/v1/playback/formats")).toHaveLength(2);
  });
});
