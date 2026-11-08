// 播放引擎：持有一个不挂进页面的音频元素、播放队列与播放状态。它是不依赖 React 的普通类，
// React 经 subscribe/getSnapshot（离散状态）与 subscribeProgress/getProgress（播放进度）读取不可变快照。
//
// 状态、进度与时长只来自媒体事件，不用定时器推断；按下播放不等于正在播放。预检（GET /tracks/{id}/playback）先于出声：
// 媒体元素读不到错误正文，所以失败原因（目录已停用、文件变化、不能直出……）在预检里得到，音频出错后也用它再次分类。
// 派生格式（ALAC）的预检会等待服务端无损转换：收到 202 就进入“正在准备”并立即再次预检，直到可以播放、失败或被放弃；
// 当前曲目出声后，为下一首派生曲目预先发一次预检，让转换在切歌之前完成。
//
// 计一次播放（阶段 P）：ListenTracker 按媒体事件累计真正出声的时间，达到规则时引擎把它经 playRecorder 交出去，一次访问最多一次；
// 引擎不发请求、不等结果，也不知道接口长什么样。
//
// 音量均衡与交叉渐变（按 Roon）：需要时按需接入 Web Audio（audio-graph.ts）。均衡与渐变都关、设备的网页音量只读（iPhone / iPad）、
// 或上下文没能进入 running 时不接入，管线与没有这两项时完全一致。已接入的元素在上下文被挂起时没有声音：运行中被挂起就“中断退回”，
// 换一个不接入的新元素从原位置继续，本页不再接入。交叉渐变只在自然播放到结尾、下一首是另一张发行时进行，两个元素交替。
import { failureOf } from "../../../shared/api";
import type { ReleaseDetailDTO } from "../../catalog";
import type { TrackAnalysisDTO } from "../api/decoders";
import type { TrackPlaybackResult } from "../api/endpoints";
import { AudioGraph, type AudioContextLike } from "./audio-graph";
import { requesting, stopped, unreachable, type Lifecycle, type MediaPipeline, type Pipeline, type RestorePhase } from "./engine-state";
import { ListenTracker, type ListenRecord, type ListenTrack } from "./listen-tracker";
import type { MediaSessionPort } from "./media-session";
import { MediaSessionBinding } from "./media-session-binding";
import { clearSavedQueue, loadSavedQueue, loadVolume, savedEntriesOf, saveQueue, saveVolume, type StorageLike } from "./persistence";
import { chooseLeveling, inAlbumContext, linearGain, type Leveling } from "./leveling";
import { loadPlaybackSettings, savePlaybackSettings, type PlaybackSettings } from "./playback-settings";
import { errorName, failureReasonOf, isDerivationBusy, MEDIA_ERR_ABORTED, MEDIA_ERR_NETWORK, type PlaybackFailureReason } from "./playback-failure";
import { Prefetcher } from "./prefetcher";
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
  patchTrackMetadata,
  removeEntry,
  replaceQueue,
  retreat,
  setMode as setQueueMode,
  type PlayMode,
  type PlayerTrack,
  type QueueEntry,
  type QueueEnv,
  type QueueState,
  type TrackDisplayMetadata,
} from "./queue";
import { loadSavedReleases, rebuildQueue } from "./queue-restore";

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

/** 一次达到计次规则的播放：曲目与达到规则那一刻累计的实际出声秒数。 */
export type PlayedTrack = ListenRecord;

/**
 * 记录播放的出口。record 把达到计次规则的播放交出去（上报到服务端），引擎不等结果、不处理失败，抛出的错误也不影响播放；
 * discard 在会话结束时调用，作废还没有发出的上报，上一位用户的记录不会在新会话下发出。
 */
export type PlayRecorder = { record: (play: PlayedTrack) => void; discard: () => void };

export type { PlaybackFailureReason } from "./playback-failure";

export type EngineDeps = {
  userId: string;
  audio: AudioLike;
  /** 预检：请求经 apiRequest，401 照常通知会话边界。派生格式的预检会在服务端等待转换，最多约 20 秒。 */
  loadPlayback: (trackId: string, signal: AbortSignal) => Promise<TrackPlaybackResult>;
  /** 这首曲目是否值得在上一首出声之后预先预检：当前浏览器可播放，且交付方式是服务端转换（预检是唯一会启动转换的请求）。 */
  shouldPrefetch: (track: PlayerTrack) => boolean;
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
  playRecorder: PlayRecorder;
  /** 曲目的音频分析（与波形组件共用查询缓存）；读取失败返回 null。没有时不做均衡与渐变。 */
  loadAnalysis?: (trackId: string) => Promise<TrackAnalysisDTO | null>;
  /** 新建一个音频元素：交叉渐变的备用元素与中断退回用。 */
  createAudio?: () => AudioLike;
  /** 新建 AudioContext；没有（或没有 createAudio）时永不接入 Web Audio。 */
  createAudioContext?: (() => AudioContextLike) | null;
};

/** 音频走哪条路：direct 是元素直接出声；web_audio 是已接入音频图；fallback 是本页中断退回后的直出，不再接入。 */
export type AudioRoute = "direct" | "web_audio" | "fallback";

/** preparing：服务端正在把曲目无损转换成浏览器能解码的格式（预检收到 202），转换完成后才开始加载音频。 */
export type PlayerStatus = "idle" | "loading" | "preparing" | "playing" | "paused" | "buffering";

/** 正在加载、准备、出声或缓冲：都是“要播放”的状态。togglePlayback 此时暂停，播放/暂停按钮也据此显示“暂停”，两者不能各判各的。 */
export function isPlaybackActive(status: PlayerStatus): boolean {
  return status === "playing" || status === "buffering" || status === "loading" || status === "preparing";
}

/**
 * 停在暂停的几种故障：暂时无法取得音频（网络、服务暂不可用，可重试）、服务器正忙于转换（可重试，与前者共用重试入口，只是文案不同），
 * 或浏览器的自动播放策略拒绝了 play()。
 */
export type PlayerFault = { kind: "transient" } | { kind: "busy" } | { kind: "autoplay_blocked" };

/** 引擎自己产生的、值得向读屏公告的事件。seq 单调递增，界面据此只公告一次。 */
export type PlayerEvent =
  | { seq: number; kind: "track_started"; entryId: string }
  | { seq: number; kind: "track_failed"; entryId: string; reason: PlaybackFailureReason }
  | { seq: number; kind: "all_failed" }
  | { seq: number; kind: "preparing"; entryId: string }
  | { seq: number; kind: "transient_failure" }
  | { seq: number; kind: "derivation_busy" }
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
  /** 播放设置（音量均衡与交叉渐变），设备偏好。 */
  settings: PlaybackSettings;
  /** 当前曲目实际施加的均衡；没有接入或均衡关时是 null。 */
  leveling: Leveling | null;
  audioRoute: AudioRoute;
  /** 网页能否调节音量（iPhone / iPad 上只读）：不能时均衡与渐变不生效。 */
  volumeControllable: boolean;
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

/** 从第一次收到“正在准备”起最多等多久：服务端一次转换的上限是 10 分钟，再留 1 分钟余量；超过按转换失败处理。 */
const PREPARING_LIMIT_MS = 11 * 60_000;

/**
 * 连续两次预检（都收到“正在准备”）之间至少间隔多久。服务端会把预检挂起约 20 秒，正常情况下这个下限不起作用；
 * 它只防止异常的、立即返回的 202 让客户端无休止地高速重试。不是退避：服务端在等，客户端不需要等。
 */
const MIN_PREPARING_POLL_MS = 1_000;

/** 预检就绪后最多再等分析结果多久；等不到这一首按响度未知处理。 */
const ANALYSIS_WAIT_MS = 1_000;
/** 播放已接入的元素前恢复上下文最多等多久（仍在同一次手势的激活期内）。 */
const RESUME_WAIT_MS = 1_000;
/** 交叉渐变提前多久规划（为下一首发预检与读取分析）。 */
const CROSSFADE_PLAN_AHEAD_SECONDS = 15;
/** 下一首开头静音超过这个长度视为异常，不跳过。 */
const MAX_LEADING_SKIP_MS = 10_000;

/**
 * 交叉渐变的计划：planned 是已为下一首发出预检与分析读取（暂不接受队列步）；starting 是备用元素已设来源、等它出声后切换；
 * skipped 是这一次访问不渐变（条件不满足或预检没就绪），队列或设置变化时重新判断。
 */
type CrossfadePlan =
  | { phase: "planned"; entryId: string; next: QueueEntry; state: QueueState; fadeStart: number; seconds: number; controller: AbortController; url: string | null; analysis: TrackAnalysisDTO | null }
  | { phase: "starting"; entryId: string; next: QueueEntry; state: QueueState; seconds: number; controller: AbortController; url: string; spare: AudioLike; level: Leveling | null; detach: () => void }
  | { phase: "skipped"; entryId: string };

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function finiteOr(value: number, fallback: number): number {
  return Number.isFinite(value) ? value : fallback;
}

/** 队列条目 → 计次的访问对象：条目 ID 区分同一首曲目的多次入队，时长是扫描得到的（未知是 null）。 */
function listenTrackOf(entry: QueueEntry): ListenTrack {
  return { entryId: entry.entryId, trackId: entry.track.trackId, durationSeconds: entry.track.durationSeconds };
}

export class PlayerEngine {
  private readonly deps: EngineDeps;
  /** 活动的音频元素：交叉渐变切换与中断退回时会换成另一个。 */
  private audio: AudioLike;
  /** 每个音频元素各自的媒体事件处理函数；只处理活动元素的事件。 */
  private readonly elementListeners = new Map<AudioLike, [string, () => void][]>();
  /** 交叉渐变的备用元素（第一次渐变时创建，之后与活动元素轮换）。 */
  private spare: AudioLike | null = null;
  private settings: PlaybackSettings;
  private readonly volumeControllable: boolean;
  /** Web Audio 音频图；设备不支持或没有提供上下文时是 null。 */
  private readonly graph: AudioGraph | null;
  /** 本页已中断退回：不再接入。 */
  private fellBack = false;
  private leveling: Leveling | null = null;
  /** 已完成的分析结果（按曲目 ID）；没完成的每次重新读取（查询缓存决定是否真的发请求）。 */
  private readonly analyses = new Map<string, TrackAnalysisDTO>();
  /** 正在加载的曲目的分析读取：预检就绪后最多再等它 1 秒。 */
  private analysisWait: { entryId: string; promise: Promise<unknown>; result: TrackAnalysisDTO | null | undefined } | null = null;
  private crossfadePlan: CrossfadePlan | null = null;
  /** 切换之后还在淡出的旧元素：渐变结束（或旧元素先播完）时卸载。 */
  private fading: { old: AudioLike; timer: ReturnType<typeof setTimeout>; onEnded: () => void } | null = null;
  private readonly env: QueueEnv;
  /** 计一次播放：只在状态为 playing 时累计，见 listen-tracker.ts。 */
  private readonly listen: ListenTracker;
  private readonly mediaSession: MediaSessionBinding;
  /** 下一首的预取预检：与当前加载互相独立，结果被丢弃（只为让转换提前完成）；队列或当前项变化时中止。 */
  private readonly prefetch: Prefetcher;

  private queue: QueueState = emptyQueue;
  private failures: Record<string, PlaybackFailureReason> = {};
  /** 当前这一次访问是否收到过 playing；同条目的旧访问不能为本轮加载背书。 */
  private playedVisitEntryId: string | null = null;
  private fault: PlayerFault | null = null;
  private event: PlayerEvent | null = null;
  private eventSeq = 0;
  private restorePhase: RestorePhase = { kind: "none" };
  private volume: number;
  private muted: boolean;
  private progress: PlayerProgress = { positionSeconds: 0, durationSeconds: null, seekable: false };
  private snapshot: PlayerSnapshot;

  private readonly listeners = new Set<() => void>();
  private readonly progressListeners = new Set<() => void>();

  private lifecycle: Lifecycle = "fresh";
  /** 用户操作过队列：恢复的结果不再覆盖它。 */
  private touched = false;

  /** 每次开始加载一首曲目加一；异步回调带着发起时的值，过期的结果一律丢弃。 */
  private attempt = 0;
  /** 预检、媒体与播放状态：见 engine-state.ts 的 Pipeline。 */
  private pipeline: Pipeline = { kind: "stopped", status: "idle" };
  /** 媒体没有加载时的位置：恢复队列后的保存位置、暂时性故障时的位置，下一次开始播放时定位到这里。 */
  private resumePosition = 0;
  private persistTimer: ReturnType<typeof setInterval> | null = null;

  constructor(deps: EngineDeps) {
    this.deps = deps;
    this.audio = deps.audio;
    this.env = { newEntryId: deps.newId, random: deps.random };
    this.listen = new ListenTracker(deps.now);
    this.mediaSession = new MediaSessionBinding(deps.mediaSession, deps.artworkUrl, deps.now);
    this.prefetch = new Prefetcher(deps.loadPlayback, deps.shouldPrefetch);
    const saved = loadVolume(deps.localStorage);
    this.volume = saved.volume;
    this.muted = saved.muted;
    this.settings = loadPlaybackSettings(deps.localStorage);
    // iPhone / iPad 上网页音量只读：写进去读回来不变。
    const before = this.audio.volume;
    this.audio.volume = 0.5;
    this.volumeControllable = this.audio.volume === 0.5;
    this.audio.volume = before;
    const createContext = deps.createAudioContext ?? null;
    this.graph = this.volumeControllable && createContext !== null && deps.createAudio !== undefined ? new AudioGraph(createContext, this.onContextStateChange) : null;
    this.audio.preload = "metadata";
    this.bindElement(this.audio);
    this.applyUserVolume();
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
    const previous = this.lifecycle;
    if (previous === "attached" || previous === "ended") return;
    this.lifecycle = "attached";
    for (const [element, listeners] of this.elementListeners) for (const [type, handler] of listeners) element.addEventListener(type, handler);
    window.addEventListener("pagehide", this.onPageHide);
    this.registerMediaSession();
    if (previous === "fresh") void this.restore();
  }

  /** 停止监听、停止音频并放弃网络；队列与保存的内容保留（会话结束由 endSession 清除）。 */
  detach(): void {
    if (this.lifecycle !== "attached") return;
    this.lifecycle = "detached";
    for (const [element, listeners] of this.elementListeners) for (const [type, handler] of listeners) element.removeEventListener(type, handler);
    window.removeEventListener("pagehide", this.onPageHide);
    this.resumePosition = this.currentPosition();
    this.persist();
    this.stopTimers();
    this.cancelPreflight();
    this.cancelPrefetch();
    this.attempt += 1;
    // 要播放的状态（加载、准备、出声、缓冲）停下；已经停下的保持空闲或暂停。
    const status = this.status;
    this.unloadAudio(status === "idle" || status === "paused" ? { kind: "stopped", status } : stopped(this.queue.currentEntryId));
    this.setProgress({ positionSeconds: this.resumePosition, durationSeconds: null, seekable: false });
    this.clearMediaSession();
    this.commit();
  }

  /** 会话结束（退出、失效、换用户）：停止播放、清空队列、删除保存的队列；之后引擎不再接受命令。 */
  endSession(): void {
    if (this.ended) return;
    this.deps.playRecorder.discard();
    this.detach();
    // 即使尚未 attach，也必须中止命令发起的预检与媒体。
    this.stopPlayback();
    this.lifecycle = "ended";
    this.analyses.clear();
    this.analysisWait = null;
    clearSavedQueue(this.deps.sessionStorage, this.deps.userId);
    this.queue = emptyQueue;
    this.playedVisitEntryId = null;
    this.failures = {};
    this.fault = null;
    this.event = null;
    this.pipeline = { kind: "stopped", status: "idle" };
    this.restorePhase = { kind: "none" };
    this.resumePosition = 0;
    this.setProgress({ positionSeconds: 0, durationSeconds: null, seekable: false });
    this.commit();
  }

  // ---- 队列命令 ----

  /** 立即播放：用 tracks 替换队列并从 startIndex 开始播放。 */
  playTracks(tracks: readonly PlayerTrack[], startIndex = 0): AddResult {
    if (this.ended || tracks.length > MAX_QUEUE_ENTRIES) return "queue_full";
    this.gesture();
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
    if (this.ended) return "queue_full";
    if (!result.ok) return "queue_full";
    this.touch();
    this.queue = result.state;
    if (this.status === "idle" && this.queue.currentEntryId !== null) {
      this.pipeline = { kind: "stopped", status: "paused" };
      this.resumePosition = 0;
    }
    this.afterQueueChange();
    return "added";
  }

  /** 播放队列里的某一项（“播放此项”）。当前项正在播放时不动；当前项已暂停就继续；已标记失败的当前项重新尝试。 */
  playEntry(entryId: string): void {
    if (this.ended || this.entryById(entryId) === undefined) return;
    this.gesture();
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
    if (this.ended || this.entryById(entryId) === undefined) return;
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

  /**
   * 刷新已在队列里的曲目的显示元数据（M5 ⑤：艺人数据就绪或艺人治理之后补上、更新艺人链接）：
   * 不改顺序、当前项、播放状态与进度，不重新加载音频，也不改刷新恢复的记录（它只存 ID）。
   */
  updateTrackMetadata(patches: ReadonlyMap<string, TrackDisplayMetadata>): void {
    if (this.ended) return;
    const next = patchTrackMetadata(this.queue, patches);
    if (next === this.queue) return;
    this.queue = next;
    this.syncMediaSession();
    this.commit();
  }

  moveEntry(entryId: string, delta: -1 | 1): void {
    if (this.ended) return;
    this.touch();
    this.queue = moveEntry(this.queue, entryId, delta);
    this.afterQueueChange();
  }

  clearQueue(): void {
    if (this.ended) return;
    this.touch();
    this.queue = clearQueue(this.queue);
    this.playedVisitEntryId = null;
    this.failures = {};
    this.stopPlayback();
    this.afterQueueChange();
  }

  setMode(mode: PlayMode): void {
    if (this.ended) return;
    this.touch();
    this.queue = setQueueMode(this.queue, mode);
    this.afterQueueChange();
  }

  // ---- 播放命令 ----

  play(): void {
    if (this.ended) return;
    this.gesture();
    // 队列里有条目但没有当前项（移除了末项的当前条目之后）：从第一项开始。
    const current = currentEntry(this.queue) ?? this.queue.entries[0];
    if (current === undefined) return;
    this.touch();
    if (isPlaybackActive(this.status)) return;
    this.acceptTransition(jumpTo(this.queue, current.entryId));
    if (this.currentMedia()?.entryId === current.entryId && this.failures[current.entryId] === undefined) {
      this.fault = null;
      this.requestPlay(this.attempt, current);
      this.commit();
      return;
    }
    this.load(current.entryId, this.resumePosition, "continue");
  }

  pause(): void {
    if (this.ended) return;
    if (this.pipeline.kind === "requesting") {
      // 预检（含等待转换）还在途中：放弃这次加载，停在暂停；服务端的转换在没有等待者之后自行取消。
      this.cancelPreflight();
      this.pipeline = { kind: "stopped", status: "paused" };
      this.attempt += 1;
      this.commit();
      this.persist();
      return;
    }
    if (this.isActive()) {
      this.finishFade();
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
    if (this.ended) return;
    this.pause();
    this.listen.end();
    this.resumePosition = 0;
    if (this.pipeline.kind === "media") this.pipeline = { ...this.pipeline, pendingSeek: null };
    if (this.isCurrentMedia() && this.progress.seekable) {
      this.audio.currentTime = 0;
      this.syncProgress();
    } else this.setProgress({ positionSeconds: 0, durationSeconds: null, seekable: false });
    this.persist();
  }

  next(): void {
    if (this.ended) return;
    this.gesture();
    this.touch();
    const step = advance(this.queue, this.env, { reason: "manual", isSkipped: this.isFailed });
    if (step.entryId === null) return;
    this.acceptTransition(step.state);
    this.load(step.entryId, 0);
  }

  previous(): void {
    if (this.ended) return;
    this.gesture();
    this.touch();
    const step = retreat(this.queue, this.isFailed);
    if (step.entryId === null) return;
    const wasCurrent = step.entryId === this.queue.currentEntryId;
    this.acceptTransition(step.state, this.queue.mode !== "shuffle");
    const media = this.currentMedia();
    if (wasCurrent && media?.entryId === step.entryId) {
      // 已经在队首（或随机模式没有历史）：当前条目从头播放；还没生效的开始位置作废，元数据到达后不能再跳过去。
      // 从头再播是新的一次访问。
      this.pipeline = { ...media, pendingSeek: null };
      this.beginListening(step.entryId);
      this.audio.currentTime = 0;
      this.play();
      return;
    }
    this.load(step.entryId, 0);
  }

  /** 定位到 seconds（秒）。只有时长有限且媒体报告了可定位范围时才生效。 */
  seek(seconds: number): void {
    if (this.ended || !this.isCurrentMedia() || !this.progress.seekable || this.progress.durationSeconds === null) return;
    if (!Number.isFinite(seconds)) return;
    this.finishFade();
    this.audio.currentTime = Math.min(Math.max(0, seconds), this.progress.durationSeconds);
  }

  setVolume(volume: number): void {
    if (this.ended || !Number.isFinite(volume)) return;
    this.volume = Math.min(1, Math.max(0, volume));
    this.afterVolumeChange();
  }

  setMuted(muted: boolean): void {
    if (this.ended) return;
    this.muted = muted;
    this.afterVolumeChange();
  }

  /**
   * 改播放设置（音量均衡、交叉渐变）：保存为设备偏好，立即作用于当前曲目的增益（平滑过去），作废进行中的渐变计划。
   * 两项都关后元素仍保持接入（接入后拆不出来），增益归 1，到重新加载页面才完全退出；打开后从下一次手势命令起接入。
   */
  setPlaybackSettings(patch: Partial<PlaybackSettings>): void {
    if (this.ended) return;
    const next = { ...this.settings, ...patch };
    if (next.leveling === this.settings.leveling && next.crossfade === this.settings.crossfade && next.crossfadeSeconds === this.settings.crossfadeSeconds) return;
    this.settings = next;
    savePlaybackSettings(this.deps.localStorage, next);
    this.cancelCrossfade();
    const current = currentEntry(this.queue);
    if (current !== null) this.applyLeveling(current, this.analyses.get(current.track.trackId) ?? null, true);
    this.commit();
  }

  /** 暂时性故障（网络、服务暂不可用、服务器忙于转换）之后重试：从停下的位置继续。 */
  retry(): void {
    if (this.ended || (this.fault?.kind !== "transient" && this.fault?.kind !== "busy")) return;
    if (this.restorePhase.kind === "retryable") {
      this.restorePhase = { kind: "none" };
      this.fault = null;
      void this.restore();
      return;
    }
    const current = currentEntry(this.queue);
    if (current === null) return;
    this.gesture();
    this.touch();
    this.load(current.entryId, this.resumePosition, "continue");
  }

  // ---- 加载与播放 ----

  /**
   * 预检并开始播放一首曲目；seekTo 是开始位置（秒）。之前的加载与媒体都被放弃。
   * visit 决定计次：默认是新的访问（换曲、同一首从头再播）；播放键与重试从停下的位置继续时是 "continue"，累计保留。
   */
  private load(entryId: string, seekTo: number, visit: "new" | "continue" = "new"): void {
    const entry = this.entryById(entryId);
    if (entry === undefined) return;
    this.cancelPreflight();
    this.cancelPrefetch();
    // 管线先进入请求中（建好中止用的控制器），通知界面之后再发出预检。
    const controller = new AbortController();
    this.unloadAudio(requesting("loading", this.deps.now(), controller));
    if (visit === "continue") this.listen.continueOrBegin(listenTrackOf(entry));
    else this.listen.begin(listenTrackOf(entry));
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
    this.raise({ kind: "track_started", entryId });
    this.afterQueueChange();
    this.requestAnalysis(entry);
    this.sendPreflight(attempt, entry, seekTo, null, controller);
  }

  /** 正在准备时再次发出预检。preparingSince 是这首曲目第一次收到“正在准备”的时刻。 */
  private requestPreflight(attempt: number, entry: QueueEntry, seekTo: number, preparingSince: number | null): void {
    const controller = new AbortController();
    this.pipeline = requesting("preparing", this.deps.now(), controller);
    this.sendPreflight(attempt, entry, seekTo, preparingSince, controller);
  }

  /** 发出预检；管线已是用 controller 中止的请求中。 */
  private sendPreflight(attempt: number, entry: QueueEntry, seekTo: number, preparingSince: number | null, controller: AbortController): void {
    this.deps.loadPlayback(entry.track.trackId, controller.signal).then(
      (result) => this.onPreflightResolved(attempt, entry, result, seekTo, preparingSince),
      (error: unknown) => this.onPreflightRejected(attempt, entry, error, seekTo),
    );
  }

  private onPreflightResolved(attempt: number, entry: QueueEntry, result: TrackPlaybackResult, seekTo: number, preparingSince: number | null): void {
    if (attempt !== this.attempt) return;
    if (result.state === "preparing") {
      this.onPreparing(attempt, entry, seekTo, preparingSince);
      return;
    }
    const playback = result.playback;
    const pipeline = this.pipeline;
    if (pipeline.kind === "requesting" && pipeline.status === "preparing") {
      // 转换完成：接下来是加载音频，不再是“正在准备”。先通知一次，再设置来源。
      this.pipeline = { ...pipeline, status: "loading" };
      this.syncMediaSessionState();
      this.commit();
    }
    const src = playback.representation.url;
    const begin = () => {
      if (attempt !== this.attempt) return;
      this.routeElement(entry);
      this.pipeline = { kind: "media", status: "loading", entryId: entry.entryId, src, pendingSeek: seekTo > 0 ? seekTo : null };
      this.audio.src = src;
      this.requestPlay(attempt, entry);
    };
    const wait = this.analysisWait;
    if (wait === null || wait.entryId !== entry.entryId || wait.result !== undefined) begin();
    else void Promise.race([wait.promise, delay(ANALYSIS_WAIT_MS)]).then(begin);
  }

  /**
   * 服务端还在转换（预检收到 202）：进入“正在准备”并立即再次预检，服务端负责等待，这里不需要退避。
   * 超过上限仍未就绪按转换失败处理；切歌、暂停、停止与会话结束都会使 attempt 失效，循环随之结束。
   */
  private onPreparing(attempt: number, entry: QueueEntry, seekTo: number, preparingSince: number | null): void {
    const pipeline = this.pipeline;
    // attempt 没有失效时管线一定还是这次预检的请求中；这里只为收窄类型。
    if (pipeline.kind !== "requesting") return;
    const since = preparingSince ?? this.deps.now();
    if (this.deps.now() - since > PREPARING_LIMIT_MS) {
      this.failEntry(entry, "derivation_failed");
      return;
    }
    if (pipeline.status !== "preparing") {
      this.pipeline = { ...pipeline, status: "preparing" };
      this.raise({ kind: "preparing", entryId: entry.entryId });
      this.syncMediaSessionState();
      this.commit();
    }
    // 钳制在 [0, 下限]：系统时钟向后跳变时不会等上很久。
    const wait = Math.min(MIN_PREPARING_POLL_MS, MIN_PREPARING_POLL_MS - (this.deps.now() - pipeline.sentAt));
    if (wait <= 0) {
      this.requestPreflight(attempt, entry, seekTo, since);
      return;
    }
    const handle = setTimeout(() => {
      if (attempt === this.attempt) this.requestPreflight(attempt, entry, seekTo, since);
    }, wait);
    this.pipeline = { kind: "requesting", status: "preparing", sentAt: pipeline.sentAt, wait: { kind: "timer", handle } };
  }

  /** 开始出声。已接入的元素要求上下文在 running：先恢复（最多 1 秒），仍不行就中断退回、换直出元素从原位置继续。 */
  private requestPlay(attempt: number, entry: QueueEntry): void {
    const graph = this.graph;
    if (graph !== null && graph.isConnected(this.audio) && !graph.running) {
      void graph.resumeWithin(RESUME_WAIT_MS).then((running) => {
        if (attempt !== this.attempt) return;
        if (running) this.playElement(attempt, entry);
        else this.fallBack(true);
      });
      return;
    }
    this.playElement(attempt, entry);
  }

  private playElement(attempt: number, entry: QueueEntry): void {
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
      this.setMediaStatus("paused");
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
    if (failure?.kind === "http" && failure.status === 401) {
      // 会话边界已被请求层通知；这里只停下，随后卸载会清理一切。
      this.unloadAudio({ kind: "stopped", status: "paused" });
      this.syncMediaSessionState();
      this.commit();
      return;
    }
    const reason = failure === null ? null : failureReasonOf(failure);
    if (reason !== null) {
      this.failEntry(entry, reason);
      return;
    }
    // 服务器忙于转换、网络失败、无效响应、5xx：暂时无法取得音频。停在暂停，保留位置，可以重试，不连续跳歌。
    this.unloadAudio({ kind: "stopped", status: "paused" });
    this.pauseWithFault(isDerivationBusy(failure) ? "busy" : "transient", position);
  }

  /** 停在暂停并保留位置：暂时无法取得音频的几种故障共用，可以重试。 */
  private pauseWithFault(kind: "transient" | "busy", position: number): void {
    this.pipeline = { kind: "stopped", status: "paused" };
    this.resumePosition = position;
    this.fault = { kind };
    this.raise(kind === "busy" ? { kind: "derivation_busy" } : { kind: "transient_failure" });
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
    this.unloadAudio(requesting("loading", this.deps.now(), controller));
    this.setProgress({ positionSeconds: position, durationSeconds: null, seekable: false });
    this.syncMediaSession();
    this.commit();
    let result: TrackPlaybackResult;
    try {
      result = await this.deps.loadPlayback(entry.track.trackId, controller.signal);
    } catch (error) {
      if (attempt !== this.attempt || controller.signal.aborted) return;
      this.onPreflightRejected(attempt, entry, error, position);
      return;
    }
    if (attempt !== this.attempt) return;
    if (result.state === "preparing") {
      // 派生文件在音频请求之前被清走（缓存淘汰）之类：预检重新启动了转换，等它完成后从停下的位置继续。
      this.onPreparing(attempt, entry, position, null);
      return;
    }
    if (mediaErrorCode === MEDIA_ERR_NETWORK) {
      this.pauseWithFault("transient", position);
      return;
    }
    this.failEntry(entry, "decode_failed");
  }

  private failEntry(entry: QueueEntry, reason: PlaybackFailureReason): void {
    this.failures = { ...this.failures, [entry.entryId]: reason };
    this.raise({ kind: "track_failed", entryId: entry.entryId, reason });
    this.unloadAudio({ kind: "stopped", status: "paused" });
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
      this.listen.end(); // 播完了：之后再按播放是从头再播，算新的访问
      this.setMediaStatus("paused");
      this.stopTimers();
      this.syncProgress();
      this.syncMediaSessionState();
      this.commit();
      this.persist();
      return;
    }
    const current = currentEntry(this.queue);
    if (current !== null && step.entryId === current.entryId && this.pipeline.kind === "media" && this.pipeline.entryId === current.entryId) {
      // 单曲循环（或只有一首的循环）：当前条目从头再播，不必重新预检；每一轮是新的访问。
      this.beginListening(current.entryId);
      this.audio.currentTime = 0;
      this.syncProgress();
      this.setMediaStatus("loading");
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
      ["timeupdate", this.onTimeUpdate],
      ["seeking", this.onSeeking],
      ["seeked", this.onSeeked],
      ["progress", this.syncProgress],
      ["canplay", this.syncProgress],
      ["playing", this.onPlaying],
      ["pause", this.onPause],
      // 只有 waiting 表示播放因缺数据停住（随后由 playing 恢复）；stalled 只说明网络暂时没送来数据，
      // 已缓冲的音频照常出声，之后也不保证再有 playing，所以不改变状态。
      ["waiting", this.onWaiting],
      ["ended", this.onEnded],
      ["error", this.onError],
    ];
  }

  /** 为一个音频元素建好媒体事件处理函数（只处理活动元素的事件）；已挂载时立即开始监听。 */
  private bindElement(element: AudioLike): void {
    const listeners = this.mediaEvents().map(([type, handler]): [string, () => void] => [type, () => {
      if (element === this.audio) handler();
    }]);
    this.elementListeners.set(element, listeners);
    if (this.attached) for (const [type, handler] of listeners) element.addEventListener(type, handler);
  }

  /** 不再使用的元素：停止监听并卸载来源。 */
  private discardElement(element: AudioLike): void {
    for (const [type, handler] of this.elementListeners.get(element) ?? []) element.removeEventListener(type, handler);
    this.elementListeners.delete(element);
    element.pause();
    element.removeAttribute("src");
    element.load();
  }

  private newElement(): AudioLike | null {
    const element = this.deps.createAudio?.() ?? null;
    if (element === null) return null;
    element.preload = "metadata";
    this.bindElement(element);
    return element;
  }

  /** 事件是否来自当前加载的媒体：切歌之后迟到的旧事件一律忽略。 */
  private isCurrentMedia(): boolean {
    return this.currentMedia() !== null;
  }

  /** 当前加载的媒体；媒体元素的来源已经不是它（切歌之后）时是 null。 */
  private currentMedia(): MediaPipeline | null {
    const pipeline = this.pipeline;
    return pipeline.kind === "media" && this.audio.getAttribute("src") === pipeline.src ? pipeline : null;
  }

  private readonly onLoadedMetadata = (): void => {
    if (!this.isCurrentMedia()) return;
    this.syncProgress();
  };

  private readonly syncProgress = (): void => {
    const media = this.currentMedia();
    if (media === null) return;
    const duration = Number.isFinite(this.audio.duration) && this.audio.duration > 0 ? this.audio.duration : null;
    this.listen.mediaDuration(duration);
    if (media.pendingSeek !== null && duration !== null && this.audio.seekable.length > 0) {
      const target = Math.min(media.pendingSeek, duration);
      this.pipeline = { ...media, pendingSeek: null };
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
    const media = this.currentMedia();
    if (media === null || this.audio.paused) return;
    const firstPlaying = this.playedVisitEntryId !== media.entryId;
    this.pipeline = { ...media, status: "playing" };
    this.playedVisitEntryId = media.entryId;
    this.fault = null;
    this.resumeListening(media.entryId);
    this.startTimers();
    this.syncMediaSessionState();
    this.commit();
    if (firstPlaying) this.prefetchNext();
  };

  private readonly onPause = (): void => {
    if (!this.isCurrentMedia() || this.audio.ended) return;
    if (!isPlaybackActive(this.status)) return;
    this.cancelCrossfade();
    this.setMediaStatus("paused");
    this.listen.suspend();
    this.stopTimers();
    this.syncMediaSessionState();
    this.commit();
    this.persist();
  };

  private readonly onWaiting = (): void => {
    if (!this.isCurrentMedia() || this.status !== "playing") return;
    this.setMediaStatus("buffering");
    this.listen.suspend();
    this.commit();
  };

  /** 出声期间的进度事件：同步进度，并把位置交给计次；跨过门槛的那一次把这次播放交给记录出口。 */
  private readonly onTimeUpdate = (): void => {
    this.syncProgress();
    if (this.status !== "playing" || !this.isCurrentMedia()) return;
    const record = this.listen.sample(finiteOr(this.audio.currentTime, Number.NaN));
    if (record !== null) this.recordPlay(record);
    this.checkCrossfade();
  };

  /** 开始拖动：拖动跨过的距离不算出声。 */
  private readonly onSeeking = (): void => {
    if (!this.isCurrentMedia()) return;
    this.cancelCrossfade();
    this.listen.seeking();
  };

  private readonly onSeeked = (): void => {
    this.syncProgress();
    if (this.status === "playing" && this.isCurrentMedia()) this.listen.seeked(finiteOr(this.audio.currentTime, Number.NaN));
  };

  private readonly onEnded = (): void => {
    if (!this.isCurrentMedia() || !this.audio.ended) return;
    this.cancelCrossfade();
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

  private get status(): PlayerStatus {
    return this.pipeline.status;
  }

  /** 已加载的媒体换一个状态（出声、暂停、缓冲、从头重播时的加载）；调用处的前置条件保证管线是 media。 */
  private setMediaStatus(status: MediaPipeline["status"]): void {
    if (this.pipeline.kind === "media") this.pipeline = { ...this.pipeline, status };
  }

  private get attached(): boolean {
    return this.lifecycle === "attached";
  }

  /** 会话已结束：引擎不再接受命令，也不再写存储。 */
  private get ended(): boolean {
    return this.lifecycle === "ended";
  }

  private readonly isFailed = (entryId: string): boolean => this.failures[entryId] !== undefined;

  private isActive(): boolean {
    return isPlaybackActive(this.status);
  }

  /** 新的访问：条目从头再播。 */
  private beginListening(entryId: string): void {
    const entry = this.entryById(entryId);
    if (entry !== undefined) this.listen.begin(listenTrackOf(entry));
  }

  /** 开始出声：沿用这个条目进行中的访问，没有（刷新后、播完后再按播放）就开始新的，再以当前位置为基准。 */
  private resumeListening(entryId: string): void {
    const entry = this.entryById(entryId);
    if (entry === undefined) return;
    this.listen.continueOrBegin(listenTrackOf(entry));
    this.listen.resume(finiteOr(this.audio.currentTime, Number.NaN));
  }

  /** 把达到规则的播放交给记录出口；会话结束后不再交出，出口抛出的错误不影响播放。 */
  private recordPlay(record: ListenRecord): void {
    if (this.ended) return;
    try {
      this.deps.playRecorder.record(record);
    } catch {
      // 记录只是信息性的：上报器自己处理失败，这里不让它波及播放。
    }
  }

  private entryById(entryId: string): QueueEntry | undefined {
    return this.queue.entries.find((entry) => entry.entryId === entryId);
  }

  private touch(): void {
    this.touched = true;
    const wasRunning = this.restorePhase.kind === "running";
    this.restorePhase = { kind: "none" };
    if (wasRunning) this.commit();
  }

  /** 中止在途的预检或取消等待下一次预检的定时器；管线的下一个状态由调用方随即给出。 */
  private cancelPreflight(): void {
    const pipeline = this.pipeline;
    if (pipeline.kind !== "requesting") return;
    switch (pipeline.wait.kind) {
      case "inflight":
        pipeline.wait.controller.abort();
        return;
      case "timer":
        clearTimeout(pipeline.wait.handle);
        return;
      default:
        unreachable(pipeline.wait);
    }
  }

  private cancelPrefetch(): void {
    this.prefetch.cancel();
  }

  /** 当前曲目第一次出声之后，为下一首预先发一次预检（见 prefetcher.ts）。 */
  private prefetchNext(): void {
    this.cancelPrefetch();
    if (this.ended) return;
    this.prefetch.start(this.queue);
  }

  /**
   * 暂停并清空媒体元素的来源：中断网络传输，之后不再有旧媒体的事件被当作当前的。next 是之后的管线，
   * 在暂停媒体之前换上（暂停会同步触发 pause 事件）；调用方已经中止了在途的预检。
   */
  private unloadAudio(next: Pipeline): void {
    this.cancelCrossfade();
    this.finishFade();
    this.stopTimers();
    this.listen.suspend();
    this.pipeline = next;
    this.audio.pause();
    this.audio.removeAttribute("src");
    this.audio.load();
  }

  /** 停止当前播放：放弃加载与媒体，状态回到“有当前项的暂停”或空闲。 */
  private stopPlayback(): void {
    this.cancelPreflight();
    this.cancelPrefetch();
    this.attempt += 1;
    this.unloadAudio(stopped(this.queue.currentEntryId));
    this.listen.end();
    this.fault = null;
    this.resumePosition = 0;
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
      restoring: this.restorePhase.kind === "running",
      settings: this.settings,
      leveling: this.graph?.isConnected(this.audio) === true ? this.leveling : null,
      audioRoute: this.fellBack ? "fallback" : this.graph?.isConnected(this.audio) === true ? "web_audio" : "direct",
      volumeControllable: this.volumeControllable,
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
    this.cancelPrefetch();
    this.cancelCrossfade();
    this.syncMediaSession();
    this.commit();
    this.persist();
  }

  /** 用户音量与静音：接入后写总音量节点（元素固定为 1、不静音），没接入时照旧写元素。音量滑块只反映用户音量。 */
  private applyUserVolume(): void {
    if (this.graph?.isConnected(this.audio) === true) {
      this.audio.volume = 1;
      this.audio.muted = false;
      this.graph.setMaster(this.muted ? 0 : this.volume);
      return;
    }
    this.audio.volume = this.volume;
    this.audio.muted = this.muted;
  }

  private afterVolumeChange(): void {
    this.applyUserVolume();
    saveVolume(this.deps.localStorage, { volume: this.volume, muted: this.muted });
    this.commit();
  }

  // ---- 音量均衡与 Web Audio ----

  /** 均衡或渐变开着（需要接入），且设备支持、本页没有退回过。 */
  private wantsGraph(): boolean {
    return this.graph !== null && !this.fellBack && (this.settings.leveling !== "off" || this.settings.crossfade);
  }

  /** 用户手势命令的开头：需要接入时创建并恢复上下文（AudioContext 只能在手势里启动）。 */
  private gesture(): void {
    if (this.wantsGraph()) this.graph?.ensure();
  }

  /** 读取分析结果：完成的记在本地，之后同步可用；读取失败按没有结果处理。 */
  private fetchAnalysis(trackId: string): Promise<TrackAnalysisDTO | null> {
    const cached = this.analyses.get(trackId);
    if (cached !== undefined) return Promise.resolve(cached);
    const load = this.deps.loadAnalysis;
    if (load === undefined) return Promise.resolve(null);
    return load(trackId).then((result) => {
      if (result?.status === "done" && !this.ended) this.analyses.set(trackId, result);
      return result;
    });
  }

  /** 加载一首曲目时，若会接入音频图，同时读取它的分析结果（预检就绪后最多再等 1 秒）。 */
  private requestAnalysis(entry: QueueEntry): void {
    this.analysisWait = null;
    if (!this.wantsGraph() || this.graph?.created !== true || this.analyses.has(entry.track.trackId)) return;
    const wait: NonNullable<PlayerEngine["analysisWait"]> = { entryId: entry.entryId, promise: Promise.resolve(), result: undefined };
    wait.promise = this.fetchAnalysis(entry.track.trackId).then((result) => {
      wait.result = result;
    });
    this.analysisWait = wait;
  }

  /** 设置来源之前：上下文在 running 就接入活动元素，按设置施加均衡与用户音量；不在 running 时这一首原样播放。 */
  private routeElement(entry: QueueEntry): void {
    if (this.wantsGraph() && this.graph?.running === true) this.graph.connect(this.audio);
    this.applyUserVolume();
    const wait = this.analysisWait;
    const analysis = this.analyses.get(entry.track.trackId) ?? (wait?.entryId === entry.entryId ? wait.result ?? null : null);
    this.applyLeveling(entry, analysis, false);
    this.graph?.setFade(this.audio, 1);
  }

  /** 按设置选增益并写到活动元素的均衡节点；没接入时不施加。 */
  private applyLeveling(entry: QueueEntry, analysis: TrackAnalysisDTO | null, smooth: boolean): void {
    if (this.graph?.isConnected(this.audio) !== true) {
      this.leveling = null;
      return;
    }
    const level = chooseLeveling(this.settings.leveling, analysis, inAlbumContext(this.queue, entry.entryId));
    this.graph.setLevel(this.audio, level === null ? 1 : linearGain(level.gainDb), smooth);
    this.leveling = level;
  }

  /** 上下文状态变化：已接入的元素正在出声时被挂起（不是 running），立即中断退回。 */
  private readonly onContextStateChange = (): void => {
    const graph = this.graph;
    if (graph === null || this.fellBack || graph.running || !graph.isConnected(this.audio)) return;
    if (this.pipeline.kind !== "media" || !this.isActive()) return;
    this.fallBack(true);
  };

  /**
   * 中断退回：停止计数，丢掉已接入的元素，换一个不接入的新元素；resume 时从原位置继续（play() 被拒走“请按播放”）。
   * 本页之后不再接入，均衡与渐变失效。
   */
  private fallBack(resume: boolean): void {
    const replacement = this.deps.createAudio?.();
    if (replacement === undefined || this.ended) return;
    const entry = currentEntry(this.queue);
    const position = this.currentPosition();
    this.listen.suspend();
    this.cancelCrossfade();
    this.finishFade();
    this.fellBack = true;
    this.graph?.abandon();
    const old = this.audio;
    replacement.preload = "metadata";
    this.audio = replacement;
    this.bindElement(replacement);
    this.discardElement(old);
    if (this.spare !== null) this.discardElement(this.spare);
    this.spare = null;
    this.leveling = null;
    this.applyUserVolume();
    if (resume && entry !== null) {
      this.load(entry.entryId, position, "continue");
      return;
    }
    this.attempt += 1;
    this.pipeline = stopped(this.queue.currentEntryId);
    this.resumePosition = position;
    this.setProgress({ positionSeconds: position, durationSeconds: null, seekable: false });
    this.syncMediaSessionState();
    this.commit();
  }

  // ---- 交叉渐变 ----

  /** 出声期间每次进度事件调用：接近渐变点时规划，到渐变点时开始。 */
  private checkCrossfade(): void {
    const graph = this.graph;
    const media = this.currentMedia();
    if (!this.settings.crossfade || this.fellBack || graph === null || media === null || this.status !== "playing" || !graph.isConnected(this.audio) || this.fading !== null) return;
    const plan = this.crossfadePlan;
    if (plan !== null && plan.entryId !== media.entryId) this.cancelCrossfade();
    const position = finiteOr(this.audio.currentTime, 0);
    if (this.crossfadePlan === null) {
      this.planCrossfade(media.entryId, position);
      return;
    }
    const current = this.crossfadePlan;
    if (current.phase !== "planned" || position < current.fadeStart) return;
    // 预检还没就绪：放弃本次渐变，照常播完再切。
    if (current.url === null) this.skipCrossfade(current);
    else this.startCrossfade({ ...current, url: current.url });
  }

  /** 到渐变点前 15 秒：判定条件，满足就为下一首发预检与读取分析（暂不接受队列步）。 */
  private planCrossfade(entryId: string, position: number): void {
    const duration = this.progress.durationSeconds;
    const entry = this.entryById(entryId);
    if (duration === null || entry === undefined) return;
    const seconds = this.settings.crossfadeSeconds;
    const trailing = this.analyses.get(entry.track.trackId)?.analysis?.trailingSilenceMs ?? null;
    const fadeStart = (trailing === null ? duration : trailing / 1000) - seconds;
    if (position < fadeStart - CROSSFADE_PLAN_AHEAD_SECONDS) return;
    const skipped: CrossfadePlan = { phase: "skipped", entryId };
    if (this.queue.mode === "repeat_one" || duration < 3 * seconds || position >= fadeStart) {
      this.crossfadePlan = skipped;
      return;
    }
    const step = advance(this.queue, this.env, { reason: "natural", isSkipped: this.isFailed });
    const next = step.entryId === null ? undefined : step.state.entries.find((candidate) => candidate.entryId === step.entryId);
    if (next === undefined || next.entryId === entryId || this.isFailed(next.entryId) || next.track.releaseId === entry.track.releaseId) {
      this.crossfadePlan = skipped;
      return;
    }
    const controller = new AbortController();
    const plan: Extract<CrossfadePlan, { phase: "planned" }> = { phase: "planned", entryId, next, state: step.state, fadeStart, seconds, controller, url: null, analysis: null };
    this.crossfadePlan = plan;
    this.deps.loadPlayback(next.track.trackId, controller.signal).then(
      (result) => {
        if (this.crossfadePlan !== plan) return;
        if (result.state === "ready") plan.url = result.playback.representation.url;
        else this.crossfadePlan = skipped;
      },
      () => {
        if (this.crossfadePlan === plan) this.crossfadePlan = skipped;
      },
    );
    void this.fetchAnalysis(next.track.trackId).then((analysis) => {
      if (this.crossfadePlan === plan) plan.analysis = analysis;
    });
  }

  /** 到渐变点：备用元素接入、设来源、跳过开头静音后开始播放（渐变增益为 0），收到它的 playing 才切换。 */
  private startCrossfade(plan: Extract<CrossfadePlan, { phase: "planned" }> & { url: string }): void {
    const graph = this.graph;
    const spare = this.spare ?? this.newElement();
    if (graph === null || spare === null || !graph.connect(spare)) {
      this.skipCrossfade(plan);
      return;
    }
    this.spare = spare;
    spare.volume = 1;
    spare.muted = false;
    const level = chooseLeveling(this.settings.leveling, plan.analysis, inAlbumContext(plan.state, plan.next.entryId));
    graph.setLevel(spare, level === null ? 1 : linearGain(level.gainDb));
    graph.setFade(spare, 0);
    const leading = plan.analysis?.analysis?.leadingSilenceMs ?? null;
    const offset = leading !== null && leading > 0 && leading <= MAX_LEADING_SKIP_MS ? leading / 1000 : 0;
    const fail = () => {
      if (this.crossfadePlan === starting) this.skipCrossfade(starting);
    };
    const onMetadata = () => {
      if (this.crossfadePlan !== starting) return;
      if (offset > 0 && spare.seekable.length > 0) spare.currentTime = offset;
      try {
        spare.play()?.catch(fail);
      } catch {
        fail();
      }
    };
    const onPlaying = () => {
      if (this.crossfadePlan === starting) this.switchToSpare(starting);
    };
    const detach = () => {
      spare.removeEventListener("loadedmetadata", onMetadata);
      spare.removeEventListener("playing", onPlaying);
      spare.removeEventListener("error", fail);
    };
    const starting: Extract<CrossfadePlan, { phase: "starting" }> = { phase: "starting", entryId: plan.entryId, next: plan.next, state: plan.state, seconds: plan.seconds, controller: plan.controller, url: plan.url, spare, level, detach };
    this.crossfadePlan = starting;
    spare.addEventListener("loadedmetadata", onMetadata);
    spare.addEventListener("playing", onPlaying);
    spare.addEventListener("error", fail);
    spare.src = plan.url;
  }

  /**
   * 切换：接受计划的队列步，活动元素换成备用元素，新曲目开始新的访问（重叠部分只算新曲目），两个渐变增益按等功率曲线交叉。
   * 旧元素在渐变结束（或它先播完）时卸载。
   */
  private switchToSpare(plan: Extract<CrossfadePlan, { phase: "starting" }>): void {
    plan.detach();
    this.crossfadePlan = null;
    const old = this.audio;
    const next = plan.spare;
    this.attempt += 1;
    this.audio = next;
    this.spare = old;
    this.acceptTransition(plan.state);
    this.pipeline = { kind: "media", status: "playing", entryId: plan.next.entryId, src: plan.url, pendingSeek: null };
    this.playedVisitEntryId = plan.next.entryId;
    this.resumePosition = 0;
    this.fault = null;
    this.leveling = plan.level;
    this.listen.begin(listenTrackOf(plan.next));
    this.listen.resume(finiteOr(next.currentTime, Number.NaN));
    this.graph?.crossfade(old, next, plan.seconds);
    const onEnded = () => this.finishFade();
    old.addEventListener("ended", onEnded);
    this.fading = { old, timer: setTimeout(() => this.finishFade(), plan.seconds * 1000), onEnded };
    this.raise({ kind: "track_started", entryId: plan.next.entryId });
    this.syncProgress();
    this.afterQueueChange();
    this.prefetchNext();
  }

  /** 这一次访问不渐变：丢掉计划（备用元素已设来源的一并卸载），照常播完再切。 */
  private skipCrossfade(plan: CrossfadePlan): void {
    this.cancelCrossfade();
    this.crossfadePlan = { phase: "skipped", entryId: plan.entryId };
  }

  /** 作废渐变计划：中止为下一首发出的预检，备用元素已设来源的卸载。 */
  private cancelCrossfade(): void {
    const plan = this.crossfadePlan;
    this.crossfadePlan = null;
    if (plan === null || plan.phase === "skipped") return;
    plan.controller.abort();
    if (plan.phase === "starting") {
      plan.detach();
      plan.spare.pause();
      plan.spare.removeAttribute("src");
      plan.spare.load();
    }
  }

  /** 结束淡出：新元素的渐变增益直接到 1，卸载旧元素。用户在渐变中暂停、拖动、切歌时也立即结束。 */
  private finishFade(): void {
    const fading = this.fading;
    if (fading === null) return;
    this.fading = null;
    clearTimeout(fading.timer);
    fading.old.removeEventListener("ended", fading.onEnded);
    this.graph?.setFade(this.audio, 1);
    fading.old.pause();
    fading.old.removeAttribute("src");
    fading.old.load();
    this.graph?.setFade(fading.old, 1);
  }

  // ---- 保存与恢复 ----

  private currentPosition(): number {
    const media = this.currentMedia();
    if (media === null) return this.resumePosition;
    const position = finiteOr(this.audio.currentTime, 0);
    // 开始位置要等时长与可定位范围都已知才定位；在那之前媒体停在开头，要保存、要重试的是待定位的位置，不是 0。
    // 媒体已经从开头走起来（来源不可定位，定位不会发生）时以实际位置为准。
    return media.pendingSeek !== null && position === 0 ? media.pendingSeek : position;
  }

  private persist(): void {
    // 恢复期间队列还是空的：此时保存会删掉刷新前保存的队列。
    if (this.ended || this.restorePhase.kind !== "none") return;
    saveQueue(this.deps.sessionStorage, this.deps.userId, {
      entries: savedEntriesOf(this.queue.entries),
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
    this.restorePhase = { kind: "running" };
    this.commit();
    const { releases, transientFailure } = await loadSavedReleases(saved.entries, this.deps.loadRelease, () => this.ended || this.touched);
    if (this.ended || this.touched || this.restorePhase.kind !== "running") return;
    if (transientFailure) {
      this.restorePhase = { kind: "retryable" };
      this.fault = { kind: "transient" };
      this.raise({ kind: "transient_failure" });
      this.commit();
      return;
    }

    const restored = rebuildQueue(saved, releases);
    this.playedVisitEntryId = null;
    this.queue = restored.queue;
    this.failures = {};
    this.resumePosition = restored.positionSeconds;
    this.pipeline = stopped(restored.queue.currentEntryId);
    this.restorePhase = { kind: "none" };
    this.setProgress({ positionSeconds: this.resumePosition, durationSeconds: null, seekable: false });
    this.raise({ kind: "queue_restored", restored: restored.queue.entries.length, dropped: restored.dropped });
    this.afterQueueChange();
  }

  // ---- 系统媒体键 ----

  private registerMediaSession(): void {
    this.mediaSession.register({
      play: () => this.play(),
      pause: () => this.pause(),
      previous: () => this.previous(),
      next: () => this.next(),
      stop: () => this.stop(),
      seek: (seconds) => this.seek(seconds),
      position: () => this.progress.positionSeconds,
    });
    this.syncMediaSession();
  }

  private clearMediaSession(): void {
    this.mediaSession.clear();
  }

  private syncMediaSession(): void {
    if (!this.attached) return;
    this.mediaSession.showEntry(currentEntry(this.queue));
    this.syncMediaSessionState();
  }

  private syncMediaSessionState(): void {
    if (!this.attached) return;
    this.mediaSession.showStatus(this.status);
  }

  private syncPositionState(): void {
    if (!this.attached) return;
    this.mediaSession.showPosition(this.progress.positionSeconds, this.progress.durationSeconds);
  }
}
