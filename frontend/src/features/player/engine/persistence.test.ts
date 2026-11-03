import { describe, expect, it } from "vitest";
import { memoryStorage } from "../../../test/memory-storage";
import { playerTrack } from "../../../test/player-data";
import {
  clearSavedQueue,
  defaultVolume,
  loadSavedQueue,
  loadVolume,
  queueStorageKey,
  savedEntriesOf,
  saveQueue,
  saveVolume,
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

describe("队列的保存与恢复", () => {
  it("保存后能原样读回，键带账号", () => {
    const storage = memoryStorage();
    expect(saveQueue(storage, userId, saved)).toBe(true);
    expect([...storage.data.keys()]).toEqual([queueStorageKey(userId)]);
    expect(loadSavedQueue(storage, userId)).toEqual(saved);
    expect(loadSavedQueue(storage, otherUserId)).toBeNull();
  });

  it("只保存标识、模式与位置，不保存标题、路径或凭据", () => {
    const storage = memoryStorage();
    saveQueue(storage, userId, saved);
    const text = storage.data.get(queueStorageKey(userId)) ?? "";
    expect(Object.keys(JSON.parse(text) as object).sort()).toEqual(["currentEntryId", "entries", "history", "mode", "positionSeconds", "version"]);
    expect(Object.keys((JSON.parse(text) as { entries: object[] }).entries[0] ?? {}).sort()).toEqual(["entryId", "groupId", "releaseId", "trackId"]);
  });

  it("同一个条目数组再次保存时复用条目的 JSON，结果与整体序列化等价", () => {
    const storage = memoryStorage();
    saveQueue(storage, userId, saved);
    const moved = { ...saved, positionSeconds: 120 };
    expect(saveQueue(storage, userId, moved)).toBe(true);
    expect(JSON.parse(storage.data.get(queueStorageKey(userId)) ?? "null")).toEqual({ version: 1, ...moved });
    expect(loadSavedQueue(storage, userId)).toEqual(moved);
  });

  it("空队列删除保存的数据；clearSavedQueue 删除保存的队列", () => {
    const storage = memoryStorage();
    saveQueue(storage, userId, saved);
    expect(saveQueue(storage, userId, { ...saved, entries: [], currentEntryId: null, history: [] })).toBe(true);
    expect(storage.data.size).toBe(0);
    saveQueue(storage, userId, saved);
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

  it("存储不可用（为 null、读写都抛错）时恢复为没有保存的队列，写入失败不声称已保存", () => {
    expect(loadSavedQueue(null, userId)).toBeNull();
    expect(saveQueue(null, userId, saved)).toBe(false);
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
    expect(saveQueue(broken, userId, saved)).toBe(false);
    expect(() => clearSavedQueue(broken, userId)).not.toThrow();
    expect(() => clearSavedQueue(null, userId)).not.toThrow();
  });

  it("要保存的条目只含标识；同一个条目数组得到同一个结果，数组变了才重建", () => {
    const entries = [{ entryId: "e1", track: playerTrack("a", { trackId: trackA, releaseId: release, groupId: group }) }];
    const ids = savedEntriesOf(entries);
    expect(ids).toEqual([{ entryId: "e1", trackId: trackA, releaseId: release, groupId: group }]);
    expect(savedEntriesOf(entries)).toBe(ids);
    const next = [...entries];
    expect(savedEntriesOf(next)).not.toBe(ids);
    expect(savedEntriesOf(next)).toEqual(ids);
  });
});

describe("音量偏好", () => {
  it("保存后读回；没有、损坏、越界或存储不可用时用默认值", () => {
    const storage = memoryStorage();
    expect(loadVolume(storage)).toEqual(defaultVolume);
    expect(saveVolume(storage, { volume: 0.4, muted: true })).toBe(true);
    expect(loadVolume(storage)).toEqual({ volume: 0.4, muted: true });
    expect(loadVolume(memoryStorage({ [VOLUME_STORAGE_KEY]: "坏" }))).toEqual(defaultVolume);
    expect(loadVolume(memoryStorage({ [VOLUME_STORAGE_KEY]: JSON.stringify({ version: 1, volume: 7, muted: false }) }))).toEqual(defaultVolume);
    expect(loadVolume(memoryStorage({ [VOLUME_STORAGE_KEY]: JSON.stringify({ version: 2, volume: 0.5, muted: false }) }))).toEqual(defaultVolume);
    expect(loadVolume(null)).toEqual(defaultVolume);
    expect(saveVolume(null, defaultVolume)).toBe(false);
  });
});
