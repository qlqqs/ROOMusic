import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../../../shared/api";
import { sampleBody } from "../../../test/api-samples";
import { FakeAudio } from "../../../test/fake-audio";
import { memoryStorage } from "../../../test/memory-storage";
import { playerTrack } from "../../../test/player-data";
import { decodeReleaseDetail } from "../../catalog/api/decoders";
import type { TrackPlaybackDTO } from "../api/decoders";
import type { MediaSessionActionName, MediaSessionMetadata, MediaSessionPort } from "./media-session";
import { PlayerEngine, type EngineDeps } from "./player-engine";
import { loadSavedQueue, queueStorageKey, saveQueue, VOLUME_STORAGE_KEY } from "./persistence";
import type { PlayerTrack } from "./queue";

const userId = "00000000-0000-4000-8000-000000000002";

const track = (name: string, overrides: Partial<PlayerTrack> = {}) => playerTrack(name, overrides);

function playbackOf(trackId: string): TrackPlaybackDTO {
  return { track_id: trackId, representation: { kind: "original", url: `/api/v1/tracks/${trackId}/audio`, mime_type: "audio/flac", codecs: null, size_bytes: 1000 } };
}

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
  const outcomes = new Map<string, () => Promise<TrackPlaybackDTO>>();
  let counter = 0;
  const loadPlayback = vi.fn((trackId: string, _signal: AbortSignal) => (outcomes.get(trackId) ?? (() => Promise.resolve(playbackOf(trackId))))());
  const loadRelease = vi.fn(async (_releaseId: string): Promise<never> => {
    throw new Error("未登记发行");
  });
  const deps: EngineDeps = {
    userId,
    audio,
    loadPlayback,
    loadRelease,
    sessionStorage,
    localStorage,
    mediaSession,
    artworkUrl: (id) => `http://localhost/api/v1/artworks/${id}`,
    newId: () => `e${++counter}`,
    random: () => 0,
    now: () => clock.now,
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
    /** 让某首曲目的预检返回指定结果（Promise 或抛出的错误）。 */
    preflight: (trackId: string, outcome: () => Promise<TrackPlaybackDTO>) => void outcomes.set(trackId, outcome),
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
    let resolveFirst: (value: TrackPlaybackDTO) => void = () => undefined;
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
    let resolveFirst: (value: TrackPlaybackDTO) => void = () => undefined;
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

  it("音量限制在 0 到 1，忽略非有限的值；媒体元素自己改变音量时同步状态", () => {
    const h = createHarness();
    h.engine.setVolume(7);
    expect(h.audio.volume).toBe(1);
    h.engine.setVolume(-1);
    expect(h.audio.volume).toBe(0);
    h.engine.setVolume(Number.NaN);
    expect(h.audio.volume).toBe(0);
    h.audio.volume = 0.5;
    h.audio.emit("volumechange");
    expect(state(h).volume).toBe(0.5);
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
