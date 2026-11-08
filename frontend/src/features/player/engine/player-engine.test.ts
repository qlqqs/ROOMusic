import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../../../shared/api";
import { sampleBody } from "../../../test/api-samples";
import { FakeAudio } from "../../../test/fake-audio";
import { memoryStorage } from "../../../test/memory-storage";
import { playerTrack } from "../../../test/player-data";
import { fakePlayRecorder } from "../../../test/play-recorder";
import { decodeReleaseDetail } from "../../catalog/api/decoders";
import type { TrackPlaybackResult } from "../api/endpoints";
import type { MediaSessionActionName, MediaSessionMetadata, MediaSessionPort } from "./media-session";
import { isPlaybackActive, PlayerEngine, type EngineDeps, type PlayerEvent } from "./player-engine";
import { loadSavedQueue, queueStorageKey, saveQueue, VOLUME_STORAGE_KEY } from "./persistence";
import { PLAYBACK_SETTINGS_STORAGE_KEY } from "./playback-settings";
import type { AudioContextLike, GainNodeLike } from "./audio-graph";
import { decodeTrackAnalysis } from "../api/decoders";
import type { PlayerTrack } from "./queue";

const userId = "00000000-0000-4000-8000-000000000002";

const track = (name: string, overrides: Partial<PlayerTrack> = {}) => playerTrack(name, overrides);

/** 预检通过：kind 是原文件直出或已转换好的派生文件。 */
function playbackOf(trackId: string, kind: "original" | "derived" = "original"): TrackPlaybackResult {
  return { state: "ready", playback: { track_id: trackId, representation: { kind, url: `/api/v1/tracks/${trackId}/audio`, mime_type: "audio/flac", codecs: null, size_bytes: 1000 } } };
}
/** 预检收到 202：派生仍在进行。 */
const preparing: TrackPlaybackResult = { state: "preparing" };

const httpError = (status: number, code: string) => new ApiError({ kind: "http", status, code, message: "后端文案", requestId: "req-1" });
const networkError = () => new ApiError({ kind: "network", requestId: "req-1" });

/** 让已排队的 Promise 回调都执行完。 */
async function settle(): Promise<void> {
  for (let turn = 0; turn < 8; turn += 1) await Promise.resolve();
}

class FakeMediaSession implements MediaSessionPort {
  metadata: MediaSessionMetadata | null = null;
  state: "none" | "paused" | "playing" = "none";
  position: { duration: number; position: number } | null = null;
  readonly handlers = new Map<MediaSessionActionName, ((details: { seekTime?: number | undefined }) => void) | null>();
  positionCalls = 0;
  setMetadata = (metadata: MediaSessionMetadata | null) => void (this.metadata = metadata);
  setPlaybackState = (state: "none" | "paused" | "playing") => void (this.state = state);
  setPositionState = (state: { duration: number; position: number } | null) => {
    this.positionCalls += 1;
    this.position = state;
  };
  setActionHandler = (action: MediaSessionActionName, handler: ((details: { seekTime?: number | undefined }) => void) | null) => void this.handlers.set(action, handler);
  invoke(action: MediaSessionActionName, details: { seekTime?: number } = {}) {
    const handler = this.handlers.get(action);
    if (handler === null || handler === undefined) throw new Error(`没有登记 ${action}`);
    handler(details);
  }
}

type Harness = ReturnType<typeof createHarness>;
const engines: PlayerEngine[] = [];

function createHarness(overrides: Partial<EngineDeps> = {}, options: { attach?: boolean } = {}) {
  const audio = new FakeAudio();
  const sessionStorage = memoryStorage();
  const localStorage = memoryStorage();
  const mediaSession = new FakeMediaSession();
  const clock = { now: 1_000_000 };
  const outcomes = new Map<string, () => Promise<TrackPlaybackResult>>();
  let counter = 0;
  const loadPlayback = vi.fn((trackId: string, _signal: AbortSignal) => (outcomes.get(trackId) ?? (() => Promise.resolve(playbackOf(trackId))))());
  const loadRelease = vi.fn(async (_releaseId: string): Promise<never> => {
    throw new Error("未登记发行");
  });
  const shouldPrefetch = vi.fn((_track: PlayerTrack) => false);
  const recorder = fakePlayRecorder();
  const deps: EngineDeps = {
    userId,
    audio,
    loadPlayback,
    shouldPrefetch,
    loadRelease,
    sessionStorage,
    localStorage,
    mediaSession,
    artworkUrl: (id) => `http://localhost/api/v1/artworks/${id}`,
    newId: () => `e${++counter}`,
    random: () => 0,
    now: () => clock.now,
    playRecorder: recorder.recorder,
    ...overrides,
  };
  const engine = new PlayerEngine(deps);
  engines.push(engine);
  if (options.attach ?? true) engine.attach();
  return {
    engine,
    audio,
    sessionStorage,
    localStorage,
    mediaSession,
    clock,
    loadPlayback,
    loadRelease,
    shouldPrefetch,
    /** 引擎交给记录出口的播放（计次达到规则的那一刻），以及出口被作废的次数。 */
    played: recorder.played,
    discardPlays: recorder.discard,
    /** 让某首曲目的预检返回指定结果（Promise 或抛出的错误）。 */
    preflight: (trackId: string, outcome: () => Promise<TrackPlaybackResult>) => void outcomes.set(trackId, outcome),
    /**
     * 让某首曲目的预检依次返回给定的结果，用完之后一直返回最后一个。收到“正在准备”的那次预检像真实服务端一样先挂起 serverWaitMs
     * （推进注入的时钟）：默认 20 秒，不触发两次预检之间的最小间隔；传 0 模拟立即返回的 202。
     */
    preflightSequence: (trackId: string, results: readonly TrackPlaybackResult[], serverWaitMs = 20_000) => {
      let index = 0;
      outcomes.set(trackId, () => {
        const result = results[Math.min(index++, results.length - 1)] as TrackPlaybackResult;
        if (result.state === "preparing") clock.now += serverWaitMs;
        return Promise.resolve(result);
      });
    },
    fail: (trackId: string, error: Error) => void outcomes.set(trackId, () => Promise.reject(error)),
  };
}

/** 开始播放并让媒体真实出声。 */
async function playAndStart(h: Harness, tracks: readonly PlayerTrack[], startIndex = 0, duration = 100): Promise<void> {
  h.engine.playTracks(tracks, startIndex);
  await settle();
  h.audio.loadMetadata(duration);
  h.audio.beginPlaying();
}

const state = (h: Harness) => h.engine.getSnapshot();
const progress = (h: Harness) => h.engine.getProgress();
const currentTitle = (h: Harness) => state(h).current?.track.title;
const preflightedTracks = (h: Harness) => h.loadPlayback.mock.calls.map(([trackId]) => trackId);
const savedFor = (h: Harness) => JSON.parse(h.sessionStorage.data.get(queueStorageKey(userId)) ?? "null") as { entries: { entryId: string }[]; currentEntryId: string | null; positionSeconds: number; mode: string } | null;

beforeEach(() => {
  vi.stubGlobal("requestAnimationFrame", undefined);
});

afterEach(() => {
  for (const engine of engines.splice(0)) engine.endSession();
  vi.useRealTimers();
});

describe("播放一首曲目", () => {
  it("先预检再设置媒体来源并请求播放；只有媒体事件才让状态变成正在播放", async () => {
    const h = createHarness();
    h.engine.playTracks([track("a"), track("b"), track("c")], 1);
    expect(state(h).status).toBe("loading");
    expect(currentTitle(h)).toBe("曲目 b");
    expect(h.audio.sources).toEqual([]);

    await settle();
    expect(preflightedTracks(h)).toEqual(["track-b"]);
    expect(h.audio.sources).toEqual(["/api/v1/tracks/track-b/audio"]);
    expect(h.audio.playCalls).toBe(1);
    // play() 已经发出，但媒体还没有出声：不能显示“正在播放”。
    expect(state(h).status).toBe("loading");

    h.audio.loadMetadata(180);
    expect(state(h).status).toBe("loading");
    h.audio.beginPlaying();
    expect(state(h).status).toBe("playing");
  });

  it("进度、时长与可定位范围只取自媒体事件", async () => {
    const h = createHarness();
    h.engine.playTracks([track("a")]);
    await settle();
    expect(progress(h)).toEqual({ positionSeconds: 0, durationSeconds: null, seekable: false });
    h.audio.loadMetadata(180);
    expect(progress(h)).toEqual({ positionSeconds: 0, durationSeconds: 180, seekable: true });
    h.audio.beginPlaying();
    h.audio.tick(12.5);
    expect(progress(h).positionSeconds).toBe(12.5);
  });

  it("时长未知（无限或不是数字）或没有可定位范围时不能拖动，seek 不生效", async () => {
    const h = createHarness();
    h.engine.playTracks([track("a")]);
    await settle();
    h.audio.loadMetadata(Number.POSITIVE_INFINITY);
    h.audio.beginPlaying();
    expect(progress(h)).toMatchObject({ durationSeconds: null, seekable: false });
    h.engine.seek(30);
    expect(h.audio.currentTime).toBe(0);

    h.audio.loadMetadata(100, { seekable: false });
    expect(progress(h)).toMatchObject({ durationSeconds: 100, seekable: false });
    h.engine.seek(30);
    expect(h.audio.currentTime).toBe(0);
  });

  it("可以拖动时 seek 定位到目标，限制在 0 与时长之间，忽略非有限的值", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a")], 0, 100);
    h.engine.seek(42);
    expect(h.audio.currentTime).toBe(42);
    h.engine.seek(500);
    expect(h.audio.currentTime).toBe(100);
    h.engine.seek(-5);
    expect(h.audio.currentTime).toBe(0);
    h.audio.currentTime = 7;
    h.engine.seek(Number.NaN);
    expect(h.audio.currentTime).toBe(7);
  });

  it("缓冲与恢复：waiting 变成缓冲中，playing 回到播放中", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a")]);
    h.audio.stall();
    expect(state(h).status).toBe("buffering");
    h.audio.beginPlaying();
    expect(state(h).status).toBe("playing");
  });

  it("stalled 只说明网络暂时没送来数据：已缓冲的音频照常出声，状态不变成缓冲中", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a")]);
    h.audio.emit("stalled");
    h.audio.tick(5);
    expect(state(h).status).toBe("playing");
  });

  it("暂停来自媒体事件；播放继续时不重新预检", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a")]);
    h.engine.pause();
    expect(state(h).status).toBe("paused");
    h.engine.togglePlayback();
    expect(preflightedTracks(h)).toEqual(["track-a"]);
    expect(h.audio.playCalls).toBe(2);
    h.audio.beginPlaying();
    expect(state(h).status).toBe("playing");
    h.engine.togglePlayback();
    expect(state(h).status).toBe("paused");
  });

  it("外部暂停（例如系统打断）也使状态变成暂停", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a")]);
    h.audio.pause();
    expect(state(h).status).toBe("paused");
  });

  it("预检还在途中时暂停：放弃这次加载，晚到的预检结果被丢弃", async () => {
    const h = createHarness();
    let resolveFirst: (value: TrackPlaybackResult) => void = () => undefined;
    h.preflight("track-a", () => new Promise((resolve) => (resolveFirst = resolve)));
    h.engine.playTracks([track("a")]);
    expect(state(h).status).toBe("loading");
    h.engine.pause();
    expect(state(h).status).toBe("paused");
    resolveFirst(playbackOf("track-a"));
    await settle();
    expect(h.audio.sources).toEqual([]);
    expect(h.audio.playCalls).toBe(0);
    expect(state(h).status).toBe("paused");
  });

  it("切歌时放弃上一首还在途中的预检（取消请求），只播放最新的一首", async () => {
    const h = createHarness();
    const signals: AbortSignal[] = [];
    let resolveFirst: (value: TrackPlaybackResult) => void = () => undefined;
    h.loadPlayback.mockImplementationOnce((_trackId, signal) => {
      signals.push(signal);
      return new Promise((resolve) => (resolveFirst = resolve));
    });
    h.engine.playTracks([track("a")]);
    h.engine.playTracks([track("b")]);
    expect(signals[0]?.aborted).toBe(true);
    resolveFirst(playbackOf("track-a"));
    await settle();
    expect(h.audio.sources).toEqual(["/api/v1/tracks/track-b/audio"]);
  });

  it("切歌之后、新的来源就绪之前，旧媒体迟到的事件被忽略", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a"), track("b"), track("c")]);
    h.preflight("track-b", () => new Promise(() => undefined));
    h.engine.next();
    const preflights = h.loadPlayback.mock.calls.length;
    h.audio.finish();
    h.audio.fail(3);
    await settle();
    expect(h.loadPlayback.mock.calls.length).toBe(preflights);
    expect(state(h).queue.currentEntryId).toBe("e2");
    expect(state(h).status).toBe("loading");
  });

  it("每次开始加载都产生 track_started 事件，序号递增", async () => {
    const h = createHarness();
    h.engine.playTracks([track("a"), track("b")]);
    const first = state(h).event;
    expect(first).toMatchObject({ kind: "track_started", entryId: "e1" });
    await settle();
    h.engine.next();
    const second = state(h).event;
    expect(second).toMatchObject({ kind: "track_started", entryId: "e2" });
    expect((second?.seq ?? 0) > (first?.seq ?? 0)).toBe(true);
  });
});

describe("预检失败（T5）", () => {
  it.each([
    ["404", httpError(404, "not_found"), "not_found"],
    ["409 目录已停用", httpError(409, "root_disabled"), "root_disabled"],
    ["409 不能直出", httpError(409, "not_directly_playable"), "not_directly_playable"],
    ["409 文件无法读取", httpError(409, "source_unavailable"), "source_unavailable"],
    ["409 文件已变化", httpError(409, "source_changed"), "source_changed"],
    ["不认识的 409", httpError(409, "something_new"), "source_unavailable"],
    ["不认识的 4xx", httpError(403, "forbidden"), "source_unavailable"],
  ] as const)("%s：标为失败并显示原因，按模式前进到下一首", async (_name, error, reason) => {
    const h = createHarness();
    h.fail("track-a", error);
    h.engine.playTracks([track("a"), track("b")]);
    await settle();
    expect(state(h).failures).toEqual({ e1: reason });
    expect(currentTitle(h)).toBe("曲目 b");
    expect(h.audio.sources).toEqual(["/api/v1/tracks/track-b/audio"]);
    expect(state(h).event).toMatchObject({ kind: "track_started", entryId: "e2" });
  });

  it("失败事件带着原因，供界面公告", async () => {
    const h = createHarness();
    h.fail("track-a", httpError(409, "source_changed"));
    h.engine.playTracks([track("a")]);
    await settle();
    expect(state(h).event).toMatchObject({ kind: "all_failed" });
    expect(state(h).failures).toEqual({ e1: "source_changed" });
  });

  it("连续失败按模式一首首前进，全部失败时停下，不对同一首无休止重试", async () => {
    const h = createHarness();
    for (const name of ["a", "b", "c"]) h.fail(`track-${name}`, httpError(404, "not_found"));
    h.engine.playTracks([track("a"), track("b"), track("c")]);
    await settle();
    expect(preflightedTracks(h)).toEqual(["track-a", "track-b", "track-c"]);
    expect(state(h).status).toBe("paused");
    expect(state(h).event).toMatchObject({ kind: "all_failed" });
    expect(h.audio.sources).toEqual([]);
  });

  it("列表循环与随机模式下全部失败也会停下", async () => {
    for (const mode of ["repeat_all", "shuffle", "repeat_one"] as const) {
      const h = createHarness();
      h.engine.setMode(mode);
      for (const name of ["a", "b"]) h.fail(`track-${name}`, httpError(409, "root_disabled"));
      h.engine.playTracks([track("a"), track("b")]);
      await settle();
      expect(h.loadPlayback.mock.calls.length, mode).toBe(2);
      expect(state(h).event, mode).toMatchObject({ kind: "all_failed" });
    }
  });

  it("只有一首的列表循环失败后不再重试自己", async () => {
    const h = createHarness();
    h.engine.setMode("repeat_all");
    h.fail("track-a", httpError(404, "not_found"));
    h.engine.playTracks([track("a")]);
    await settle();
    expect(h.loadPlayback.mock.calls.length).toBe(1);
    expect(state(h).status).toBe("paused");
  });

  it("最后一首失败时停在暂停，没有全部失败就不公告“全部失败”", async () => {
    const h = createHarness();
    h.fail("track-b", httpError(404, "not_found"));
    await playAndStart(h, [track("a"), track("b")]);
    h.audio.finish();
    await settle();
    expect(state(h).failures).toEqual({ e2: "not_found" });
    expect(state(h).status).toBe("paused");
    expect(state(h).event).toMatchObject({ kind: "track_failed", entryId: "e2" });
  });

  it("自动前进跳过已知失败的条目；用户手动选中它时重新尝试并清除失败标记", async () => {
    const h = createHarness();
    h.fail("track-b", httpError(404, "not_found"));
    h.engine.playTracks([track("a"), track("b"), track("c")]);
    await settle();
    h.audio.loadMetadata(10);
    h.audio.beginPlaying();
    h.engine.next();
    await settle();
    expect(currentTitle(h)).toBe("曲目 c");
    expect(state(h).failures).toEqual({ e2: "not_found" });
    h.preflight("track-b", () => Promise.resolve(playbackOf("track-b")));
    h.engine.playEntry("e2");
    await settle();
    expect(state(h).failures).toEqual({});
    expect(currentTitle(h)).toBe("曲目 b");
    expect(h.audio.getAttribute("src")).toBe("/api/v1/tracks/track-b/audio");
  });

  it("401 停下播放，不标记失败也不前进（会话边界负责结束会话）", async () => {
    const h = createHarness();
    h.fail("track-a", httpError(401, "unauthorized"));
    h.engine.playTracks([track("a"), track("b")]);
    await settle();
    expect(state(h).failures).toEqual({});
    expect(state(h).fault).toBeNull();
    expect(state(h).status).toBe("paused");
    expect(h.loadPlayback.mock.calls.length).toBe(1);
    expect(h.audio.sources).toEqual([]);
  });

  it.each([
    ["网络失败", networkError()],
    ["服务暂时不可用（503）", httpError(503, "database_unavailable")],
    ["内部错误（500）", httpError(500, "internal")],
    ["无效响应", new ApiError({ kind: "invalid_response", status: 200, requestId: "req-1" })],
  ])("%s：停在暂停并可重试，不连续跳歌，也不标记失败", async (_name, error) => {
    const h = createHarness();
    h.fail("track-a", error);
    h.engine.playTracks([track("a"), track("b")]);
    await settle();
    expect(state(h).status).toBe("paused");
    expect(state(h).fault).toEqual({ kind: "transient" });
    expect(state(h).failures).toEqual({});
    expect(currentTitle(h)).toBe("曲目 a");
    expect(h.loadPlayback.mock.calls.length).toBe(1);
    expect(state(h).event).toMatchObject({ kind: "transient_failure" });
  });

  it("重试：重新预检并开始播放，故障清除", async () => {
    const h = createHarness();
    h.fail("track-a", networkError());
    h.engine.playTracks([track("a")]);
    await settle();
    h.preflight("track-a", () => Promise.resolve(playbackOf("track-a")));
    h.engine.retry();
    expect(state(h).fault).toBeNull();
    expect(state(h).status).toBe("loading");
    await settle();
    expect(h.audio.sources).toEqual(["/api/v1/tracks/track-a/audio"]);
    h.audio.loadMetadata(60);
    h.audio.beginPlaying();
    expect(state(h).status).toBe("playing");
  });

  it("没有故障时 retry 什么也不做", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a")]);
    h.engine.retry();
    expect(h.loadPlayback.mock.calls.length).toBe(1);
  });
});

describe("媒体元素出错：再次预检来分类", () => {
  it("预检通过：判定为解码失败，标为失败并前进", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a"), track("b")]);
    h.audio.fail(3);
    expect(state(h).status).toBe("loading");
    await settle();
    expect(state(h).failures).toEqual({ e1: "decode_failed" });
    expect(currentTitle(h)).toBe("曲目 b");
    expect(preflightedTracks(h)).toEqual(["track-a", "track-a", "track-b"]);
  });

  it("格式不支持（MEDIA_ERR_SRC_NOT_SUPPORTED）同样是解码失败", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a")]);
    h.audio.fail(4);
    await settle();
    expect(state(h).failures).toEqual({ e1: "decode_failed" });
  });

  it("预检给出具体原因时按原因标记：音频出错是因为目录刚被停用", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a"), track("b")]);
    h.fail("track-a", httpError(409, "root_disabled"));
    h.audio.fail(2);
    await settle();
    expect(state(h).failures).toEqual({ e1: "root_disabled" });
    expect(currentTitle(h)).toBe("曲目 b");
  });

  it("预检得到 401：停下，会话边界结束会话", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a"), track("b")]);
    h.fail("track-a", httpError(401, "unauthorized"));
    h.audio.fail(2);
    await settle();
    expect(state(h).failures).toEqual({});
    expect(state(h).status).toBe("paused");
    expect(currentTitle(h)).toBe("曲目 a");
  });

  it("传输中断（MEDIA_ERR_NETWORK）而预检通过：暂时性故障，保留位置，重试从停下的位置继续", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a")]);
    h.audio.tick(41.5);
    h.audio.fail(2);
    await settle();
    expect(state(h).fault).toEqual({ kind: "transient" });
    expect(state(h).failures).toEqual({});
    expect(progress(h).positionSeconds).toBe(41.5);
    h.engine.retry();
    await settle();
    h.audio.loadMetadata(100);
    expect(h.audio.currentTime).toBe(41.5);
  });

  it("预检也遇到网络失败：暂时性故障并保留位置", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a")]);
    h.audio.tick(10);
    h.fail("track-a", networkError());
    h.audio.fail(2);
    await settle();
    expect(state(h).fault).toEqual({ kind: "transient" });
    expect(progress(h).positionSeconds).toBe(10);
  });

  it("用户主动中止（MEDIA_ERR_ABORTED）不当作失败", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a")]);
    const preflights = h.loadPlayback.mock.calls.length;
    h.audio.fail(1);
    await settle();
    expect(h.loadPlayback.mock.calls.length).toBe(preflights);
    expect(state(h).failures).toEqual({});
    expect(state(h).status).toBe("playing");
  });

  it("play() 被拒并且不是自动播放策略（不支持的来源）：按媒体错误分类", async () => {
    const h = createHarness();
    h.audio.playQueue.push("not-supported");
    h.engine.playTracks([track("a"), track("b")]);
    await settle();
    expect(state(h).failures).toEqual({ e1: "decode_failed" });
    expect(currentTitle(h)).toBe("曲目 b");
  });

  it("play() 被新的加载打断（AbortError）不是失败", async () => {
    const h = createHarness();
    h.audio.playBehavior = "abort";
    h.engine.playTracks([track("a")]);
    await settle();
    expect(state(h).failures).toEqual({});
    expect(state(h).fault).toBeNull();
    expect(state(h).status).toBe("loading");
  });
});

describe("自动播放被拒绝", () => {
  it("停在暂停并提示按播放，不显示正在播放；再按播放时直接 play()，不重新预检", async () => {
    const h = createHarness();
    h.audio.playBehavior = "not-allowed";
    h.engine.playTracks([track("a")]);
    await settle();
    expect(state(h).status).toBe("paused");
    expect(state(h).fault).toEqual({ kind: "autoplay_blocked" });
    expect(state(h).event).toMatchObject({ kind: "autoplay_blocked" });

    h.audio.playBehavior = "resolve";
    h.engine.play();
    expect(h.loadPlayback.mock.calls.length).toBe(1);
    expect(h.audio.playCalls).toBe(2);
    expect(state(h).fault).toBeNull();
    h.audio.loadMetadata(30);
    h.audio.beginPlaying();
    expect(state(h).status).toBe("playing");
  });
});

describe("自然结束与播放模式", () => {
  it("顺序：播完前进到下一首，末曲结束后停在暂停", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a"), track("b")]);
    h.audio.finish();
    await settle();
    expect(currentTitle(h)).toBe("曲目 b");
    expect(h.audio.getAttribute("src")).toBe("/api/v1/tracks/track-b/audio");
    h.audio.loadMetadata(50);
    h.audio.beginPlaying();
    h.audio.finish();
    await settle();
    expect(currentTitle(h)).toBe("曲目 b");
    expect(state(h).status).toBe("paused");
    expect(preflightedTracks(h)).toEqual(["track-a", "track-b"]);
  });

  it("列表循环：末曲结束后回到首曲", async () => {
    const h = createHarness();
    h.engine.setMode("repeat_all");
    await playAndStart(h, [track("a"), track("b")], 1);
    h.audio.finish();
    await settle();
    expect(currentTitle(h)).toBe("曲目 a");
  });

  it("单曲循环：自然结束从头再播当前曲目，不重新预检；手动下一首照常切换", async () => {
    const h = createHarness();
    h.engine.setMode("repeat_one");
    await playAndStart(h, [track("a"), track("b")]);
    h.audio.finish();
    await settle();
    expect(currentTitle(h)).toBe("曲目 a");
    expect(h.audio.currentTime).toBe(0);
    expect(h.audio.playCalls).toBe(2);
    expect(preflightedTracks(h)).toEqual(["track-a"]);
    h.audio.beginPlaying();
    h.engine.next();
    await settle();
    expect(currentTitle(h)).toBe("曲目 b");
  });

  it("随机：按注入的随机数选下一首，不连续重复", async () => {
    const h = createHarness({ random: () => 0.99 });
    h.engine.setMode("shuffle");
    await playAndStart(h, [track("a"), track("b"), track("c")]);
    h.audio.finish();
    await settle();
    expect(currentTitle(h)).toBe("曲目 c");
    h.audio.loadMetadata(10);
    h.audio.beginPlaying();
    h.audio.finish();
    await settle();
    expect(currentTitle(h)).toBe("曲目 b");
  });

  it("自然结束跳过已知失败的条目", async () => {
    const h = createHarness();
    h.fail("track-b", httpError(404, "not_found"));
    h.engine.playTracks([track("a"), track("b"), track("c")]);
    await settle();
    h.audio.loadMetadata(10);
    h.audio.beginPlaying();
    h.audio.finish();
    await settle();
    expect(currentTitle(h)).toBe("曲目 c");
    expect(state(h).failures).toEqual({ e2: "not_found" });
  });
});

describe("手动切歌", () => {
  it("暂停时按下一首也开始播放；末项没有下一首时不动", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a"), track("b")]);
    h.engine.pause();
    h.engine.next();
    await settle();
    expect(currentTitle(h)).toBe("曲目 b");
    expect(h.audio.playCalls).toBe(2);
    const calls = h.loadPlayback.mock.calls.length;
    h.engine.next();
    expect(currentTitle(h)).toBe("曲目 b");
    expect(h.loadPlayback.mock.calls.length).toBe(calls);
    expect(state(h).hasNext).toBe(false);
  });

  it("上一首回到列表里的上一项；在队首时当前曲目从头播放", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a"), track("b")], 1);
    h.engine.previous();
    await settle();
    expect(currentTitle(h)).toBe("曲目 a");
    h.audio.loadMetadata(100);
    h.audio.beginPlaying();
    h.audio.tick(33);
    h.engine.previous();
    expect(currentTitle(h)).toBe("曲目 a");
    expect(h.audio.currentTime).toBe(0);
  });

  it("随机模式的上一首沿实际播放历史返回", async () => {
    const h = createHarness({ random: () => 0.99 });
    h.engine.setMode("shuffle");
    await playAndStart(h, [track("a"), track("b"), track("c")]);
    h.engine.next();
    await settle();
    expect(currentTitle(h)).toBe("曲目 c");
    h.engine.previous();
    await settle();
    expect(currentTitle(h)).toBe("曲目 a");
  });

  it("随机模式上一首之后再下一首：离开的条目本轮仍会轮到，不被跳过", async () => {
    const h = createHarness();
    const start = async () => {
      await settle();
      h.audio.loadMetadata(100);
      h.audio.beginPlaying();
    };
    h.engine.setMode("shuffle");
    await playAndStart(h, [track("a"), track("b"), track("c"), track("d")]);
    h.engine.next();
    await start();
    expect(currentTitle(h)).toBe("曲目 b");
    h.engine.previous();
    await start();
    expect(currentTitle(h)).toBe("曲目 a");
    const visited: (string | undefined)[] = [];
    for (let step = 0; step < 3; step += 1) {
      h.engine.next();
      await start();
      visited.push(currentTitle(h));
    }
    expect(visited).toEqual(["曲目 c", "曲目 d", "曲目 b"]);
  });

  it("没有队列时下一首与上一首什么也不做", () => {
    const h = createHarness();
    h.engine.next();
    h.engine.previous();
    expect(h.loadPlayback).not.toHaveBeenCalled();
    expect(state(h).status).toBe("idle");
  });
});

describe("队列操作", () => {
  it("下一首播放与加入队列不打断当前播放、不重新加载", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a"), track("b")]);
    h.audio.tick(20);
    expect(h.engine.playNext([track("x")])).toBe("added");
    expect(h.engine.enqueue([track("y")])).toBe("added");
    expect(state(h).queue.entries.map((entry) => entry.track.title)).toEqual(["曲目 a", "曲目 x", "曲目 b", "曲目 y"]);
    expect(currentTitle(h)).toBe("曲目 a");
    expect(state(h).status).toBe("playing");
    expect(h.audio.sources).toHaveLength(1);
    expect(progress(h).positionSeconds).toBe(20);
  });

  it("队列为空时加入第一首：队列有了当前项，停在暂停，不预检也不发声", () => {
    const h = createHarness();
    expect(h.engine.enqueue([track("a"), track("b")])).toBe("added");
    expect(currentTitle(h)).toBe("曲目 a");
    expect(state(h).status).toBe("paused");
    expect(h.loadPlayback).not.toHaveBeenCalled();
    expect(h.audio.playCalls).toBe(0);
  });

  it("超过 10,000 条时整批拒绝并报告", () => {
    const h = createHarness();
    h.engine.enqueue(Array.from({ length: 10_000 }, (_, index) => track(String(index))));
    expect(h.engine.enqueue([track("x")])).toBe("queue_full");
    expect(h.engine.playNext([track("x")])).toBe("queue_full");
    expect(state(h).queue.entries).toHaveLength(10_000);
  });

  it("移除当前项且正在播放：切到后继并继续播放", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a"), track("b"), track("c")]);
    h.engine.removeEntry("e1");
    await settle();
    expect(currentTitle(h)).toBe("曲目 b");
    expect(h.audio.getAttribute("src")).toBe("/api/v1/tracks/track-b/audio");
    expect(state(h).queue.entries).toHaveLength(2);
  });

  it("移除当前项且已暂停：停在后继上，不预检也不发声", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a"), track("b")]);
    h.engine.pause();
    const preflights = h.loadPlayback.mock.calls.length;
    h.engine.removeEntry("e1");
    expect(currentTitle(h)).toBe("曲目 b");
    expect(state(h).status).toBe("paused");
    expect(h.loadPlayback.mock.calls.length).toBe(preflights);
    expect(h.audio.getAttribute("src")).toBeNull();
  });

  it("移除没有后继的当前项：停在空闲（队列里剩下的条目保留）", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a"), track("b")], 1);
    h.engine.removeEntry("e2");
    expect(state(h).status).toBe("idle");
    expect(state(h).current).toBeNull();
    expect(state(h).queue.entries).toHaveLength(1);
    expect(h.audio.getAttribute("src")).toBeNull();
    // 此时按播放从队首开始。
    h.engine.play();
    await settle();
    expect(currentTitle(h)).toBe("曲目 a");
  });

  it("移除非当前项不影响播放", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a"), track("b")]);
    h.engine.removeEntry("e2");
    expect(currentTitle(h)).toBe("曲目 a");
    expect(state(h).status).toBe("playing");
    expect(h.audio.sources).toHaveLength(1);
  });

  it("上移与下移不改变当前项与播放", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a"), track("b"), track("c")]);
    h.engine.moveEntry("e3", -1);
    expect(state(h).queue.entries.map((entry) => entry.track.title)).toEqual(["曲目 a", "曲目 c", "曲目 b"]);
    expect(currentTitle(h)).toBe("曲目 a");
    expect(state(h).status).toBe("playing");
  });

  it("清空队列：停止播放、放弃媒体，回到空闲", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a"), track("b")]);
    h.engine.clearQueue();
    expect(state(h).status).toBe("idle");
    expect(state(h).queue.entries).toEqual([]);
    expect(h.audio.getAttribute("src")).toBeNull();
    expect(progress(h)).toEqual({ positionSeconds: 0, durationSeconds: null, seekable: false });
  });

  it("播放此项：当前项正在播放时不动，已暂停时继续，其他项切换过去", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a"), track("b")]);
    h.engine.playEntry("e1");
    expect(h.audio.playCalls).toBe(1);
    h.engine.pause();
    h.engine.playEntry("e1");
    expect(h.audio.playCalls).toBe(2);
    h.audio.beginPlaying();
    h.engine.playEntry("e2");
    await settle();
    expect(currentTitle(h)).toBe("曲目 b");
    expect(preflightedTracks(h)).toEqual(["track-a", "track-b"]);
  });

  it("播放模式切换保存在队列里，清空后保留", () => {
    const h = createHarness();
    h.engine.setMode("repeat_all");
    h.engine.enqueue([track("a")]);
    h.engine.clearQueue();
    expect(state(h).queue.mode).toBe("repeat_all");
  });

  it("同一首曲目多次入队是独立的条目，移除其中一个不影响另一个", async () => {
    const h = createHarness();
    h.engine.enqueue([track("a"), track("a")]);
    h.engine.removeEntry("e1");
    expect(state(h).queue.entries.map((entry) => entry.entryId)).toEqual(["e2"]);
  });
});

describe("保存与会话结束", () => {
  it("队列变化时保存标识、模式与位置，不保存标题", async () => {
    const h = createHarness();
    h.engine.setMode("repeat_all");
    h.engine.enqueue([track("a", { title: "秘密标题" }), track("b")]);
    const saved = savedFor(h);
    expect(saved?.entries.map((entry) => entry.entryId)).toEqual(["e1", "e2"]);
    expect(saved?.mode).toBe("repeat_all");
    expect(h.sessionStorage.data.get(queueStorageKey(userId))).not.toContain("秘密标题");
  });

  it("暂停时保存当前位置；页面隐藏时也保存", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a")]);
    h.audio.tick(27);
    h.engine.pause();
    expect(savedFor(h)?.positionSeconds).toBe(27);
    h.audio.currentTime = 31;
    window.dispatchEvent(new Event("pagehide"));
    expect(savedFor(h)?.positionSeconds).toBe(31);
  });

  it("播放中每 5 秒保存一次，暂停后不再保存", async () => {
    vi.useFakeTimers();
    const h = createHarness();
    h.engine.playTracks([track("a")]);
    await settle();
    h.audio.loadMetadata(300);
    h.audio.beginPlaying();
    h.audio.currentTime = 12;
    vi.advanceTimersByTime(5_000);
    expect(savedFor(h)?.positionSeconds).toBe(12);
    h.audio.currentTime = 17;
    vi.advanceTimersByTime(5_000);
    expect(savedFor(h)?.positionSeconds).toBe(17);
    h.engine.pause();
    h.audio.currentTime = 99;
    vi.advanceTimersByTime(20_000);
    expect(savedFor(h)?.positionSeconds).toBe(17);
  });

  it("播放中定时保存不再序列化没变的队列条目，保存的内容仍完整可读", async () => {
    vi.useFakeTimers();
    const h = createHarness();
    h.engine.playTracks([track("a"), track("b")]);
    await settle();
    h.audio.loadMetadata(300);
    h.audio.beginPlaying();
    const stringify = vi.spyOn(JSON, "stringify");
    h.audio.currentTime = 12;
    vi.advanceTimersByTime(5_000);
    // 条目只能以缓存的 JSON 出现：既不单独序列化条目数组，也不序列化带着条目的整个对象。
    const entrySerializations = stringify.mock.calls.filter(([value]) => Array.isArray(value) || (typeof value === "object" && value !== null && "entries" in value)).length;
    stringify.mockRestore();
    expect(entrySerializations).toBe(0);
    expect(savedFor(h)).toMatchObject({ entries: [{ entryId: "e1" }, { entryId: "e2" }], currentEntryId: "e1", positionSeconds: 12 });
  });

  it("保存失败（存储不可用）不影响播放", async () => {
    const h = createHarness({ sessionStorage: null });
    await playAndStart(h, [track("a")]);
    expect(state(h).status).toBe("playing");
  });

  it("会话结束：停止播放、清空队列、删除保存的队列，之后不再接受命令", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a"), track("b")]);
    h.engine.endSession();
    expect(state(h).status).toBe("idle");
    expect(state(h).queue.entries).toEqual([]);
    expect(h.audio.getAttribute("src")).toBeNull();
    expect(h.sessionStorage.data.has(queueStorageKey(userId))).toBe(false);
    h.engine.playTracks([track("x")]);
    h.engine.enqueue([track("y")]);
    expect(state(h).queue.entries).toEqual([]);
    expect(h.sessionStorage.data.size).toBe(0);
  });
});

describe("音量", () => {
  it("初始值取自设备偏好；设置音量与静音同步到媒体元素并保存", () => {
    const localStorage = memoryStorage({ [VOLUME_STORAGE_KEY]: JSON.stringify({ version: 1, volume: 0.3, muted: true }) });
    const h = createHarness({ localStorage });
    expect(h.audio.volume).toBe(0.3);
    expect(h.audio.muted).toBe(true);
    expect(state(h)).toMatchObject({ volume: 0.3, muted: true });

    h.engine.setVolume(0.8);
    h.engine.setMuted(false);
    expect(h.audio.volume).toBe(0.8);
    expect(state(h)).toMatchObject({ volume: 0.8, muted: false });
    expect(JSON.parse(localStorage.data.get(VOLUME_STORAGE_KEY) ?? "null")).toEqual({ version: 1, volume: 0.8, muted: false });
  });

  it("音量限制在 0 到 1，忽略非有限的值；命令直接同步快照与存储", () => {
    const h = createHarness();
    h.engine.setVolume(7);
    expect(h.audio.volume).toBe(1);
    h.engine.setVolume(-1);
    expect(h.audio.volume).toBe(0);
    h.engine.setVolume(Number.NaN);
    expect(h.audio.volume).toBe(0);
    expect(state(h).volume).toBe(0);
    h.engine.setVolume(0.5);
    expect(state(h).volume).toBe(0.5);
    expect(JSON.parse(h.localStorage.data.get(VOLUME_STORAGE_KEY) ?? "null")).toEqual({ version: 1, volume: 0.5, muted: false });
    h.engine.setMuted(true);
    expect(state(h).muted).toBe(true);
    expect(JSON.parse(h.localStorage.data.get(VOLUME_STORAGE_KEY) ?? "null")).toEqual({ version: 1, volume: 0.5, muted: true });
  });

  it("音量偏好不随会话结束清除", () => {
    const h = createHarness();
    h.engine.setVolume(0.2);
    h.engine.endSession();
    expect(h.localStorage.data.has(VOLUME_STORAGE_KEY)).toBe(true);
  });
});


describe("刷新恢复与异步边界", () => {
  const release = decodeReleaseDetail(sampleBody("releases/get"));
  const first = release.media.flatMap((medium) => medium.tracks)[0];
  if (first === undefined) throw new Error("测试样本缺少曲目");
  const savedEntry = { entryId: "restored-1", trackId: first.id, releaseId: release.id, groupId: "00000000-0000-4000-8000-000000000099" };
  const saved = {
    entries: [savedEntry],
    currentEntryId: "restored-1",
    mode: "shuffle" as const,
    positionSeconds: 22,
    history: [],
  };
  function storageOf(value = saved) {
    const storage = memoryStorage();
    saveQueue(storage, userId, value);
    return storage;
  }

  it("恢复时重新读取发行，保持暂停且不预检；首次播放后定位到保存位置", async () => {
    const h = createHarness({ sessionStorage: storageOf(), loadRelease: vi.fn(async () => release) });
    expect(state(h).restoring).toBe(true);
    await settle();
    expect(state(h)).toMatchObject({ status: "paused", restoring: false, event: { kind: "queue_restored", restored: 1, dropped: 0 } });
    expect(h.audio.playCalls).toBe(0);
    expect(h.loadPlayback).not.toHaveBeenCalled();
    expect(progress(h).positionSeconds).toBe(22);
    h.engine.play();
    await settle();
    h.audio.loadMetadata(100, { seekable: false });
    expect(h.audio.currentTime).toBe(0);
    h.audio.seekableLength = 1;
    h.audio.emit("progress");
    expect(h.audio.currentTime).toBe(22);
  });

  it("恢复位置超出当前时长时限制到媒体实际时长", async () => {
    const h = createHarness({ sessionStorage: storageOf(), loadRelease: async () => release });
    await settle();
    h.engine.play();
    await settle();
    h.audio.loadMetadata(10);
    expect(h.audio.currentTime).toBe(10);
  });

  it("恢复后播放预检尚未完成就暂停，不丢保存位置", async () => {
    const h = createHarness({ sessionStorage: storageOf(), loadRelease: async () => release });
    await settle();
    h.preflight(first.id, () => new Promise(() => undefined));
    h.engine.play();
    h.engine.pause();
    expect(progress(h).positionSeconds).toBe(22);
    h.preflight(first.id, () => Promise.resolve(playbackOf(first.id)));
    h.engine.play();
    await settle();
    h.audio.loadMetadata(100);
    expect(h.audio.currentTime).toBe(22);
  });

  it("首播已设来源但还没定位（元数据未到）时页面隐藏或暂停，保存的仍是要定位的位置", async () => {
    const storage = storageOf();
    const savedPosition = () => loadSavedQueue(storage, userId)?.positionSeconds;
    const h = createHarness({ sessionStorage: storage, loadRelease: async () => release });
    await settle();
    h.engine.play();
    await settle();
    expect(h.audio.sources).toHaveLength(1);
    window.dispatchEvent(new Event("pagehide"));
    expect(savedPosition()).toBe(22);
    h.engine.pause();
    expect(state(h).status).toBe("paused");
    expect(savedPosition()).toBe(22);
    h.engine.play();
    h.audio.loadMetadata(100);
    expect(h.audio.currentTime).toBe(22);
  });

  it("定位前媒体出错且再预检暂时失败：保留要定位的位置，重试从那里开始", async () => {
    const h = createHarness({ sessionStorage: storageOf(), loadRelease: async () => release });
    await settle();
    h.engine.play();
    await settle();
    h.fail(first.id, networkError());
    h.audio.fail(4);
    await settle();
    expect(state(h).fault).toEqual({ kind: "transient" });
    expect(progress(h).positionSeconds).toBe(22);
    h.preflight(first.id, () => Promise.resolve(playbackOf(first.id)));
    h.engine.retry();
    await settle();
    h.audio.loadMetadata(100);
    expect(h.audio.currentTime).toBe(22);
  });

  it("定位生效前在队首按上一首：从头播放，元数据到达后不再跳到旧位置", async () => {
    const h = createHarness({ sessionStorage: storageOf(), loadRelease: async () => release });
    await settle();
    h.engine.play();
    await settle();
    h.engine.previous();
    h.audio.loadMetadata(100);
    expect(h.audio.currentTime).toBe(0);
  });

  it("来源不可定位、媒体已从开头走起来时，保存实际位置而不是一直没能生效的定位目标", async () => {
    const storage = storageOf();
    const h = createHarness({ sessionStorage: storage, loadRelease: async () => release });
    await settle();
    h.engine.play();
    await settle();
    h.audio.loadMetadata(100, { seekable: false });
    h.audio.beginPlaying();
    h.audio.tick(7);
    h.engine.pause();
    expect(loadSavedQueue(storage, userId)?.positionSeconds).toBe(7);
  });

  it("暂时读取失败保留原存储，可重试恢复；只有404才丢弃", async () => {
    const storage = storageOf();
    let online = false;
    const h = createHarness({ sessionStorage: storage, loadRelease: async () => {
      if (!online) throw networkError();
      return release;
    } });
    await settle();
    expect(state(h)).toMatchObject({ restoring: false, fault: { kind: "transient" } });
    expect(storage.data.has(queueStorageKey(userId))).toBe(true);
    online = true;
    h.engine.retry();
    await settle();
    expect(state(h)).toMatchObject({ status: "paused", fault: null, current: { entryId: "restored-1" } });

    const missing = createHarness({ sessionStorage: storageOf(), loadRelease: async () => { throw httpError(404, "not_found"); } });
    await settle();
    expect(state(missing)).toMatchObject({ status: "idle", event: { kind: "queue_restored", restored: 0, dropped: 1 } });
  });

  it.each([
    ["403", httpError(403, "forbidden")],
    ["503", httpError(503, "database_unavailable")],
    ["无效响应", new ApiError({ kind: "invalid_response", status: 200, requestId: "req-1" })],
  ])("恢复读取%s不删除原始保存内容；重试成功仍暂停", async (_label, failure) => {
    const storage = storageOf();
    const before = storage.data.get(queueStorageKey(userId));
    const loader = vi.fn().mockRejectedValueOnce(failure).mockResolvedValueOnce(release);
    const h = createHarness({ sessionStorage: storage, loadRelease: loader });
    await settle();
    expect(state(h)).toMatchObject({ restoring: false, fault: { kind: "transient" } });
    h.engine.detach();
    h.engine.attach();
    window.dispatchEvent(new Event("pagehide"));
    expect(storage.data.get(queueStorageKey(userId))).toBe(before);
    h.engine.retry();
    await settle();
    expect(state(h)).toMatchObject({ status: "paused", fault: null, current: { entryId: savedEntry.entryId } });
    expect(h.audio.playCalls).toBe(0);
    expect(h.loadPlayback).not.toHaveBeenCalled();
  });

  it("恢复中的StrictMode式detach/attach保留待完成结果且不自动播放", async () => {
    let resolveRelease: (value: typeof release) => void = () => undefined;
    const storage = storageOf();
    const before = storage.data.get(queueStorageKey(userId));
    const h = createHarness({ sessionStorage: storage, loadRelease: () => new Promise((resolve) => { resolveRelease = resolve; }) });
    h.engine.detach();
    h.engine.attach();
    expect(storage.data.get(queueStorageKey(userId))).toBe(before);
    resolveRelease(release);
    await settle();
    expect(state(h)).toMatchObject({ status: "paused", restoring: false, current: { entryId: savedEntry.entryId } });
    expect(h.audio.playCalls).toBe(0);
  });

  it("同一发行只读取一次，条目身份与随机历史保留，失效曲目丢弃并报告", async () => {
    const storage = memoryStorage();
    saveQueue(storage, userId, {
      ...saved,
      entries: [savedEntry, { ...savedEntry, entryId: "duplicate" }, { ...savedEntry, entryId: "missing", trackId: "00000000-0000-4000-8000-000000000088" }],
      currentEntryId: "duplicate",
      history: ["restored-1", "missing"],
    });
    const loader = vi.fn(async () => release);
    const h = createHarness({ sessionStorage: storage, loadRelease: loader });
    await settle();
    expect(loader).toHaveBeenCalledTimes(1);
    expect(state(h).queue.history).toEqual(["restored-1"]);
    expect(state(h).event).toMatchObject({ kind: "queue_restored", restored: 2, dropped: 1 });
    h.engine.previous();
    await settle();
    expect(state(h).current?.entryId).toBe("restored-1");
  });

  it("恢复并发最多4；用户操作或会话结束后不再调度剩余发行", async () => {
    const storage = memoryStorage();
    const entries = Array.from({ length: 8 }, (_, i) => ({ ...savedEntry, entryId: `r${i}`, releaseId: `00000000-0000-4000-8000-${String(i).padStart(12, "0")}` }));
    saveQueue(storage, userId, { ...saved, entries, currentEntryId: "r0" });
    const pending: ((value: typeof release) => void)[] = [];
    const loader = vi.fn(() => new Promise<typeof release>((resolve) => pending.push(resolve)));
    const h = createHarness({ sessionStorage: storage, loadRelease: loader });
    expect(loader).toHaveBeenCalledTimes(4);
    h.engine.enqueue([track("new")]);
    for (const resolve of pending) resolve(release);
    await settle();
    expect(loader).toHaveBeenCalledTimes(4);
    expect(currentTitle(h)).toBe("曲目 new");
    expect(state(h).restoring).toBe(false);
  });

  it("会话结束后，迟到恢复与预检不能写回存储或发声", async () => {
    let resolveRelease: (value: typeof release) => void = () => undefined;
    const storage = storageOf();
    const h = createHarness({ sessionStorage: storage, loadRelease: () => new Promise((resolve) => { resolveRelease = resolve; }) });
    h.engine.endSession();
    resolveRelease(release);
    await settle();
    expect(state(h).queue.entries).toEqual([]);
    expect(storage.data.size).toBe(0);
    expect(h.audio.sources).toEqual([]);
  });

  it("媒体错误再预检期间暂停仍保留原位置，已中断的媒体不能再seek", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a")]);
    h.audio.tick(18);
    h.preflight("track-a", () => new Promise(() => undefined));
    h.audio.fail(2);
    h.engine.pause();
    expect(progress(h)).toEqual({ positionSeconds: 18, durationSeconds: null, seekable: false });
    h.preflight("track-a", () => Promise.resolve(playbackOf("track-a")));
    h.engine.play();
    await settle();
    h.audio.loadMetadata(100);
    expect(h.audio.currentTime).toBe(18);
  });

  it("detach保存当前进度并中断媒体，再attach不会自动发声；继续播放重新预检与定位", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a")]);
    h.audio.tick(17);
    h.engine.detach();
    expect(savedFor(h)?.positionSeconds).toBe(17);
    expect(progress(h)).toEqual({ positionSeconds: 17, durationSeconds: null, seekable: false });
    expect(h.audio.src).toBe("");
    h.engine.attach();
    expect(state(h).status).toBe("paused");
    h.engine.play();
    await settle();
    h.audio.loadMetadata(100);
    expect(h.audio.currentTime).toBe(17);
  });

  it("超容量立即播放整批拒绝，旧队列与媒体不变", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a")]);
    expect(h.engine.playTracks(Array.from({ length: 10_001 }, () => track("b")))).toBe("queue_full");
    expect(currentTitle(h)).toBe("曲目 a");
    expect(state(h).status).toBe("playing");
    expect(h.audio.sources).toHaveLength(1);
  });

  it.each(["enqueue", "playNext"] as const)("%s超容量整批拒绝，不消耗条目身份或改变正在播放", async (command) => {
    const h = createHarness();
    await playAndStart(h, [track("a")]);
    const before = state(h);
    expect(h.engine[command](Array.from({ length: 10_000 }, () => track("b")))).toBe("queue_full");
    expect(state(h)).toBe(before);
    expect(h.audio.sources).toHaveLength(1);
    expect(h.engine.enqueue([track("c")])).toBe("added");
    expect(state(h).queue.entries[1]?.entryId).toBe("e2");
  });

  it("暂停后迟到的play拒绝不会跳歌或标记失败", async () => {
    const h = createHarness();
    let reject: (reason: unknown) => void = () => undefined;
    h.audio.play = () => new Promise<void>((_resolve, done) => { reject = done; });
    h.engine.playTracks([track("a"), track("b")]);
    await settle();
    h.engine.pause();
    reject(new DOMException("旧请求拒绝", "NotSupportedError"));
    await settle();
    expect(state(h)).toMatchObject({ status: "paused", failures: {} });
    expect(h.loadPlayback).toHaveBeenCalledTimes(1);
  });

  it("删除失败项不会修改已发布快照里的failures", async () => {
    const h = createHarness();
    h.fail("track-a", httpError(404, "not_found"));
    h.engine.playTracks([track("a"), track("b")]);
    await settle();
    const before = state(h);
    h.engine.removeEntry("e1");
    expect(before.failures).toEqual({ e1: "not_found" });
    expect(state(h).failures).toEqual({});
  });

  it("曾播过的随机条目重新加载但未出声就切走，不新增本轮历史", async () => {
    const h = createHarness();
    h.engine.setMode("shuffle");
    await playAndStart(h, [track("a"), track("b"), track("c")]);
    h.engine.next();
    await settle();
    h.audio.beginPlaying();
    expect(state(h).queue.history).toEqual(["e1"]);
    h.engine.previous();
    await settle();
    expect(state(h).current?.entryId).toBe("e1");
    h.engine.next();
    await settle();
    expect(state(h).queue.history).toEqual([]);
  });

  it("重新访问曾播条目但未出声的手动跳转保留此前真实历史", async () => {
    const h = createHarness();
    h.engine.setMode("shuffle");
    await playAndStart(h, [track("a"), track("b"), track("c"), track("d")]);
    h.engine.next();
    await settle();
    h.audio.beginPlaying();
    h.engine.next();
    await settle();
    h.audio.beginPlaying();
    expect(state(h).queue.history).toEqual(["e1", "e2"]);
    h.engine.playEntry("e2");
    await settle();
    expect(state(h).queue.history).toEqual(["e1", "e2", "e3"]);
    h.engine.playEntry("e4");
    await settle();
    expect(state(h).queue.history).toEqual(["e1", "e2", "e3"]);
  });

  it("随机历史不包含从未出声的加载项", async () => {
    const h = createHarness();
    h.engine.setMode("shuffle");
    h.engine.playTracks([track("a"), track("b"), track("c")]);
    await settle();
    h.engine.next();
    await settle();
    h.audio.loadMetadata(100);
    h.audio.beginPlaying();
    expect(state(h).queue.history).toEqual([]);
    h.engine.next();
    await settle();
    expect(state(h).queue.history).toEqual(["e2"]);
    h.engine.previous();
    await settle();
    expect(state(h).current?.entryId).toBe("e2");
  });
});

describe("系统媒体键", () => {
  it("媒体信息和状态只使用当前曲目与真实播放状态；切歌/清空清除旧位置", async () => {
    const h = createHarness();
    h.engine.playTracks([track("a", { artworkId: "artwork" }), track("b")]);
    expect(h.mediaSession.state).toBe("paused");
    expect(h.mediaSession.metadata).toMatchObject({ title: "曲目 a", artist: "艺术家", album: "专辑", artwork: [{ src: "http://localhost/api/v1/artworks/artwork" }] });
    await settle();
    h.audio.loadMetadata(100);
    h.audio.beginPlaying();
    expect(h.mediaSession.state).toBe("playing");
    h.clock.now += 1_000;
    h.audio.tick(13);
    expect(h.mediaSession.position).toEqual({ duration: 100, position: 13 });
    const calls = h.mediaSession.positionCalls;
    h.audio.tick(14);
    expect(h.mediaSession.positionCalls).toBe(calls);
    h.mediaSession.invoke("nexttrack");
    expect(h.mediaSession.position).toBeNull();
    expect(h.mediaSession.metadata?.title).toBe("曲目 b");
    h.engine.clearQueue();
    expect(h.mediaSession.state).toBe("none");
    expect(h.mediaSession.metadata).toBeNull();
  });

  it("支持播放、暂停、上一首、下一首、定位、快进快退和停止", async () => {
    const h = createHarness();
    h.engine.enqueue([track("a"), track("b")]);
    h.mediaSession.invoke("play");
    await settle();
    h.audio.loadMetadata(100);
    h.audio.beginPlaying();
    h.mediaSession.invoke("seekto", { seekTime: 42 });
    expect(h.audio.currentTime).toBe(42);
    h.audio.tick(42);
    h.mediaSession.invoke("seekbackward");
    expect(h.audio.currentTime).toBe(32);
    h.audio.tick(32);
    h.mediaSession.invoke("seekforward");
    expect(h.audio.currentTime).toBe(42);
    h.mediaSession.invoke("pause");
    expect(state(h).status).toBe("paused");
    h.mediaSession.invoke("nexttrack");
    await settle();
    expect(currentTitle(h)).toBe("曲目 b");
    h.mediaSession.invoke("previoustrack");
    await settle();
    expect(currentTitle(h)).toBe("曲目 a");
    h.audio.loadMetadata(100);
    h.audio.beginPlaying();
    h.audio.tick(30);
    h.mediaSession.invoke("stop");
    expect(h.audio.currentTime).toBe(0);
    expect(state(h).status).toBe("paused");
    expect(savedFor(h)?.positionSeconds).toBe(0);
  });

  it("没有MediaSession也能播放；会话结束移除所有媒体键与位置", async () => {
    const without = createHarness({ mediaSession: null });
    await playAndStart(without, [track("a")]);
    expect(state(without).status).toBe("playing");
    const h = createHarness();
    await playAndStart(h, [track("a")]);
    h.engine.endSession();
    expect([...h.mediaSession.handlers.values()].every((handler) => handler === null)).toBe(true);
    expect(h.mediaSession.metadata).toBeNull();
    expect(h.mediaSession.position).toBeNull();
    expect(h.mediaSession.state).toBe("none");
  });
});

/** 记录引擎产生的全部事件（快照里只保留最近一个）。 */
function collectEvents(h: Harness): PlayerEvent[] {
  const events: PlayerEvent[] = [];
  h.engine.subscribe(() => {
    const event = state(h).event;
    if (event !== null && events.at(-1)?.seq !== event.seq) events.push(event);
  });
  return events;
}

/** 第 index 次预检（含预取）收到的取消信号。 */
const signalOf = (h: Harness, index: number): AbortSignal => {
  const call = h.loadPlayback.mock.calls[index];
  if (call === undefined) throw new Error(`没有第 ${index + 1} 次预检`);
  return call[1];
};

describe("派生格式：正在准备", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  it("收到 202：状态变成正在准备并立即再次预检，直到可以播放才设置媒体来源", async () => {
    const h = createHarness();
    const events = collectEvents(h);
    h.preflightSequence("track-a", [preparing, preparing, playbackOf("track-a", "derived")]);
    h.engine.playTracks([track("a")]);
    expect(state(h).status).toBe("loading");
    await settle();
    expect(preflightedTracks(h)).toEqual(["track-a", "track-a", "track-a"]);
    expect(h.audio.sources).toEqual(["/api/v1/tracks/track-a/audio"]);
    expect(h.audio.playCalls).toBe(1);
    // 只有媒体事件才让状态变成正在播放；准备期间公告只说一次。
    expect(state(h).status).toBe("loading");
    expect(events.filter((event) => event.kind === "preparing")).toEqual([{ seq: expect.any(Number) as number, kind: "preparing", entryId: "e1" }]);
    h.audio.loadMetadata(120);
    h.audio.beginPlaying();
    expect(state(h).status).toBe("playing");
  });

  it("准备期间不加载媒体：状态是正在准备，按钮判定为“要播放”，进度保持开头", async () => {
    const h = createHarness();
    let polls = 0;
    h.preflight("track-a", () => {
      h.clock.now += 20_000;
      return ++polls === 1 ? Promise.resolve(preparing) : new Promise(() => undefined);
    });
    h.engine.playTracks([track("a")]);
    await settle();
    expect(state(h).status).toBe("preparing");
    expect(isPlaybackActive(state(h).status)).toBe(true);
    expect(h.audio.sources).toEqual([]);
    expect(h.audio.playCalls).toBe(0);
    expect(progress(h)).toEqual({ positionSeconds: 0, durationSeconds: null, seekable: false });
    expect(h.mediaSession.state).toBe("paused");
    expect(state(h).event).toMatchObject({ kind: "preparing", entryId: "e1" });
  });

  it("暂停：放弃等待并取消在途的预检，晚到的结果被丢弃", async () => {
    const h = createHarness();
    let resolveSecond: (value: TrackPlaybackResult) => void = () => undefined;
    let polls = 0;
    h.preflight("track-a", () => {
      h.clock.now += 20_000;
      return ++polls === 1 ? Promise.resolve(preparing) : new Promise((resolve) => (resolveSecond = resolve));
    });
    h.engine.playTracks([track("a")]);
    await settle();
    expect(state(h).status).toBe("preparing");
    h.engine.togglePlayback();
    expect(state(h).status).toBe("paused");
    expect(signalOf(h, 1).aborted).toBe(true);
    resolveSecond(playbackOf("track-a", "derived"));
    await settle();
    expect(h.audio.sources).toEqual([]);
    expect(h.audio.playCalls).toBe(0);
    expect(state(h).status).toBe("paused");
    expect(preflightedTracks(h)).toEqual(["track-a", "track-a"]);
  });

  it("等待转换时切歌：放弃这首的等待，只播放最新的一首", async () => {
    const h = createHarness();
    h.preflightSequence("track-a", [preparing]);
    h.engine.playTracks([track("a"), track("b")]);
    await settle();
    // track-a 一直在等待转换（每次预检都得到 202）；切歌之后不再为它预检。
    const waiting = h.loadPlayback.mock.calls.filter(([id]) => id === "track-a").length;
    expect(state(h).status).toBe("preparing");
    h.engine.next();
    await settle();
    expect(currentTitle(h)).toBe("曲目 b");
    expect(h.audio.sources).toEqual(["/api/v1/tracks/track-b/audio"]);
    expect(h.loadPlayback.mock.calls.filter(([id]) => id === "track-a").length).toBe(waiting);
    expect(state(h).failures).toEqual({});
  });

  it("等待转换时结束会话：取消预检，不再发请求", async () => {
    const h = createHarness();
    let polls = 0;
    h.preflight("track-a", () => {
      h.clock.now += 20_000;
      return ++polls === 1 ? Promise.resolve(preparing) : new Promise(() => undefined);
    });
    h.engine.playTracks([track("a")]);
    await settle();
    expect(state(h).status).toBe("preparing");
    h.engine.endSession();
    expect(signalOf(h, 1).aborted).toBe(true);
    expect(state(h).status).toBe("idle");
    await settle();
    vi.advanceTimersByTime(60_000);
    expect(h.loadPlayback.mock.calls.length).toBe(2);
  });

  it("立即返回的 202 不会被高速重试：两次预检之间至少间隔 1 秒，暂停取消定时器", async () => {
    const h = createHarness();
    h.preflightSequence("track-a", [preparing], 0);
    h.engine.playTracks([track("a")]);
    await settle();
    expect(state(h).status).toBe("preparing");
    expect(h.loadPlayback.mock.calls.length).toBe(1);
    vi.advanceTimersByTime(999);
    await settle();
    expect(h.loadPlayback.mock.calls.length).toBe(1);
    vi.advanceTimersByTime(1);
    await settle();
    expect(h.loadPlayback.mock.calls.length).toBe(2);
    h.engine.pause();
    vi.advanceTimersByTime(60_000);
    await settle();
    expect(h.loadPlayback.mock.calls.length).toBe(2);
    expect(state(h).status).toBe("paused");
  });

  it("系统时钟向后跳变时最小间隔仍不超过 1 秒", async () => {
    const h = createHarness();
    let polls = 0;
    h.preflight("track-a", () => {
      h.clock.now -= 3_600_000;
      return ++polls === 1 ? Promise.resolve(preparing) : Promise.resolve(playbackOf("track-a", "derived"));
    });
    h.engine.playTracks([track("a")]);
    await settle();
    vi.advanceTimersByTime(1000);
    await settle();
    expect(h.audio.sources).toEqual(["/api/v1/tracks/track-a/audio"]);
  });

  it("从第一次 202 起超过 11 分钟仍未就绪按转换失败处理，并前进到下一首", async () => {
    const h = createHarness();
    let polls = 0;
    // 每次预检挂起 5 分钟再返回 202：第四次返回时距第一次 202 已过 15 分钟。
    h.preflight("track-a", () => {
      polls += 1;
      h.clock.now += 5 * 60_000;
      return Promise.resolve(preparing);
    });
    h.engine.playTracks([track("a"), track("b")]);
    await settle();
    expect(polls).toBe(4);
    expect(state(h).failures).toEqual({ e1: "derivation_failed" });
    expect(currentTitle(h)).toBe("曲目 b");
    expect(h.audio.sources).toEqual(["/api/v1/tracks/track-b/audio"]);
  });

  it("服务端报告转换失败（409 derivation_failed）：标记这首失败并前进，不重试", async () => {
    const h = createHarness();
    h.fail("track-a", httpError(409, "derivation_failed"));
    h.engine.playTracks([track("a"), track("b")]);
    await settle();
    expect(state(h).failures).toEqual({ e1: "derivation_failed" });
    expect(state(h).fault).toBeNull();
    expect(currentTitle(h)).toBe("曲目 b");
    expect(preflightedTracks(h)).toEqual(["track-a", "track-b"]);
  });

  it("等待途中转换失败：同样标记失败并前进", async () => {
    const h = createHarness();
    let polls = 0;
    h.preflight("track-a", () => {
      h.clock.now += 20_000;
      return ++polls === 1 ? Promise.resolve(preparing) : Promise.reject(httpError(409, "derivation_failed"));
    });
    h.engine.playTracks([track("a"), track("b")]);
    await settle();
    expect(state(h).failures).toEqual({ e1: "derivation_failed" });
    expect(currentTitle(h)).toBe("曲目 b");
  });

  it("服务器忙于转换（503 derivation_busy）：停在暂停并可重试，不标记失败、不跳歌，与网络失败区分", async () => {
    const h = createHarness();
    const events = collectEvents(h);
    h.fail("track-a", httpError(503, "derivation_busy"));
    h.engine.playTracks([track("a"), track("b")]);
    await settle();
    expect(state(h).status).toBe("paused");
    expect(state(h).fault).toEqual({ kind: "busy" });
    expect(state(h).failures).toEqual({});
    expect(currentTitle(h)).toBe("曲目 a");
    expect(events.map((event) => event.kind)).toEqual(["track_started", "derivation_busy"]);

    h.preflight("track-a", () => Promise.resolve(playbackOf("track-a", "derived")));
    h.engine.retry();
    expect(state(h).fault).toBeNull();
    expect(state(h).status).toBe("loading");
    await settle();
    expect(h.audio.sources).toEqual(["/api/v1/tracks/track-a/audio"]);
    h.audio.loadMetadata(100);
    h.audio.beginPlaying();
    expect(state(h).status).toBe("playing");
  });

  it("等待转换后服务器报告忙：同样是可重试的 busy，保留位置", async () => {
    const h = createHarness();
    let polls = 0;
    h.preflight("track-a", () => {
      h.clock.now += 20_000;
      return ++polls === 1 ? Promise.resolve(preparing) : Promise.reject(httpError(503, "derivation_busy"));
    });
    h.engine.playTracks([track("a")]);
    await settle();
    expect(state(h).fault).toEqual({ kind: "busy" });
    expect(state(h).status).toBe("paused");
  });

  it("音频请求失败后的再次预检得到 202：回到正在准备，转换完成后从停下的位置继续", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a")]);
    h.audio.tick(33);
    // 派生文件在音频请求之前被清走之类：再次预检重新启动了转换。
    h.preflightSequence("track-a", [preparing, playbackOf("track-a", "derived")]);
    h.audio.fail(4);
    await settle();
    expect(state(h).failures).toEqual({});
    expect(h.audio.sources).toEqual(["/api/v1/tracks/track-a/audio", "/api/v1/tracks/track-a/audio"]);
    h.audio.loadMetadata(100);
    expect(h.audio.currentTime).toBe(33);
    h.audio.beginPlaying();
    expect(state(h).status).toBe("playing");
  });

  it("音频请求失败后的再次预检在等待转换时失败：按预检失败处理", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a"), track("b")]);
    let polls = 0;
    h.preflight("track-a", () => {
      h.clock.now += 20_000;
      return ++polls === 1 ? Promise.resolve(preparing) : Promise.reject(httpError(409, "derivation_failed"));
    });
    h.audio.fail(4);
    await settle();
    expect(state(h).failures).toEqual({ e1: "derivation_failed" });
    expect(currentTitle(h)).toBe("曲目 b");
  });
});

describe("预取下一首派生曲目", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  const prefetchOnly = (h: Harness, ...trackIds: string[]) => h.shouldPrefetch.mockImplementation((candidate) => trackIds.includes(candidate.trackId));

  it("当前曲目第一次出声之后预检下一首一次；暂停再继续不重复", async () => {
    const h = createHarness();
    prefetchOnly(h, "track-b");
    h.engine.playTracks([track("a"), track("b"), track("c")]);
    await settle();
    // 出声之前不预取：转换只在当前曲目已经开始播放之后才占用服务端。
    expect(preflightedTracks(h)).toEqual(["track-a"]);
    h.audio.loadMetadata(100);
    h.audio.beginPlaying();
    expect(preflightedTracks(h)).toEqual(["track-a", "track-b"]);
    expect(signalOf(h, 1).aborted).toBe(false);
    h.audio.pause();
    h.audio.beginPlaying();
    h.audio.stall();
    h.audio.beginPlaying();
    expect(preflightedTracks(h)).toEqual(["track-a", "track-b"]);
  });

  it("预取的结果不改变任何状态：成功、202 与失败都被丢弃，不产生事件，也不标记失败", async () => {
    for (const outcome of [() => Promise.resolve(playbackOf("track-b", "derived")), () => Promise.resolve(preparing), () => Promise.reject(httpError(409, "derivation_failed")), () => Promise.reject(networkError())]) {
      const h = createHarness();
      prefetchOnly(h, "track-b");
      h.preflight("track-b", outcome);
      await playAndStart(h, [track("a"), track("b")]);
      const before = state(h);
      await settle();
      vi.advanceTimersByTime(60_000);
      await settle();
      expect(preflightedTracks(h)).toEqual(["track-a", "track-b"]);
      expect(state(h).status).toBe("playing");
      expect(state(h).failures).toEqual({});
      expect(state(h).fault).toBeNull();
      expect(state(h).event).toBe(before.event);
      expect(currentTitle(h)).toBe("曲目 a");
      expect(h.audio.sources).toEqual(["/api/v1/tracks/track-a/audio"]);
    }
  });

  it("不预取：下一首不是转换格式、没有下一首、单曲循环、随机", async () => {
    const notDerived = createHarness();
    await playAndStart(notDerived, [track("a"), track("b")]);
    expect(preflightedTracks(notDerived)).toEqual(["track-a"]);
    expect(notDerived.shouldPrefetch).toHaveBeenCalledWith(expect.objectContaining({ trackId: "track-b" }));

    const last = createHarness();
    prefetchOnly(last, "track-a", "track-b");
    await playAndStart(last, [track("a"), track("b")], 1);
    expect(preflightedTracks(last)).toEqual(["track-b"]);
    expect(last.shouldPrefetch).not.toHaveBeenCalled();

    const single = createHarness();
    prefetchOnly(single, "track-a", "track-b");
    single.engine.setMode("repeat_one");
    await playAndStart(single, [track("a"), track("b")]);
    expect(preflightedTracks(single)).toEqual(["track-a"]);

    const shuffled = createHarness();
    prefetchOnly(shuffled, "track-a", "track-b", "track-c");
    shuffled.engine.setMode("shuffle");
    await playAndStart(shuffled, [track("a"), track("b"), track("c")]);
    expect(preflightedTracks(shuffled)).toEqual(["track-a"]);
  });

  it("列表循环在末尾没有可预取的下一首（不绕回队首）", async () => {
    const h = createHarness();
    prefetchOnly(h, "track-a", "track-b");
    h.engine.setMode("repeat_all");
    await playAndStart(h, [track("a"), track("b")], 1);
    expect(preflightedTracks(h)).toEqual(["track-b"]);
  });

  it("切歌、改队列、停止与结束会话都会中止还在进行的预取", async () => {
    const start = async () => {
      const h = createHarness();
      prefetchOnly(h, "track-b", "track-c");
      // 预取的预检一直挂着：只有被取消才结束。
      h.preflight("track-b", () => new Promise(() => undefined));
      h.preflight("track-c", () => new Promise(() => undefined));
      await playAndStart(h, [track("a"), track("b"), track("c")]);
      expect(signalOf(h, 1).aborted).toBe(false);
      return h;
    };
    const next = await start();
    next.engine.next();
    expect(signalOf(next, 1).aborted).toBe(true);

    const enqueued = await start();
    enqueued.engine.enqueue([track("d")]);
    expect(signalOf(enqueued, 1).aborted).toBe(true);

    const mode = await start();
    mode.engine.setMode("repeat_one");
    expect(signalOf(mode, 1).aborted).toBe(true);

    const removed = await start();
    removed.engine.removeEntry("e2");
    expect(signalOf(removed, 1).aborted).toBe(true);

    const stopped = await start();
    stopped.engine.stop();
    // 停止是暂停加回到开头：当前项没变、队列没变，预取可以继续；结束会话才一定中止。
    stopped.engine.endSession();
    expect(signalOf(stopped, 1).aborted).toBe(true);

    const detached = await start();
    detached.engine.detach();
    expect(signalOf(detached, 1).aborted).toBe(true);

    const cleared = await start();
    cleared.engine.clearQueue();
    expect(signalOf(cleared, 1).aborted).toBe(true);
  });

  it("下一首成为当前曲目之后，它自己的预检照常进行，并在它出声后再预取再下一首", async () => {
    const h = createHarness();
    prefetchOnly(h, "track-b", "track-c");
    await playAndStart(h, [track("a"), track("b"), track("c")]);
    expect(preflightedTracks(h)).toEqual(["track-a", "track-b"]);
    h.engine.next();
    await settle();
    h.audio.loadMetadata(100);
    h.audio.beginPlaying();
    expect(preflightedTracks(h)).toEqual(["track-a", "track-b", "track-b", "track-c"]);
    expect(currentTitle(h)).toBe("曲目 b");
  });

  it("预检失败（没有出声）的曲目不触发预取", async () => {
    const h = createHarness();
    prefetchOnly(h, "track-b");
    h.fail("track-a", httpError(404, "not_found"));
    h.engine.playTracks([track("a"), track("b")]);
    await settle();
    expect(state(h).failures).toEqual({ e1: "not_found" });
    expect(preflightedTracks(h)).toEqual(["track-a", "track-b"]);
    // 第二次预检是前进到 track-b 的正常加载，不是预取：它出声之前没有预取调用。
    expect(h.shouldPrefetch).not.toHaveBeenCalled();
  });
});

describe("计一次播放（阶段 P）", () => {
  /**
   * 出声 seconds 秒：每一秒同时推进注入的时钟与播放位置，像浏览器大约每秒一次的 timeupdate。
   * 默认曲目长 60 秒，门槛是 30 秒；达到门槛时引擎把这次播放交给记录出口（h.played）。
   */
  function listenFor(h: Harness, seconds: number): void {
    for (let step = 0; step < seconds; step += 1) {
      h.clock.now += 1_000;
      h.audio.tick(h.audio.currentTime + 1);
    }
  }
  const played = (trackId: string, playedSeconds = 30) => ({ trackId, playedSeconds });

  it("累计出声达到曲长一半时恰好交出一次，之后继续播放也不再交出", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a")], 0, 60);
    listenFor(h, 29);
    expect(h.played).toEqual([]);
    listenFor(h, 1);
    expect(h.played).toEqual([played("track-a")]);
    listenFor(h, 29);
    expect(h.played).toEqual([played("track-a")]);
  });

  it("门槛封顶 4 分钟；曲目没有扫描时长时用媒体报告的时长，两者都没有时也是 4 分钟", async () => {
    const long = createHarness();
    await playAndStart(long, [track("long", { durationSeconds: 1800 })], 0, 1800);
    listenFor(long, 239);
    expect(long.played).toEqual([]);
    listenFor(long, 1);
    expect(long.played).toEqual([played("track-long", 240)]);

    const scannedWithoutDuration = createHarness();
    await playAndStart(scannedWithoutDuration, [track("short", { durationSeconds: null })], 0, 40);
    listenFor(scannedWithoutDuration, 19);
    expect(scannedWithoutDuration.played).toEqual([]);
    listenFor(scannedWithoutDuration, 1);
    expect(scannedWithoutDuration.played).toEqual([played("track-short", 20)]);

    const unknown = createHarness();
    await playAndStart(unknown, [track("unknown", { durationSeconds: null })], 0, Number.POSITIVE_INFINITY);
    listenFor(unknown, 239);
    expect(unknown.played).toEqual([]);
    listenFor(unknown, 1);
    expect(unknown.played).toEqual([played("track-unknown", 240)]);
  });

  it("拖动跨过的部分不算：往后拖、往回拖再继续听，只累计真正出声的时间", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a")], 0, 60);
    listenFor(h, 20);
    h.audio.seekTo(55);
    listenFor(h, 4);
    h.audio.seekTo(0);
    listenFor(h, 5);
    expect(h.played).toEqual([]); // 20 + 4 + 5 = 29
    listenFor(h, 1);
    expect(h.played).toEqual([played("track-a")]);
  });

  it("只拖动、几乎没有出声的播放不上报，哪怕位置一路走到了结尾", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a")], 0, 60);
    for (const position of [20, 40, 58]) {
      h.audio.seekTo(position);
      listenFor(h, 1);
    }
    h.audio.finish();
    await settle();
    expect(h.played).toEqual([]);
  });

  it("没有 seeking 事件的位置跳变（引擎自己设置 currentTime）按流逝的时间折算，跳过的距离不累计", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a")], 0, 60);
    listenFor(h, 10);
    h.engine.seek(30); // FakeAudio 的 currentTime 赋值不发事件：下一次 timeupdate 看到的是 21 秒的跳变
    listenFor(h, 1);
    listenFor(h, 18);
    expect(h.played).toEqual([]); // 10 + 18 = 28，跳变没有算进去
    listenFor(h, 2);
    expect(h.played).toEqual([played("track-a")]);
  });

  it("暂停后继续是同一次访问：暂停的时间不算，累计保留", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a")], 0, 60);
    listenFor(h, 20);
    h.engine.pause();
    h.clock.now += 120_000;
    h.engine.play();
    h.audio.beginPlaying();
    listenFor(h, 9);
    expect(h.played).toEqual([]);
    listenFor(h, 1);
    expect(h.played).toEqual([played("track-a")]);
  });

  it("缓冲期间不算：卡住的时间与缓冲中的进度变化都不累计", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a")], 0, 60);
    listenFor(h, 20);
    h.audio.stall();
    expect(state(h).status).toBe("buffering");
    h.clock.now += 30_000;
    h.audio.tick(23);
    h.clock.now += 1_000;
    h.audio.tick(24);
    h.audio.beginPlaying();
    listenFor(h, 9);
    expect(h.played).toEqual([]); // 20 + 9 = 29
    listenFor(h, 1);
    expect(h.played).toEqual([played("track-a")]);
  });

  it("预检、正在准备与加载期间的时间不算，出声之前的位置变化也不算", async () => {
    const h = createHarness();
    h.preflightSequence("track-a", [preparing, playbackOf("track-a", "derived")]); // 服务端让预检等了 20 秒
    h.engine.playTracks([track("a")]);
    await settle();
    expect(state(h).status).toBe("loading");
    h.audio.loadMetadata(60);
    h.audio.tick(10); // 还没有 playing 事件
    h.clock.now += 1_000;
    h.audio.tick(11);
    h.audio.beginPlaying();
    listenFor(h, 29);
    expect(h.played).toEqual([]);
    listenFor(h, 1);
    expect(h.played).toEqual([played("track-a")]);
  });

  it("预检失败的曲目没有出声，不交出；下一首正常计次", async () => {
    const h = createHarness();
    h.fail("track-a", httpError(409, "source_unavailable"));
    h.engine.playTracks([track("a"), track("b")]);
    await settle();
    h.audio.loadMetadata(60);
    h.audio.beginPlaying();
    listenFor(h, 30);
    expect(h.played).toEqual([played("track-b")]);
  });

  it("静音与音量为 0 照常累计", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a")], 0, 60);
    h.engine.setMuted(true);
    h.engine.setVolume(0);
    listenFor(h, 30);
    expect(h.played).toEqual([played("track-a")]);
  });

  it("换曲是新的访问：上一首的累计不带到下一首", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a"), track("b")], 0, 60);
    listenFor(h, 20);
    h.engine.next();
    await settle();
    h.audio.loadMetadata(60);
    h.audio.beginPlaying();
    listenFor(h, 29);
    expect(h.played).toEqual([]);
    listenFor(h, 1);
    expect(h.played).toEqual([played("track-b")]);
  });

  it("在队首按上一首是从头重播：新的访问，之前的累计作废", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a"), track("b")], 0, 60);
    listenFor(h, 20);
    h.engine.previous();
    h.audio.seekTo(0); // 浏览器对 currentTime 赋值发 seeking 与 seeked，媒体继续出声
    listenFor(h, 29);
    expect(h.played).toEqual([]);
    listenFor(h, 1);
    expect(h.played).toEqual([played("track-a")]);
  });

  it("单曲循环：每一轮是新的访问，各交出一次", async () => {
    const h = createHarness();
    h.engine.setMode("repeat_one");
    await playAndStart(h, [track("a")], 0, 60);
    listenFor(h, 30);
    expect(h.played).toEqual([played("track-a")]);
    h.audio.finish();
    await settle();
    h.audio.beginPlaying();
    listenFor(h, 29);
    expect(h.played).toHaveLength(1);
    listenFor(h, 1);
    expect(h.played).toEqual([played("track-a"), played("track-a")]);
  });

  it("播完最后一首后再按播放是从头重播，算新的访问", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a")], 0, 60);
    listenFor(h, 30);
    h.audio.finish();
    await settle();
    expect(state(h).status).toBe("paused");
    h.engine.play();
    h.audio.ended = false;
    h.audio.seekTo(0); // 浏览器在已播完的媒体上 play() 会先回到开头
    h.audio.beginPlaying();
    listenFor(h, 29);
    expect(h.played).toEqual([played("track-a")]);
    listenFor(h, 1);
    expect(h.played).toEqual([played("track-a"), played("track-a")]);
  });

  it("停止回到曲目开头：之后再播放是新的访问", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a")], 0, 60);
    listenFor(h, 20);
    h.engine.stop();
    h.engine.play();
    h.audio.beginPlaying();
    listenFor(h, 29);
    expect(h.played).toEqual([]);
    listenFor(h, 1);
    expect(h.played).toEqual([played("track-a")]);
  });

  it("暂时性故障后重试是同一次访问：累计保留，从停下的位置继续", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a")], 0, 60);
    listenFor(h, 20);
    h.audio.fail(2);
    await settle();
    expect(state(h).fault).toEqual({ kind: "transient" });
    h.engine.retry();
    await settle();
    h.audio.loadMetadata(60);
    h.audio.beginPlaying();
    expect(h.audio.currentTime).toBe(20);
    listenFor(h, 9);
    expect(h.played).toEqual([]);
    listenFor(h, 1);
    expect(h.played).toEqual([played("track-a")]);
  });

  it("普通卸载再挂载（开发模式的模拟卸载）保留这次访问，也不作废上报", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a")], 0, 60);
    listenFor(h, 20);
    h.engine.detach();
    h.engine.attach();
    expect(h.discardPlays).not.toHaveBeenCalled();
    h.engine.play();
    await settle();
    h.audio.loadMetadata(60);
    h.audio.beginPlaying();
    listenFor(h, 9);
    expect(h.played).toEqual([]);
    listenFor(h, 1);
    expect(h.played).toEqual([played("track-a")]);
  });

  it("刷新后恢复的队列再播放：累计从零开始，保存的位置不算", async () => {
    const release = decodeReleaseDetail(sampleBody("releases/get"));
    const first = release.media.flatMap((medium) => medium.tracks)[0];
    if (first === undefined) throw new Error("测试样本缺少曲目");
    const storage = memoryStorage();
    saveQueue(storage, userId, {
      entries: [{ entryId: "restored-1", trackId: first.id, releaseId: release.id, groupId: "00000000-0000-4000-8000-000000000099" }],
      currentEntryId: "restored-1",
      mode: "shuffle",
      positionSeconds: 22,
      history: [],
    });
    const h = createHarness({ sessionStorage: storage, loadRelease: vi.fn(async () => release) });
    await settle();
    h.engine.play();
    await settle();
    h.audio.loadMetadata(240);
    h.audio.beginPlaying();
    expect(h.audio.currentTime).toBe(22);
    listenFor(h, 119);
    expect(h.played).toEqual([]); // 样本曲目长 240 秒，门槛 120 秒
    listenFor(h, 1);
    expect(h.played).toEqual([played(first.id, 120)]);
  });

  it("记录出口抛出错误也不影响播放", async () => {
    const record = vi.fn(() => {
      throw new Error("上报器坏了");
    });
    const h = createHarness({ playRecorder: { record, discard: () => undefined } });
    await playAndStart(h, [track("a")], 0, 60);
    listenFor(h, 30);
    expect(record).toHaveBeenCalledTimes(1);
    expect(state(h).status).toBe("playing");
    listenFor(h, 5);
    expect(progress(h).positionSeconds).toBe(35);
    expect(record).toHaveBeenCalledTimes(1);
  });

  it("会话结束作废还没有发出的上报，之后的进度不再交出记录；重复结束只作废一次", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a")], 0, 60);
    listenFor(h, 20);
    h.engine.endSession();
    expect(h.discardPlays).toHaveBeenCalledTimes(1);
    listenFor(h, 20);
    h.engine.endSession();
    expect(h.discardPlays).toHaveBeenCalledTimes(1);
    expect(h.played).toEqual([]);
  });
});

describe("characterization (C2)", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  const restorableTrack = track("a", {
    trackId: "00000000-0000-4000-8000-000000000011",
    releaseId: "00000000-0000-4000-8000-000000000012",
    groupId: "00000000-0000-4000-8000-000000000013",
  });

  /** 不去重：同一 status 的重复通知也是要保留的现状。 */
  function collectStatuses(h: Harness) {
    const statuses: ReturnType<typeof state>["status"][] = [];
    h.engine.subscribe(() => statuses.push(state(h).status));
    return statuses;
  }

  it("1：attach 前 playTracks 再 endSession 中止预检，迟到结果不设置 src", async () => {
    const h = createHarness({}, { attach: false });
    let resolve: (value: TrackPlaybackResult) => void = () => undefined;
    h.preflight("track-a", () => new Promise((done) => (resolve = done)));
    h.engine.playTracks([track("a")]);
    expect(signalOf(h, 0).aborted).toBe(false);

    h.engine.endSession();
    expect(signalOf(h, 0).aborted).toBe(true);
    expect(h.audio.getAttribute("src")).toBeNull();
    resolve(playbackOf("track-a"));
    await settle();
    expect(h.audio.sources).toEqual([]);
    expect(h.audio.playCalls).toBe(0);
    expect(state(h)).toMatchObject({ status: "idle", current: null, queue: { entries: [] } });
    expect(savedFor(h)).toBeNull();
  });

  it("1：attach 前 playTracks 已设置 src，endSession 仍移除来源并中断媒体", async () => {
    const h = createHarness({}, { attach: false });
    h.engine.playTracks([track("a")]);
    await settle();
    expect(h.audio.getAttribute("src")).toBe("/api/v1/tracks/track-a/audio");
    const loads = h.audio.loadCalls;

    h.engine.endSession();
    expect(h.audio.getAttribute("src")).toBeNull();
    expect(h.audio.loadCalls).toBe(loads + 1);
    expect(h.audio.paused).toBe(true);
    expect(state(h)).toMatchObject({ status: "idle", current: null, queue: { entries: [] } });
    expect(savedFor(h)).toBeNull();
  });

  it("2：attach 前已有命令使恢复停住并阻挡保存，直到下一次操作（固定现状，见 C2 PRD 的已知行为）", async () => {
    const h = createHarness({}, { attach: false });
    h.engine.playTracks([restorableTrack]);
    await settle();
    const before = h.sessionStorage.data.get(queueStorageKey(userId));
    expect(loadSavedQueue(h.sessionStorage, userId)?.entries).toHaveLength(1);
    h.engine.attach();
    await settle();
    expect(state(h).restoring).toBe(true);
    expect(h.loadRelease).not.toHaveBeenCalled();

    h.audio.loadMetadata(100);
    h.audio.beginPlaying();
    h.audio.tick(23);
    vi.advanceTimersByTime(10_000);
    window.dispatchEvent(new Event("pagehide"));
    expect(state(h)).toMatchObject({ status: "playing", restoring: true });
    expect(progress(h).positionSeconds).toBe(23);
    expect(h.sessionStorage.data.get(queueStorageKey(userId))).toBe(before);

    h.engine.enqueue([track("b")]);
    expect(state(h).restoring).toBe(false);
    expect(savedFor(h)).toMatchObject({ entries: [{ entryId: "e1" }, { entryId: "e2" }], positionSeconds: 23 });
    h.audio.tick(28);
    vi.advanceTimersByTime(5_000);
    expect(savedFor(h)?.positionSeconds).toBe(28);
  });

  it("3：恢复可重试后 enqueue 保留 transient，retry 加载当前项并恢复保存", async () => {
    const loadRelease = vi.fn(() => Promise.reject(networkError()));
    const h = createHarness({ loadRelease }, { attach: false });
    saveQueue(h.sessionStorage, userId, {
      entries: [{ entryId: "restored-1", trackId: restorableTrack.trackId, releaseId: restorableTrack.releaseId, groupId: restorableTrack.groupId }],
      currentEntryId: "restored-1",
      mode: "sequential",
      positionSeconds: 22,
      history: [],
    });
    const before = h.sessionStorage.data.get(queueStorageKey(userId));
    h.engine.attach();
    await settle();
    expect(state(h)).toMatchObject({ restoring: false, fault: { kind: "transient" }, current: null });
    window.dispatchEvent(new Event("pagehide"));
    expect(h.sessionStorage.data.get(queueStorageKey(userId))).toBe(before);

    h.engine.enqueue([track("b")]);
    expect(state(h)).toMatchObject({ status: "paused", fault: { kind: "transient" }, current: { entryId: "e1" } });
    expect(h.loadPlayback).not.toHaveBeenCalled();
    // enqueue 已经结束恢复重试阶段，保存当下队列；retry 不能再走发行恢复。
    expect(savedFor(h)).toMatchObject({ entries: [{ entryId: "e1", trackId: "track-b" }], positionSeconds: 0 });
    h.engine.retry();
    expect(state(h)).toMatchObject({ status: "loading", fault: null });
    await settle();
    expect(loadRelease).toHaveBeenCalledTimes(1);
    expect(preflightedTracks(h)).toEqual(["track-b"]);
    expect(h.audio.sources).toEqual(["/api/v1/tracks/track-b/audio"]);
    h.audio.loadMetadata(100);
    h.audio.beginPlaying();
    h.audio.tick(19);
    vi.advanceTimersByTime(5_000);
    expect(savedFor(h)).toMatchObject({ currentEntryId: "e1", positionSeconds: 19 });
  });

  it("4：正在准备的定时等待期间 detach，停止预检并保持 paused", async () => {
    const h = createHarness();
    h.preflightSequence("track-a", [preparing, playbackOf("track-a", "derived")], 0);
    h.engine.playTracks([track("a")]);
    await settle();
    expect(state(h).status).toBe("preparing");
    vi.advanceTimersByTime(999);
    await settle();
    expect(preflightedTracks(h)).toEqual(["track-a"]);

    h.engine.detach();
    expect(state(h).status).toBe("paused");
    vi.advanceTimersByTime(60_000);
    await settle();
    expect(preflightedTracks(h)).toEqual(["track-a"]);
    expect(h.audio.sources).toEqual([]);
    expect(h.audio.playCalls).toBe(0);
    expect(state(h)).toMatchObject({ status: "paused", fault: null, current: { entryId: "e1" } });
  });

  it.each(["预检在途", "准备轮询在途"] as const)("5：%s时 stop 中止请求、位置归零并保持 paused", async (phase) => {
    const h = createHarness();
    await playAndStart(h, [track("a")]);
    h.audio.tick(37);
    h.engine.detach();
    h.engine.attach();
    let resolve: (value: TrackPlaybackResult) => void = () => undefined;
    let polls = 0;
    h.preflight("track-a", () => {
      if (phase === "准备轮询在途" && polls++ === 0) {
        h.clock.now += 20_000;
        return Promise.resolve(preparing);
      }
      return new Promise((done) => (resolve = done));
    });
    h.engine.play();
    await settle();
    expect(state(h).status).toBe(phase === "预检在途" ? "loading" : "preparing");
    expect(progress(h).positionSeconds).toBe(37);
    const calls = h.loadPlayback.mock.calls.length;
    expect(signalOf(h, calls - 1).aborted).toBe(false);

    h.engine.stop();
    expect(signalOf(h, calls - 1).aborted).toBe(true);
    expect(state(h).status).toBe("paused");
    expect(progress(h)).toEqual({ positionSeconds: 0, durationSeconds: null, seekable: false });
    expect(savedFor(h)?.positionSeconds).toBe(0);
    resolve(playbackOf("track-a", "derived"));
    await settle();
    vi.advanceTimersByTime(60_000);
    await settle();
    expect(h.loadPlayback).toHaveBeenCalledTimes(calls);
    expect(h.audio.sources).toEqual(["/api/v1/tracks/track-a/audio"]);
    expect(h.audio.getAttribute("src")).toBeNull();
    expect(h.audio.playCalls).toBe(1);
    expect(state(h).status).toBe("paused");

    h.preflight("track-a", () => Promise.resolve(playbackOf("track-a")));
    h.engine.play();
    await settle();
    h.audio.loadMetadata(100);
    expect(h.audio.currentTime).toBe(0);
  });

  it("5：准备轮询的定时等待期间 stop 取消后续预检并将位置归零", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a")]);
    h.audio.tick(37);
    h.preflightSequence("track-a", [preparing, playbackOf("track-a", "derived")], 0);
    h.audio.fail(2);
    await settle();
    expect(state(h).status).toBe("preparing");
    expect(progress(h).positionSeconds).toBe(37);
    expect(preflightedTracks(h)).toEqual(["track-a", "track-a"]);

    h.engine.stop();
    vi.advanceTimersByTime(60_000);
    await settle();
    expect(preflightedTracks(h)).toEqual(["track-a", "track-a"]);
    expect(state(h).status).toBe("paused");
    expect(progress(h)).toEqual({ positionSeconds: 0, durationSeconds: null, seekable: false });
    expect(savedFor(h)?.positionSeconds).toBe(0);
    expect(h.audio.getAttribute("src")).toBeNull();
  });

  it("6：普通加载的完整 status 通知序列", async () => {
    const h = createHarness();
    const statuses = collectStatuses(h);
    await playAndStart(h, [track("a")]);
    expect(statuses).toEqual(["loading", "playing"]);
  });

  it("6：202 到就绪的完整 status 通知序列，loading 通知先于设置 src", async () => {
    const h = createHarness();
    const statuses = collectStatuses(h);
    const sources: (string | null)[] = [];
    h.engine.subscribe(() => sources.push(h.audio.getAttribute("src")));
    h.preflightSequence("track-a", [preparing, preparing, playbackOf("track-a", "derived")], 0);
    h.engine.playTracks([track("a")]);
    await settle();
    vi.advanceTimersByTime(1_000);
    await settle();
    vi.advanceTimersByTime(1_000);
    await settle();
    h.audio.loadMetadata(100);
    h.audio.beginPlaying();
    expect(preflightedTracks(h)).toEqual(["track-a", "track-a", "track-a"]);
    expect(statuses).toEqual(["loading", "preparing", "loading", "playing"]);
    expect(sources).toEqual([null, null, null, "/api/v1/tracks/track-a/audio"]);
  });

  it("6：媒体错误再分类并前进的完整 status 通知序列", async () => {
    const h = createHarness();
    const statuses = collectStatuses(h);
    await playAndStart(h, [track("a"), track("b")]);
    h.audio.fail(3);
    await settle();
    h.audio.loadMetadata(100);
    h.audio.beginPlaying();
    expect(state(h).failures).toEqual({ e1: "decode_failed" });
    expect(currentTitle(h)).toBe("曲目 b");
    expect(preflightedTracks(h)).toEqual(["track-a", "track-a", "track-b"]);
    expect(statuses).toEqual(["loading", "playing", "loading", "loading", "playing"]);
  });

  it("6：单曲循环的完整 status 通知序列，不重新预检", async () => {
    const h = createHarness();
    h.engine.setMode("repeat_one");
    const statuses = collectStatuses(h);
    await playAndStart(h, [track("a")]);
    h.audio.finish();
    await settle();
    expect(h.audio.currentTime).toBe(0);
    h.audio.beginPlaying();
    expect(preflightedTracks(h)).toEqual(["track-a"]);
    expect(h.audio.playCalls).toBe(2);
    expect(statuses).toEqual(["loading", "playing", "loading", "playing"]);
  });

  it("7：准备轮询收到 401 后 paused、没有 fault，保留原位置", async () => {
    const h = createHarness();
    await playAndStart(h, [track("a")]);
    h.audio.tick(31);
    h.preflightSequence("track-a", [preparing], 0);
    h.audio.fail(2);
    await settle();
    expect(state(h).status).toBe("preparing");
    expect(progress(h).positionSeconds).toBe(31);
    h.fail("track-a", httpError(401, "unauthorized"));
    vi.advanceTimersByTime(1_000);
    await settle();
    expect(state(h)).toMatchObject({ status: "paused", fault: null, failures: {}, current: { entryId: "e1" } });
    expect(progress(h)).toEqual({ positionSeconds: 31, durationSeconds: null, seekable: false });
    expect(h.audio.getAttribute("src")).toBeNull();
    vi.advanceTimersByTime(60_000);
    await settle();
    expect(preflightedTracks(h)).toEqual(["track-a", "track-a", "track-a"]);
    window.dispatchEvent(new Event("pagehide"));
    expect(savedFor(h)?.positionSeconds).toBe(31);
  });

  it("8：单曲循环重播被 NotAllowed 拒绝后 paused 仍定时保存（固定现状，见 C2 PRD 的已知行为）", async () => {
    const h = createHarness();
    h.engine.setMode("repeat_one");
    await playAndStart(h, [track("a")]);
    h.audio.playBehavior = "not-allowed";
    h.audio.finish();
    await settle();
    expect(state(h)).toMatchObject({ status: "paused", fault: { kind: "autoplay_blocked" } });
    expect(h.audio.paused).toBe(true);
    expect(h.audio.playCalls).toBe(2);
    expect(savedFor(h)?.positionSeconds).toBe(0);

    // 只改 fake 的位置，不发媒体事件或命令：存储变化只能来自仍在运行的定时保存。
    h.audio.currentTime = 7;
    vi.advanceTimersByTime(4_999);
    expect(savedFor(h)?.positionSeconds).toBe(0);
    vi.advanceTimersByTime(1);
    expect(savedFor(h)?.positionSeconds).toBe(7);
    h.audio.currentTime = 9;
    vi.advanceTimersByTime(5_000);
    expect(savedFor(h)?.positionSeconds).toBe(9);
    expect(state(h)).toMatchObject({ status: "paused", fault: { kind: "autoplay_blocked" } });
    expect(preflightedTracks(h)).toEqual(["track-a"]);
  });

  it.each(["媒体错误先到", "play 拒绝先到"] as const)("9：%s时媒体错误与 play 拒绝只启动一条分类链", async (order) => {
    const h = createHarness();
    const events = collectEvents(h);
    const statuses = collectStatuses(h);
    let rejectPlay: (reason: unknown) => void = () => undefined;
    // 只替换现有 fake 的公开 play 方法，精确控制两种失败的到达顺序。
    vi.spyOn(h.audio, "play").mockImplementationOnce(() => new Promise<void>((_resolve, reject) => (rejectPlay = reject)));
    h.engine.playTracks([track("a"), track("b")]);
    await settle();
    let resolveClassification: (value: TrackPlaybackResult) => void = () => undefined;
    h.preflight("track-a", () => new Promise((resolve) => (resolveClassification = resolve)));
    const rejection = new DOMException("不支持的来源", "NotSupportedError");
    if (order === "媒体错误先到") {
      h.audio.fail(3);
      rejectPlay(rejection);
    } else {
      rejectPlay(rejection);
      await settle();
      h.audio.fail(3);
    }
    await settle();
    expect(preflightedTracks(h)).toEqual(["track-a", "track-a"]);
    expect(signalOf(h, 1).aborted).toBe(false);
    expect(state(h).status).toBe("loading");

    resolveClassification(playbackOf("track-a"));
    await settle();
    expect(preflightedTracks(h)).toEqual(["track-a", "track-a", "track-b"]);
    expect(state(h).failures).toEqual({ e1: "decode_failed" });
    expect(currentTitle(h)).toBe("曲目 b");
    expect(h.audio.sources).toEqual(["/api/v1/tracks/track-a/audio", "/api/v1/tracks/track-b/audio"]);
    expect(events.map((event) => event.kind)).toEqual(["track_started", "track_started"]);
    expect(statuses).toEqual(["loading", "loading", "loading"]);
  });
});


describe("Web Audio 播放路由", () => {
  function graphFixture(state = "running", resumeToRunning = true) {
    const calls: { set: number[]; target: number[]; curves: Float32Array[] }[] = [];
    const listeners = new Set<() => void>();
    let contextState = state;
    const createGain = () => {
      const record = { set: [] as number[], target: [] as number[], curves: [] as Float32Array[] };
      calls.push(record);
      const gain = { value: 1, setValueAtTime: (value: number) => { record.set.push(value); }, setTargetAtTime: (value: number) => { record.target.push(value); }, setValueCurveAtTime: (value: Float32Array) => { record.curves.push(value); }, cancelScheduledValues: () => undefined };
      return { gain, connect: () => undefined } as unknown as GainNodeLike;
    };
    const ctx: AudioContextLike = { get state() { return contextState; }, currentTime: 1, destination: {}, resume: async () => { if (resumeToRunning) contextState = "running"; }, createGain, createMediaElementSource: () => ({ connect: () => undefined }), addEventListener: (_type, listener) => { listeners.add(listener); }, removeEventListener: (_type, listener) => { listeners.delete(listener); } };
    return { calls, ctx, setState: (next: string) => { contextState = next; listeners.forEach((listener) => listener()); } };
  }

  it("两项关闭时直出且不创建上下文", async () => {
    const storage = memoryStorage();
    storage.setItem(PLAYBACK_SETTINGS_STORAGE_KEY, JSON.stringify({ version: 1, leveling: "off", crossfade: false, crossfadeSeconds: 5 }));
    const graph = graphFixture();
    const createAudio = vi.fn(() => new FakeAudio());
    const createAudioContext = vi.fn(() => graph.ctx);
    const h = createHarness({ localStorage: storage, createAudio, createAudioContext });
    h.engine.playTracks([track("1")]);
    await settle();
    expect(createAudioContext).not.toHaveBeenCalled();
    expect(state(h).audioRoute).toBe("direct");
  });

  it("跨发行自然前进时备用元素开始播放后切换，并对两支增益执行渐变曲线", async () => {
    const storage = memoryStorage();
    storage.setItem(PLAYBACK_SETTINGS_STORAGE_KEY, JSON.stringify({ version: 1, leveling: "off", crossfade: true, crossfadeSeconds: 5 }));
    const graph = graphFixture();
    const spares: FakeAudio[] = [];
    const h = createHarness({ localStorage: storage, createAudioContext: () => graph.ctx, createAudio: () => { const audio = new FakeAudio(); spares.push(audio); return audio; }, loadAnalysis: async () => null });
    const first = track("1", { releaseId: "release-a" });
    const second = track("2", { releaseId: "release-b" });
    await playAndStart(h, [first, second], 0, 100);
    h.audio.tick(80);
    await settle();
    h.audio.tick(95);
    await settle();
    const spare = spares[0];
    expect(spare?.src).toBe("/api/v1/tracks/track-2/audio");
    spare?.loadMetadata(100);
    spare?.beginPlaying();
    expect(state(h).current?.track.trackId).toBe("track-2");
    expect(graph.calls[2]?.curves).toHaveLength(1);
    expect(graph.calls[4]?.curves).toHaveLength(1);
  });

  it("均衡正增益写入 levelGain，且上下文离开 running 后退回直出", async () => {
    const graph = graphFixture();
    const replacements: FakeAudio[] = [];
    const analysis = decodeTrackAnalysis(sampleBody("tracks/analysis-done"));
    const h = createHarness({ createAudioContext: () => graph.ctx, createAudio: () => { const next = new FakeAudio(); replacements.push(next); return next; }, loadAnalysis: async () => ({ ...analysis, analysis: analysis.analysis === null ? null : { ...analysis.analysis, trackGainDb: 7.8 } }) });
    h.engine.playTracks([track("1")]);
    await settle();
    h.audio.loadMetadata(100);
    h.audio.beginPlaying();
    await settle();
    expect(graph.calls[1]?.set.at(-1)).toBeCloseTo(10 ** (7.8 / 20));
    expect(state(h).leveling).toMatchObject({ gainDb: 7.8 });
    expect(state(h).audioRoute).toBe("web_audio");
    h.engine.setVolume(0.4);
    expect(graph.calls[0]?.set.at(-1)).toBe(0.4);
    expect(h.audio.volume).toBe(1);
    graph.setState("suspended");
    expect(state(h).audioRoute).toBe("fallback");
    expect(replacements).toHaveLength(1);
    expect(replacements[0]?.currentTime).toBe(h.audio.currentTime);
  });

  it.each([
    { reason: "下一首同一发行", sameRelease: true, repeatOne: false, manual: false, endTrack: "track-2" },
    { reason: "单曲循环", sameRelease: false, repeatOne: true, manual: false, endTrack: "track-1" },
    { reason: "fadeStart 前手动 next", sameRelease: false, repeatOne: false, manual: true, endTrack: "track-2" },
  ])("不渐变：$reason", async ({ sameRelease, repeatOne, manual, endTrack }) => {
    const storage = memoryStorage();
    storage.setItem(PLAYBACK_SETTINGS_STORAGE_KEY, JSON.stringify({ version: 1, leveling: "off", crossfade: true, crossfadeSeconds: 5 }));
    const graph = graphFixture();
    const spares: FakeAudio[] = [];
    const h = createHarness({ localStorage: storage, createAudioContext: () => graph.ctx, createAudio: () => { const audio = new FakeAudio(); spares.push(audio); return audio; }, loadAnalysis: async () => null });
    const first = track("1", { releaseId: "release-a" });
    const second = track("2", { releaseId: sameRelease ? "release-a" : "release-b" });
    await playAndStart(h, [first, second], 0, 100);
    if (repeatOne) h.engine.setMode("repeat_one");
    if (manual) {
      h.audio.tick(10);
      h.engine.next();
      await settle();
    } else {
      h.audio.tick(80);
      await settle();
      h.audio.tick(95);
      await settle();
      h.audio.finish();
      await settle();
    }
    expect(spares.every((spare) => spare.src === "")).toBe(true);
    expect(graph.calls.flatMap((call) => call.curves)).toHaveLength(0);
    expect(state(h).current?.track.trackId).toBe(endTrack);
  });

  it("备用元素 play() 被拒时退回普通切歌", async () => {
    const storage = memoryStorage();
    storage.setItem(PLAYBACK_SETTINGS_STORAGE_KEY, JSON.stringify({ version: 1, leveling: "off", crossfade: true, crossfadeSeconds: 5 }));
    const graph = graphFixture();
    const spares: FakeAudio[] = [];
    const h = createHarness({ localStorage: storage, createAudioContext: () => graph.ctx, createAudio: () => { const audio = new FakeAudio(); spares.push(audio); return audio; }, loadAnalysis: async () => null });
    await playAndStart(h, [track("1", { releaseId: "release-a" }), track("2", { releaseId: "release-b" })], 0, 100);
    h.audio.tick(80);
    await settle();
    h.audio.tick(95);
    await settle();
    const spare = spares[0];
    expect(spare?.src).toBe("/api/v1/tracks/track-2/audio");
    if (spare === undefined) throw new Error("缺少渐变备用元素");
    spare.playBehavior = "not-allowed";
    spare.loadMetadata(100);
    await settle();
    expect(state(h).current?.track.trackId).toBe("track-1");
    expect(graph.calls.flatMap((call) => call.curves)).toHaveLength(0);
    h.audio.finish();
    await settle();
    expect(state(h).current?.track.trackId).toBe("track-2");
    expect(h.audio.sources).toContain("/api/v1/tracks/track-2/audio");
  });

  it("渐变切换后聆听计数归新曲，旧曲的渐变前出声未达门槛", async () => {
    const storage = memoryStorage();
    storage.setItem(PLAYBACK_SETTINGS_STORAGE_KEY, JSON.stringify({ version: 1, leveling: "off", crossfade: true, crossfadeSeconds: 5 }));
    const graph = graphFixture();
    const spares: FakeAudio[] = [];
    const analysis = decodeTrackAnalysis(sampleBody("tracks/analysis-done"));
    const h = createHarness({ localStorage: storage, createAudioContext: () => graph.ctx, createAudio: () => { const audio = new FakeAudio(); spares.push(audio); return audio; }, loadAnalysis: async () => ({ ...analysis, analysis: analysis.analysis === null ? null : { ...analysis.analysis, trailingSilenceMs: 20_000 } }) });
    await playAndStart(h, [track("1", { releaseId: "release-a", durationSeconds: 100 }), track("2", { releaseId: "release-b", durationSeconds: 60 })], 0, 100);
    for (let second = 0; second <= 15; second += 1) {
      h.clock.now += 1_000;
      h.audio.tick(second);
      await settle();
    }
    const spare = spares[0];
    expect(spare).toBeDefined();
    spare?.loadMetadata(60);
    spare?.beginPlaying();
    expect(h.played).toEqual([]);
    for (let second = 1; second <= 30; second += 1) {
      h.clock.now += 1_000;
      spare?.tick(second);
    }
    expect(h.played).toEqual([{ trackId: "track-2", playedSeconds: 30 }]);
  });

  it("上下文无法恢复为 running 时本首保持 direct 并照常播放", async () => {
    const graph = graphFixture("suspended", false);
    const h = createHarness({ createAudioContext: () => graph.ctx, createAudio: () => new FakeAudio() });
    h.engine.playTracks([track("1")]);
    await settle();
    await vi.waitFor(() => expect(h.audio.sources.length).toBeGreaterThan(0));
    h.audio.loadMetadata(60);
    h.audio.beginPlaying();
    expect(h.audio.playCalls).toBeGreaterThan(0);
    expect(state(h).status).toBe("playing");
    expect(state(h).audioRoute).toBe("direct");
  });

  it("volumeControllable 为 false 时自动均衡也不创建 AudioContext", async () => {
    const locked = new FakeAudio();
    Object.defineProperty(locked, "volume", { configurable: true, get: () => 1, set: () => undefined });
    const graph = graphFixture();
    const createAudioContext = vi.fn(() => graph.ctx);
    const h = createHarness({ audio: locked, createAudioContext, createAudio: () => new FakeAudio() });
    expect(state(h).volumeControllable).toBe(false);
    h.engine.playTracks([track("1")]);
    await settle();
    expect(createAudioContext).not.toHaveBeenCalled();
    expect(state(h).audioRoute).toBe("direct");
  });

});
