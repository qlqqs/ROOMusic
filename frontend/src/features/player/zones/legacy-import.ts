// R11：升级后第一次打开时，把旧版本留在本标签页 sessionStorage 里的队列导入本机播放区（design.md §5.9）。
// 每个标签页在登记完成后读自己的那份；有内容就发 import，请求有了结果（不论服务端是否采用）就删除这个键。
// 服务端只在播放区为空且没导入过时导入，所以多个旧标签页里只有第一个生效。
import { failureOf } from "../../../shared/api";
import { clearSavedQueue, loadSavedQueue, type StorageLike } from "../engine/persistence";
import type { ZoneCommand } from "./api";

export async function importLegacyQueue(storage: StorageLike | null, userId: string, send: (command: ZoneCommand) => Promise<unknown>): Promise<void> {
  const saved = loadSavedQueue(storage, userId);
  if (saved === null) return;
  if (saved.entries.length === 0) {
    clearSavedQueue(storage, userId);
    return;
  }
  try {
    await send({
      command: "import",
      entries: saved.entries.map((entry) => ({ entry_id: entry.entryId, track_id: entry.trackId })),
      current_entry_id: saved.currentEntryId,
      mode: saved.mode,
      position_seconds: saved.positionSeconds,
      history: saved.history,
    });
  } catch (error) {
    // 网络或服务暂时失败：留着，下次打开再导入；服务端明确拒绝（4xx）就不再试。
    const failure = failureOf(error);
    if (failure?.kind !== "http" || failure.status >= 500) return;
  }
  clearSavedQueue(storage, userId);
}
