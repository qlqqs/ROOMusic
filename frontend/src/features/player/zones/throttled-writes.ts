// 按键节流的写入（音量一类连续拖动的值）：第一次立即发出，之后每 125 ms 最多发一次最新值；
// 发出后 1 秒内界面以本地值为准，到期后回到服务端的值。每个键（设备 ID、播放区 ID）各自节流，互不覆盖。
const THROTTLE_MS = 125;
const OPTIMISTIC_MS = 1_000;

type Entry<T> = { value: T; until: number; timer: ReturnType<typeof setTimeout> | null; dirty: boolean };

export class ThrottledWrites<T> {
  private readonly entries = new Map<string, Entry<T>>();
  private readonly now: () => number;
  private readonly send: (key: string, value: T) => void;
  private readonly settled: () => void;

  /** send 发出一次写入；settled 在本地值到期时调用（重新组装界面状态）。 */
  constructor(deps: { now: () => number; send: (key: string, value: T) => void; settled: () => void }) {
    this.now = deps.now;
    this.send = deps.send;
    this.settled = deps.settled;
  }

  set(key: string, value: T): void {
    const until = this.now() + OPTIMISTIC_MS;
    const held = this.entries.get(key);
    if (held !== undefined && held.timer !== null) {
      held.value = value;
      held.until = until;
      held.dirty = true;
      return;
    }
    const entry: Entry<T> = { value, until, timer: null, dirty: false };
    this.entries.set(key, entry);
    this.flush(key, entry);
  }

  /** 还在本地值有效期内时返回本地值。 */
  get(key: string): T | undefined {
    const entry = this.entries.get(key);
    return entry !== undefined && entry.until > this.now() ? entry.value : undefined;
  }

  private flush(key: string, entry: Entry<T>): void {
    entry.dirty = false;
    this.send(key, entry.value);
    entry.timer = setTimeout(() => {
      entry.timer = null;
      if (entry.dirty) this.flush(key, entry);
      setTimeout(this.settled, OPTIMISTIC_MS);
    }, THROTTLE_MS);
  }
}
