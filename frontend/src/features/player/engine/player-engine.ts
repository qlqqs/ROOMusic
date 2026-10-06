// 播放引擎：持有一个不挂进页面的音频元素、播放队列与播放状态。它是不依赖 React 的普通类，
// React 经 subscribe/getSnapshot（离散状态）与 subscribeProgress/getProgress（播放进度）读取不可变快照。
//
// 状态、进度与时长只来自媒体事件，不用定时器推断；按下播放不等于正在播放。预检（GET /tracks/{id}/playback）先于出声：
// 媒体元素读不到错误正文，所以失败原因（目录已停用、文件变化、不能直出……）在预检里得到，音频出错后也用它再次分类。
import { failureOf, type ApiFailure } from "../../../shared/api";
import type { ReleaseDetailDTO, ReleaseTrackDTO } from "../../catalog";
import type { TrackPlaybackDTO } from "../api/decoders";
import type { MediaSessionActionName, MediaSessionPort } from "./media-session";
import { clearSavedQueue, loadSavedQueue, loadVolume, saveQueue, saveVolume, type SavedQueue, type StorageLike } from "./persistence";
import {
  advance,
  appendEntries,
  clearQueue,
  currentEntry,
  emptyQueue,
  hasNext,
  insertAfterCurrent,
  jumpTo,
  moveEntry,
  MAX_QUEUE_ENTRIES,
  removeEntry,
  replaceQueue,
  retreat,
  setMode as setQueueMode,
  type PlayMode,
  type PlayerTrack,
  type QueueEntry,
  type QueueEnv,
  type QueueState,
} from "./queue";
import { playerTrackOf } from "./tracks";

/** 引擎需要的音频元素能力；HTMLAudioElement 满足它，测试用替身。 */
export type AudioLike = {
  src: string;
  currentTime: number;
  readonly duration: number;
  readonly paused: boolean;
  readonly ended: boolean;
  readonly seekable: { readonly length: number };
  volume: number;
  muted: boolean;
  preload: string;
  readonly error: { readonly code: number } | null;
  play: () => Promise<void> | undefined;
  pause: () => void;
  load: () => void;
  getAttribute: (name: "src") => string | null;
  removeAttribute: (name: "src") => void;
  addEventListener: (type: string, listener: () => void) => void;
  removeEventListener: (type: string, listener: () => void) => void;
};

export type EngineDeps = {
  userId: string;
  audio: AudioLike;
  /** 预检：请求经 apiRequest，401 照常通知会话边界。 */
  loadPlayback: (trackId: string, signal: AbortSignal) => Promise<TrackPlaybackDTO>;
  /** 恢复队列时按发行读取详情（与详情页共用查询缓存）。 */
  loadRelease: (releaseId: string) => Promise<ReleaseDetailDTO>;
  sessionStorage: StorageLike | null;
  localStorage: StorageLike | null;
  mediaSession: MediaSessionPort | null;
  /** 封面资源 ID 对应的绝对地址（给系统媒体信息用）。 */
  artworkUrl: (artworkId: string) => string;
  newId: () => string;
  random: () => number;
  now: () => number;
};

/** 曲目不能播放的原因；界面按它选文案（不比较后端 message）。 */
export type PlaybackFailureReason = "not_found" | "root_disabled" | "not_directly_playable" | "source_unavailable" | "source_changed" | "decode_failed";

export type PlayerStatus = "idle" | "loading" | "playing" | "paused" | "buffering";

/** 正在加载、出声或缓冲：都是“要播放”的状态。togglePlayback 此时暂停，播放/暂停按钮也据此显示“暂停”，两者不能各判各的。 */
export function isPlaybackActive(status: PlayerStatus): boolean {
  return status === "playing" || status === "buffering" || status === "loading";
}

/** 停在暂停的两种故障：暂时无法取得音频（网络、服务暂不可用，可重试），或浏览器的自动播放策略拒绝了 play()。 */
export type PlayerFault = { kind: "transient" } | { kind: "autoplay_blocked" };

/** 引擎自己产生的、值得向读屏公告的事件。seq 单调递增，界面据此只公告一次。 */
export type PlayerEvent =
  | { seq: number; kind: "track_started"; entryId: string }
  | { seq: number; kind: "track_failed"; entryId: string; reason: PlaybackFailureReason }
  | { seq: number; kind: "all_failed" }
  | { seq: number; kind: "transient_failure" }
  | { seq: number; kind: "autoplay_blocked" }
  | { seq: number; kind: "queue_restored"; restored: number; dropped: number };

type EventBody = PlayerEvent extends infer E ? (E extends { seq: number } ? Omit<E, "seq"> : never) : never;

export type PlayerSnapshot = {
  status: PlayerStatus;
  queue: QueueState;
  current: QueueEntry | null;
  hasNext: boolean;
  /** 已标记为失败的条目（条目 ID → 原因）；自动前进会跳过它们，用户可以手动重试。 */
  failures: Readonly<Record<string, PlaybackFailureReason>>;
  fault: PlayerFault | null;
  event: PlayerEvent | null;
  volume: number;
  muted: boolean;
  /** 刷新后正在恢复队列。 */
  restoring: boolean;
};

export type PlayerProgress = {
  positionSeconds: number;
  /** 媒体报告的时长；未知（还没加载、无限或不是数字）是 null。 */
  durationSeconds: number | null;
  /** 只有时长有限且媒体报告了可定位范围时才允许拖动。 */
  seekable: boolean;
};

export type AddResult = "added" | "queue_full";

const PERSIST_INTERVAL_MS = 5_000;
const POSITION_STATE_INTERVAL_MS = 1_000;
const SEEK_STEP_SECONDS = 10;
const RESTORE_CONCURRENCY = 4;

const MEDIA_ERR_ABORTED = 1;
const MEDIA_ERR_NETWORK = 2;

const mediaSessionActions: readonly MediaSessionActionName[] = ["play", "pause", "previoustrack", "nexttrack", "stop", "seekto", "seekbackward", "seekforward"];

/** DOMException 等错误的名字（NotAllowedError、AbortError……）；不依赖 instanceof，不同运行环境里的 DOMException 不一定继承同一个 Error。 */
function errorName(error: unknown): string {
  return typeof error === "object" && error !== null && "name" in error ? String(error.name) : "";
}

function finiteOr(value: number, fallback: number): number {
  return Number.isFinite(value) ? value : fallback;
}

/** 后端对预检与音频的 404/409 分类 → 曲目不能播放的原因；不认识的 4xx 按“文件无法读取”处理，5xx 与其他失败是暂时性的。 */
function failureReasonOf(failure: ApiFailure): PlaybackFailureReason | null {
  if (failure.kind !== "http") return null;
  if (failure.status === 404) return "not_found";
  if (failure.status === 409) {
    switch (failure.code) {
      case "root_disabled":
        return "root_disabled";
      case "not_directly_playable":
        return "not_directly_playable";
      case "source_changed":
        return "source_changed";
      default:
        return "source_unavailable";
    }
  }
  return failure.status >= 400 && failure.status < 500 ? "source_unavailable" : null;
}

export class PlayerEngine {
  private readonly deps: EngineDeps;
  private readonly audio: AudioLike;
  private readonly env: QueueEnv;

  private queue: QueueState = emptyQueue;
  private status: PlayerStatus = "idle";
  private failures: Record<string, PlaybackFailureReason> = {};
  /** 当前这一次访问是否收到过 playing；同条目的旧访问不能为本轮加载背书。 */
  private playedVisitEntryId: string | null = null;
  private fault: PlayerFault | null = null;
  private event: PlayerEvent | null = null;
  private eventSeq = 0;
  private restoring = false;
  private volume: number;
  private muted: boolean;
  private progress: PlayerProgress = { positionSeconds: 0, durationSeconds: null, seekable: false };
  private snapshot: PlayerSnapshot;

  private readonly listeners = new Set<() => void>();
  private readonly progressListeners = new Set<() => void>();

  private attached = false;
  /** 会话已结束：引擎不再接受命令，也不再写存储。 */
  private finished = false;
  private restoreStarted = false;
  private restoreRetry = false;
  /** 用户操作过队列：恢复的结果不再覆盖它。 */
  private touched = false;

  /** 每次开始加载一首曲目加一；异步回调带着发起时的值，过期的结果一律丢弃。 */
  private attempt = 0;
  private preflight: AbortController | null = null;
  private loadedEntryId: string | null = null;
  private expectedSrc: string | null = null;
  private pendingSeek: number | null = null;
  /** 媒体没有加载时的位置：恢复队列后的保存位置、暂时性故障时的位置，下一次开始播放时定位到这里。 */
  private resumePosition = 0;
  private persistTimer: ReturnType<typeof setInterval> | null = null;
  private savedEntries: { source: readonly QueueEntry[]; entries: SavedQueue["entries"] } | null = null;
  private lastPositionStateAt = Number.NEGATIVE_INFINITY;

  constructor(deps: EngineDeps) {
    this.deps = deps;
    this.audio = deps.audio;
    this.env = { newEntryId: deps.newId, random: deps.random };
    const saved = loadVolume(deps.localStorage);
    this.volume = saved.volume;
    this.muted = saved.muted;
    this.audio.preload = "metadata";
    this.audio.volume = this.volume;
    this.audio.muted = this.muted;
    this.snapshot = this.buildSnapshot();
  }

  // ---- 订阅 ----

  readonly subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => void this.listeners.delete(listener);
  };

  readonly getSnapshot = (): PlayerSnapshot => this.snapshot;

  readonly subscribeProgress = (listener: () => void): (() => void) => {
    this.progressListeners.add(listener);
    return () => void this.progressListeners.delete(listener);
  };

  readonly getProgress = (): PlayerProgress => this.progress;

  // ---- 生命周期 ----

  /**
   * 开始监听媒体事件与页面事件、登记系统媒体键，并在第一次时恢复刷新前的队列（保持暂停，不发声）。
   * 可以在 detach 之后再次调用（开发模式下 React 会模拟卸载再挂载）。
   */
  attach(): void {
    if (this.attached || this.finished) return;
    this.attached = true;
    for (const [type, handler] of this.mediaEvents()) this.audio.addEventListener(type, handler);
    window.addEventListener("pagehide", this.onPageHide);
    this.registerMediaSession();
    if (!this.restoreStarted) {
      this.restoreStarted = true;
      void this.restore();
    }
  }

  /** 停止监听、停止音频并放弃网络；队列与保存的内容保留（会话结束由 endSession 清除）。 */
  detach(): void {
    if (!this.attached) return;
    this.attached = false;
    for (const [type, handler] of this.mediaEvents()) this.audio.removeEventListener(type, handler);
    window.removeEventListener("pagehide", this.onPageHide);
    this.resumePosition = this.currentPosition();
    this.persist();
    this.stopTimers();
    this.cancelPreflight();
    this.attempt += 1;
    this.unloadAudio();
    this.setProgress({ positionSeconds: this.resumePosition, durationSeconds: null, seekable: false });
    if (isPlaybackActive(this.status)) this.status = this.queue.currentEntryId === null ? "idle" : "paused";
    this.clearMediaSession();
    this.commit();
  }

  /** 会话结束（退出、失效、换用户）：停止播放、清空队列、删除保存的队列；之后引擎不再接受命令。 */
  endSession(): void {
    if (this.finished) return;
    this.detach();
    // 即使尚未 attach，也必须中止命令发起的预检与媒体。
    this.stopPlayback();
    this.finished = true;
    clearSavedQueue(this.deps.sessionStorage, this.deps.userId);
    this.queue = emptyQueue;
    this.playedVisitEntryId = null;
    this.failures = {};
    this.fault = null;
    this.event = null;
    this.status = "idle";
    this.restoring = false;
    this.resumePosition = 0;
    this.setProgress({ positionSeconds: 0, durationSeconds: null, seekable: false });
    this.commit();
  }

  // ---- 队列命令 ----

  /** 立即播放：用 tracks 替换队列并从 startIndex 开始播放。 */
  playTracks(tracks: readonly PlayerTrack[], startIndex = 0): AddResult {
    if (this.finished || tracks.length > MAX_QUEUE_ENTRIES) return "queue_full";
    this.touch();
    this.queue = replaceQueue(this.queue, tracks, startIndex, this.env);
    this.playedVisitEntryId = null;
    this.failures = {};
    const current = currentEntry(this.queue);
    if (current === null) {
      this.stopPlayback();
      this.afterQueueChange();
      return "added";
    }
    this.load(current.entryId, 0);
    return "added";
  }

  /** 下一首播放：插在当前条目之后，不打断当前播放。 */
  playNext(tracks: readonly PlayerTrack[]): AddResult {
    return this.addToQueue(insertAfterCurrent(this.queue, tracks, this.env));
  }

  /** 加入队列：追加到队尾，不打断当前播放。 */
  enqueue(tracks: readonly PlayerTrack[]): AddResult {
    return this.addToQueue(appendEntries(this.queue, tracks, this.env));
  }

  private addToQueue(result: ReturnType<typeof appendEntries>): AddResult {
    if (this.finished) return "queue_full";
    if (!result.ok) return "queue_full";
    this.touch();
    this.queue = result.state;
    if (this.status === "idle" && this.queue.currentEntryId !== null) {
      this.status = "paused";
      this.resumePosition = 0;
    }
    this.afterQueueChange();
    return "added";
  }

  /** 播放队列里的某一项（“播放此项”）。当前项正在播放时不动；当前项已暂停就继续；已标记失败的当前项重新尝试。 */
  playEntry(entryId: string): void {
    if (this.finished || this.entryById(entryId) === undefined) return;
    this.touch();
    if (entryId === this.queue.currentEntryId && this.failures[entryId] === undefined) {
      this.play();
      return;
    }
    this.acceptTransition(jumpTo(this.queue, entryId));
    this.load(entryId, 0);
  }

  /** 移除一个条目（按条目 ID）。移除当前项：正在播放就切到后继并继续，已暂停就停在后继上，没有后继就停在空闲。 */
  removeEntry(entryId: string): void {
    if (this.finished || this.entryById(entryId) === undefined) return;
    this.touch();
    const wasActive = this.isActive();
    const result = removeEntry(this.queue, entryId, this.env);
    this.queue = result.state;
    this.failures = { ...this.failures };
    delete this.failures[entryId];
    if (!result.removedCurrent) {
      this.afterQueueChange();
      return;
    }
    const successor = result.state.currentEntryId;
    if (successor === null) {
      this.stopPlayback();
      this.afterQueueChange();
    } else if (wasActive) {
      this.load(successor, 0);
    } else {
      this.stopPlayback();
      this.afterQueueChange();
    }
  }

  moveEntry(entryId: string, delta: -1 | 1): void {
    if (this.finished) return;
    this.touch();
    this.queue = moveEntry(this.queue, entryId, delta);
    this.afterQueueChange();
  }

  clearQueue(): void {
    if (this.finished) return;
    this.touch();
    this.queue = clearQueue(this.queue);
    this.playedVisitEntryId = null;
    this.failures = {};
    this.stopPlayback();
    this.afterQueueChange();
  }

  setMode(mode: PlayMode): void {
    if (this.finished) return;
    this.touch();
    this.queue = setQueueMode(this.queue, mode);
    this.afterQueueChange();
  }

  // ---- 播放命令 ----

  play(): void {
    if (this.finished) return;
    // 队列里有条目但没有当前项（移除了末项的当前条目之后）：从第一项开始。
    const current = currentEntry(this.queue) ?? this.queue.entries[0];
    if (current === undefined) return;
    this.touch();
    if (isPlaybackActive(this.status)) return;
    this.acceptTransition(jumpTo(this.queue, current.entryId));
    if (this.loadedEntryId === current.entryId && this.isCurrentMedia() && this.failures[current.entryId] === undefined) {
      this.fault = null;
      this.requestPlay(this.attempt, current);
      this.commit();
      return;
    }
    this.load(current.entryId, this.resumePosition);
  }

  pause(): void {
    if (this.finished) return;
    if (this.status === "loading" && this.loadedEntryId === null) {
      // 预检还在途中：放弃这次加载，停在暂停。
      this.cancelPreflight();
      this.attempt += 1;
      this.status = "paused";
      this.commit();
      this.persist();
      return;
    }
    if (this.isActive()) {
      // 同一媒体的 play() 拒绝可能晚于暂停到达，不能再覆盖新的用户意图。
      this.attempt += 1;
      this.audio.pause();
      this.onPause();
    }
  }

  togglePlayback(): void {
    if (isPlaybackActive(this.status)) this.pause();
    else this.play();
  }

  /** 停止：暂停并回到曲目开头（系统媒体键的“停止”）。 */
  stop(): void {
    if (this.finished) return;
    this.pause();
    this.resumePosition = 0;
    this.pendingSeek = null;
    if (this.isCurrentMedia() && this.progress.seekable) {
      this.audio.currentTime = 0;
      this.syncProgress();
    } else this.setProgress({ positionSeconds: 0, durationSeconds: null, seekable: false });
    this.persist();
  }

  next(): void {
    if (this.finished) return;
    this.touch();
    const step = advance(this.queue, this.env, { reason: "manual", isSkipped: this.isFailed });
    if (step.entryId === null) return;
    this.acceptTransition(step.state);
    this.load(step.entryId, 0);
  }

  previous(): void {
    if (this.finished) return;
    this.touch();
    const step = retreat(this.queue, this.isFailed);
    if (step.entryId === null) return;
    const wasCurrent = step.entryId === this.queue.currentEntryId;
    this.acceptTransition(step.state, this.queue.mode !== "shuffle");
    if (wasCurrent && this.loadedEntryId === step.entryId && this.isCurrentMedia()) {
      // 已经在队首（或随机模式没有历史）：当前条目从头播放；还没生效的开始位置作废，元数据到达后不能再跳过去。
      this.pendingSeek = null;
      this.audio.currentTime = 0;
      this.play();
      return;
    }
    this.load(step.entryId, 0);
  }

  /** 定位到 seconds（秒）。只有时长有限且媒体报告了可定位范围时才生效。 */
  seek(seconds: number): void {
    if (this.finished || !this.isCurrentMedia() || !this.progress.seekable || this.progress.durationSeconds === null) return;
    if (!Number.isFinite(seconds)) return;
    this.audio.currentTime = Math.min(Math.max(0, seconds), this.progress.durationSeconds);
  }

  setVolume(volume: number): void {
    if (this.finished || !Number.isFinite(volume)) return;
    this.audio.volume = Math.min(1, Math.max(0, volume));
    this.syncVolume();
  }

  setMuted(muted: boolean): void {
    if (this.finished) return;
    this.audio.muted = muted;
    this.syncVolume();
  }

  /** 暂时性故障（网络、服务暂不可用）之后重试：从停下的位置继续。 */
  retry(): void {
    if (this.finished || this.fault?.kind !== "transient") return;
    if (this.restoreRetry) {
      this.restoreRetry = false;
      this.fault = null;
      void this.restore();
      return;
    }
    const current = currentEntry(this.queue);
    if (current === null) return;
    this.touch();
    this.load(current.entryId, this.resumePosition);
  }

  // ---- 加载与播放 ----

  /** 预检并开始播放一首曲目；seekTo 是开始位置（秒）。之前的加载与媒体都被放弃。 */
  private load(entryId: string, seekTo: number): void {
    const entry = this.entryById(entryId);
    if (entry === undefined) return;
    this.cancelPreflight();
    this.unloadAudio();
    this.queue = this.queue.currentEntryId === entryId ? this.queue : { ...this.queue, currentEntryId: entryId };
    const attempt = ++this.attempt;
    this.playedVisitEntryId = null;
    this.resumePosition = seekTo;
    this.setProgress({ positionSeconds: seekTo, durationSeconds: null, seekable: false });
    this.fault = null;
    if (this.failures[entryId] !== undefined) {
      this.failures = { ...this.failures };
      delete this.failures[entryId];
    }
    this.status = "loading";
    this.raise({ kind: "track_started", entryId });
    this.afterQueueChange();
    const controller = new AbortController();
    this.preflight = controller;
    this.deps.loadPlayback(entry.track.trackId, controller.signal).then(
      (playback) => this.onPreflightResolved(attempt, entry, playback, seekTo),
      (error: unknown) => this.onPreflightRejected(attempt, entry, error, seekTo),
    );
  }

  private onPreflightResolved(attempt: number, entry: QueueEntry, playback: TrackPlaybackDTO, seekTo: number): void {
    if (attempt !== this.attempt) return;
    this.preflight = null;
    this.loadedEntryId = entry.entryId;
    this.expectedSrc = playback.representation.url;
    this.pendingSeek = seekTo > 0 ? seekTo : null;
    this.audio.src = playback.representation.url;
    this.requestPlay(attempt, entry);
  }

  private requestPlay(attempt: number, entry: QueueEntry): void {
    try {
      const started = this.audio.play();
      if (started === undefined) return;
      started.catch((error: unknown) => this.onPlayRejected(attempt, entry, error));
    } catch (error) {
      this.onPlayRejected(attempt, entry, error);
    }
  }

  private onPlayRejected(attempt: number, entry: QueueEntry, error: unknown): void {
    if (attempt !== this.attempt) return;
    const name = errorName(error);
    if (name === "AbortError") return; // 被新的加载或暂停打断，不是失败
    if (name === "NotAllowedError") {
      // 浏览器的自动播放策略拒绝了 play()：停在暂停，等用户按播放；不显示“正在播放”。
      this.status = "paused";
      this.fault = { kind: "autoplay_blocked" };
      this.raise({ kind: "autoplay_blocked" });
      this.syncMediaSessionState();
      this.commit();
      this.persist();
      return;
    }
    void this.classifyMediaError(attempt, entry, 0);
  }

  private onPreflightRejected(attempt: number, entry: QueueEntry, error: unknown, position: number): void {
    if (attempt !== this.attempt) return;
    const failure = failureOf(error);
    if (failure?.kind === "aborted") return;
    this.preflight = null;
    if (failure?.kind === "http" && failure.status === 401) {
      // 会话边界已被请求层通知；这里只停下，随后卸载会清理一切。
      this.unloadAudio();
      this.status = "paused";
      this.syncMediaSessionState();
      this.commit();
      return;
    }
    const reason = failure === null ? null : failureReasonOf(failure);
    if (reason !== null) {
      this.failEntry(entry, reason);
      return;
    }
    // 网络失败、无效响应、5xx：暂时无法取得音频。停在暂停，保留位置，可以重试，不连续跳歌。
    this.unloadAudio();
    this.resumePosition = position;
    this.status = "paused";
    this.fault = { kind: "transient" };
    this.raise({ kind: "transient_failure" });
    this.syncMediaSessionState();
    this.commit();
    this.persist();
  }

  /**
   * 媒体元素出错：媒体元素读不到错误正文，所以重新预检来区分原因。
   * 预检失败按预检失败处理（401 结束会话、404/409 标为失败、网络与 5xx 暂停可重试）；
   * 预检通过时，传输中断（MEDIA_ERR_NETWORK）是暂时性的，其余（解码失败、格式不支持）标为解码失败并前进。
   */
  private async classifyMediaError(attempt: number, entry: QueueEntry, mediaErrorCode: number): Promise<void> {
    if (attempt !== this.attempt) return;
    // 同一次媒体错误可能伴随 play() 拒绝；只接受第一条分类链的结果。
    attempt = ++this.attempt;
    const position = this.currentPosition();
    this.resumePosition = position;
    this.cancelPreflight();
    const controller = new AbortController();
    this.preflight = controller;
    this.unloadAudio();
    this.status = "loading";
    this.setProgress({ positionSeconds: position, durationSeconds: null, seekable: false });
    this.syncMediaSession();
    this.commit();
    try {
      await this.deps.loadPlayback(entry.track.trackId, controller.signal);
    } catch (error) {
      if (attempt !== this.attempt || controller.signal.aborted) return;
      this.onPreflightRejected(attempt, entry, error, position);
      return;
    }
    if (attempt !== this.attempt) return;
    this.preflight = null;
    if (mediaErrorCode === MEDIA_ERR_NETWORK) {
      this.resumePosition = position;
      this.status = "paused";
      this.fault = { kind: "transient" };
      this.raise({ kind: "transient_failure" });
      this.syncMediaSessionState();
      this.commit();
      this.persist();
      return;
    }
    this.failEntry(entry, "decode_failed");
  }

  private failEntry(entry: QueueEntry, reason: PlaybackFailureReason): void {
    this.failures = { ...this.failures, [entry.entryId]: reason };
    this.raise({ kind: "track_failed", entryId: entry.entryId, reason });
    this.unloadAudio();
    this.status = "paused";
    this.resumePosition = 0;
    this.setProgress({ positionSeconds: 0, durationSeconds: null, seekable: false });
    this.advanceAfterFailure();
  }

  /** 按模式前进到下一首可以尝试的曲目（跳过已知失败的）；没有就停下。 */
  private advanceAfterFailure(): void {
    const step = advance(this.queue, this.env, { reason: "manual", isSkipped: this.isFailed });
    // 单条目的列表循环或随机会回到刚失败的条目自己：不再尝试，避免对同一首曲目无休止地重试。
    if (step.entryId === null || this.failures[step.entryId] !== undefined) {
      if (this.queue.entries.length > 0 && this.queue.entries.every((entry) => this.failures[entry.entryId] !== undefined)) this.raise({ kind: "all_failed" });
      this.afterQueueChange();
      return;
    }
    this.acceptTransition(step.state);
    this.load(step.entryId, 0);
  }

  /** 曲目自然结束：按模式前进；没有下一首时停在暂停（顺序模式的末曲）。 */
  private advanceAfterEnd(): void {
    const step = advance(this.queue, this.env, { reason: "natural", isSkipped: this.isFailed });
    if (step.entryId === null) {
      this.status = "paused";
      this.stopTimers();
      this.syncProgress();
      this.syncMediaSessionState();
      this.commit();
      this.persist();
      return;
    }
    const current = currentEntry(this.queue);
    if (current !== null && step.entryId === current.entryId && this.loadedEntryId === current.entryId) {
      // 单曲循环（或只有一首的循环）：当前条目从头再播，不必重新预检。
      this.audio.currentTime = 0;
      this.syncProgress();
      this.status = "loading";
      this.syncMediaSessionState();
      this.commit();
      this.requestPlay(this.attempt, current);
      return;
    }
    this.acceptTransition(step.state);
    this.load(step.entryId, 0);
  }

  // ---- 媒体事件 ----

  private mediaEvents(): [string, () => void][] {
    return [
      ["loadedmetadata", this.onLoadedMetadata],
      ["durationchange", this.syncProgress],
      ["timeupdate", this.syncProgress],
      ["seeked", this.syncProgress],
      ["progress", this.syncProgress],
      ["canplay", this.syncProgress],
      ["playing", this.onPlaying],
      ["pause", this.onPause],
      // 只有 waiting 表示播放因缺数据停住（随后由 playing 恢复）；stalled 只说明网络暂时没送来数据，
      // 已缓冲的音频照常出声，之后也不保证再有 playing，所以不改变状态。
      ["waiting", this.onWaiting],
      ["ended", this.onEnded],
      ["error", this.onError],
      ["volumechange", this.syncVolume],
    ];
  }

  /** 事件是否来自当前加载的媒体：切歌之后迟到的旧事件一律忽略。 */
  private isCurrentMedia(): boolean {
    return this.loadedEntryId !== null && this.audio.getAttribute("src") === this.expectedSrc;
  }

  private readonly onLoadedMetadata = (): void => {
    if (!this.isCurrentMedia()) return;
    this.syncProgress();
  };

  private readonly syncProgress = (): void => {
    if (!this.isCurrentMedia()) return;
    const duration = Number.isFinite(this.audio.duration) && this.audio.duration > 0 ? this.audio.duration : null;
    if (this.pendingSeek !== null && duration !== null && this.audio.seekable.length > 0) {
      const target = Math.min(this.pendingSeek, duration);
      this.pendingSeek = null;
      this.audio.currentTime = target;
    }
    this.setProgress({
      positionSeconds: finiteOr(this.audio.currentTime, 0),
      durationSeconds: duration,
      seekable: duration !== null && this.audio.seekable.length > 0,
    });
    this.syncPositionState();
  };

  private readonly onPlaying = (): void => {
    if (!this.isCurrentMedia() || this.audio.paused) return;
    this.status = "playing";
    this.playedVisitEntryId = this.loadedEntryId;
    this.fault = null;
    this.startTimers();
    this.syncMediaSessionState();
    this.commit();
  };

  private readonly onPause = (): void => {
    if (!this.isCurrentMedia() || this.audio.ended) return;
    if (!isPlaybackActive(this.status)) return;
    this.status = "paused";
    this.stopTimers();
    this.syncMediaSessionState();
    this.commit();
    this.persist();
  };

  private readonly onWaiting = (): void => {
    if (!this.isCurrentMedia() || this.status !== "playing") return;
    this.status = "buffering";
    this.commit();
  };

  private readonly onEnded = (): void => {
    if (!this.isCurrentMedia() || !this.audio.ended) return;
    this.advanceAfterEnd();
  };

  private readonly onError = (): void => {
    if (!this.isCurrentMedia() || this.audio.error === null) return;
    const code = this.audio.error.code;
    if (code === MEDIA_ERR_ABORTED) return;
    const entry = currentEntry(this.queue);
    if (entry === null) return;
    void this.classifyMediaError(this.attempt, entry, code);
  };

  private readonly onPageHide = (): void => {
    this.persist();
  };

  // ---- 内部状态 ----

  private readonly isFailed = (entryId: string): boolean => this.failures[entryId] !== undefined;

  private isActive(): boolean {
    return isPlaybackActive(this.status);
  }

  private entryById(entryId: string): QueueEntry | undefined {
    return this.queue.entries.find((entry) => entry.entryId === entryId);
  }

  private touch(): void {
    this.touched = true;
    this.restoreRetry = false;
    if (this.restoring) {
      this.restoring = false;
      this.commit();
    }
  }

  private cancelPreflight(): void {
    this.preflight?.abort();
    this.preflight = null;
  }

  /** 暂停并清空媒体元素的来源：中断网络传输，之后不再有旧媒体的事件被当作当前的。 */
  private unloadAudio(): void {
    this.stopTimers();
    this.loadedEntryId = null;
    this.expectedSrc = null;
    this.pendingSeek = null;
    this.audio.pause();
    this.audio.removeAttribute("src");
    this.audio.load();
  }

  /** 停止当前播放：放弃加载与媒体，状态回到“有当前项的暂停”或空闲。 */
  private stopPlayback(): void {
    this.cancelPreflight();
    this.attempt += 1;
    this.unloadAudio();
    this.fault = null;
    this.resumePosition = 0;
    this.status = this.queue.currentEntryId === null ? "idle" : "paused";
    this.stopTimers();
    this.setProgress({ positionSeconds: 0, durationSeconds: null, seekable: false });
  }

  private setProgress(next: PlayerProgress): void {
    const previous = this.progress;
    if (previous.positionSeconds === next.positionSeconds && previous.durationSeconds === next.durationSeconds && previous.seekable === next.seekable) return;
    this.progress = next;
    for (const listener of [...this.progressListeners]) listener();
  }

  private raise(body: EventBody): void {
    this.eventSeq += 1;
    this.event = { ...body, seq: this.eventSeq } as PlayerEvent;
  }

  private buildSnapshot(): PlayerSnapshot {
    return {
      status: this.status,
      queue: this.queue,
      current: currentEntry(this.queue),
      hasNext: hasNext(this.queue),
      failures: this.failures,
      fault: this.fault,
      event: this.event,
      volume: this.volume,
      muted: this.muted,
      restoring: this.restoring,
    };
  }

  private commit(): void {
    this.snapshot = this.buildSnapshot();
    for (const listener of [...this.listeners]) listener();
  }

  /** 纯函数会把离开的当前项追加到历史；只接受本次访问确实出声的追加，旧历史原样保留。 */
  private acceptTransition(next: QueueState, appendsHistory = true): void {
    if (appendsHistory && next.history !== this.queue.history && this.playedVisitEntryId !== this.queue.currentEntryId) {
      // 不先追加再删尾：历史达到上限时，追加会挤掉最早的真实播放记录。
      next = { ...next, history: this.queue.history };
    }
    this.queue = next;
  }

  /** 队列或当前项变化之后：同步系统媒体信息、保存队列、通知界面。 */
  private afterQueueChange(): void {
    this.syncMediaSession();
    this.commit();
    this.persist();
  }

  private syncVolume = (): void => {
    const volume = finiteOr(this.audio.volume, this.volume);
    const muted = this.audio.muted;
    if (volume === this.volume && muted === this.muted) return;
    this.volume = volume;
    this.muted = muted;
    saveVolume(this.deps.localStorage, { volume, muted });
    this.commit();
  };

  // ---- 保存与恢复 ----

  private currentPosition(): number {
    if (this.loadedEntryId === null || !this.isCurrentMedia()) return this.resumePosition;
    const position = finiteOr(this.audio.currentTime, 0);
    // 开始位置要等时长与可定位范围都已知才定位；在那之前媒体停在开头，要保存、要重试的是待定位的位置，不是 0。
    // 媒体已经从开头走起来（来源不可定位，定位不会发生）时以实际位置为准。
    return this.pendingSeek !== null && position === 0 ? this.pendingSeek : position;
  }

  /** 要保存的条目标识，按队列条目数组的引用缓存：队列没变时（播放中定时保存只有位置在变）不重建上万个条目。 */
  private savedEntriesOf(source: readonly QueueEntry[]): SavedQueue["entries"] {
    if (this.savedEntries?.source !== source) {
      this.savedEntries = { source, entries: source.map((entry) => ({ entryId: entry.entryId, trackId: entry.track.trackId, releaseId: entry.track.releaseId, groupId: entry.track.groupId })) };
    }
    return this.savedEntries.entries;
  }

  private persist(): void {
    // 恢复期间队列还是空的：此时保存会删掉刷新前保存的队列。
    if (this.finished || this.restoring || this.restoreRetry) return;
    saveQueue(this.deps.sessionStorage, this.deps.userId, {
      entries: this.savedEntriesOf(this.queue.entries),
      currentEntryId: this.queue.currentEntryId,
      mode: this.queue.mode,
      positionSeconds: this.currentPosition(),
      history: this.queue.history,
    });
  }

  private startTimers(): void {
    if (this.persistTimer !== null) return;
    this.persistTimer = setInterval(() => this.persist(), PERSIST_INTERVAL_MS);
  }

  private stopTimers(): void {
    if (this.persistTimer === null) return;
    clearInterval(this.persistTimer);
    this.persistTimer = null;
  }

  /**
   * 恢复刷新前保存的队列：按发行重新读取详情（并发 4）重建条目，已经不存在的曲目丢弃并报告数量。
   * 恢复后保持暂停，首次按播放时先预检再定位到保存的位置。用户在恢复完成前操作过队列时，恢复结果被丢弃。
   */
  private async restore(): Promise<void> {
    const saved = loadSavedQueue(this.deps.sessionStorage, this.deps.userId);
    if (saved === null || saved.entries.length === 0) return;
    this.restoring = true;
    this.commit();
    const releaseIds = [...new Set(saved.entries.map((entry) => entry.releaseId))];
    const releases = new Map<string, ReleaseDetailDTO>();
    let next = 0;
    let transientFailure = false;
    const worker = async (): Promise<void> => {
      for (let index = next++; index < releaseIds.length; index = next++) {
        const releaseId = releaseIds[index];
        if (this.finished || this.touched) return;
        if (releaseId === undefined) continue;
        try {
          releases.set(releaseId, await this.deps.loadRelease(releaseId));
        } catch (error) {
          const failure = failureOf(error);
          // 只有明确的 404 才能证明已不存在；网络/服务失败不能删除已保存的队列。
          if (failure?.kind !== "http" || failure.status !== 404) transientFailure = true;
        }
      }
    };
    await Promise.all(Array.from({ length: Math.min(RESTORE_CONCURRENCY, releaseIds.length) }, worker));
    if (this.finished || this.touched || !this.restoring) return;
    if (transientFailure) {
      this.restoring = false;
      this.restoreRetry = true;
      this.fault = { kind: "transient" };
      this.raise({ kind: "transient_failure" });
      this.commit();
      return;
    }

    const trackMaps = new Map<string, Map<string, ReleaseTrackDTO>>();
    for (const [releaseId, release] of releases) {
      trackMaps.set(releaseId, new Map(release.media.flatMap((medium) => medium.tracks.map((track) => [track.id, track] as const))));
    }
    const entries: QueueEntry[] = [];
    let dropped = 0;
    for (const item of saved.entries) {
      const release = releases.get(item.releaseId);
      const track = trackMaps.get(item.releaseId)?.get(item.trackId);
      if (release === undefined || track === undefined) {
        dropped += 1;
        continue;
      }
      entries.push({ entryId: item.entryId, track: playerTrackOf(release, track, item.groupId) });
    }
    const survivors = new Set(entries.map((entry) => entry.entryId));
    const savedIds = saved.entries.map((entry) => entry.entryId);
    // 当前条目被丢弃时，取它之后第一个保留下来的条目（位置从头开始）。
    const currentSurvived = saved.currentEntryId !== null && survivors.has(saved.currentEntryId);
    const successor = saved.currentEntryId === null ? undefined : savedIds.slice(savedIds.indexOf(saved.currentEntryId) + 1).find((id) => survivors.has(id));
    const currentEntryId = currentSurvived ? saved.currentEntryId : (successor ?? null);
    const history = saved.history.filter((id) => survivors.has(id));
    this.playedVisitEntryId = null;
    const exclude = new Set([...history, ...(currentEntryId === null ? [] : [currentEntryId])]);
    this.queue = {
      entries,
      currentEntryId,
      mode: saved.mode,
      history,
      bag: saved.mode === "shuffle" ? entries.filter((entry) => !exclude.has(entry.entryId)).map((entry) => entry.entryId) : [],
    };
    this.failures = {};
    this.resumePosition = currentSurvived ? saved.positionSeconds : 0;
    this.status = currentEntryId === null ? "idle" : "paused";
    this.restoring = false;
    this.setProgress({ positionSeconds: this.resumePosition, durationSeconds: null, seekable: false });
    this.raise({ kind: "queue_restored", restored: entries.length, dropped });
    this.afterQueueChange();
  }

  // ---- 系统媒体键 ----

  private registerMediaSession(): void {
    const session = this.deps.mediaSession;
    if (session === null) return;
    const handlers: Record<MediaSessionActionName, (details: { seekTime?: number | undefined }) => void> = {
      play: () => this.play(),
      pause: () => this.pause(),
      previoustrack: () => this.previous(),
      nexttrack: () => this.next(),
      stop: () => this.stop(),
      seekto: (details) => {
        if (details.seekTime !== undefined) this.seek(details.seekTime);
      },
      seekbackward: () => this.seek(this.progress.positionSeconds - SEEK_STEP_SECONDS),
      seekforward: () => this.seek(this.progress.positionSeconds + SEEK_STEP_SECONDS),
    };
    for (const action of mediaSessionActions) session.setActionHandler(action, handlers[action]);
    this.syncMediaSession();
  }

  private clearMediaSession(): void {
    const session = this.deps.mediaSession;
    if (session === null) return;
    for (const action of mediaSessionActions) session.setActionHandler(action, null);
    session.setMetadata(null);
    session.setPositionState(null);
    session.setPlaybackState("none");
  }

  private syncMediaSession(): void {
    const session = this.deps.mediaSession;
    if (session === null || !this.attached) return;
    const current = currentEntry(this.queue);
    if (current === null) {
      session.setMetadata(null);
    } else {
      const { track } = current;
      session.setMetadata({
        title: track.title,
        artist: track.artist,
        album: track.albumTitle,
        artwork: track.artworkId === null ? [] : [{ src: this.deps.artworkUrl(track.artworkId) }],
      });
    }
    session.setPositionState(null);
    this.lastPositionStateAt = Number.NEGATIVE_INFINITY;
    this.syncMediaSessionState();
  }

  private syncMediaSessionState(): void {
    const session = this.deps.mediaSession;
    if (session === null || !this.attached) return;
    session.setPlaybackState(this.status === "idle" ? "none" : this.status === "playing" || this.status === "buffering" ? "playing" : "paused");
  }

  /** 播放中最多每秒一次把位置告诉系统；时长未知时不设置。 */
  private syncPositionState(): void {
    const session = this.deps.mediaSession;
    if (session === null || !this.attached || this.progress.durationSeconds === null) return;
    const now = this.deps.now();
    if (now - this.lastPositionStateAt < POSITION_STATE_INTERVAL_MS) return;
    this.lastPositionStateAt = now;
    session.setPositionState({ duration: this.progress.durationSeconds, position: Math.min(this.progress.positionSeconds, this.progress.durationSeconds) });
  }
}
