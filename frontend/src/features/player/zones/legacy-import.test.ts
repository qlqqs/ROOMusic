import { expect, it, vi } from "vitest";
import { fakeZoneServer } from "../../../test/fake-zones";
import { memoryStorage } from "../../../test/memory-storage";
import { queueStorageKey } from "../engine/persistence";
import type { ZoneCommand } from "./api";
import { importLegacyQueue } from "./legacy-import";

it("旧队列导入服务端后删除当前用户的 sessionStorage 键", async () => {
  const userId = "00000000-0000-4000-8000-000000000003";
  const trackId = "00000000-0000-4000-8000-000000000004";
  const saved = {
    version: 1,
    entries: [{ entryId: "legacy-1", trackId, releaseId: "00000000-0000-4000-8000-000000000005", groupId: "00000000-0000-4000-8000-000000000006" }],
    currentEntryId: "legacy-1", mode: "repeat_all", positionSeconds: 12, history: ["legacy-1"],
  };
  const storage = memoryStorage({ [queueStorageKey(userId)]: JSON.stringify(saved) });
  const server = fakeZoneServer();
  const send = vi.fn(async (command: ZoneCommand) => {
    expect(storage.getItem(queueStorageKey(userId))).not.toBeNull();
    return server.api.sendZoneCommand(server.zone().zoneId, command);
  });
  await importLegacyQueue(storage, userId, send);
  expect(send).toHaveBeenCalledExactlyOnceWith({
    command: "import", entries: [{ entry_id: "legacy-1", track_id: trackId }], current_entry_id: "legacy-1",
    mode: "repeat_all", position_seconds: 12, history: ["legacy-1"],
  });
  expect(server.queueIds()).toEqual([trackId]);
  expect(storage.getItem(queueStorageKey(userId))).toBeNull();
});
