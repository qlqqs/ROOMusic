// 播放区 store：服务端快照（设备与播放区）、正在显示的播放区队列与时钟偏移。不依赖 React；界面经 subscribe/getState 读不可变状态。
// 规则（design.md §5.2、合同 §2.4）：每个播放区按 revision 丢弃旧状态；队列按 queue_revision 应用增量，序号对不上就整段重读；
// 时钟偏移取最近一次带 server_time 的响应（偏移 = server_time − 收到时的本地时间）。
import { patchEntries, patchTrack, type QueueEntry, type TrackDisplayMetadata } from "../engine/queue";
import type { CommandResult } from "./decoders";
import type { Device, QueueChange, Snapshot, Zone, ZoneQueue, ZoneStream } from "./model";

export type ZoneStoreState = {
  /** 第一次快照到达之前为 false。 */
  loaded: boolean;
  devices: readonly Device[];
  zones: ReadonlyMap<string, Zone>;
  /** 正在显示的播放区的队列（只保存需要显示的）。 */
  queues: ReadonlyMap<string, ZoneQueue>;
};

export type ZoneStoreDeps = {
  fetchQueue: (zoneId: string) => Promise<ZoneQueue>;
  now: () => number;
  /** 播放区事件第一次出现时调用（每个 seq 一次），例如 play_recorded 让播放历史失效。 */
  onZoneEvent?: (zone: Zone) => void;
};

function applyChange(entries: readonly QueueEntry[], change: QueueChange): readonly QueueEntry[] | null {
  switch (change.kind) {
    case "replace":
      return change.entries;
    case "clear":
      return [];
    case "insert":
      return [...entries.slice(0, change.index), ...change.entries, ...entries.slice(change.index)];
    case "remove":
      return entries.filter((entry) => entry.entryId !== change.entryId);
    case "move": {
      const at = entries.findIndex((entry) => entry.entryId === change.entryId);
      const to = at + change.delta;
      const moving = entries[at];
      const other = entries[to];
      if (moving === undefined || other === undefined) return null;
      const next = [...entries];
      next[at] = other;
      next[to] = moving;
      return next;
    }
    case "reload":
      return null;
  }
}

function patchStream(stream: ZoneStream | null, patches: ReadonlyMap<string, TrackDisplayMetadata>): ZoneStream | null {
  if (stream === null) return null;
  let changed = false;
  const segments = stream.segments.map((segment) => {
    const track = patchTrack(segment.track, patches);
    if (track === segment.track) return segment;
    changed = true;
    return { ...segment, track };
  });
  return changed ? { ...stream, segments } : stream;
}

export class ZoneStore {
  private readonly deps: ZoneStoreDeps;
  private state: ZoneStoreState = { loaded: false, devices: [], zones: new Map(), queues: new Map() };
  private readonly listeners = new Set<() => void>();
  private clockOffsetMs = 0;
  /** 需要显示队列的播放区。 */
  private readonly watched = new Set<string>();
  /** 正在重读的队列；重读期间又发现对不上时再读一次。 */
  private readonly reloading = new Map<string, { again: boolean }>();
  private readonly announced = new Map<string, number>();
  private disposed = false;

  constructor(deps: ZoneStoreDeps) {
    this.deps = deps;
  }

  readonly subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => void this.listeners.delete(listener);
  };

  readonly getState = (): ZoneStoreState => this.state;

  /** 服务端此刻的时间（毫秒），按最近一次响应的时钟偏移换算。 */
  serverNow(): number {
    return this.deps.now() + this.clockOffsetMs;
  }

  noteServerTime(serverTime: number): void {
    this.clockOffsetMs = serverTime - this.deps.now();
  }

  zone(zoneId: string): Zone | undefined {
    return this.state.zones.get(zoneId);
  }

  device(deviceId: string): Device | undefined {
    return this.state.devices.find((device) => device.deviceId === deviceId);
  }

  /** 开始显示一个播放区的队列：没有就读一次。 */
  watchQueue(zoneId: string): void {
    if (this.watched.has(zoneId)) return;
    this.watched.add(zoneId);
    this.reloadQueue(zoneId);
  }

  /** 完整快照（连接建立、重连）：设备与播放区整体替换，但比手里旧的播放区状态不覆盖。 */
  applySnapshot(snapshot: Snapshot): void {
    this.noteServerTime(snapshot.serverTime);
    const zones = new Map<string, Zone>();
    for (const zone of snapshot.zones) {
      const held = this.state.zones.get(zone.zoneId);
      zones.set(zone.zoneId, held !== undefined && held.revision > zone.revision ? held : zone);
    }
    const queues = new Map([...this.state.queues].filter(([zoneId]) => zones.has(zoneId)));
    this.state = { loaded: true, devices: snapshot.devices, zones, queues };
    for (const zone of zones.values()) this.afterZone(zone);
    this.emit();
  }

  /** 一份播放区状态（事件、命令或回报的响应）：revision 不比手里的新就丢弃。返回是否采用。 */
  applyZone(zone: Zone): boolean {
    const held = this.state.zones.get(zone.zoneId);
    if (held !== undefined && held.revision >= zone.revision) return false;
    const zones = new Map(this.state.zones);
    zones.set(zone.zoneId, zone);
    this.state = { ...this.state, zones };
    this.afterZone(zone);
    this.emit();
    return true;
  }

  applyDevice(device: Device): void {
    const at = this.state.devices.findIndex((item) => item.deviceId === device.deviceId);
    const devices = at < 0 ? [...this.state.devices, device] : this.state.devices.map((item, index) => (index === at ? device : item));
    this.state = { ...this.state, devices };
    this.emit();
  }

  removeDevice(deviceId: string, zoneId: string): void {
    const zones = new Map(this.state.zones);
    zones.delete(zoneId);
    const queues = new Map(this.state.queues);
    queues.delete(zoneId);
    this.watched.delete(zoneId);
    this.state = { ...this.state, devices: this.state.devices.filter((device) => device.deviceId !== deviceId), zones, queues };
    this.emit();
  }

  /** 队列增量：手里的 queue_revision 等于 change − 1 才应用；不比手里新的忽略（命令响应已经应用过）；其余整段重读。 */
  applyQueueChange(zoneId: string, change: QueueChange): void {
    if (!this.watched.has(zoneId)) return;
    const held = this.state.queues.get(zoneId);
    if (held !== undefined && change.queueRevision <= held.queueRevision) return;
    const entries = held === undefined || held.queueRevision !== change.queueRevision - 1 ? null : applyChange(held.entries, change);
    if (entries === null) {
      this.reloadQueue(zoneId);
      return;
    }
    this.setQueue({ zoneId, queueRevision: change.queueRevision, entries });
  }

  /** 命令的响应：先队列后状态（与事件的顺序相同）。 */
  applyCommandResult(zoneId: string, result: CommandResult): void {
    this.noteServerTime(result.serverTime);
    if (result.queueChange !== null) this.applyQueueChange(zoneId, result.queueChange);
    this.applyZone(result.zone);
    if (result.targetZone !== null) this.applyZone(result.targetZone);
  }

  /** 只改显示数据（艺人治理之后的标题、艺人、专辑名与封面）：队列、当前项与分段里的曲目都改；下一次服务端状态照常覆盖。 */
  patchTrackMetadata(patches: ReadonlyMap<string, TrackDisplayMetadata>): void {
    let changed = false;
    const zones = new Map<string, Zone>();
    for (const [zoneId, zone] of this.state.zones) {
      const current = zone.current === null ? null : { ...zone.current, track: patchTrack(zone.current.track, patches) };
      const stream = patchStream(zone.stream, patches);
      const nextStream = patchStream(zone.nextStream, patches);
      const same = (current === null || current.track === zone.current?.track) && stream === zone.stream && nextStream === zone.nextStream;
      zones.set(zoneId, same ? zone : { ...zone, current: current ?? null, stream, nextStream });
      changed ||= !same;
    }
    const queues = new Map<string, ZoneQueue>();
    for (const [zoneId, queue] of this.state.queues) {
      const entries = patchEntries(queue.entries, patches);
      queues.set(zoneId, entries === queue.entries ? queue : { ...queue, entries });
      changed ||= entries !== queue.entries;
    }
    if (!changed) return;
    this.state = { ...this.state, zones, queues };
    this.emit();
  }

  /** 会话结束：丢掉这个账号的全部状态，之后不再接收任何东西。 */
  dispose(): void {
    this.disposed = true;
    this.watched.clear();
    this.state = { loaded: false, devices: [], zones: new Map(), queues: new Map() };
    this.emit();
    this.listeners.clear();
  }

  private afterZone(zone: Zone): void {
    const event = zone.event;
    if (event !== null && (this.announced.get(zone.zoneId) ?? -1) < event.seq) {
      this.announced.set(zone.zoneId, event.seq);
      this.deps.onZoneEvent?.(zone);
    }
    // 状态里的 queue_revision 比手里的新，而增量没有到（或解不了）：重读。增量先于状态到达，所以这里只补漏。
    const held = this.state.queues.get(zone.zoneId);
    if (this.watched.has(zone.zoneId) && held !== undefined && held.queueRevision < zone.queueRevision) this.reloadQueue(zone.zoneId);
  }

  private setQueue(queue: ZoneQueue): void {
    const queues = new Map(this.state.queues);
    queues.set(queue.zoneId, queue);
    this.state = { ...this.state, queues };
    this.emit();
  }

  private reloadQueue(zoneId: string): void {
    const running = this.reloading.get(zoneId);
    if (running !== undefined) {
      running.again = true;
      return;
    }
    const marker = { again: false };
    this.reloading.set(zoneId, marker);
    this.deps.fetchQueue(zoneId).then(
      (queue) => {
        this.reloading.delete(zoneId);
        if (this.disposed || !this.watched.has(zoneId)) return;
        const held = this.state.queues.get(zoneId);
        if (held === undefined || queue.queueRevision >= held.queueRevision) this.setQueue(queue);
        const zone = this.state.zones.get(zoneId);
        if (marker.again || (zone !== undefined && zone.queueRevision > queue.queueRevision)) this.reloadQueue(zoneId);
      },
      () => {
        // 读不到就留着旧的：下一次状态变化或重连的快照会再触发。
        this.reloading.delete(zoneId);
      },
    );
  }

  private emit(): void {
    for (const listener of [...this.listeners]) listener();
  }
}
