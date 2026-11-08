// Web Audio 音频图：按需接入（见 player-engine.ts 的接入规则）。每个音频元素一条支路，两条支路共用总音量节点：
// <audio> → MediaElementAudioSourceNode → levelGain（均衡）→ fadeGain（渐变）→ masterGain（用户音量 / 静音）→ destination
// 元素一旦接入就拆不出来；上下文不在 running 时接入的元素没有声音，所以只在 running 时接入，播放已接入的元素前先确认 running。
import type { AudioLike } from "./player-engine";

export type AudioParamLike = {
  value: number;
  setValueAtTime: (value: number, time: number) => unknown;
  setTargetAtTime: (target: number, time: number, timeConstant: number) => unknown;
  setValueCurveAtTime: (values: Float32Array, time: number, duration: number) => unknown;
  cancelScheduledValues: (time: number) => unknown;
};

export type AudioNodeLike = { connect: (destination: never) => unknown };
export type GainNodeLike = AudioNodeLike & { readonly gain: AudioParamLike };

/** 引擎需要的 AudioContext 能力；浏览器的 AudioContext 满足它，测试用替身。 */
export type AudioContextLike = {
  readonly state: string;
  readonly currentTime: number;
  readonly destination: unknown;
  resume: () => Promise<void>;
  createGain: () => GainNodeLike;
  createMediaElementSource: (element: never) => AudioNodeLike;
  addEventListener: (type: "statechange", listener: () => void) => void;
  removeEventListener: (type: "statechange", listener: () => void) => void;
};

type Branch = { level: GainNodeLike; fade: GainNodeLike };

const CURVE_POINTS = 64;
/** 运行中改变均衡时平滑过去的时间常数（秒）。 */
const SMOOTHING = 0.05;

/** 等功率渐变曲线：淡出用 cos，淡入用 sin。 */
function curve(kind: "out" | "in"): Float32Array {
  const values = new Float32Array(CURVE_POINTS);
  for (let index = 0; index < CURVE_POINTS; index += 1) {
    const phase = (index / (CURVE_POINTS - 1)) * (Math.PI / 2);
    values[index] = kind === "out" ? Math.cos(phase) : Math.sin(phase);
  }
  return values;
}

function connect(from: AudioNodeLike, to: unknown): void {
  from.connect(to as never);
}

export class AudioGraph {
  private ctx: AudioContextLike | null = null;
  private master: GainNodeLike | null = null;
  private readonly branches = new Map<AudioLike, Branch>();
  private readonly create: () => AudioContextLike;
  private readonly onStateChange: () => void;

  constructor(create: () => AudioContextLike, onStateChange: () => void) {
    this.create = create;
    this.onStateChange = onStateChange;
  }

  /** 在用户手势命令里同步调用：没有上下文就创建，不在 running 就请求恢复。 */
  ensure(): void {
    if (this.ctx === null) {
      try {
        this.ctx = this.create();
      } catch {
        return;
      }
      this.master = this.ctx.createGain();
      connect(this.master, this.ctx.destination);
      this.ctx.addEventListener("statechange", this.onStateChange);
    }
    if (this.ctx.state !== "running") this.ctx.resume().catch(() => undefined);
  }

  get running(): boolean {
    return this.ctx?.state === "running";
  }

  get created(): boolean {
    return this.ctx !== null;
  }

  isConnected(element: AudioLike): boolean {
    return this.branches.has(element);
  }

  /** 接入一个元素（已接入的直接成功）；上下文不在 running 时不接入。 */
  connect(element: AudioLike): boolean {
    if (this.branches.has(element)) return true;
    const ctx = this.ctx;
    if (ctx === null || this.master === null || ctx.state !== "running") return false;
    const source = ctx.createMediaElementSource(element as never);
    const level = ctx.createGain();
    const fade = ctx.createGain();
    connect(source, level);
    connect(level, fade);
    connect(fade, this.master);
    this.branches.set(element, { level, fade });
    return true;
  }

  /** 恢复上下文，最多等 timeoutMs；返回此刻是否在 running。 */
  async resumeWithin(timeoutMs: number): Promise<boolean> {
    const ctx = this.ctx;
    if (ctx === null) return false;
    if (ctx.state === "running") return true;
    await Promise.race([ctx.resume().catch(() => undefined), new Promise((resolve) => setTimeout(resolve, timeoutMs))]);
    return ctx.state === "running";
  }

  /** 用户音量（静音写 0）。 */
  setMaster(volume: number): void {
    if (this.ctx === null || this.master === null) return;
    this.master.gain.setValueAtTime(volume, this.ctx.currentTime);
  }

  /** 均衡增益（线性）：开始播放前直接设好，运行中改设置时平滑过去。 */
  setLevel(element: AudioLike, linear: number, smooth = false): void {
    const branch = this.branches.get(element);
    if (this.ctx === null || branch === undefined) return;
    const now = this.ctx.currentTime;
    branch.level.gain.cancelScheduledValues(now);
    if (smooth) branch.level.gain.setTargetAtTime(linear, now, SMOOTHING);
    else branch.level.gain.setValueAtTime(linear, now);
  }

  /** 渐变增益：取消已排的曲线，直接设成 value。 */
  setFade(element: AudioLike, value: number): void {
    const branch = this.branches.get(element);
    if (this.ctx === null || branch === undefined) return;
    const now = this.ctx.currentTime;
    branch.fade.gain.cancelScheduledValues(now);
    branch.fade.gain.setValueAtTime(value, now);
  }

  /** 交叉渐变：从此刻起 seconds 秒内，旧元素按 cos 淡出、新元素按 sin 淡入（等功率），由音频线程按采样精度执行。 */
  crossfade(from: AudioLike, to: AudioLike, seconds: number): void {
    const out = this.branches.get(from);
    const into = this.branches.get(to);
    if (this.ctx === null || out === undefined || into === undefined) return;
    const start = this.ctx.currentTime;
    out.fade.gain.cancelScheduledValues(start);
    into.fade.gain.cancelScheduledValues(start);
    out.fade.gain.setValueCurveAtTime(curve("out"), start, seconds);
    into.fade.gain.setValueCurveAtTime(curve("in"), start, seconds);
  }

  /** 中断退回：丢掉已接入的元素（它们不再使用），之后这个音频图不再被用到。 */
  abandon(): void {
    this.branches.clear();
    this.ctx?.removeEventListener("statechange", this.onStateChange);
  }
}
