// 旧版本（浏览器本地播放）留在浏览器里的播放状态，只读不写（合同 §8）：
// - 队列：sessionStorage，按账号命名，只存标识；升级后第一次打开时读一次导入本机播放区（R11），之后删除。
// - 音量与静音：localStorage 的设备偏好，只在登记设备时读作初值，不删除。
import { decode as d } from "../../../shared/api";
import { MAX_QUEUE_ENTRIES, playModes, type PlayMode } from "./queue";

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
 * 读取旧版本保存的队列。没有、不是合法 JSON、版本未知、字段不合法、条目 ID 重复或当前条目不在队列里，都返回 null，
 * 并删除这份坏数据。历史里不在队列里的条目 ID 被丢掉。存储不可用时返回 null。
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
