// 计一次播放的跟踪器（阶段 P）：只累计媒体真正向前走的出声时间，达到门槛时交出一次记录。它是纯逻辑，不碰媒体元素与定时器：
// 引擎在媒体事件里把播放位置告诉它，时钟由外部注入，所以规则可以脱离浏览器测试。
//
// 规则（与后端 backend/internal/listening 一致）：
// - 门槛 T = min(D / 2, 240 秒)。D 优先取扫描得到的曲目时长（前后端用同一个 D，门槛一致），没有时取媒体报告的时长，再没有就 T = 240 秒。
// - 只累计引擎状态为 playing 时相邻两次采样之间播放位置的前进量：拖动跨过的距离、后退都不算；前进量超过同一段时间的真实流逝加 1 秒时
//   当作跳变丢弃（没有 seeking 事件的跳变，例如引擎自己设置 currentTime）。后台标签页的 timeupdate 变稀时前进量变大，但仍受真实流逝限制。
// - 静音与音量为 0 照常累计（媒体在播放，网页无从得知用户是否在听）。
// - 一次访问最多记一次。新的访问：换到另一首，或者同一首从头再播；同一次访问继续：暂停后继续、缓冲、暂时性故障后重试、
//   媒体错误重新分类后继续、卸载再挂载。刷新页面之后引擎是新的，累计从零开始（不写浏览器存储）。

/** 门槛的上限：再长的曲目也只要累计出声 4 分钟。 */
export const MAX_LISTEN_THRESHOLD_SECONDS = 240;

/** 前进量允许超过真实流逝的余量：吸收事件粒度与浮点误差。 */
const JUMP_TOLERANCE_SECONDS = 1;

/** 一次访问对应的曲目；durationSeconds 是扫描得到的时长，未知是 null。 */
export type ListenTrack = { entryId: string; trackId: string; durationSeconds: number | null };

/** 达到计次规则时交出的记录：曲目与达到规则那一刻累计的实际出声秒数。 */
export type ListenRecord = { trackId: string; playedSeconds: number };

function knownDuration(seconds: number | null): seconds is number {
  return seconds !== null && Number.isFinite(seconds) && seconds > 0;
}

/** 计一次播放所需的实际出声秒数：min(D / 2, 240)；时长未知时是 240。 */
export function listenThreshold(durationSeconds: number | null): number {
  return knownDuration(durationSeconds) ? Math.min(durationSeconds / 2, MAX_LISTEN_THRESHOLD_SECONDS) : MAX_LISTEN_THRESHOLD_SECONDS;
}

type Visit = {
  entryId: string;
  trackId: string;
  /** 扫描得到的时长；null 表示未知，门槛随媒体报告的时长确定。 */
  catalogDuration: number | null;
  threshold: number;
  accumulated: number;
  recorded: boolean;
};

export class ListenTracker {
  private readonly now: () => number;
  private visit: Visit | null = null;
  /** 上一次采样的位置与时刻；null 表示没有可比较的基准（暂停、缓冲、拖动中、刚开始），下一次采样只建立基准。 */
  private baseline: { position: number; at: number } | null = null;

  /** now 返回毫秒时钟（Date.now 即可）；只用来限制两次采样之间可以累计的前进量。 */
  constructor(now: () => number) {
    this.now = now;
  }

  /** 新的访问：换了曲目，或同一首从头再播。之前的累计作废。 */
  begin(track: ListenTrack): void {
    const catalogDuration = knownDuration(track.durationSeconds) ? track.durationSeconds : null;
    this.visit = { entryId: track.entryId, trackId: track.trackId, catalogDuration, threshold: listenThreshold(catalogDuration), accumulated: 0, recorded: false };
    this.baseline = null;
  }

  /** 继续同一次访问；这个条目没有进行中的访问（刷新后、访问已结束、从没出过声）时开始新的。 */
  continueOrBegin(track: ListenTrack): void {
    if (this.visit?.entryId === track.entryId) return;
    this.begin(track);
  }

  /** 访问结束：停止、清空队列、播完最后一首、会话结束。之后的播放是新的访问。 */
  end(): void {
    this.visit = null;
    this.baseline = null;
  }

  /** 不再出声（暂停、缓冲、加载、出错、卸载）：停止累计，访问保留。 */
  suspend(): void {
    this.baseline = null;
  }

  /** 开始出声（playing 事件）：以当前位置为基准。 */
  resume(position: number): void {
    this.baseline = this.visit !== null && Number.isFinite(position) ? { position, at: this.now() } : null;
  }

  /** 开始拖动：跨过的距离不算。 */
  seeking(): void {
    this.baseline = null;
  }

  /** 拖动完成且仍在出声：以新位置为基准继续。 */
  seeked(position: number): void {
    this.baseline = this.visit !== null && Number.isFinite(position) ? { position, at: this.now() } : null;
  }

  /** 曲目没有扫描时长时，门槛随媒体报告的时长确定（已经记录过的访问不受影响）。 */
  mediaDuration(seconds: number | null): void {
    const visit = this.visit;
    if (visit === null || visit.recorded || visit.catalogDuration !== null || !knownDuration(seconds)) return;
    visit.threshold = listenThreshold(seconds);
  }

  /** 引擎在出声期间的 timeupdate 里调用。跨过门槛的那一次返回记录，之后同一次访问不再返回。 */
  sample(position: number): ListenRecord | null {
    const visit = this.visit;
    if (visit === null || !Number.isFinite(position)) return null;
    const at = this.now();
    const previous = this.baseline;
    this.baseline = { position, at };
    if (previous === null || visit.recorded) return null;
    const delta = position - previous.position;
    const elapsedSeconds = (at - previous.at) / 1000;
    if (delta > 0 && delta <= elapsedSeconds + JUMP_TOLERANCE_SECONDS) visit.accumulated += delta;
    if (visit.accumulated < visit.threshold) return null;
    visit.recorded = true;
    return { trackId: visit.trackId, playedSeconds: visit.accumulated };
  }
}
