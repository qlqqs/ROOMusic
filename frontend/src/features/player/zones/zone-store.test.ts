import { expect, it, vi } from "vitest";
import { waitFor } from "@testing-library/react";
import { fakeZoneServer } from "../../../test/fake-zones";
import { playerTrack } from "../../../test/player-data";
import { ZoneStore } from "./zone-store";

it("队列增量序号不连续时整段重读，不把缺失的增量跳过去", async () => {
  const server = fakeZoneServer();
  const zone = server.zone();
  const entries = ["一", "二", "三"].map((name) => ({ entryId: name, track: playerTrack(name) }));
  const fetchQueue = vi.fn()
    .mockResolvedValueOnce({ zoneId: zone.zoneId, queueRevision: 1, entries: entries.slice(0, 1) })
    .mockResolvedValueOnce({ zoneId: zone.zoneId, queueRevision: 3, entries });
  const store = new ZoneStore({ fetchQueue, now: Date.now });
  try {
    store.applySnapshot({ serverTime: Date.now(), devices: [server.device()], zones: [{ ...zone, queueRevision: 1 }] });
    store.watchQueue(zone.zoneId);
    await waitFor(() => expect(store.getState().queues.get(zone.zoneId)?.queueRevision).toBe(1));
    store.applyQueueChange(zone.zoneId, { kind: "insert", queueRevision: 3, index: 2, entries: entries.slice(2) });
    await waitFor(() => expect(store.getState().queues.get(zone.zoneId)).toEqual({ zoneId: zone.zoneId, queueRevision: 3, entries }));
    expect(fetchQueue.mock.calls).toEqual([[zone.zoneId], [zone.zoneId]]);
  } finally {
    store.dispose();
  }
});


it.each([
  ["移到首位", "四", 0, ["四", "一", "二", "三"]],
  ["移到末位", "一", 3, ["二", "三", "四", "一"]],
  ["向后移到中间", "一", 2, ["二", "三", "一", "四"]],
  ["向前移到中间", "四", 1, ["一", "四", "二", "三"]],
  ["当前位置不变", "二", 1, ["一", "二", "三", "四"]],
] as const)("移动增量%s采用取出再插入语义，不交换两项", async (_label, entryId, toIndex, expected) => {
  const server = fakeZoneServer();
  const zone = server.zone();
  const entries = ["一", "二", "三", "四"].map((name) => ({ entryId: name, track: playerTrack(name) }));
  const fetchQueue = vi.fn().mockResolvedValue({ zoneId: zone.zoneId, queueRevision: 1, entries });
  const store = new ZoneStore({ fetchQueue, now: Date.now });
  try {
    store.applySnapshot({ serverTime: Date.now(), devices: [server.device()], zones: [{ ...zone, queueRevision: 1, current: entries[1] ?? null }] });
    store.watchQueue(zone.zoneId);
    await waitFor(() => expect(store.getState().queues.get(zone.zoneId)?.queueRevision).toBe(1));
    store.applyQueueChange(zone.zoneId, { kind: "move", queueRevision: 2, entryId, toIndex });
    const queue = store.getState().queues.get(zone.zoneId);
    expect(queue?.entries.map((entry) => entry.entryId)).toEqual(expected);
    expect(queue?.queueRevision).toBe(2);
    expect(entries.map((entry) => entry.entryId)).toEqual(["一", "二", "三", "四"]);
    expect(store.zone(zone.zoneId)?.current?.entryId).toBe("二");
    expect(fetchQueue).toHaveBeenCalledTimes(1);
  } finally {
    store.dispose();
  }
});

it.each([
  ["目标等于队列长度", "一", 3],
  ["目标超过队列长度", "一", 10],
  ["条目已不存在", "不存在", 0],
] as const)("移动增量%s时重读服务端队列", async (_label, entryId, toIndex) => {
  const server = fakeZoneServer();
  const zone = server.zone();
  const entries = ["一", "二", "三"].map((name) => ({ entryId: name, track: playerTrack(name) }));
  const restored = [entries[2], entries[0]].filter((entry) => entry !== undefined);
  const fetchQueue = vi.fn()
    .mockResolvedValueOnce({ zoneId: zone.zoneId, queueRevision: 1, entries })
    .mockResolvedValueOnce({ zoneId: zone.zoneId, queueRevision: 2, entries: restored });
  const store = new ZoneStore({ fetchQueue, now: Date.now });
  try {
    store.applySnapshot({ serverTime: Date.now(), devices: [server.device()], zones: [{ ...zone, queueRevision: 1 }] });
    store.watchQueue(zone.zoneId);
    await waitFor(() => expect(store.getState().queues.get(zone.zoneId)?.queueRevision).toBe(1));
    store.applyQueueChange(zone.zoneId, { kind: "move", queueRevision: 2, entryId, toIndex });
    await waitFor(() => expect(store.getState().queues.get(zone.zoneId)).toEqual({ zoneId: zone.zoneId, queueRevision: 2, entries: restored }));
    expect(fetchQueue.mock.calls).toEqual([[zone.zoneId], [zone.zoneId]]);
  } finally {
    store.dispose();
  }
});
