import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createSessionController, SessionProvider, ViewerScopeProvider } from "../../features/auth";
import { PlayerEngine, PlayerProvider } from "../../features/player";
import { artistDataOf, artistTestId, primaryArtistId } from "../../test/artist-data";
import { installArtistsApi, installFakeApi, installFakeSession } from "../../test/fake-api";
import { FakeAudio } from "../../test/fake-audio";
import { jsonResponse, sampleResponse } from "../../test/http";
import { silentPlayRecorder } from "../../test/play-recorder";
import { playerTrack } from "../../test/player-data";
import { ArtistPlayActions } from "./artist-playback";

const dispose: (() => void)[] = [];
afterEach(() => { for (const cleanup of dispose.splice(0)) cleanup(); vi.restoreAllMocks(); });
const artistId = primaryArtistId();
const path = `/api/v1/artists/${artistId}/queue`;
async function setup() {
  const api = installFakeApi();
  const session = installFakeSession(api, { current: "listener" });
  const data = installArtistsApi(api, artistDataOf(), session);
  const controller = createSessionController({ channel: null });
  await controller.start();
  const state = controller.getSnapshot();
  if (state.status !== "authenticated") throw new Error("会话未就绪");
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const audio = new FakeAudio();
  let nextId = 0;
  const engine = new PlayerEngine({
    userId: state.viewer.id, audio,
    loadPlayback: async (trackId) => ({ state: "ready", playback: { track_id: trackId, representation: { kind: "original", url: `/api/v1/tracks/${trackId}/audio`, mime_type: "audio/flac", codecs: null, size_bytes: 100 } } }),
    loadRelease: async () => { throw new Error("本测试没有持久化队列"); },
    shouldPrefetch: () => false,
    sessionStorage: null, localStorage: null, mediaSession: null,
    artworkUrl: (value) => value, newId: () => `entry-${++nextId}`, random: () => 0, now: Date.now, playRecorder: silentPlayRecorder,
  });
  const canPlay = vi.spyOn(HTMLMediaElement.prototype, "canPlayType").mockReturnValue("probably");
  dispose.push(() => { engine.endSession(); controller.dispose(); client.clear(); });
  const view = (children: ReactNode) => render(children, {
    wrapper: ({ children: content }) => (
      <QueryClientProvider client={client}><SessionProvider controller={controller}>
        <ViewerScopeProvider viewer={state.viewer} generation={state.generation}>
          <PlayerProvider userId={state.viewer.id} engine={engine}>{content}</PlayerProvider>
        </ViewerScopeProvider>
      </SessionProvider></QueryClientProvider>
    ),
  });
  return { api, data, session, controller, engine, audio, canPlay, view };
}

const queueIds = (engine: PlayerEngine) => engine.getSnapshot().queue.entries.map((entry) => entry.track.trackId);
async function ready() {
  await waitFor(() => expect(screen.getByRole("button", { name: "播放全部" }).hasAttribute("disabled")).toBe(false));
}
async function startOldPlayback(engine: PlayerEngine, audio: FakeAudio) {
  await act(async () => { engine.playTracks([playerTrack("旧队列甲"), playerTrack("旧队列乙")]); });
  act(() => { audio.loadMetadata(120); audio.beginPlaying(); audio.tick(17); });
  return { queue: engine.getSnapshot().queue, current: engine.getSnapshot().current, plays: audio.playCalls, pauses: audio.pauseCalls };
}
function expectOldPlayback(engine: PlayerEngine, audio: FakeAudio, before: Awaited<ReturnType<typeof startOldPlayback>>) {
  expect(engine.getSnapshot().queue).toBe(before.queue);
  expect(engine.getSnapshot().current).toEqual(before.current);
  expect(engine.getSnapshot().status).toBe("playing");
  expect(audio.currentTime).toBe(17);
  expect(audio.playCalls).toBe(before.plays);
  expect(audio.pauseCalls).toBe(before.pauses);
}


describe("艺人播放全部与随机播放", () => {
  it("不预取，点击每次重读，缺失曲目跳过；替换队列但保持随机与循环模式", async () => {
    const { api, engine, view, data } = await setup();
    view(<ArtistPlayActions artistId={artistId} />);
    await ready();
    expect(api.requestsTo("GET", path)).toHaveLength(0);
    act(() => { engine.playTracks([playerTrack("旧曲目")]); engine.setMode("shuffle"); });
    await userEvent.click(screen.getByRole("button", { name: "播放全部" }));
    expect(await screen.findByText("已选择 2 首。跳过了1 首文件缺失。")).toBeTruthy();
    expect(queueIds(engine)).toEqual(data.tracks.get(`${artistId}:track_artist`)?.filter((item) => item.present).map((item) => item.track_id));
    expect(engine.getSnapshot().queue.mode).toBe("shuffle");
    await userEvent.click(screen.getByRole("button", { name: "播放全部" }));
    await waitFor(() => expect(api.requestsTo("GET", path)).toHaveLength(2));
    expect(api.requestsTo("GET", path).every((request) => request.path.endsWith("order=album"))).toBe(true);
  });

  it("随机播放每次新的seed，精确保留服务端乱序，不切换播放模式", async () => {
    const { api, engine, view, data } = await setup();
    const sample = data.tracks.get(`${artistId}:track_artist`)?.[0];
    if (!sample) throw new Error("缺少曲目样本");
    const expectedIds = [artistTestId(9), artistTestId(2), artistTestId(7)];
    const items = expectedIds.map((trackId, index) => ({ ...sample, track_id: trackId, title: `服务端乱序曲目 ${index + 1}` }));
    // HTTP边界返回固定的非ID顺序；不调用替身随机算法计算预期值，防止前端再次排序也通过。
    api.on("GET", path, (request) => {
      const query = new URL(request.path, "http://test").searchParams;
      return jsonResponse(200, { artist_id: artistId, order: "random", seed: query.get("seed"), items, total: 3, returned: 3, limit: 10_000, truncated: false, missing_total: 1 });
    });
    view(<ArtistPlayActions artistId={artistId} />);
    await ready();
    act(() => engine.setMode("repeat_all"));
    for (let i = 0; i < 2; i++) {
      await userEvent.click(screen.getByRole("button", { name: "随机播放" }));
      await screen.findByText("已选择 3 首。跳过了1 首文件缺失。");
      expect(queueIds(engine)).toEqual(expectedIds);
    }
    const queries = api.requestsTo("GET", path).map((request) => new URL(request.path, "http://test").searchParams);
    expect(queries).toHaveLength(2);
    expect(queries.every((query) => query.get("order") === "random")).toBe(true);
    expect(new Set(queries.map((query) => query.get("seed"))).size).toBe(2);
    expect(engine.getSnapshot().queue.mode).toBe("repeat_all");
  });

  it("超过一万首候选只接收一万首并显示截断提示，缺失与浏览器不支持分别提示", async () => {
    const { data, engine, view } = await setup();
    const sample = data.tracks.get(`${artistId}:track_artist`)?.[0];
    if (!sample) throw new Error("缺少样本");
    data.tracks.set(`${artistId}:track_artist`, Array.from({ length: 10_002 }, (_, index) => ({ ...sample, track_id: artistTestId(index + 1), present: index !== 10_001, source: index === 0 ? { ...sample.source, source_kind: "cue_virtual" } : sample.source })));
    view(<ArtistPlayActions artistId={artistId} />);
    await ready();
    await userEvent.click(screen.getByRole("button", { name: "播放全部" }));
    expect(await screen.findByText(/已选择 9,999 首。跳过了1 首文件缺失、1 首不能在这里播放。曲目较多，本次最多播放 10,000 首。/)).toBeTruthy();
    expect(queueIds(engine)).toHaveLength(9999);
    expect(queueIds(engine)).not.toContain(artistTestId(10_001));
  });

  it.each(["empty", "missing", "unsupported", "error"])("%s 时保留当前队列、音频与进度", async (scenario) => {
    const { api, data, engine, audio, view } = await setup();
    const sample = data.tracks.get(`${artistId}:track_artist`)?.[0];
    if (!sample) throw new Error("缺少样本");
    data.tracks.set(`${artistId}:track_artist`, scenario === "empty" ? [] : [{ ...sample, present: scenario !== "missing", source: { ...sample.source, source_kind: "cue_virtual" } }]);
    if (scenario === "error") api.on("GET", path, () => sampleResponse("errors/database-unavailable"));
    view(<ArtistPlayActions artistId={artistId} />);
    await ready();
    const before = await startOldPlayback(engine, audio);
    await userEvent.click(screen.getByRole("button", { name: "播放全部" }));
    if (scenario === "error") await screen.findByText("没能读取曲目");
    else await screen.findByText(/队列没有变/);
    expectOldPlayback(engine, audio, before);
  });

  it("播放器拒绝queue_full时不清空原队列，显示可恢复提示", async () => {
    const { engine, audio, view } = await setup();
    view(<ArtistPlayActions artistId={artistId} />);
    await ready();
    const before = await startOldPlayback(engine, audio);
    // 返回值边界注入，不制造违反10000上限、无法通过decoder的响应。
    vi.spyOn(engine, "playTracks").mockReturnValue("queue_full");
    await userEvent.click(screen.getByRole("button", { name: "播放全部" }));
    await screen.findByText(/队列已满/);
    expectOldPlayback(engine, audio, before);
  });
});
