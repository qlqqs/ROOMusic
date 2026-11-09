import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createSessionController, SessionProvider, ViewerScopeProvider } from "../../features/auth";
import { PlayerProvider } from "../../features/player";
import { artistDataOf, artistTestId, primaryArtistId } from "../../test/artist-data";
import { installArtistsApi, installFakeApi, installFakeSession } from "../../test/fake-api";
import { playerHarness } from "../../test/fake-zones";
import { jsonResponse, sampleResponse } from "../../test/http";
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
  return { api, data, session, controller, engine, player, view };
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


describe("艺人播放全部与随机播放", () => {
  it("不预取，点击每次重读，缺失曲目跳过；替换队列但保持随机与循环模式", async () => {
    const { api, engine, view, data } = await setup();
    view(<ArtistPlayActions artistId={artistId} />);
    await ready();
    expect(api.requestsTo("GET", path)).toHaveLength(0);
    act(() => { engine.playTracks([playerTrack("旧曲目")]); engine.setMode("shuffle"); });
    await userEvent.click(screen.getByRole("button", { name: "播放全部" }));
    expect(await screen.findByText("已选择 2 首。跳过了1 首文件缺失。")).toBeTruthy();
    await waitFor(() => expect(queueIds(engine)).toEqual(data.tracks.get(`${artistId}:track_artist`)?.filter((item) => item.present).map((item) => item.track_id)));
    await waitFor(() => expect(engine.getSnapshot().queue.mode).toBe("shuffle"));
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
      await waitFor(() => expect(queueIds(engine)).toEqual(expectedIds));
    }
    const queries = api.requestsTo("GET", path).map((request) => new URL(request.path, "http://test").searchParams);
    expect(queries).toHaveLength(2);
    expect(queries.every((query) => query.get("order") === "random")).toBe(true);
    expect(new Set(queries.map((query) => query.get("seed"))).size).toBe(2);
    await waitFor(() => expect(engine.getSnapshot().queue.mode).toBe("repeat_all"));
  });

  it("超过一万首候选只接收一万首并显示截断提示，缺失与浏览器不支持分别提示", async () => {
    const { data, engine, view } = await setup();
    const sample = data.tracks.get(`${artistId}:track_artist`)?.[0];
    if (!sample) throw new Error("缺少样本");
    data.tracks.set(`${artistId}:track_artist`, Array.from({ length: 10_002 }, (_, index) => ({ ...sample, track_id: artistTestId(index + 1), present: index !== 10_001, source: index === 0 ? { ...sample.source, codec: "unknown" } : sample.source })));
    view(<ArtistPlayActions artistId={artistId} />);
    await ready();
    await userEvent.click(screen.getByRole("button", { name: "播放全部" }));
    expect(await screen.findByText(/已选择 9,999 首。跳过了1 首文件缺失、1 首不能在这里播放。曲目较多，本次最多播放 10,000 首。/)).toBeTruthy();
    await waitFor(() => expect(queueIds(engine)).toHaveLength(9999));
    await waitFor(() => expect(queueIds(engine)).not.toContain(artistTestId(10_001)));
  });

  it.each(["empty", "missing", "unsupported", "error"])("%s 时保留当前队列、音频与进度", async (scenario) => {
    const { api, data, player, view } = await setup();
    const sample = data.tracks.get(`${artistId}:track_artist`)?.[0];
    if (!sample) throw new Error("缺少样本");
    data.tracks.set(`${artistId}:track_artist`, scenario === "empty" ? [] : [{ ...sample, present: scenario !== "missing", source: { ...sample.source, codec: "unknown" } }]);
    if (scenario === "error") api.on("GET", path, () => sampleResponse("errors/database-unavailable"));
    view(<ArtistPlayActions artistId={artistId} />);
    await ready();
    const before = await startOldPlayback(player);
    await userEvent.click(screen.getByRole("button", { name: "播放全部" }));
    if (scenario === "error") await screen.findByText("没能读取曲目");
    else await screen.findByText(/队列没有变/);
    expectOldPlayback(player, before);
  });

  it("播放器拒绝queue_full时不清空原队列，显示可恢复提示", async () => {
    const { engine, player, view } = await setup();
    view(<ArtistPlayActions artistId={artistId} />);
    await ready();
    const before = await startOldPlayback(player);
    // 返回值边界注入，不制造违反10000上限、无法通过decoder的响应。
    vi.spyOn(engine, "playTracks").mockReturnValue("queue_full");
    await userEvent.click(screen.getByRole("button", { name: "播放全部" }));
    await screen.findByText(/队列已满/);
    expectOldPlayback(player, before);
  });
});
