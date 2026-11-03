// 浏览器里保存的播放状态。两处都带版本、边界校验，损坏、未知版本与存储不可用都能恢复，写失败不声称“已保存”：
// - 队列：sessionStorage（只在本标签页、刷新后仍在、关闭标签页即消失，多个标签页互不覆盖），按账号命名，只存标识，
//   不存标题、路径或凭据（不把私有服务器数据放进浏览器持久层）；会话结束时删除。
// - 音量与静音：localStorage，是设备偏好，不含任何私有数据，不随会话清除。
import { decode as d } from "../../../shared/api";
import { MAX_QUEUE_ENTRIES, playModes, type PlayMode, type QueueEntry } from "./queue";

export type StorageLike = Pick<Storage, "getItem" | "setItem" | "removeItem">;

export const QUEUE_FORMAT_VERSION = 1;
export const VOLUME_STORAGE_KEY = "roomusic:player-volume:v1";

/** 队列的存储键：带账号，换用户后读不到上一个人的队列。 */
export function queueStorageKey(userId: string): string {
  return `roomusic:player:v${QUEUE_FORMAT_VERSION}:${userId}`;
}

/** 保存的历史条数上限（与队列模块一致）。 */
const MAX_SAVED_HISTORY = 500;

export type SavedQueue = {
  entries: readonly { entryId: string; trackId: string; releaseId: string; groupId: string }[];
  currentEntryId: string | null;
  mode: PlayMode;
  positionSeconds: number;
  history: readonly string[];
};

const entryIdDecoder = d.string({ min: 1, max: 64 });

const savedQueueDecoder = d.defineDecoder(
  d.object({
    version: d.literal(QUEUE_FORMAT_VERSION),
    entries: d.array(
      d.object({ entryId: entryIdDecoder, trackId: d.uuid(), releaseId: d.uuid(), groupId: d.uuid() }),
      { max: MAX_QUEUE_ENTRIES },
    ),
    currentEntryId: d.nullable(entryIdDecoder),
    mode: d.oneOf(playModes),
    positionSeconds: d.number({ min: 0, max: 10_000_000 }),
    history: d.array(entryIdDecoder, { max: MAX_SAVED_HISTORY }),
  }),
);

/**
 * 读取保存的队列。没有、不是合法 JSON、版本未知、字段不合法、条目 ID 重复或当前条目不在队列里，都返回 null，
 * 并删除这份坏数据（下一次保存会写入新的）。历史里不在队列里的条目 ID 被丢掉。存储不可用时返回 null。
 */
export function loadSavedQueue(storage: StorageLike | null, userId: string): SavedQueue | null {
  if (storage === null) return null;
  const key = queueStorageKey(userId);
  let text: string | null;
  try {
    text = storage.getItem(key);
  } catch {
    return null;
  }
  if (text === null) return null;
  try {
    const saved = savedQueueDecoder(JSON.parse(text));
    const ids = new Set(saved.entries.map((entry) => entry.entryId));
    if (ids.size !== saved.entries.length) throw new Error("条目 ID 重复");
    if (saved.currentEntryId !== null && !ids.has(saved.currentEntryId)) throw new Error("当前条目不在队列里");
    return {
      entries: saved.entries,
      currentEntryId: saved.currentEntryId,
      mode: saved.mode,
      positionSeconds: saved.positionSeconds,
      history: saved.history.filter((entryId) => ids.has(entryId)),
    };
  } catch {
    clearSavedQueue(storage, userId);
    return null;
  }
}

/** 队列条目数组 → 要保存的条目标识，按数组引用缓存：队列没变时（播放中定时保存只有位置在变）不重建上万个条目。 */
const savedEntriesCache = new WeakMap<readonly QueueEntry[], SavedQueue["entries"]>();

/** 队列条目 → 要保存的条目标识（不含标题等曲目信息）。同一个条目数组返回同一个结果数组，saveQueue 据此复用条目的 JSON。 */
export function savedEntriesOf(source: readonly QueueEntry[]): SavedQueue["entries"] {
  let entries = savedEntriesCache.get(source);
  if (entries === undefined) {
    entries = source.map((entry) => ({ entryId: entry.entryId, trackId: entry.track.trackId, releaseId: entry.track.releaseId, groupId: entry.track.groupId }));
    savedEntriesCache.set(source, entries);
  }
  return entries;
}

/** 条目数组 → 它的 JSON。队列没变时引擎传入同一个数组，播放中每 5 秒的保存就不必再序列化上万个条目。 */
const entriesJson = new WeakMap<SavedQueue["entries"], string>();

/** 保存队列；空队列直接删除。返回是否写入成功：存储不可用或已满时返回 false，调用方不能因此提示“已保存”。 */
export function saveQueue(storage: StorageLike | null, userId: string, saved: SavedQueue): boolean {
  if (storage === null) return false;
  try {
    if (saved.entries.length === 0) {
      storage.removeItem(queueStorageKey(userId));
      return true;
    }
    let entries = entriesJson.get(saved.entries);
    if (entries === undefined) {
      entries = JSON.stringify(saved.entries);
      entriesJson.set(saved.entries, entries);
    }
    const head = JSON.stringify({ version: QUEUE_FORMAT_VERSION, currentEntryId: saved.currentEntryId, mode: saved.mode, positionSeconds: saved.positionSeconds, history: saved.history });
    // 把缓存的条目拼进同一个 JSON 对象：head 以“}”结尾且至少有 version 一个字段，结果与整体 JSON.stringify 等价。
    storage.setItem(queueStorageKey(userId), `${head.slice(0, -1)},"entries":${entries}}`);
    return true;
  } catch {
    return false;
  }
}

export function clearSavedQueue(storage: StorageLike | null, userId: string): void {
  try {
    storage?.removeItem(queueStorageKey(userId));
  } catch {
    // 存储不可用时没有可删的东西。
  }
}

export type VolumeSetting = { volume: number; muted: boolean };
export const defaultVolume: VolumeSetting = { volume: 1, muted: false };

const volumeDecoder = d.defineDecoder(
  d.object({ version: d.literal(1), volume: d.number({ min: 0, max: 1 }), muted: d.boolean() }),
);

/** 读取音量偏好；没有、损坏或存储不可用时用默认值。 */
export function loadVolume(storage: StorageLike | null): VolumeSetting {
  if (storage === null) return defaultVolume;
  try {
    const text = storage.getItem(VOLUME_STORAGE_KEY);
    if (text === null) return defaultVolume;
    const saved = volumeDecoder(JSON.parse(text));
    return { volume: saved.volume, muted: saved.muted };
  } catch {
    return defaultVolume;
  }
}

export function saveVolume(storage: StorageLike | null, setting: VolumeSetting): boolean {
  if (storage === null) return false;
  try {
    storage.setItem(VOLUME_STORAGE_KEY, JSON.stringify({ version: 1, ...setting }));
    return true;
  } catch {
    return false;
  }
}
