import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import { Profiler, StrictMode, type ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createSessionController, SessionProvider, ViewerScopeProvider, type SessionController } from "../auth";
import { setSessionBoundary } from "../../shared/api";
import { sampleBody } from "../../test/api-samples";
import { installFakeApi, installFakeSession } from "../../test/fake-api";
import { FakeAudio } from "../../test/fake-audio";
import { memoryStorage } from "../../test/memory-storage";
import { playerTrack } from "../../test/player-data";
import { jsonResponse, sampleResponse } from "../../test/http";
import { listeningKeys } from "../listening";
import { decodePlaybackFormats } from "./api/decoders";
import { buildCapabilityTable } from "./engine/capability";
import { PlayerEngine } from "./engine/player-engine";
import { queueStorageKey } from "./engine/persistence";
import { silentPlayRecorder } from "../../test/play-recorder";
import { PlayerProvider, usePlayer, usePlayerEngine, usePlayerProgress, usePlayerSelector } from "./player-provider";
import { usePlaybackCapabilities } from "./hooks/use-playback-capabilities";
import { playerKeys } from "./query-keys";

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
  return { api, client, controller, state, Wrapper };
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
    loadPlayback: async (trackId) => ({ state: "ready", playback: { track_id: trackId, representation: { kind: "original", url: `/api/v1/tracks/${trackId}/audio`, mime_type: "audio/flac", codecs: null, size_bytes: 100 } } }),
    shouldPrefetch: () => false,
    loadRelease: async () => { throw new Error("没有保存的队列"); },
    sessionStorage: storage, localStorage: null, mediaSession: null,
    artworkUrl: (id) => id, newId: () => "entry", random: () => 0, now: Date.now, playRecorder: silentPlayRecorder,
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

  /** 默认装配：点“播放”开始一首 60 秒的曲目（门槛 30 秒）并让媒体出声；返回已发出的播放上报。 */
  async function startListening(options: { reportResponse: () => Response }) {
    const { api, client, controller, state, Wrapper } = await context();
    const audio = new FakeAudio();
    vi.stubGlobal("Audio", class { constructor() { return audio; } });
    const trackId = "00000000-0000-4000-8000-000000000001";
    api.on("GET", `/api/v1/tracks/${trackId}/playback`, () => sampleResponse("tracks/playback"));
    api.on("POST", "/api/v1/me/plays", options.reportResponse);
    function Command() {
      const { engine } = usePlayer();
      return <button onClick={() => engine.playTracks([playerTrack("a", { trackId })])}>播放</button>;
    }
    render(<Wrapper><PlayerProvider userId={state.viewer.id}><Command /><Display /></PlayerProvider></Wrapper>);
    act(() => screen.getByRole("button", { name: "播放" }).click());
    await waitFor(() => expect(audio.sources).toEqual([`/api/v1/tracks/${trackId}/audio`]));
    act(() => { audio.loadMetadata(60); audio.beginPlaying(); });
    return {
      client, controller, state, trackId,
      plays: () => api.requestsTo("POST", "/api/v1/me/plays"),
      // 引擎的计次时钟是真实时间：每次前进 1 秒，在容忍范围内，累计与浏览器里一样。
      listenUntil: (second: number) => act(() => { for (let at = Math.floor(audio.currentTime) + 1; at <= second; at += 1) audio.tick(at); }),
    };
  }

  it("默认装配：播放过半后带幂等键上报一次，成功后让这个会话的“最近播放”查询失效", async () => {
    const { client, state, trackId, plays, listenUntil } = await startListening({ reportResponse: () => sampleResponse("listening/play-recorded") });
    const recentKey = listeningKeys.recentReleases(state.generation);
    client.setQueryData(recentKey, { cached: true });
    listenUntil(29);
    expect(plays()).toHaveLength(0);
    listenUntil(30);
    await waitFor(() => expect(plays()).toHaveLength(1));
    const [request] = plays();
    expect(request?.body).toEqual({ track_id: trackId, played_seconds: 30 });
    expect(request?.headers.get("Idempotency-Key")).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/);
    await waitFor(() => expect(client.getQueryState(recentKey)?.isInvalidated).toBe(true));
    listenUntil(59);
    expect(plays()).toHaveLength(1);
  });

  it("默认装配：上报失败时用同一个幂等键有限重试；会话结束后等待重试的上报不再发出", async () => {
    // 网络失败：结果不确定，上报器重试。假定时器下不能用 waitFor（它收尾时等一个真实的 setTimeout），请求本身是同步发出的。
    const { controller, state, plays, listenUntil } = await startListening({ reportResponse: () => { throw new TypeError("网络中断"); } });
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    try {
      listenUntil(30);
      expect(plays()).toHaveLength(1);
      await act(async () => { await vi.advanceTimersByTimeAsync(2_000); });
      expect(plays()).toHaveLength(2);
      expect(new Set(plays().map((request) => request.headers.get("Idempotency-Key"))).size).toBe(1);
      // 第二次也失败了，10 秒后还有一次重试在等；会话结束时它被作废。
      await act(async () => controller.handleUnauthorized(state.generation));
      await act(async () => { await vi.advanceTimersByTimeAsync(120_000); });
      expect(plays()).toHaveLength(2);
    } finally {
      vi.useRealTimers();
    }
  });

  it("默认装配收到 202 后进入“正在准备”并立即再次预检，直到收到派生表示才加载音频", async () => {
    const { api, state, Wrapper } = await context();
    const audio = new FakeAudio();
    vi.stubGlobal("Audio", class { constructor() { return audio; } });
    const trackId = "00000000-0000-4000-8000-000000000001";
    let calls = 0;
    api.on("GET", `/api/v1/tracks/${trackId}/playback`, () => (++calls < 3 ? sampleResponse("tracks/playback-preparing") : sampleResponse("tracks/playback-derived")));
    function Command() {
      const { engine } = usePlayer();
      return <button onClick={() => engine.playTracks([playerTrack("a", { trackId, source: { sourceKind: "m4a", codec: "alac", bitDepth: 24, sampleRate: 96000, channels: 2, bitrate: null } })])}>播放</button>;
    }
    render(<Wrapper><PlayerProvider userId={state.viewer.id}><Command /><Display /></PlayerProvider></Wrapper>);
    act(() => screen.getByRole("button", { name: "播放" }).click());
    // 测试里的 202 立即返回：引擎按最小间隔（1 秒）再次预检，两次 202 共约 2 秒；真实服务端会把预检挂起约 20 秒，不会触发这个间隔。
    await waitFor(() => expect(screen.getByText("曲目 a · preparing · 0")).toBeTruthy());
    await waitFor(() => expect(audio.sources).toEqual([`/api/v1/tracks/${trackId}/audio`]), { timeout: 6000 });
    expect(api.requestsTo("GET", `/api/v1/tracks/${trackId}/playback`)).toHaveLength(3);
    expect(audio.sources).toHaveLength(1);
  });

  it("默认装配对状态码与响应体不符的预检不循环：200 带 preparing 按无效响应处理", async () => {
    const { api, state, Wrapper } = await context();
    const audio = new FakeAudio();
    vi.stubGlobal("Audio", class { constructor() { return audio; } });
    const trackId = "00000000-0000-4000-8000-000000000001";
    api.on("GET", `/api/v1/tracks/${trackId}/playback`, () => jsonResponse(200, sampleBody("tracks/playback-preparing")));
    function Command() {
      const { engine } = usePlayer();
      return <button onClick={() => engine.playTracks([playerTrack("a", { trackId })])}>播放</button>;
    }
    render(<Wrapper><PlayerProvider userId={state.viewer.id}><Command /><Display /></PlayerProvider></Wrapper>);
    act(() => screen.getByRole("button", { name: "播放" }).click());
    await waitFor(() => expect(screen.getByText("曲目 a · paused · 0")).toBeTruthy());
    expect(api.requestsTo("GET", `/api/v1/tracks/${trackId}/playback`)).toHaveLength(1);
    expect(audio.sources).toEqual([]);
  });

  describe("预取下一首", () => {
    const first = "00000000-0000-4000-8000-000000000001";
    const second = "00000000-0000-4000-8000-000000000002";
    const alac = { sourceKind: "m4a", codec: "alac", bitDepth: 16, sampleRate: 44100, channels: 2, bitrate: null };

    async function start(cacheCapabilities: boolean) {
      const { api, client, state, Wrapper } = await context();
      const audio = new FakeAudio();
      vi.stubGlobal("Audio", class { constructor() { return audio; } });
      api.on("GET", `/api/v1/tracks/${first}/playback`, () => sampleResponse("tracks/playback"));
      api.on("GET", `/api/v1/tracks/${second}/playback`, () => jsonResponse(200, { track_id: second, representation: { kind: "derived", url: `/api/v1/tracks/${second}/audio`, mime_type: "audio/flac", codecs: null, size_bytes: 100 } }));
      if (cacheCapabilities) {
        const table = await buildCapabilityTable(decodePlaybackFormats(sampleBody("playback/formats")).items, { canPlayType: () => "probably" });
        client.setQueryData(playerKeys.capabilities(state.generation), table);
      }
      function Command() {
        const { engine } = usePlayer();
        return <button onClick={() => engine.playTracks([playerTrack("a", { trackId: first }), playerTrack("b", { trackId: second, source: alac })])}>播放</button>;
      }
      render(<Wrapper><PlayerProvider userId={state.viewer.id}><Command /><Display /></PlayerProvider></Wrapper>);
      act(() => screen.getByRole("button", { name: "播放" }).click());
      await waitFor(() => expect(audio.sources).toEqual([`/api/v1/tracks/${first}/audio`]));
      act(() => { audio.loadMetadata(100); audio.beginPlaying(); });
      return { api };
    }

    it("能力表显示下一首要经转换时，当前曲目出声后预检它一次，不改变当前状态", async () => {
      const { api } = await start(true);
      await waitFor(() => expect(api.requestsTo("GET", `/api/v1/tracks/${second}/playback`)).toHaveLength(1));
      expect(screen.getByText("曲目 a · playing · 0")).toBeTruthy();
    });

    it("还没有取得能力表时不预取，播放照常", async () => {
      const { api } = await start(false);
      expect(screen.getByText("曲目 a · playing · 0")).toBeTruthy();
      expect(api.requestsTo("GET", `/api/v1/tracks/${second}/playback`)).toHaveLength(0);
    });
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
