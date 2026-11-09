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
