// 输出端（design-protocol.md §3）：本标签页持有输出权时，用两个 <audio> 元素播放服务端渲染流，把流内位置回报给服务端。
// 换曲、电平化、无缝、淡化都在服务端完成；这里只“对齐到期望状态”（§3.1）：当前流、接续流、播放意图与设备音量。
// 漏掉、重复、乱序的状态都不会让输出端做错事：每次拿到状态（事件、命令响应或回报响应）都对齐一次，revision 旧的丢弃。
// 系统接口也在这里：锁屏信息按流的分段在曲目边界本地切换，媒体键发命令给本机播放区，第一次点击时同步解锁两个元素。
import { failureOf } from "../../../shared/api";
import { errorName } from "../engine/playback-failure";
import type { ClaimBody, ReportBody } from "../zones/api";
import type { ClaimResult, ReportResult } from "../zones/decoders";
import type { Capabilities, StreamSegment, Zone, ZoneStream } from "../zones/model";
import { browserCapabilities } from "./capabilities";
import { MediaSessionBinding, type MediaSessionCommands } from "./media-session-binding";
import { ReportQueue } from "./report-queue";

/** 输出端用到的音频元素能力；HTMLAudioElement 满足它，测试用替身。 */
export type AudioLike = {
  src: string;
  currentTime: number;
  readonly paused: boolean;
  readonly ended: boolean;
  volume: number;
  muted: boolean;
  preload: string;
  canPlayType: (type: string) => string;
  play: () => Promise<void> | undefined;
  pause: () => void;
  load: () => void;
  getAttribute: (name: "src") => string | null;
  removeAttribute: (name: "src") => void;
  addEventListener: (type: string, listener: () => void) => void;
  removeEventListener: (type: string, listener: () => void) => void;
};

/** 播放中每隔多久回报一次（也是心跳）。 */
const STATUS_INTERVAL_MS = 10_000;
/** 一直缓冲多久按断流处理。 */
const STALL_LIMIT_MS = 15_000;
/** 认领暂时失败后多久再试。 */
const CLAIM_RETRY_MS = 5_000;

/** 0.1 秒的 8 kHz 单声道静音 WAV：手势解锁时在点击里同步 play() 一下。 */
const SILENCE = (() => {
  const samples = 800;
  const bytes = new Uint8Array(44 + samples);
  const view = new DataView(bytes.buffer);
  const text = (at: number, value: string) => [...value].forEach((char, index) => view.setUint8(at + index, char.charCodeAt(0)));
  text(0, "RIFF");
  view.setUint32(4, 36 + samples, true);
  text(8, "WAVEfmt ");
  view.setUint32(16, 16, true);
  view.setUint16(20, 1, true);
  view.setUint16(22, 1, true);
  view.setUint32(24, 8000, true);
  view.setUint32(28, 8000, true);
  view.setUint16(32, 1, true);
  view.setUint16(34, 8, true);
  text(36, "data");
  view.setUint32(40, samples, true);
  bytes.fill(128, 44);
  return `data:audio/wav;base64,${btoa(String.fromCharCode(...bytes))}`;
})();

type Phase = "loading" | "playing" | "paused" | "buffering";

/** 一个元素与它放着的流。idle：空着；active：正在放（或暂停在）当前流；switch：正在打开新的当前流，出声后换上；next：预载的接续流。 */
/** done：活动流已经放完或出错（起始条目打不开的空流会立刻这样），之后才出现的接续流要直接换上。 */
type Slot = { el: AudioLike; stream: ZoneStream | null; role: "idle" | "active" | "switch" | "next"; phase: Phase; unlocked: boolean; ownPause: boolean; done: boolean };

export type OutputDeps = {
  claim: (body: ClaimBody) => Promise<ClaimResult>;
  report: (sessionId: string, body: ReportBody) => Promise<ReportResult>;
  release: (sessionId: string, body: { stream_seq: number | null; stream_seconds: number }) => void;
  /** 认领与回报的响应：交给 store（之后 store 的变化会回到 align）。 */
  onResult: (result: ClaimResult | ReportResult) => void;
  /** 输出会话变化（事件流据此带上 output_session）。 */
  onSession: (sessionId: string | null) => void;
  /** 认领得到 404：设备被清理或删除了，要重新登记。 */
  onDeviceMissing: () => void;
  /** 会话被同一设备的新认领取代：本标签页停声。 */
  onSuperseded: () => void;
  createAudio: () => AudioLike;
  mediaSession: MediaSessionBinding;
  /** 媒体键与系统暂停发给本机播放区的命令。 */
  commands: MediaSessionCommands;
  /** 流内位置变了（timeupdate、换流）：控制器据此立即刷新进度。 */
  onTime: () => void;
  now: () => number;
};

export class OutputEndpoint {
  private readonly deps: OutputDeps;
  private readonly capabilities: Capabilities;
  private active: Slot;
  private spare: Slot;
  private sessionId: string | null = null;
  private running = false;
  private claiming = false;
  private zone: Zone | null = null;
  private lastRevision = -1;
  /** 已经放完的流：服务端还没跟上时，它的状态里仍可能是这一条，不能重新打开。 */
  private readonly endedSeqs = new Set<number>();
  private volume = { volume: 1, muted: false };
  private lastStatusAt = Number.NEGATIVE_INFINITY;
  private stallTimer: ReturnType<typeof setTimeout> | null = null;
  private claimTimer: ReturnType<typeof setTimeout> | null = null;
  private shownEntryId: string | null | undefined = undefined;
  private readonly reports: ReportQueue<ReportResult>;
  private readonly listeners = new Map<AudioLike, [string, () => void][]>();

  constructor(deps: OutputDeps) {
    this.deps = deps;
    this.active = this.slot(deps.createAudio());
    this.spare = this.slot(deps.createAudio());
    this.capabilities = browserCapabilities(this.active.el);
    this.reports = new ReportQueue((body) => this.postReport(body), (result) => deps.onResult(result), (error) => this.onReportError(error));
  }

  get session(): string | null {
    return this.sessionId;
  }

  /** 认领输出权并开始出声。takeover：排队拿到输出锁的标签页接替刚释放的会话。 */
  start(takeover: boolean): void {
    if (this.running) return;
    this.running = true;
    window.addEventListener("pagehide", this.onPageHide);
    window.addEventListener("pageshow", this.onPageShow);
    this.deps.mediaSession.register(this.deps.commands);
    void this.claim(takeover);
  }

  /** 释放输出权、停声、清掉锁屏信息（会话结束、普通卸载、被取代）。 */
  stop(): void {
    if (!this.running) return;
    this.running = false;
    window.removeEventListener("pagehide", this.onPageHide);
    window.removeEventListener("pageshow", this.onPageShow);
    if (this.claimTimer !== null) clearTimeout(this.claimTimer);
    this.claimTimer = null;
    this.releaseSession();
    this.unload(this.active);
    this.unload(this.spare);
    this.clearStall();
    this.deps.mediaSession.clear();
    this.shownEntryId = undefined;
  }

  /** 设备音量：直接写两个元素（没有 Web Audio）。 */
  setVolume(volume: number, muted: boolean): void {
    this.volume = { volume, muted };
    for (const slot of [this.active, this.spare]) {
      slot.el.volume = volume;
      slot.el.muted = muted;
    }
  }

  /**
   * 手势解锁（合同 §8）：在点击里对空着的元素同步 play() 一段静音再暂停，之后远程起播、换流、续流不需要新的手势。
   * playNow：用户在本机按了播放类按钮，正在暂停的当前流也在这次点击里直接 play()，不等服务端的响应。
   */
  unlock(playNow = false): void {
    if (!this.running) return;
    for (const slot of [this.active, this.spare]) {
      if (slot.role !== "idle" || slot.unlocked) continue;
      slot.unlocked = true;
      const el = slot.el;
      el.src = SILENCE;
      const reset = () => {
        if (el.getAttribute("src") !== SILENCE) return;
        el.pause();
        el.removeAttribute("src");
        el.load();
      };
      el.play()?.then(reset, reset);
    }
    if (playNow && this.active.role === "active" && this.active.stream !== null && this.active.el.paused && !this.active.done && !this.active.el.ended) this.playSlot(this.active);
  }

  /** 曲目内的位置：按分段与流内位置换算；没有在放的流时是 null。 */
  trackPosition(): { entryId: string; seconds: number; durationSeconds: number | null } | null {
    const segment = this.segment();
    if (segment === null) return null;
    return { entryId: segment.entryId, seconds: Math.max(0, segment.trackStartSeconds + this.active.el.currentTime - segment.streamStartSeconds), durationSeconds: segment.track.durationSeconds };
  }

  /** 对齐到播放区的期望状态（design-protocol.md §3.1）。认领成功之前不对齐：流只给当前输出会话。 */
  align(zone: Zone): void {
    if (!this.running || this.sessionId === null || zone.revision < this.lastRevision) return;
    this.lastRevision = zone.revision;
    this.zone = zone;
    this.reconcile();
  }

  private reconcile(): void {
    const zone = this.zone;
    if (zone === null) return;
    const stream = zone.stream;
    if (stream === null) {
      this.unload(this.active);
      this.unload(this.spare);
    } else if (this.active.stream?.seq === stream.seq || this.endedSeqs.has(stream.seq)) {
      // 正在放，或者刚放完换上了接续流、服务端还没跟上。
    } else if (this.spare.stream?.seq === stream.seq) {
      // 接续流已被服务端认定为当前流，而这边的当前流已经没有了：直接换上。
      if (this.spare.role === "next" && (this.active.stream === null || this.handsOff())) this.promote(zone.intent === "play");
    } else {
      this.open(stream, zone.intent === "play");
    }
    const next = zone.nextStream;
    if (next === null) {
      if (this.spare.role === "next") this.unload(this.spare);
    } else if (this.spare.role !== "switch" && this.spare.stream?.seq !== next.seq && this.active.stream?.seq !== next.seq && !this.endedSeqs.has(next.seq)) {
      this.preload(next);
    }
    // 晚到的接续流：活动流已经放完（空流立即 EOF，ended 先于服务端宣布边界），而服务端仍把它当作当前流，就直接换上。
    if (this.spare.role === "next" && this.handsOff() && zone.stream?.seq === this.active.stream?.seq) this.promote(zone.intent === "play");
    if (this.spare.role === "switch") {
      // 正在打开新流时意图变成暂停：不等它出声，直接换上并停着。
      if (zone.intent === "pause") {
        this.pauseSlot(this.spare);
        this.swap();
      }
    } else if (this.active.stream !== null) {
      if (zone.intent === "play" && this.active.el.paused && !this.active.done && !this.active.el.ended) this.playSlot(this.active);
      if (zone.intent === "pause" && !this.active.el.paused) this.pauseSlot(this.active);
    }
    this.syncMediaSession();
  }

  /**
   * 活动流能让位给接续流：真正放完了，或者是起始条目打不开的空流（格式为 null）出错。
   * 其他出错是曲目中途断流，照旧回报 stream_lost 等服务端续流，不能换上接续流跳过这首剩下的部分。
   */
  private handsOff(): boolean {
    const { stream, done, el } = this.active;
    return stream !== null && done && (el.ended || stream.format === null);
  }

  // ---- 元素 ----

  private slot(el: AudioLike): Slot {
    el.preload = "auto";
    const slot: Slot = { el, stream: null, role: "idle", phase: "paused", unlocked: false, ownPause: false, done: false };
    const handlers: [string, () => void][] = [
      ["playing", () => this.onPlaying(slot)],
      ["pause", () => this.onPause(slot)],
      ["waiting", () => this.onWaiting(slot)],
      ["timeupdate", () => this.onTimeUpdate(slot)],
      ["ended", () => this.onEnded(slot)],
      ["error", () => this.onError(slot)],
    ];
    for (const [type, handler] of handlers) el.addEventListener(type, handler);
    this.listeners.set(el, handlers);
    return slot;
  }

  private load(slot: Slot, stream: ZoneStream, role: Slot["role"]): void {
    slot.stream = stream;
    slot.role = role;
    slot.phase = "loading";
    slot.done = false;
    slot.el.volume = this.volume.volume;
    slot.el.muted = this.volume.muted;
    slot.el.src = stream.url;
  }

  private unload(slot: Slot): void {
    if (slot.role === "idle" && slot.stream === null) return;
    if (slot === this.active) this.clearStall();
    slot.stream = null;
    slot.role = "idle";
    slot.phase = "paused";
    slot.ownPause = false;
    slot.done = false;
    slot.el.pause();
    slot.el.removeAttribute("src");
    slot.el.load();
  }

  private playSlot(slot: Slot): void {
    const stream = slot.stream;
    const promise = slot.el.play();
    promise?.catch((error: unknown) => {
      if (slot.stream !== stream || errorName(error) !== "NotAllowedError") return;
      // 浏览器拒绝自动播放：服务端把意图改为暂停，用户在这台设备上点播放时清除。
      this.report({ kind: "fault", fault: "autoplay_blocked" }, slot);
    });
  }

  /** 由本模块暂停：记下来，随后的 pause 事件（异步到达）不当作系统暂停。 */
  private pauseSlot(slot: Slot): void {
    if (slot.el.paused) return;
    slot.ownPause = true;
    slot.el.pause();
  }

  /** 在备用元素上打开新的当前流：意图是播放时等它出声再换上（旧流一直放到那时），暂停时直接换上并停着。 */
  private open(stream: ZoneStream, play: boolean): void {
    this.unload(this.spare);
    this.load(this.spare, stream, "switch");
    if (play) this.playSlot(this.spare);
    else this.swap();
  }

  /** 在备用元素上预载接续流（不播放；替换旧的预载）。 */
  private preload(stream: ZoneStream): void {
    this.unload(this.spare);
    this.load(this.spare, stream, "next");
  }

  /** 当前流放完：立即播放预载的接续流并对调。 */
  private promote(play: boolean): void {
    const ended = this.active.stream;
    if (ended !== null) this.endedSeqs.add(ended.seq);
    if (play) this.playSlot(this.spare);
    this.swap();
  }

  /** 备用元素成为活动元素，旧的卸载；换上后回报一次。 */
  private swap(): void {
    const old = this.active;
    this.active = this.spare;
    this.spare = old;
    this.active.role = "active";
    this.unload(old);
    this.lastStatusAt = Number.NEGATIVE_INFINITY;
    this.report({ kind: "status" }, this.active);
    this.syncMediaSession();
    this.deps.onTime();
  }

  // ---- 媒体事件 ----

  private onPlaying(slot: Slot): void {
    slot.phase = "playing";
    if (slot.role === "switch") {
      this.swap();
      return;
    }
    if (slot !== this.active) return;
    this.clearStall();
    this.report({ kind: "status" }, slot);
    this.syncMediaSession();
  }

  private onPause(slot: Slot): void {
    const own = slot.ownPause;
    slot.ownPause = false;
    if (slot.role === "idle" || slot.el.ended) return;
    slot.phase = "paused";
    if (slot !== this.active) return;
    this.clearStall();
    this.report({ kind: "status" }, slot);
    this.syncMediaSession();
    // 系统暂停（耳机拔出、来电、别的标签页的控件）：让播放区的意图跟着变，不然下一次对齐又会把它放起来。
    if (!own && this.zone?.intent === "play") this.deps.commands.pause();
  }

  private onWaiting(slot: Slot): void {
    if (slot !== this.active || slot.role !== "active") return;
    slot.phase = "buffering";
    this.report({ kind: "status" }, slot);
    if (this.stallTimer !== null) return;
    this.stallTimer = setTimeout(() => {
      this.stallTimer = null;
      if (this.active === slot && slot.phase === "buffering") this.report({ kind: "fault", fault: "stream_lost" }, slot);
    }, STALL_LIMIT_MS);
  }

  private onTimeUpdate(slot: Slot): void {
    if (slot !== this.active || slot.role !== "active") return;
    if (slot.phase === "playing" && this.deps.now() - this.lastStatusAt >= STATUS_INTERVAL_MS) this.report({ kind: "status" }, slot);
    this.syncMediaSession();
    this.deps.onTime();
  }

  private onEnded(slot: Slot): void {
    if (slot !== this.active || slot.role !== "active") return;
    this.clearStall();
    if (this.spare.role === "next" && this.spare.stream !== null) {
      this.promote(true);
      return;
    }
    slot.done = true;
    if (slot.stream !== null) this.endedSeqs.add(slot.stream.seq);
    this.report({ kind: "ended" }, slot);
  }

  private onError(slot: Slot): void {
    if (slot.role === "idle" || slot.stream === null) return;
    // 流断开或取不到（网络、410、5xx）：服务端从断开处续一条新流，旧流（若还在放）放到那时为止。
    this.report({ kind: "fault", fault: "stream_lost" }, slot);
    if (slot === this.active) slot.done = true;
    else this.unload(slot);
  }

  private clearStall(): void {
    if (this.stallTimer !== null) clearTimeout(this.stallTimer);
    this.stallTimer = null;
  }

  // ---- 回报 ----

  private report(body: { kind: "status" } | { kind: "ended" } | { kind: "fault"; fault: "autoplay_blocked" | "stream_lost" }, slot: Slot): void {
    if (this.sessionId === null) return;
    const seconds = slot.role === "switch" ? 0 : slot.el.currentTime;
    if (body.kind === "status") this.lastStatusAt = this.deps.now();
    this.reports.send({
      kind: body.kind,
      stream_seq: slot.stream?.seq ?? null,
      stream_seconds: Number.isFinite(seconds) ? Math.max(0, seconds) : 0,
      status: slot.phase,
      fault: body.kind === "fault" ? body.fault : null,
    });
  }

  private postReport(body: ReportBody): Promise<ReportResult> {
    const sessionId = this.sessionId;
    if (sessionId === null) return Promise.reject(new Error("没有输出会话"));
    return this.deps.report(sessionId, body);
  }

  private onReportError(error: unknown): void {
    const failure = failureOf(error);
    if (failure?.kind !== "http") return;
    if (failure.status === 409 && failure.code === "output_superseded") {
      this.deps.onSuperseded();
      return;
    }
    // 服务重启或会话已释放：带上正在放的曲目与位置重新认领，服务端从那里接着放。
    if (failure.status === 404 && failure.code === "output_unknown") this.reclaim();
  }

  private reclaim(): void {
    this.reports.close();
    this.sessionId = null;
    this.deps.onSession(null);
    void this.claim(false);
  }

  private async claim(takeover: boolean): Promise<void> {
    if (!this.running || this.claiming) return;
    this.claiming = true;
    try {
      const result = await this.deps.claim({ protocol: 1, capabilities: this.capabilities, takeover, playing: this.playing() });
      if (!this.running) {
        this.deps.release(result.sessionId, { stream_seq: null, stream_seconds: 0 });
        return;
      }
      this.sessionId = result.sessionId;
      this.reports.reopen();
      this.deps.onSession(result.sessionId);
      this.deps.onResult(result);
      // store 里可能已经是同一份状态（不会再通知），这里直接对齐一次。
      this.align(result.zone);
    } catch (error) {
      const failure = failureOf(error);
      if (!this.running) return;
      if (failure?.kind === "http" && failure.status === 404) {
        this.deps.onDeviceMissing();
        return;
      }
      if (failure?.kind === "http" && failure.status < 500) return;
      this.claimTimer = setTimeout(() => {
        this.claimTimer = null;
        void this.claim(takeover);
      }, CLAIM_RETRY_MS);
    } finally {
      this.claiming = false;
    }
  }

  /** 重新认领时仍在出声的曲目与曲目内位置。 */
  private playing(): ClaimBody["playing"] {
    if (this.active.el.paused) return null;
    const position = this.trackPosition();
    return position === null ? null : { entry_id: position.entryId, position_seconds: position.seconds };
  }

  private releaseSession(): void {
    const sessionId = this.sessionId;
    if (sessionId === null) return;
    const seconds = this.active.el.currentTime;
    this.deps.release(sessionId, { stream_seq: this.active.stream?.seq ?? null, stream_seconds: Number.isFinite(seconds) ? Math.max(0, seconds) : 0 });
    this.reports.close();
    this.sessionId = null;
    this.deps.onSession(null);
  }

  private readonly onPageHide = (): void => {
    this.releaseSession();
  };

  /** 从往返缓存回来：重新认领（普通认领，保持暂停）。 */
  private readonly onPageShow = (event?: Event): void => {
    if (event instanceof PageTransitionEvent && event.persisted && this.sessionId === null) void this.claim(false);
  };

  // ---- 锁屏信息 ----

  private segment(): StreamSegment | null {
    const stream = this.active.stream;
    if (stream === null || this.active.role !== "active") return null;
    const position = this.active.el.currentTime;
    let found: StreamSegment | null = null;
    for (const segment of stream.segments) if (segment.streamStartSeconds <= position + 0.05) found = segment;
    return found ?? stream.segments[0] ?? null;
  }

  private syncMediaSession(): void {
    const binding = this.deps.mediaSession;
    const segment = this.segment();
    const entryId = segment?.entryId ?? this.zone?.current?.entryId ?? null;
    if (entryId !== this.shownEntryId) {
      this.shownEntryId = entryId;
      binding.showTrack(segment?.track ?? this.zone?.current?.track ?? null);
    }
    const playing = this.active.stream !== null && !this.active.el.paused;
    binding.showStatus(entryId === null ? "idle" : playing ? (this.active.phase === "buffering" ? "buffering" : "playing") : "paused");
    const position = this.trackPosition();
    if (position !== null && playing) binding.showPosition(position.seconds, position.durationSeconds);
  }
}
