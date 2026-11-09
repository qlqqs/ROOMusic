import { describe, expect, it } from "vitest";
import { memoryStorage } from "../../../test/memory-storage";
import {
  clearSavedQueue,
  defaultVolume,
  loadSavedQueue,
  loadVolume,
  queueStorageKey,
  VOLUME_STORAGE_KEY,
  type SavedQueue,
  type StorageLike,
} from "./persistence";

const userId = "00000000-0000-4000-8000-000000000002";
const otherUserId = "00000000-0000-4000-8000-000000000003";
const trackA = "00000000-0000-4000-8000-0000000000a1";
const trackB = "00000000-0000-4000-8000-0000000000a2";
const release = "00000000-0000-4000-8000-0000000000b1";
const group = "00000000-0000-4000-8000-0000000000c1";

const saved: SavedQueue = {
  entries: [
    { entryId: "e1", trackId: trackA, releaseId: release, groupId: group },
    { entryId: "e2", trackId: trackB, releaseId: release, groupId: group },
  ],
  currentEntryId: "e2",
  mode: "shuffle",
  positionSeconds: 83.5,
  history: ["e1"],
};

const stored = (queue: SavedQueue) => JSON.stringify({ version: 1, ...queue });

describe("旧版本保存的队列", () => {
  it("原样读回，键带账号；clearSavedQueue 删除它", () => {
    const storage = memoryStorage({ [queueStorageKey(userId)]: stored(saved) });
    expect(loadSavedQueue(storage, userId)).toEqual(saved);
    expect(loadSavedQueue(storage, otherUserId)).toBeNull();
    clearSavedQueue(storage, userId);
    expect(storage.data.size).toBe(0);
  });

  it.each([
    ["不是 JSON", "{坏掉"],
    ["未知版本", JSON.stringify({ ...saved, version: 2 })],
    ["缺少版本", JSON.stringify(saved)],
    ["未知播放模式", JSON.stringify({ version: 1, ...saved, mode: "party" })],
    ["曲目标识不是 UUID", JSON.stringify({ version: 1, ...saved, entries: [{ entryId: "e1", trackId: "x", releaseId: release, groupId: group }] })],
    ["位置为负数", JSON.stringify({ version: 1, ...saved, positionSeconds: -1 })],
    ["条目 ID 重复", JSON.stringify({ version: 1, ...saved, entries: [saved.entries[0], saved.entries[0]] })],
    ["当前条目不在队列里", JSON.stringify({ version: 1, ...saved, currentEntryId: "gone" })],
    ["条目数超过上限", JSON.stringify({ version: 1, ...saved, entries: Array.from({ length: 10_001 }, (_, index) => ({ entryId: `e${index}`, trackId: trackA, releaseId: release, groupId: group })), currentEntryId: null, history: [] })],
  ])("损坏的数据被丢弃并删除：%s", (_name, text) => {
    const storage = memoryStorage({ [queueStorageKey(userId)]: text });
    expect(loadSavedQueue(storage, userId)).toBeNull();
    expect(storage.data.has(queueStorageKey(userId))).toBe(false);
  });

  it("历史里不在队列里的条目被丢掉，其余保留", () => {
    const storage = memoryStorage({ [queueStorageKey(userId)]: JSON.stringify({ version: 1, ...saved, history: ["gone", "e1"] }) });
    expect(loadSavedQueue(storage, userId)?.history).toEqual(["e1"]);
  });

  it("存储不可用（为 null、读写都抛错）时当作没有保存的队列", () => {
    expect(loadSavedQueue(null, userId)).toBeNull();
    const broken: StorageLike = {
      getItem: () => {
        throw new Error("SecurityError");
      },
      setItem: () => {
        throw new Error("QuotaExceededError");
      },
      removeItem: () => {
        throw new Error("SecurityError");
      },
    };
    expect(loadSavedQueue(broken, userId)).toBeNull();
    expect(() => clearSavedQueue(broken, userId)).not.toThrow();
    expect(() => clearSavedQueue(null, userId)).not.toThrow();
  });
});

describe("音量偏好", () => {
  it("读回旧版本的偏好；没有、损坏、越界或存储不可用时用默认值", () => {
    expect(loadVolume(memoryStorage())).toEqual(defaultVolume);
    expect(loadVolume(memoryStorage({ [VOLUME_STORAGE_KEY]: JSON.stringify({ version: 1, volume: 0.4, muted: true }) }))).toEqual({ volume: 0.4, muted: true });
    expect(loadVolume(memoryStorage({ [VOLUME_STORAGE_KEY]: "坏" }))).toEqual(defaultVolume);
    expect(loadVolume(memoryStorage({ [VOLUME_STORAGE_KEY]: JSON.stringify({ version: 1, volume: 7, muted: false }) }))).toEqual(defaultVolume);
    expect(loadVolume(memoryStorage({ [VOLUME_STORAGE_KEY]: JSON.stringify({ version: 2, volume: 0.5, muted: false }) }))).toEqual(defaultVolume);
    expect(loadVolume(null)).toEqual(defaultVolume);
  });
});
