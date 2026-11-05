// 播放队列的纯函数：条目、四种播放模式、插入、移除、移动、前进与后退。不依赖 React、媒体元素或存储，
// 随机数与条目 ID 由调用方注入，所以每个语义都能用确定的输入验证。

import type { ArtistRefDTO } from "../../catalog";

export const playModes = ["sequential", "repeat_all", "repeat_one", "shuffle"] as const;
export type PlayMode = (typeof playModes)[number];

/** 队列容量上限，与发行详情的曲目上限一致：整张发行版本都能一次放进队列。 */
export const MAX_QUEUE_ENTRIES = 10_000;

/** 实际播放过的条目历史的上限：随机模式的“上一首”沿它返回，不让它无界增长。 */
const MAX_HISTORY = 500;

/** 源文件的事实（取自扫描观察，不是当前文件可播性的证明）。缺失的值是 null，显示为“未知”。 */
export type SourceFacts = {
  sourceKind: string;
  codec: string | null;
  bitDepth: number | null;
  sampleRate: number | null;
  channels: number | null;
  bitrate: number | null;
};

/** 队列里一首曲目需要的显示与来源信息。曲目身份是 trackId，同一首可以多次入队。 */
export type PlayerTrack = {
  trackId: string;
  releaseId: string;
  groupId: string;
  title: string;
  artist: string;
  /**
   * 艺人文字对应的艺人（M5 ⑤）：与 artist 同一个回退，名字与链接不会错配。所有构造入口都会给出；缺省（旧的测试数据）等于空数组，
   * 艺人名显示为纯文字。只用于显示，不进入刷新恢复的记录。
   */
  artistRefs?: readonly ArtistRefDTO[];
  albumTitle: string;
  albumArtist: string;
  /** 扫描得到的时长，只用于队列显示；播放进度与时长取自媒体事件。 */
  durationSeconds: number | null;
  /** 封面资源 ID（同源封面接口）；没有封面是 null。 */
  artworkId: string | null;
  source: SourceFacts;
};

/** 队列条目：entryId 表达同一首曲目多次入队的独立位置，排序与移除按它操作，不能与 trackId 混用。 */
export type QueueEntry = { entryId: string; track: PlayerTrack };

export type QueueState = {
  entries: readonly QueueEntry[];
  currentEntryId: string | null;
  mode: PlayMode;
  /** 实际播放过的条目 ID，最近的在末尾；随机模式的“上一首”沿它返回。 */
  history: readonly string[];
  /** 随机模式本轮还没播放过的条目 ID。 */
  bag: readonly string[];
};

export type QueueEnv = {
  newEntryId: () => string;
  /** 返回 [0, 1)；随机模式用它选下一首。 */
  random: () => number;
};

export const emptyQueue: QueueState = { entries: [], currentEntryId: null, mode: "sequential", history: [], bag: [] };

export type QueueAddResult = { ok: true; state: QueueState } | { ok: false; reason: "queue_full" };

function entryOf(track: PlayerTrack, env: QueueEnv): QueueEntry {
  return { entryId: env.newEntryId(), track };
}

function indexOfEntry(entries: readonly QueueEntry[], entryId: string | null): number {
  return entryId === null ? -1 : entries.findIndex((entry) => entry.entryId === entryId);
}

/** 随机模式的“袋子”：除当前条目外的全部条目；其他模式没有袋子。 */
function freshBag(entries: readonly QueueEntry[], currentEntryId: string | null, mode: PlayMode, exclude: readonly string[] = []): string[] {
  if (mode !== "shuffle") return [];
  const excluded = new Set(exclude);
  return entries.filter((entry) => entry.entryId !== currentEntryId && !excluded.has(entry.entryId)).map((entry) => entry.entryId);
}

function pushHistory(history: readonly string[], entryId: string | null): readonly string[] {
  if (entryId === null) return history;
  const next = [...history, entryId];
  return next.length > MAX_HISTORY ? next.slice(next.length - MAX_HISTORY) : next;
}

/** 立即播放：用 tracks 替换整个队列，从 startIndex 开始（越界时取首项）。tracks 为空时返回空队列。模式保留。 */
export function replaceQueue(state: QueueState, tracks: readonly PlayerTrack[], startIndex: number, env: QueueEnv): QueueState {
  if (tracks.length > MAX_QUEUE_ENTRIES) return state;
  const entries = tracks.map((track) => entryOf(track, env));
  const current = entries[startIndex] ?? entries[0];
  const currentEntryId = current?.entryId ?? null;
  return { entries, currentEntryId, mode: state.mode, history: [], bag: freshBag(entries, currentEntryId, state.mode) };
}

/** 下一首播放：插在当前条目之后；没有当前条目时放在队首。不改变当前条目与播放。超出容量时整批拒绝。 */
export function insertAfterCurrent(state: QueueState, tracks: readonly PlayerTrack[], env: QueueEnv): QueueAddResult {
  if (tracks.length === 0) return { ok: true, state };
  if (state.entries.length + tracks.length > MAX_QUEUE_ENTRIES) return { ok: false, reason: "queue_full" };
  const added = tracks.map((track) => entryOf(track, env));
  const at = indexOfEntry(state.entries, state.currentEntryId) + 1;
  const entries = [...state.entries.slice(0, at), ...added, ...state.entries.slice(at)];
  return { ok: true, state: withAdded(state, entries, added) };
}

/** 加入队列：追加到队尾，不打断当前播放。超出容量时整批拒绝。 */
export function appendEntries(state: QueueState, tracks: readonly PlayerTrack[], env: QueueEnv): QueueAddResult {
  if (tracks.length === 0) return { ok: true, state };
  if (state.entries.length + tracks.length > MAX_QUEUE_ENTRIES) return { ok: false, reason: "queue_full" };
  const added = tracks.map((track) => entryOf(track, env));
  return { ok: true, state: withAdded(state, [...state.entries, ...added], added) };
}

/** 队列从空变为非空时，第一个加入的条目成为当前条目（不开始播放）；随机模式把新条目放进袋子。 */
function withAdded(state: QueueState, entries: readonly QueueEntry[], added: readonly QueueEntry[]): QueueState {
  const currentEntryId = state.entries.length === 0 ? (added[0]?.entryId ?? null) : state.currentEntryId;
  const next: QueueState = { ...state, entries, currentEntryId };
  if (state.mode !== "shuffle") return next;
  return { ...next, bag: [...state.bag, ...added.map((entry) => entry.entryId).filter((entryId) => entryId !== currentEntryId)] };
}

export type RemoveResult = {
  state: QueueState;
  /** 被移除的是当前条目：调用方要停止它，并在 state.currentEntryId（后继条目，可能为 null）上继续。 */
  removedCurrent: boolean;
};

/** 移除一个条目（按条目 ID，同一首曲目的其他条目不受影响）。移除当前条目时，后继按模式选出，没有后继就是 null（停在空闲）。 */
export function removeEntry(state: QueueState, entryId: string, env: QueueEnv): RemoveResult {
  const index = indexOfEntry(state.entries, entryId);
  if (index < 0) return { state, removedCurrent: false };
  const entries = state.entries.filter((entry) => entry.entryId !== entryId);
  const history = state.history.filter((id) => id !== entryId);
  const bag = state.bag.filter((id) => id !== entryId);
  const removedCurrent = state.currentEntryId === entryId;
  if (!removedCurrent) return { state: { ...state, entries, history, bag }, removedCurrent };
  const remaining: QueueState = { ...state, entries, history, bag, currentEntryId: null };
  const successor = successorAfterRemoval(state, index, entries, env);
  return { state: { ...remaining, currentEntryId: successor, bag: successor === null ? remaining.bag : remaining.bag.filter((id) => id !== successor) }, removedCurrent };
}

function successorAfterRemoval(before: QueueState, removedIndex: number, entries: readonly QueueEntry[], env: QueueEnv): string | null {
  if (entries.length === 0) return null;
  if (before.mode === "shuffle") {
    const candidates = before.bag.filter((id) => id !== before.currentEntryId && entries.some((entry) => entry.entryId === id));
    const pool = candidates.length > 0 ? candidates : entries.map((entry) => entry.entryId);
    return pool[Math.floor(env.random() * pool.length)] ?? null;
  }
  const following = entries[removedIndex];
  if (following !== undefined) return following.entryId;
  return before.mode === "repeat_all" ? (entries[0]?.entryId ?? null) : null;
}

/** 在队列里把条目上移（delta = -1）或下移（+1）一位；在两端时不变。当前条目与播放状态都不受影响。 */
export function moveEntry(state: QueueState, entryId: string, delta: -1 | 1): QueueState {
  const index = indexOfEntry(state.entries, entryId);
  const target = index + delta;
  if (index < 0 || target < 0 || target >= state.entries.length) return state;
  const entries = [...state.entries];
  const moved = entries[index];
  const swapped = entries[target];
  if (moved === undefined || swapped === undefined) return state;
  entries[index] = swapped;
  entries[target] = moved;
  return { ...state, entries };
}

/** 清空队列；播放模式保留。 */
export function clearQueue(state: QueueState): QueueState {
  return { ...emptyQueue, mode: state.mode };
}

/** 切换播放模式：进入随机时重新装袋，离开时清袋。 */
export function setMode(state: QueueState, mode: PlayMode): QueueState {
  if (state.mode === mode) return state;
  return { ...state, mode, bag: freshBag(state.entries, state.currentEntryId, mode, state.history) };
}

/** 用户明确选中某个条目（“播放此项”）：它成为当前条目，原来的当前条目进入历史。 */
export function jumpTo(state: QueueState, entryId: string): QueueState {
  if (indexOfEntry(state.entries, entryId) < 0 || state.currentEntryId === entryId) return state;
  return {
    ...state,
    currentEntryId: entryId,
    history: pushHistory(state.history, state.currentEntryId),
    bag: state.bag.filter((id) => id !== entryId),
  };
}

export type Step = { state: QueueState; entryId: string | null };

type AdvanceOptions = {
  /** natural：曲目自然结束；manual：用户点了“下一首”。 */
  reason: "natural" | "manual";
  /** 已知失败（或不应自动选中）的条目；前进时跳过它们。 */
  isSkipped?: (entryId: string) => boolean;
};

/**
 * 前进到下一个条目。返回要播放的条目 ID 与更新后的状态；没有下一个条目时 entryId 为 null，状态不变（调用方停在暂停）。
 * - 顺序：往后一项，末项之后没有；
 * - 列表循环：往后一项，末项之后回到首项；
 * - 单曲循环：只影响自然结束（再播当前条目）；手动切歌按顺序，行为同顺序模式；
 * - 随机：从本轮没播放过的条目里随机选，袋子空了重新装袋；多于一项时不连续重复。
 */
export function advance(state: QueueState, env: QueueEnv, options: AdvanceOptions): Step {
  const { reason, isSkipped = () => false } = options;
  const current = state.currentEntryId;
  if (state.entries.length === 0) return { state, entryId: null };
  if (state.mode === "repeat_one" && reason === "natural" && current !== null && !isSkipped(current)) return { state, entryId: current };
  const picked = state.mode === "shuffle" ? pickShuffled(state, env, isSkipped) : pickInOrder(state, isSkipped);
  if (picked === null) return { state, entryId: null };
  return { state: { ...picked.state, currentEntryId: picked.entryId, history: pushHistory(state.history, current) }, entryId: picked.entryId };
}

function pickInOrder(state: QueueState, isSkipped: (entryId: string) => boolean): { state: QueueState; entryId: string } | null {
  const { entries } = state;
  const at = indexOfEntry(entries, state.currentEntryId);
  const wraps = state.mode === "repeat_all";
  // 从当前位置之后依次找；列表循环绕回开头，最后才轮到当前条目自己（只剩它时再播它）。
  for (let step = 1; step <= entries.length; step += 1) {
    const index = at + step;
    if (index >= entries.length && !wraps) return null;
    const candidate = entries[index % entries.length];
    if (candidate === undefined) return null;
    if (candidate.entryId === state.currentEntryId && entries.length > 1) continue;
    if (!isSkipped(candidate.entryId)) return { state, entryId: candidate.entryId };
  }
  return null;
}

function pickShuffled(state: QueueState, env: QueueEnv, isSkipped: (entryId: string) => boolean): { state: QueueState; entryId: string } | null {
  const { entries } = state;
  const ids = new Set(entries.map((entry) => entry.entryId));
  const usable = (entryId: string) => !isSkipped(entryId) && ids.has(entryId);
  let bag = state.bag.filter((entryId) => entryId !== state.currentEntryId && usable(entryId));
  if (bag.length === 0) {
    // 一轮播完：重新装袋（除了当前条目，避免连续重复）。只有一项时只能再播它自己。
    bag = entries.filter((entry) => entry.entryId !== state.currentEntryId && usable(entry.entryId)).map((entry) => entry.entryId);
    if (bag.length === 0) {
      const only = entries.find((entry) => entry.entryId === state.currentEntryId);
      return only === undefined || !usable(only.entryId) ? null : { state: { ...state, bag: [] }, entryId: only.entryId };
    }
  }
  const entryId = bag[Math.min(bag.length - 1, Math.floor(env.random() * bag.length))];
  if (entryId === undefined) return null;
  return { state: { ...state, bag: bag.filter((id) => id !== entryId) }, entryId };
}

/**
 * 后退到上一个条目。随机模式沿实际播放历史返回，离开的当前条目放回袋子（本轮还会轮到它）；
 * 其他模式取列表里的上一项（列表循环绕到末项）。
 * 没有上一项时（队首，或没有历史）返回当前条目，调用方把它从头播放。没有当前条目时 entryId 为 null。
 */
export function retreat(state: QueueState, isSkipped: (entryId: string) => boolean = () => false): Step {
  const current = state.currentEntryId;
  if (current === null) return { state, entryId: null };
  if (state.mode === "shuffle") {
    const history = [...state.history];
    while (history.length > 0) {
      const previous = history.pop();
      if (previous !== undefined && previous !== current && indexOfEntry(state.entries, previous) >= 0 && !isSkipped(previous)) {
        // 当前条目抽出袋子时就算本轮已轮到；不放回去，“上一首”之后再“下一首”会把它跳过。
        const bag = [...state.bag.filter((id) => id !== previous && id !== current), current];
        return { state: { ...state, currentEntryId: previous, history, bag }, entryId: previous };
      }
    }
    return { state: { ...state, history: [] }, entryId: current };
  }
  const { entries } = state;
  const at = indexOfEntry(entries, current);
  for (let step = 1; step < entries.length; step += 1) {
    const index = at - step;
    if (index < 0 && state.mode !== "repeat_all") break;
    const candidate = entries[(index + entries.length) % entries.length];
    if (candidate !== undefined && !isSkipped(candidate.entryId)) {
      return { state: { ...state, currentEntryId: candidate.entryId, history: pushHistory(state.history, current) }, entryId: candidate.entryId };
    }
  }
  return { state, entryId: current };
}

/** “下一首”按钮是否可用：顺序与单曲循环在末项时没有下一首；其他情况只要有条目。 */
export function hasNext(state: QueueState): boolean {
  if (state.entries.length === 0) return false;
  if (state.mode === "repeat_all" || state.mode === "shuffle") return true;
  const at = indexOfEntry(state.entries, state.currentEntryId);
  return at < 0 ? state.entries.length > 0 : at < state.entries.length - 1;
}

/** 队列条目上只用于显示的字段（M5 ⑤）：刷新它们不影响身份、来源事实与播放。 */
export type TrackDisplayMetadata = Pick<PlayerTrack, "title" | "artist" | "artistRefs" | "albumTitle" | "albumArtist" | "artworkId">;

function sameRefs(a: PlayerTrack["artistRefs"], b: PlayerTrack["artistRefs"]): boolean {
  const left = a ?? [];
  const right = b ?? [];
  return left.length === right.length && left.every((ref, index) => ref.artist_id === right[index]?.artist_id && ref.name === right[index]?.name && ref.display_name === right[index]?.display_name);
}

function sameMetadata(track: PlayerTrack, patch: TrackDisplayMetadata): boolean {
  return (
    track.title === patch.title &&
    track.artist === patch.artist &&
    track.albumTitle === patch.albumTitle &&
    track.albumArtist === patch.albumArtist &&
    track.artworkId === patch.artworkId &&
    sameRefs(track.artistRefs, patch.artistRefs)
  );
}

/**
 * 按曲目 ID 刷新队列条目的显示元数据（同一首的每个条目都改）：条目 ID、顺序、当前项、历史与随机袋都不变，曲目身份与来源事实不动。
 * 没有任何变化时返回原来的状态（调用方据此不通知界面）。
 */
export function patchTrackMetadata(state: QueueState, patches: ReadonlyMap<string, TrackDisplayMetadata>): QueueState {
  let changed = false;
  const entries = state.entries.map((entry) => {
    const patch = patches.get(entry.track.trackId);
    if (patch === undefined || sameMetadata(entry.track, patch)) return entry;
    changed = true;
    return { ...entry, track: { ...entry.track, ...patch } };
  });
  return changed ? { ...state, entries } : state;
}

export function currentEntry(state: QueueState): QueueEntry | null {
  const at = indexOfEntry(state.entries, state.currentEntryId);
  return state.entries[at] ?? null;
}

/**
 * “接下来”：当前条目之后的条目，最多 limit 个。随机模式下顺序是不确定的，所以返回袋子里还没播放的条目，
 * 并不代表真实的播放顺序；调用方据此写明“随机”。
 */
export function upNext(state: QueueState, limit: number): readonly QueueEntry[] {
  if (state.mode === "shuffle") {
    const entries = new Map(state.entries.map((entry) => [entry.entryId, entry]));
    return state.bag.flatMap((entryId) => {
      const entry = entries.get(entryId);
      return entry === undefined ? [] : [entry];
    }).slice(0, limit);
  }
  const at = indexOfEntry(state.entries, state.currentEntryId);
  return state.entries.slice(at + 1, at + 1 + limit);
}
