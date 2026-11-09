import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import { Profiler, StrictMode, type ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createSessionController, SessionProvider, ViewerScopeProvider, type SessionController } from "../auth";
import { setSessionBoundary } from "../../shared/api";
import { installFakeApi, installFakeSession } from "../../test/fake-api";
import { fakeZoneServer, testPlayerController } from "../../test/fake-zones";
import { playerTrack } from "../../test/player-data";
import { sampleResponse } from "../../test/http";
import type { PlayerController } from "./zones/controller";
import { PlayerProvider, usePlayer, usePlayerEngine, usePlayerProgress, usePlayerSelector } from "./player-provider";
import { usePlaybackCapabilities } from "./hooks/use-playback-capabilities";

const controllers: SessionController[] = [];
const clients: QueryClient[] = [];
const players: PlayerController[] = [];

afterEach(() => {
  for (const player of players.splice(0)) player.endSession();
  for (const controller of controllers.splice(0)) controller.dispose();
  for (const client of clients.splice(0)) client.clear();
  setSessionBoundary(null);
  vi.restoreAllMocks();
});

async function context() {
  const api = installFakeApi();
  installFakeSession(api, { current: "curator" });
  const controller = createSessionController({ channel: null });
  controllers.push(controller);
  await controller.start();
  const state = controller.getSnapshot();
  if (state.status !== "authenticated") throw new Error("应已登录");
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(client);
  const Wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>
    <SessionProvider controller={controller}>
      <ViewerScopeProvider viewer={state.viewer} generation={state.generation}>{children}</ViewerScopeProvider>
    </SessionProvider>
  </QueryClientProvider>;
  return { api, client, controller, state, Wrapper };
}

function Display() {
  const { snapshot } = usePlayer();
  const progress = usePlayerProgress();
  return <output>{snapshot.current?.track.title ?? "空队列"} · {snapshot.status} · {Math.round(progress.positionSeconds)}</output>;
}

function playerFor(audio = false) {
  const server = fakeZoneServer();
  const test = testPlayerController(server, { audio });
  players.push(test.controller);
  return { server, ...test };
}

describe("播放器provider", () => {
  it("StrictMode模拟卸载后继续订阅；普通卸载释放输出权，队列留在服务端；会话结束同步停声", async () => {
    const { controller, state, Wrapper } = await context();
    const { controller: player, server, playing } = playerFor(true);
    const view = render(<StrictMode><Wrapper><PlayerProvider userId={state.viewer.id} controller={player}><Display /></PlayerProvider></Wrapper></StrictMode>);
    await waitFor(() => expect(server.claims.length).toBeGreaterThan(0));
    act(() => { player.playTracks([playerTrack("a")]); });
    await waitFor(() => expect(playing()).toBeDefined());
    act(() => { playing()?.beginPlaying(); playing()?.tick(12); });
    await waitFor(() => expect(screen.getByText("曲目 a · playing · 12")).toBeTruthy());
    const audio = playing();
    view.unmount();
    expect(audio?.src).toBe("");
    expect(server.released).toBeGreaterThan(0);
    render(<Wrapper><PlayerProvider userId={state.viewer.id} controller={player}><Display /></PlayerProvider></Wrapper>);
    await waitFor(() => expect(screen.getByText(/^曲目 a · /)).toBeTruthy());
    await waitFor(() => expect(playing()).toBeDefined());
    const resumed = playing();
    await act(async () => controller.handleUnauthorized(state.generation));
    // 会话控制器完成串行转换后，清理不依赖新路由渲染/卸载。
    expect(resumed?.src).toBe("");
  });

  it("按需订阅：usePlayerSelector 只在选出的值变化时重绘，usePlayerEngine 不随状态变化重绘", async () => {
    const { state, Wrapper } = await context();
    const { controller: player, server } = playerFor();
    function CurrentTitle() {
      const title = usePlayerSelector((snapshot) => snapshot.current?.track.title);
      return <p>{title ?? "没有当前曲目"}</p>;
    }
    function Commands() {
      const commands = usePlayerEngine();
      return <button onClick={() => commands.enqueue([playerTrack("a")])}>加入队列</button>;
    }
    const commits = { selector: 0, engine: 0 };
    render(<Wrapper><PlayerProvider userId={state.viewer.id} controller={player}>
      <Profiler id="selector" onRender={() => { commits.selector += 1; }}><CurrentTitle /></Profiler>
      <Profiler id="engine" onRender={() => { commits.engine += 1; }}><Commands /></Profiler>
    </PlayerProvider></Wrapper>);
    await waitFor(() => expect(player.getSnapshot().restoring).toBe(false));
    commits.selector = 0;
    commits.engine = 0;
    act(() => player.setVolume(0.3));
    expect(commits).toEqual({ selector: 0, engine: 0 });
    act(() => screen.getByRole("button", { name: "加入队列" }).click());
    await waitFor(() => expect(screen.getByText("曲目 a")).toBeTruthy());
    expect(server.queueIds()).toEqual(["track-a"]);
    expect(commits).toEqual({ selector: 1, engine: 0 });
  });
});

function CapabilityDisplay() {
  const result = usePlaybackCapabilities();
  return <output>{result.data === undefined ? result.status : String(result.data.verdictOf({ sourceKind: "flac_vorbis_comment", codec: "flac" }).playable)}</output>;
}

describe("能力查询缓存", () => {
  it("两个使用者共用格式请求；同一会话重新挂载不过期", async () => {
    const { api, Wrapper } = await context();
    api.on("GET", "/api/v1/playback/formats", () => sampleResponse("playback/formats"));
    const view = render(<Wrapper><CapabilityDisplay /><CapabilityDisplay /></Wrapper>);
    await waitFor(() => expect(screen.getAllByText("true")).toHaveLength(2));
    expect(api.requestsTo("GET", "/api/v1/playback/formats")).toHaveLength(1);
    view.unmount();
    render(<Wrapper><CapabilityDisplay /></Wrapper>);
    expect(screen.getByText("true")).toBeTruthy();
    expect(api.requestsTo("GET", "/api/v1/playback/formats")).toHaveLength(1);
  });

  it("格式接口失败保留错误状态，不当作空能力表", async () => {
    const { api, Wrapper } = await context();
    api.on("GET", "/api/v1/playback/formats", () => sampleResponse("errors/database-unavailable"));
    render(<Wrapper><CapabilityDisplay /></Wrapper>);
    await waitFor(() => expect(screen.getByText("error")).toBeTruthy());
  });
});
