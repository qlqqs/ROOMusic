import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import { Profiler, StrictMode, type ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createSessionController, SessionProvider, ViewerScopeProvider, type SessionController } from "../auth";
import { setSessionBoundary } from "../../shared/api";
import { installFakeApi, installFakeSession } from "../../test/fake-api";
import { FakeAudio } from "../../test/fake-audio";
import { memoryStorage } from "../../test/memory-storage";
import { playerTrack } from "../../test/player-data";
import { sampleResponse } from "../../test/http";
import { PlayerEngine } from "./engine/player-engine";
import { queueStorageKey } from "./engine/persistence";
import { PlayerProvider, usePlayer, usePlayerEngine, usePlayerProgress, usePlayerSelector } from "./player-provider";
import { usePlaybackCapabilities } from "./hooks/use-playback-capabilities";

const controllers: SessionController[] = [];
const clients: QueryClient[] = [];
const engines: PlayerEngine[] = [];

afterEach(() => {
  for (const engine of engines.splice(0)) engine.endSession();
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
  return { api, controller, state, Wrapper };
}

function Display() {
  const { snapshot } = usePlayer();
  const progress = usePlayerProgress();
  return <output>{snapshot.current?.track.title ?? "空队列"} · {snapshot.status} · {progress.positionSeconds}</output>;
}

function engineFor(userId: string) {
  const audio = new FakeAudio();
  const storage = memoryStorage();
  const engine = new PlayerEngine({
    userId, audio,
    loadPlayback: async (trackId) => ({ track_id: trackId, representation: { kind: "original", url: `/api/v1/tracks/${trackId}/audio`, mime_type: "audio/flac", codecs: null, size_bytes: 100 } }),
    loadRelease: async () => { throw new Error("没有保存的队列"); },
    sessionStorage: storage, localStorage: null, mediaSession: null,
    artworkUrl: (id) => id, newId: () => "entry", random: () => 0, now: Date.now,
  });
  engines.push(engine);
  return { engine, audio, storage };
}

describe("播放器provider", () => {
  it("StrictMode模拟卸载后继续订阅；普通卸载保留队列，会话结束同步清理", async () => {
    const { controller, state, Wrapper } = await context();
    const { engine, audio, storage } = engineFor(state.viewer.id);
    const view = render(<StrictMode><Wrapper><PlayerProvider userId={state.viewer.id} engine={engine}><Display /></PlayerProvider></Wrapper></StrictMode>);
    await act(async () => { engine.playTracks([playerTrack("a")]); });
    act(() => { audio.loadMetadata(100); audio.beginPlaying(); audio.tick(12); });
    expect(screen.getByText("曲目 a · playing · 12")).toBeTruthy();
    view.unmount();
    expect(audio.src).toBe("");
    expect(storage.data.has(queueStorageKey(state.viewer.id))).toBe(true);
    render(<Wrapper><PlayerProvider userId={state.viewer.id} engine={engine}><Display /></PlayerProvider></Wrapper>);
    expect(screen.getByText("曲目 a · paused · 12")).toBeTruthy();
    await act(async () => controller.handleUnauthorized(state.generation));
    // 会话控制器完成串行转换后，清理不依赖新路由渲染/卸载。
    expect(audio.src).toBe("");
    expect(storage.data.has(queueStorageKey(state.viewer.id))).toBe(false);
    expect(screen.getByText("空队列 · idle · 0")).toBeTruthy();
  });

  it("按需订阅：usePlayerSelector 只在选出的值变化时重绘，usePlayerEngine 不随引擎变化重绘", async () => {
    const { state, Wrapper } = await context();
    const { engine } = engineFor(state.viewer.id);
    function CurrentTitle() {
      const title = usePlayerSelector((snapshot) => snapshot.current?.track.title);
      return <p>{title ?? "没有当前曲目"}</p>;
    }
    function Commands() {
      const commands = usePlayerEngine();
      return <button onClick={() => commands.enqueue([playerTrack("a")])}>加入队列</button>;
    }
    const commits = { selector: 0, engine: 0 };
    render(<Wrapper><PlayerProvider userId={state.viewer.id} engine={engine}>
      <Profiler id="selector" onRender={() => { commits.selector += 1; }}><CurrentTitle /></Profiler>
      <Profiler id="engine" onRender={() => { commits.engine += 1; }}><Commands /></Profiler>
    </PlayerProvider></Wrapper>);
    commits.selector = 0;
    commits.engine = 0;
    act(() => engine.setVolume(0.3));
    expect(commits).toEqual({ selector: 0, engine: 0 });
    act(() => screen.getByRole("button", { name: "加入队列" }).click());
    expect(screen.getByText("曲目 a")).toBeTruthy();
    expect(commits).toEqual({ selector: 1, engine: 0 });
  });

  it("默认装配使用预检API，浏览器存储属性不可读取也能播放", async () => {
    const { api, state, Wrapper } = await context();
    const audio = new FakeAudio();
    vi.stubGlobal("Audio", class { constructor() { return audio; } });
    const storageGetter = vi.spyOn(window, "sessionStorage", "get").mockImplementation(() => { throw new DOMException("禁用", "SecurityError"); });
    const trackId = "00000000-0000-4000-8000-000000000001";
    api.on("GET", `/api/v1/tracks/${trackId}/playback`, () => sampleResponse("tracks/playback"));
    function Command() {
      const { engine } = usePlayer();
      return <button onClick={() => engine.playTracks([playerTrack("a", { trackId })])}>播放</button>;
    }
    render(<Wrapper><PlayerProvider userId={state.viewer.id}><Command /><Display /></PlayerProvider></Wrapper>);
    act(() => screen.getByRole("button", { name: "播放" }).click());
    await waitFor(() => expect(audio.sources).toEqual([`/api/v1/tracks/${trackId}/audio`]));
    expect(api.requestsTo("GET", `/api/v1/tracks/${trackId}/playback`)).toHaveLength(1);
    storageGetter.mockRestore();
  });
});

function CapabilityDisplay() {
  const result = usePlaybackCapabilities();
  return <output>{result.data === undefined ? result.status : String(result.data.verdictOf({ sourceKind: "flac_vorbis_comment", codec: "flac" }).playable)}</output>;
}

describe("能力查询缓存", () => {
  it("两个使用者共用格式请求与类型探测；同一会话重新挂载不过期", async () => {
    const { api, Wrapper } = await context();
    const probe = vi.spyOn(HTMLMediaElement.prototype, "canPlayType").mockReturnValue("probably");
    api.on("GET", "/api/v1/playback/formats", () => sampleResponse("playback/formats"));
    const view = render(<Wrapper><CapabilityDisplay /><CapabilityDisplay /></Wrapper>);
    await waitFor(() => expect(screen.getAllByText("true")).toHaveLength(2));
    expect(api.requestsTo("GET", "/api/v1/playback/formats")).toHaveLength(1);
    const calls = probe.mock.calls.length;
    view.unmount();
    render(<Wrapper><CapabilityDisplay /></Wrapper>);
    expect(screen.getByText("true")).toBeTruthy();
    expect(probe.mock.calls).toHaveLength(calls);
  });

  it("格式接口失败保留错误状态，不当作空能力表", async () => {
    const { api, Wrapper } = await context();
    api.on("GET", "/api/v1/playback/formats", () => sampleResponse("errors/database-unavailable"));
    render(<Wrapper><CapabilityDisplay /></Wrapper>);
    await waitFor(() => expect(screen.getByText("error")).toBeTruthy());
  });
});
