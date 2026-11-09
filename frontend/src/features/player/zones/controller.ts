// 播放器控制器：界面唯一的入口。命令方法与旧的浏览器引擎同名（playTracks、next、seek……），都发给正在控制的播放区；
// 状态来自 zone store，组装成界面认识的 PlayerSnapshot / PlayerProgress。它还负责本标签页的启动顺序：
// 读快照 → 确认或登记设备（Web Lock 串行）→ 读本机播放区的队列 → 连事件流 → 导入旧队列（R11）→ 排队拿输出锁，拿到的标签页才出声。
// 正在控制的播放区默认是本机；选了别的设备时按 control-target.ts 的规则在它不再可用时回到本机（R4）。媒体键与系统暂停总是发给本机。
import { defaultPeq, peqToWire } from "../peq/document";
import { failureOf, isHttpFailure } from "../../../shared/api";
import type { MediaSessionPort } from "../engine/media-session";
import type { StorageLike } from "../engine/persistence";
import { emptyQueue, MAX_QUEUE_ENTRIES, type PlayerTrack, type QueueState, type TrackDisplayMetadata } from "../engine/queue";
import { MediaSessionBinding, type MediaSessionCommands } from "../output/media-session-binding";
import { OutputEndpoint, type AudioLike } from "../output/output";
import type { ClaimBody, RegisterBody, ReportBody, SettingsPatch, ZoneCommand } from "./api";
import { isUsableTarget, readControlTarget, writeControlTarget, controlTargetStorageKey, type ControlTarget } from "./control-target";
import type { ClaimResult, CommandResult, DeviceResult, RegisterResult, ReportResult, StreamEvent } from "./decoders";
import { deviceStorageKey, readDeviceId, registerBodyOf, withRegistrationLock, writeDeviceId, type LockManagerLike } from "./device";
import { EventStream, type EventStreamDeps } from "./event-stream";
import { importLegacyQueue } from "./legacy-import";
import type { CommandRejection, Device, PlaybackFailureReason, PlaybackSettings, PlayerEvent, PlayerFault, PlayerStatus, Snapshot, Zone, ZoneQueue } from "./model";
import { ZoneStore } from "./zone-store";

export type AddResult = "added" | "queue_full";

export type PlayerSnapshot = {
  status: PlayerStatus;
  queue: QueueState;
  current: QueueState["entries"][number] | null;
  hasNext: boolean;
  /** 已标记为失败的条目（条目 ID → 原因）；自动前进会跳过它们，用户可以手动重试。 */
  failures: Readonly<Record<string, PlaybackFailureReason>>;
  fault: PlayerFault | null;
  event: PlayerEvent | null;
  volume: number;
  muted: boolean;
  /** 还在读取播放区（第一次快照或登记没完成）。 */
  restoring: boolean;
  settings: PlaybackSettings;
  /** 正在控制的设备能否由软件调音量（iPhone / iPad 的网页不能）。 */
  volumeControllable: boolean;
  /** 正在控制的播放区与它的设备；还没读到时是 null。 */
  zone: Zone | null;
  device: Device | null;
  /** 正在控制的是本机播放区。 */
  isLocal: boolean;
  /** 设备列表：本机在前，其余按登记顺序。 */
  devices: readonly PlaybackDeviceRow[];
  /** 最近一条被拒的命令；之后有命令成功或改了控制目标时清掉。 */
  rejection: CommandRejection | null;
};

/** 设备列表的一行：设备、它的播放区（还没读到时为 null）、是不是本机、是不是正在控制的。 */
export type PlaybackDeviceRow = { device: Device; zone: Zone | null; isLocal: boolean; isTarget: boolean };

export type PlayerProgress = {
  positionSeconds: number;
  /** 曲目时长；未知是 null。 */
  durationSeconds: number | null;
  /** 时长已知且服务端允许定位时才能拖动。 */
  seekable: boolean;
};

/** 正在加载、出声或缓冲：都是“要播放”的状态。 */
export function isPlaybackActive(status: PlayerStatus): boolean {
  return status === "playing" || status === "buffering" || status === "loading";
}

/** 控制器用到的接口；默认是真实端点（zones/api.ts），测试可以换成替身。 */
export type ZoneApi = {
  fetchSnapshot: () => Promise<Snapshot>;
  fetchZoneQueue: (zoneId: string) => Promise<ZoneQueue>;
  registerDevice: (body: RegisterBody) => Promise<RegisterResult>;
  sendZoneCommand: (zoneId: string, command: ZoneCommand) => Promise<CommandResult>;
  putDeviceVolume: (deviceId: string, volume: number, muted: boolean) => Promise<DeviceResult>;
  renameDevice: (deviceId: string, name: string) => Promise<DeviceResult>;
  deleteDevice: (deviceId: string) => Promise<void>;
  claimOutput: (deviceId: string, body: ClaimBody) => Promise<ClaimResult>;
  postOutputReport: (sessionId: string, body: ReportBody) => Promise<ReportResult>;
  releaseOutput: (sessionId: string, body: { stream_seq: number | null; stream_seconds: number }) => void;
  openEventStream: (outputSessionId: string | null, signal: AbortSignal) => Promise<Response>;
};

export type PlayerControllerDeps = {
  userId: string;
  api: ZoneApi;
  localStorage: StorageLike | null;
  sessionStorage: StorageLike | null;
  locks: LockManagerLike | null;
  /** 新建音频元素；没有（不能出声的环境）时本标签页只做控制端。 */
  createAudio: (() => AudioLike) | null;
  mediaSession: MediaSessionPort | null;
  /** 封面资源 ID 对应的绝对地址（给系统媒体信息用）。 */
  artworkUrl: (artworkId: string) => string;
  userAgent: string;
  maxTouchPoints: number;
  visibility?: EventStreamDeps["visibility"];
  now: () => number;
  /** 服务端记下一次播放（play_recorded）：让本人的播放历史查询失效。 */
  onPlayRecorded: () => void;
};

const BOOT_RETRY_MS = [1_000, 2_000, 5_000, 10_000] as const;
const PROGRESS_INTERVAL_MS = 250;
const VOLUME_THROTTLE_MS = 125;
const VOLUME_OPTIMISTIC_MS = 1_000;
const TARGET_RECHECK_MS = 60_000;

const defaultSettings: PlaybackSettings = { leveling: "auto", gapless: true, crossfade: false, crossfadeSeconds: 5, quality: "lossless", headroom: { enabled: false, db: -3, clipIndicator: false }, sampleRate: { strategy: "none", customRate: null, filter: "linear_precise" }, downmix: { enabled: true }, peq: defaultPeq };
const idleProgress: PlayerProgress = { positionSeconds: 0, durationSeconds: null, seekable: false };

function settingsPatchOf(patch: Partial<PlaybackSettings>): SettingsPatch {
  const out: SettingsPatch = {};
  if (patch.leveling !== undefined) out.leveling = patch.leveling;
  if (patch.gapless !== undefined) out.gapless = patch.gapless;
  if (patch.crossfade !== undefined) out.crossfade = patch.crossfade;
  if (patch.crossfadeSeconds !== undefined) out.crossfade_seconds = patch.crossfadeSeconds;
  if (patch.quality !== undefined) out.quality = patch.quality;
  if (patch.headroom !== undefined) out.headroom = { enabled: patch.headroom.enabled, db: patch.headroom.db, clip_indicator: patch.headroom.clipIndicator };
  if (patch.sampleRate !== undefined) out.sample_rate = { strategy: patch.sampleRate.strategy, custom_rate: patch.sampleRate.customRate, filter: patch.sampleRate.filter };
  if (patch.downmix !== undefined) out.downmix = { enabled: patch.downmix.enabled };
  if (patch.peq !== undefined) out.peq = peqToWire(patch.peq);
  return out;
}

function delay(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const timer = setTimeout(resolve, ms);
    signal.addEventListener("abort", () => {
      clearTimeout(timer);
      resolve();
    }, { once: true });
  });
}

export class PlayerController {
  private readonly deps: PlayerControllerDeps;
  private readonly store: ZoneStore;
  private readonly events: EventStream;
  private readonly mediaSession: MediaSessionBinding;
  private lifecycle: "fresh" | "attached" | "detached" | "ended" = "fresh";
  private abort: AbortController | null = null;
  private deviceId: string | null = null;
  private ready = false;
  private resolveReady: () => void = () => undefined;
  private readyPromise: Promise<void>;
  private chain: Promise<unknown> = Promise.resolve();
  private output: OutputEndpoint | null = null;
  private releaseOutputLock: (() => void) | null = null;
  private snapshot: PlayerSnapshot;
  private progress: PlayerProgress = idleProgress;
  private progressTimer: ReturnType<typeof setInterval> | null = null;
  /** 拖动的目标位置：revision 是拖动时的状态版本，streamSeq 是那时本机输出端正在放的流（本机不出声时为 null）。 */
  private seekOverride: { zoneId: string; revision: number; seconds: number; streamSeq: number | null } | null = null;
  private pendingVolume: { deviceId: string; volume: number; muted: boolean; until: number } | null = null;
  private volumeTimer: ReturnType<typeof setTimeout> | null = null;
  private volumeDirty = false;
  private alignedZone: Zone | null = null;
  private appliedVolume: { volume: number; muted: boolean } | null = null;
  private readonly listeners = new Set<() => void>();
  private readonly progressListeners = new Set<() => void>();
  private unsubscribeStore: (() => void) | null = null;
  private target: ControlTarget | null;
  private targetTimer: ReturnType<typeof setInterval> | null = null;
  private unsubscribeVisibility: (() => void) | null = null;
  private rejection: CommandRejection | null = null;
  private rejectionSeq = 0;
  private rowsCache: { devices: readonly Device[]; zones: ReadonlyMap<string, Zone>; localId: string | null; targetId: string | undefined; rows: readonly PlaybackDeviceRow[] } | null = null;

  constructor(deps: PlayerControllerDeps) {
    this.deps = deps;
    this.target = readControlTarget(deps.localStorage, deps.userId);
    this.readyPromise = new Promise((resolve) => (this.resolveReady = resolve));
    this.store = new ZoneStore({
      fetchQueue: (zoneId) => deps.api.fetchZoneQueue(zoneId),
      now: deps.now,
      onZoneEvent: (zone) => {
        if (zone.event?.kind === "play_recorded") deps.onPlayRecorded();
      },
    });
    this.events = new EventStream({ open: deps.api.openEventStream, onEvent: this.onStreamEvent, visibility: deps.visibility });
    this.mediaSession = new MediaSessionBinding(deps.mediaSession, deps.artworkUrl, deps.now);
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

  /** 开始：读快照、登记设备、连事件流、排队拿输出锁。可以在 detach 之后再次调用（开发模式下 React 会模拟卸载再挂载）。 */
  attach(): void {
    if (this.lifecycle === "attached" || this.lifecycle === "ended") return;
    this.lifecycle = "attached";
    this.abort = new AbortController();
    this.unsubscribeStore = this.store.subscribe(this.onStoreChange);
    document.addEventListener("pointerdown", this.onFirstGesture, { capture: true, once: true });
    document.addEventListener("keydown", this.onFirstGesture, { capture: true, once: true });
    window.addEventListener("storage", this.onStorage);
    this.targetTimer = setInterval(this.recheckTarget, TARGET_RECHECK_MS);
    this.unsubscribeVisibility = this.deps.visibility?.subscribe(() => {
      if (this.deps.visibility?.hidden() === false) this.recheckTarget();
    }) ?? null;
    void this.boot(this.abort.signal);
  }

  /** 停下：断开事件流，释放输出权并停声。服务端的队列与状态不受影响。 */
  detach(): void {
    if (this.lifecycle !== "attached") return;
    this.lifecycle = "detached";
    this.abort?.abort();
    this.abort = null;
    this.events.stop();
    this.stopOutput();
    this.unsubscribeStore?.();
    this.unsubscribeStore = null;
    document.removeEventListener("pointerdown", this.onFirstGesture, { capture: true });
    document.removeEventListener("keydown", this.onFirstGesture, { capture: true });
    window.removeEventListener("storage", this.onStorage);
    if (this.targetTimer !== null) clearInterval(this.targetTimer);
    this.targetTimer = null;
    this.unsubscribeVisibility?.();
    this.unsubscribeVisibility = null;
    this.stopProgressTimer();
  }

  /** 会话结束（退出、失效、换用户）：停下、清掉界面上这个账号的状态，不再接受命令。存着的设备 ID 保留。 */
  endSession(): void {
    if (this.lifecycle === "ended") return;
    this.detach();
    this.lifecycle = "ended";
    this.store.dispose();
    this.deviceId = null;
    this.rebuild();
    this.progress = idleProgress;
    this.emitProgress();
  }

  private async boot(signal: AbortSignal): Promise<void> {
    for (let attempt = 0; !signal.aborted; attempt += 1) {
      try {
        this.store.applySnapshot(await this.deps.api.fetchSnapshot());
        const deviceId = await this.ensureDevice();
        if (signal.aborted) return;
        this.deviceId = deviceId;
        const zoneId = this.store.device(deviceId)?.zoneId;
        if (zoneId !== undefined) this.store.watchQueue(zoneId);
        this.events.start();
        this.ready = true;
        this.resolveReady();
        this.rebuild();
        void importLegacyQueue(this.deps.sessionStorage, this.deps.userId, (command) => this.send(command));
        this.requestOutput(deviceId, signal);
        return;
      } catch (error) {
        const failure = failureOf(error);
        // 401 由会话边界处理；404 是没有播放区接口的旧后端（design.md §8，不做兼容），都不再重试。
        if (signal.aborted || (failure?.kind === "http" && (failure.status === 401 || failure.status === 404))) return;
        await delay(BOOT_RETRY_MS[Math.min(attempt, BOOT_RETRY_MS.length - 1)] ?? 10_000, signal);
      }
    }
  }

  /** 存着的设备 ID 还在快照里就用它；否则在登记锁里再看一次（别的标签页可能刚登记过），还没有就登记一台。 */
  private async ensureDevice(): Promise<string> {
    const { localStorage, userId } = this.deps;
    const stored = readDeviceId(localStorage, userId);
    if (stored !== null && this.store.device(stored) !== undefined) return stored;
    return withRegistrationLock(this.deps.locks, userId, async () => {
      const again = readDeviceId(localStorage, userId);
      if (again !== null && again !== stored) {
        if (this.store.device(again) === undefined) this.store.applySnapshot(await this.deps.api.fetchSnapshot());
        if (this.store.device(again) !== undefined) return again;
      }
      const result = await this.deps.api.registerDevice(registerBodyOf(localStorage, this.deps.userAgent, this.deps.maxTouchPoints));
      this.store.noteServerTime(result.serverTime);
      this.store.applyDevice(result.device);
      this.store.applyZone(result.zone);
      writeDeviceId(localStorage, userId, result.device.deviceId);
      return result.device.deviceId;
    });
  }

  /** 排队拿本设备的输出锁：立即拿到的是普通认领，排队等到的是接替刚关掉的标签页（takeover）。没有 Web Locks 时直接出声。 */
  private requestOutput(deviceId: string, signal: AbortSignal): void {
    if (this.deps.createAudio === null) return;
    const locks = this.deps.locks;
    if (locks === null) {
      this.startOutput(deviceId, false);
      return;
    }
    const name = `roomusic:output:${deviceId}`;
    const hold = (takeover: boolean) => () => {
      if (signal.aborted || this.deviceId !== deviceId) return Promise.resolve();
      return new Promise<void>((resolve) => {
        this.releaseOutputLock = resolve;
        this.startOutput(deviceId, takeover);
      });
    };
    let queued = false;
    locks.request(name, { ifAvailable: true }, (lock) => {
      if (lock === null) {
        queued = true;
        return Promise.resolve();
      }
      return hold(false)();
    }).then(() => {
      if (queued && !signal.aborted) return locks.request(name, { signal }, hold(true));
      return undefined;
    }).catch(() => {
      // 中止排队（卸载）时 request 以 AbortError 结束。
    });
  }

  private startOutput(deviceId: string, takeover: boolean): void {
    const createAudio = this.deps.createAudio;
    if (createAudio === null) return;
    const api = this.deps.api;
    const output = new OutputEndpoint({
      claim: (body) => api.claimOutput(deviceId, body),
      report: (sessionId, body) => api.postOutputReport(sessionId, body),
      release: (sessionId, body) => api.releaseOutput(sessionId, body),
      onResult: (result) => {
        this.store.noteServerTime(result.serverTime);
        this.store.applyDevice(result.device);
        this.store.applyZone(result.zone);
      },
      onSession: (sessionId) => this.events.setOutputSession(sessionId),
      onDeviceMissing: () => this.onDeviceMissing(deviceId),
      onSuperseded: () => this.stopOutput(),
      createAudio,
      mediaSession: this.mediaSession,
      commands: this.localCommands,
      now: this.deps.now,
      onTime: () => this.updateProgress(),
    });
    this.output = output;
    this.alignedZone = null;
    this.appliedVolume = null;
    output.start(takeover);
    this.syncOutput();
    this.rebuild();
  }

  private stopOutput(): void {
    this.output?.stop();
    this.output = null;
    this.events.setOutputSession(null);
    this.releaseOutputLock?.();
    this.releaseOutputLock = null;
  }

  /** 设备被清理或删除（认领 404、device_removed）：重新登记一台，再拿输出锁。 */
  private onDeviceMissing(deviceId: string): void {
    if (this.deviceId !== deviceId || this.lifecycle !== "attached") return;
    this.stopOutput();
    this.deviceId = null;
    if (readDeviceId(this.deps.localStorage, this.deps.userId) === deviceId) {
      try {
        this.deps.localStorage?.removeItem(deviceStorageKey(this.deps.userId));
      } catch {
        // 存储不可用：下面照样登记。
      }
    }
    const signal = this.abort?.signal;
    if (signal === undefined) return;
    void this.ensureDevice().then((id) => {
      if (signal.aborted) return;
      this.deviceId = id;
      const zoneId = this.store.device(id)?.zoneId;
      if (zoneId !== undefined) this.store.watchQueue(zoneId);
      this.rebuild();
      this.requestOutput(id, signal);
    }, () => undefined);
  }

  private readonly onFirstGesture = (): void => {
    this.output?.unlock();
  };

  private readonly onStreamEvent = (event: StreamEvent, session: string | null): void => {
    switch (event.type) {
      case "snapshot": {
        // 带着本标签页的输出会话连上，快照却说设备不在线或会话不是它：会话已失效（服务重启）。
        // 先重新认领（此时取正在放的条目与位置），再应用快照；认领前输出端不对齐，不会因快照里的 stream: null 卸载媒体。
        const output = this.output;
        const device = event.snapshot.devices.find((item) => item.deviceId === this.deviceId);
        if (output !== null && session !== null && session === output.session && device !== undefined && (!device.online || device.outputSessionId !== session)) output.reclaim();
        this.store.applySnapshot(event.snapshot);
        return;
      }
      case "zone":
        this.store.applyZone(event.zone);
        return;
      case "queue":
        this.store.applyQueueChange(event.zoneId, event.change);
        return;
      case "device":
        this.store.applyDevice(event.device);
        return;
      case "device_removed":
        this.store.removeDevice(event.deviceId, event.zoneId);
        if (event.deviceId === this.deviceId) this.onDeviceMissing(event.deviceId);
        return;
    }
  };

  private readonly onStoreChange = (): void => {
    this.syncOutput();
    this.rebuild();
  };

  /** 把本机播放区的最新状态交给输出端对齐，设备音量写进元素（拖动后 1 秒内以本地值为准）。 */
  private syncOutput(): void {
    const output = this.output;
    const zone = this.localZone();
    if (output === null || zone === undefined) return;
    if (zone !== this.alignedZone) {
      this.alignedZone = zone;
      output.align(zone);
    }
    const device = this.localDevice();
    if (device === undefined) return;
    const volume = this.effectiveVolume(device);
    if (this.appliedVolume?.volume !== volume.volume || this.appliedVolume.muted !== volume.muted) {
      this.appliedVolume = volume;
      output.setVolume(volume.volume, volume.muted);
    }
  }

  // ---- 队列命令 ----

  /** 立即播放：用 tracks 替换队列并从 startIndex 开始播放。 */
  playTracks(tracks: readonly PlayerTrack[], startIndex = 0): AddResult {
    if (this.lifecycle === "ended" || tracks.length > MAX_QUEUE_ENTRIES) return "queue_full";
    if (tracks.length === 0) return "added";
    this.gesture();
    void this.send({ command: "play_tracks", track_ids: tracks.map((track) => track.trackId), start_index: startIndex });
    return "added";
  }

  /** 下一首播放：插在当前条目之后，不打断当前播放。超出上限时整批拒绝。 */
  playNext(tracks: readonly PlayerTrack[]): AddResult {
    return this.add("play_next", tracks);
  }

  /** 加入队列：追加到队尾，不打断当前播放。 */
  enqueue(tracks: readonly PlayerTrack[]): AddResult {
    return this.add("enqueue", tracks);
  }

  private add(command: "play_next" | "enqueue", tracks: readonly PlayerTrack[]): AddResult {
    if (this.lifecycle === "ended") return "queue_full";
    if (tracks.length === 0) return "added";
    if ((this.snapshot.zone?.queueLength ?? 0) + tracks.length > MAX_QUEUE_ENTRIES) return "queue_full";
    void this.send({ command, track_ids: tracks.map((track) => track.trackId) });
    return "added";
  }

  /** 播放队列里的某一项（“播放此项”）。 */
  playEntry(entryId: string): void {
    this.gesture();
    void this.send({ command: "play_entry", entry_id: entryId });
  }

  removeEntry(entryId: string): void {
    void this.send({ command: "remove_entry", entry_id: entryId });
  }

  moveEntry(entryId: string, delta: -1 | 1): void {
    void this.send({ command: "move_entry", entry_id: entryId, delta });
  }

  clearQueue(): void {
    void this.send({ command: "clear_queue" });
  }

  setMode(mode: QueueState["mode"]): void {
    void this.send({ command: "set_mode", mode });
  }

  /** 刷新已在队列里的曲目的显示元数据（艺人治理之后）：只改显示，不改顺序、当前项与播放。 */
  updateTrackMetadata(patches: ReadonlyMap<string, TrackDisplayMetadata>): void {
    if (this.lifecycle === "ended") return;
    this.store.patchTrackMetadata(patches);
  }

  // ---- 播放命令 ----

  play(): void {
    this.gesture();
    void this.send({ command: "play" });
  }

  pause(): void {
    void this.send({ command: "pause" });
  }

  togglePlayback(): void {
    if (this.snapshot.zone?.intent === "play") this.pause();
    else this.play();
  }

  /** 停止：暂停并回到曲目开头（系统媒体键的“停止”）。 */
  stop(): void {
    void this.send({ command: "stop" });
  }

  next(): void {
    this.gesture();
    void this.send({ command: "next" });
  }

  previous(): void {
    this.gesture();
    void this.send({ command: "previous" });
  }

  /** 定位到 seconds（秒）；进度条先显示目标位置，等服务端的新状态。 */
  seek(seconds: number): void {
    const zone = this.snapshot.zone;
    if (this.lifecycle === "ended" || zone === null || !Number.isFinite(seconds) || !this.progress.seekable) return;
    const duration = zone.position.durationSeconds;
    const target = Math.max(0, duration === null ? seconds : Math.min(seconds, duration));
    this.seekOverride = { zoneId: zone.zoneId, revision: zone.revision, seconds: target, streamSeq: this.snapshot.isLocal ? (this.output?.trackPosition()?.streamSeq ?? null) : null };
    this.updateProgress();
    void this.send({ command: "seek", position_seconds: target });
  }

  /** 暂时性故障（渲染器不可用或忙）之后重试：从停下的位置继续。 */
  retry(): void {
    this.gesture();
    void this.send({ command: "retry" });
  }

  setPlaybackSettings(patch: Partial<PlaybackSettings>): void {
    const settings = settingsPatchOf(patch);
    if (Object.keys(settings).length === 0) return;
    void this.send({ command: "set_settings", settings });
  }

  setVolume(volume: number): void {
    if (!Number.isFinite(volume)) return;
    this.changeVolume(Math.min(1, Math.max(0, volume)), this.snapshot.muted);
  }

  setMuted(muted: boolean): void {
    this.changeVolume(this.snapshot.volume, muted);
  }

  /** 音量属于设备：本机先立即作用于元素，再按 125 ms 节流发给服务端；1 秒内以本地值显示。 */
  private changeVolume(volume: number, muted: boolean): void {
    const device = this.snapshot.device;
    if (this.lifecycle === "ended" || device === null || !device.volumeControllable) return;
    this.pendingVolume = { deviceId: device.deviceId, volume, muted, until: this.deps.now() + VOLUME_OPTIMISTIC_MS };
    this.syncOutput();
    this.rebuild();
    if (this.volumeTimer !== null) {
      this.volumeDirty = true;
      return;
    }
    this.flushVolume();
  }

  private flushVolume(): void {
    const pending = this.pendingVolume;
    if (pending === null) return;
    this.volumeDirty = false;
    this.deps.api.putDeviceVolume(pending.deviceId, pending.volume, pending.muted).then((result) => {
      this.store.noteServerTime(result.serverTime);
      this.store.applyDevice(result.device);
    }, () => undefined);
    this.volumeTimer = setTimeout(() => {
      this.volumeTimer = null;
      if (this.volumeDirty) this.flushVolume();
      // 乐观显示到期后回到服务端的值。
      setTimeout(() => this.rebuild(), VOLUME_OPTIMISTIC_MS);
    }, VOLUME_THROTTLE_MS);
  }

  private effectiveVolume(device: Device): { volume: number; muted: boolean } {
    const pending = this.pendingVolume;
    if (pending !== null && pending.deviceId === device.deviceId && pending.until > this.deps.now()) return { volume: pending.volume, muted: pending.muted };
    return { volume: device.volume, muted: device.muted };
  }

  // ---- 设备与控制目标 ----

  /** 改为控制这个播放区；选本机即清掉存的目标。 */
  setControlTarget(zoneId: string): void {
    if (this.lifecycle === "ended") return;
    const local = this.localDevice()?.zoneId;
    this.setTarget(zoneId === local ? null : { zoneId, selectedAt: this.deps.now() });
  }

  /**
   * 把 sourceZoneId 的播放整体转到 targetZoneId（design.md §5.4）：目标从原位置直接播放，源清空。成功后控制目标跟到目标。
   * 失败（例如目标刚离线）时 promise 带着错误，界面据此提示。
   */
  async transfer(sourceZoneId: string, targetZoneId: string): Promise<void> {
    if (this.lifecycle === "ended") return;
    if (targetZoneId === this.localDevice()?.zoneId) this.output?.unlock();
    // 转移失败由设备列表就近提示，不记成被拒的命令。
    await this.enqueueCommand(() => sourceZoneId, { command: "transfer", target_zone_id: targetZoneId }, false);
    this.setControlTarget(targetZoneId);
  }

  async renameDevice(deviceId: string, name: string): Promise<void> {
    if (this.lifecycle === "ended") return;
    const result = await this.deps.api.renameDevice(deviceId, name);
    if (this.isEnded()) return;
    this.store.noteServerTime(result.serverTime);
    this.store.applyDevice(result.device);
  }

  /** 删除离线设备，连同它的播放区与队列。 */
  async deleteDevice(deviceId: string): Promise<void> {
    if (this.lifecycle === "ended") return;
    const zoneId = this.store.device(deviceId)?.zoneId;
    await this.deps.api.deleteDevice(deviceId);
    if (!this.isEnded() && zoneId !== undefined) this.store.removeDevice(deviceId, zoneId);
  }

  private setTarget(target: ControlTarget | null): void {
    this.target = target;
    this.rejection = null;
    writeControlTarget(this.deps.localStorage, this.deps.userId, target);
    this.rebuild();
  }

  /** 别的标签页改了控制目标。 */
  private readonly onStorage = (event: StorageEvent): void => {
    if (event.key !== controlTargetStorageKey(this.deps.userId)) return;
    this.target = readControlTarget(this.deps.localStorage, this.deps.userId);
    this.rebuild();
  };

  /** 每分钟与页面重新可见时：目标不再可用就回到本机。 */
  private readonly recheckTarget = (): void => {
    if (this.target !== null && !this.targetUsable(this.target)) this.rebuild();
  };

  private targetUsable(target: ControlTarget): boolean {
    const zone = this.store.zone(target.zoneId);
    const device = zone === undefined ? undefined : this.store.device(zone.deviceId);
    return isUsableTarget(target, zone, device, this.store.serverNow(), this.deps.now());
  }

  /** 读完快照后，存的目标不再可用（或就是本机）时清掉，此后控制本机。 */
  private settleTarget(): void {
    const target = this.target;
    if (!this.ready || target === null) return;
    if (target.zoneId !== this.localDevice()?.zoneId && this.targetUsable(target)) return;
    this.target = null;
    writeControlTarget(this.deps.localStorage, this.deps.userId, null);
  }

  /** 播放类命令的点击：本机是输出端时在这次点击里解锁元素、直接 play() 暂停中的当前流。 */
  private gesture(): void {
    if (this.isControllingLocal()) this.output?.unlock(true);
  }

  /** 媒体键与系统暂停：总是发给本机播放区。 */
  private readonly localCommands: MediaSessionCommands = {
    play: () => void this.sendTo(this.localZone()?.zoneId, { command: "play" }),
    pause: () => void this.sendTo(this.localZone()?.zoneId, { command: "pause" }),
    previous: () => void this.sendTo(this.localZone()?.zoneId, { command: "previous" }),
    next: () => void this.sendTo(this.localZone()?.zoneId, { command: "next" }),
    stop: () => void this.sendTo(this.localZone()?.zoneId, { command: "stop" }),
    seek: (seconds) => void this.sendTo(this.localZone()?.zoneId, { command: "seek", position_seconds: Math.max(0, seconds) }),
    position: () => this.output?.trackPosition()?.seconds ?? 0,
  };

  /** 发给正在控制的播放区；命令按顺序一条一条发，不自动重试。 */
  private send(command: ZoneCommand): Promise<void> {
    if (this.lifecycle === "ended") return Promise.resolve();
    return this.enqueueCommand(() => this.controlledZoneId(), command);
  }

  private sendTo(zoneId: string | undefined, command: ZoneCommand): Promise<void> {
    if (zoneId === undefined || this.lifecycle === "ended") return Promise.resolve();
    return this.enqueueCommand(() => zoneId, command);
  }

  private enqueueCommand(target: () => string | undefined, command: ZoneCommand, noteRejection = true): Promise<void> {
    const run = this.chain.then(() => this.readyPromise).then(async () => {
      const zoneId = target();
      if (zoneId === undefined || this.lifecycle === "ended") return;
      let result: CommandResult;
      try {
        result = await this.deps.api.sendZoneCommand(zoneId, command);
      } catch (error) {
        if (noteRejection && !this.isEnded() && isHttpFailure(failureOf(error), 409, "zone_offline")) {
          this.rejectionSeq += 1;
          this.rejection = { seq: this.rejectionSeq, reason: "zone_offline" };
          this.rebuild();
        }
        throw error;
      }
      if (this.isEnded()) return;
      const cleared = this.rejection !== null;
      this.rejection = null;
      this.store.applyCommandResult(zoneId, result);
      // 服务端状态可能已经从事件流到过、store 不再通知，提示要自己清掉。
      if (cleared) this.rebuild();
    });
    // 命令失败不中断后面的命令；import 需要知道结果，所以返回的 promise 带着错误。
    this.chain = run.catch(() => undefined);
    return run;
  }

  // ---- 状态 ----

  private isEnded(): boolean {
    return this.lifecycle === "ended";
  }

  private localDevice(): Device | undefined {
    return this.deviceId === null ? undefined : this.store.device(this.deviceId);
  }

  private localZone(): Zone | undefined {
    const device = this.localDevice();
    return device === undefined ? undefined : this.store.zone(device.zoneId);
  }

  /** 正在控制的播放区：可用的控制目标，否则是本机播放区。 */
  private controlledZoneId(): string | undefined {
    const target = this.target;
    if (target !== null && this.targetUsable(target)) return target.zoneId;
    return this.localDevice()?.zoneId;
  }

  /** 设备列表的行：输入没变时沿用上一次的数组，订阅它的组件不因无关变化重绘。 */
  private deviceRows(targetId: string | undefined): readonly PlaybackDeviceRow[] {
    const { devices, zones } = this.store.getState();
    const localId = this.deviceId;
    const cache = this.rowsCache;
    if (cache !== null && cache.devices === devices && cache.zones === zones && cache.localId === localId && cache.targetId === targetId) return cache.rows;
    const rows = devices.map((device) => ({ device, zone: zones.get(device.zoneId) ?? null, isLocal: device.deviceId === localId, isTarget: device.zoneId === targetId }));
    rows.sort((a, b) => Number(b.isLocal) - Number(a.isLocal));
    this.rowsCache = { devices, zones, localId, targetId, rows };
    return rows;
  }

  private isControllingLocal(): boolean {
    const local = this.localDevice()?.zoneId;
    return local !== undefined && local === this.controlledZoneId();
  }

  private buildSnapshot(): PlayerSnapshot {
    const zoneId = this.controlledZoneId();
    const zone = zoneId === undefined ? null : (this.store.zone(zoneId) ?? null);
    const device = zone === null ? null : (this.store.device(zone.deviceId) ?? null);
    const entries = zoneId === undefined ? emptyQueue.entries : (this.store.getState().queues.get(zoneId)?.entries ?? emptyQueue.entries);
    const previous = this.snapshot as PlayerSnapshot | undefined;
    const currentEntryId = zone?.current?.entryId ?? null;
    const mode = zone?.mode ?? "sequential";
    const queue = previous !== undefined && previous.queue.entries === entries && previous.queue.currentEntryId === currentEntryId && previous.queue.mode === mode ? previous.queue : { entries, currentEntryId, mode };
    const volume = device === null ? { volume: 1, muted: false } : this.effectiveVolume(device);
    return {
      status: zone === null || zone.current === null ? "idle" : zone.status,
      queue,
      current: zone?.current ?? null,
      hasNext: zone?.hasNext ?? false,
      failures: zone?.failures ?? {},
      fault: zone?.fault ?? null,
      event: zone !== null && this.store.isFreshEvent(zone) ? zone.event : null,
      volume: volume.volume,
      muted: volume.muted,
      restoring: !this.ready,
      settings: zone?.settings ?? defaultSettings,
      volumeControllable: device?.volumeControllable ?? true,
      zone,
      device,
      isLocal: zone !== null && zone.deviceId === this.deviceId,
      devices: this.deviceRows(zoneId),
      rejection: this.rejection,
    };
  }

  private rebuild(): void {
    this.settleTarget();
    const zoneId = this.controlledZoneId();
    if (this.ready && zoneId !== undefined) this.store.watchQueue(zoneId);
    this.snapshot = this.buildSnapshot();
    for (const listener of [...this.listeners]) listener();
    this.updateProgress();
    const zone = this.snapshot.zone;
    if (zone !== null && (zone.status === "playing" || zone.status === "buffering") && this.lifecycle === "attached") this.startProgressTimer();
    else this.stopProgressTimer();
  }

  /** 进度：本机出声时用元素的流内位置按分段换算（更准），否则按服务端的位置与时间戳外推。 */
  private computeProgress(): PlayerProgress {
    const zone = this.snapshot.zone;
    if (zone === null || zone.current === null) return idleProgress;
    const duration = zone.position.durationSeconds;
    const seekable = zone.position.seekable && duration !== null;
    const clamp = (seconds: number) => Math.max(0, duration === null ? seconds : Math.min(seconds, duration));
    const local = this.snapshot.isLocal ? this.output?.trackPosition() : null;
    // 拖动之后先显示目标位置：远端直到服务端的新状态到达；本机出声时直到输出端换上新流（旧流的播放头还停在原处）。
    const override = this.seekOverride;
    const pending = override !== null && override.zoneId === zone.zoneId
      && (override.revision === zone.revision || (override.streamSeq !== null && local?.streamSeq === override.streamSeq));
    if (override !== null && pending) return { positionSeconds: override.seconds, durationSeconds: duration, seekable };
    this.seekOverride = null;
    if (local !== null && local !== undefined && local.entryId === zone.current.entryId) return { positionSeconds: clamp(local.seconds), durationSeconds: duration ?? local.durationSeconds, seekable };
    const elapsed = zone.status === "playing" ? (this.store.serverNow() - zone.position.at) / 1000 : 0;
    return { positionSeconds: clamp(zone.position.seconds + Math.max(0, elapsed)), durationSeconds: duration, seekable };
  }

  private updateProgress(): void {
    const next = this.computeProgress();
    const current = this.progress;
    if (Math.abs(next.positionSeconds - current.positionSeconds) < 0.05 && next.durationSeconds === current.durationSeconds && next.seekable === current.seekable) return;
    this.progress = next;
    this.emitProgress();
  }

  private emitProgress(): void {
    for (const listener of [...this.progressListeners]) listener();
  }

  private startProgressTimer(): void {
    if (this.progressTimer !== null) return;
    this.progressTimer = setInterval(() => this.updateProgress(), PROGRESS_INTERVAL_MS);
  }

  private stopProgressTimer(): void {
    if (this.progressTimer !== null) clearInterval(this.progressTimer);
    this.progressTimer = null;
  }
}
