import type { AudioLike } from "../features/player/engine/player-engine";

export type PlayBehavior = "resolve" | "not-allowed" | "not-supported" | "abort";

/**
 * 音频元素的替身：只做播放器引擎用到的那部分，并且不自己推进时间——测试用 loadMetadata、beginPlaying、tick、finish、fail
 * 这些方法按真实浏览器的事件顺序触发事件，所以引擎的状态仍然只来自媒体事件。
 */
export class FakeAudio extends EventTarget implements AudioLike {
  currentTime = 0;
  duration = Number.NaN;
  paused = true;
  ended = false;
  volume = 1;
  muted = false;
  preload = "auto";
  error: { code: number } | null = null;
  seekableLength = 0;
  playBehavior: PlayBehavior = "resolve";
  /** 下几次 play() 的结果，按顺序消耗；用完后用 playBehavior。 */
  readonly playQueue: PlayBehavior[] = [];

  /** 引擎设置过的全部 src，按顺序。 */
  readonly sources: string[] = [];
  playCalls = 0;
  pauseCalls = 0;
  loadCalls = 0;

  private readonly attributes = new Map<string, string>();

  get seekable(): { readonly length: number } {
    return { length: this.seekableLength };
  }

  get src(): string {
    return this.attributes.get("src") ?? "";
  }

  set src(value: string) {
    this.attributes.set("src", value);
    this.sources.push(value);
    this.currentTime = 0;
    this.duration = Number.NaN;
    this.seekableLength = 0;
    this.ended = false;
    this.error = null;
  }

  getAttribute(name: "src"): string | null {
    return this.attributes.get(name) ?? null;
  }

  removeAttribute(name: "src"): void {
    this.attributes.delete(name);
  }

  play(): Promise<void> {
    this.playCalls += 1;
    switch (this.playQueue.shift() ?? this.playBehavior) {
      case "resolve":
        this.paused = false;
        return Promise.resolve();
      case "not-allowed":
        return Promise.reject(new DOMException("play() 被自动播放策略拒绝", "NotAllowedError"));
      case "not-supported":
        return Promise.reject(new DOMException("不支持的来源", "NotSupportedError"));
      case "abort":
        return Promise.reject(new DOMException("被新的加载打断", "AbortError"));
    }
  }

  pause(): void {
    this.pauseCalls += 1;
    if (this.paused) return;
    this.paused = true;
    this.emit("pause");
  }

  load(): void {
    this.loadCalls += 1;
    this.currentTime = 0;
    this.duration = Number.NaN;
    this.seekableLength = 0;
    this.paused = true;
    this.ended = false;
    this.emit("emptied");
  }

  emit(type: string): void {
    this.dispatchEvent(new Event(type));
  }

  /** 元数据到达：时长已知，可定位范围有无由 seekable 决定。 */
  loadMetadata(duration: number, options: { seekable?: boolean } = {}): void {
    this.duration = duration;
    this.seekableLength = (options.seekable ?? true) ? 1 : 0;
    this.emit("loadedmetadata");
    this.emit("durationchange");
  }

  /** 开始真实出声（playing 事件）。 */
  beginPlaying(): void {
    this.paused = false;
    this.emit("playing");
  }

  tick(position: number): void {
    this.currentTime = position;
    this.emit("timeupdate");
  }

  /** 拖动或跳转：浏览器先发 seeking，到达新位置后发 seeked；currentTime 在 seeking 时就已是目标位置。 */
  seekTo(position: number): void {
    this.currentTime = position;
    this.emit("seeking");
    this.emit("seeked");
  }

  stall(): void {
    this.emit("waiting");
  }

  /** 播到结尾：先 pause（此时 ended 已为 true），再 ended，与浏览器的顺序一致。 */
  finish(): void {
    this.currentTime = Number.isFinite(this.duration) ? this.duration : this.currentTime;
    this.ended = true;
    this.paused = true;
    this.emit("pause");
    this.emit("ended");
  }

  fail(code: number): void {
    this.error = { code };
    this.emit("error");
  }
}
