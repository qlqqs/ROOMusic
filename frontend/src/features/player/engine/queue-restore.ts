// 刷新后恢复队列：按发行重新读取详情（并发 4），用详情重建保存的条目。已经不存在的曲目丢弃并报告数量；
// 只有明确的 404 才能证明发行已不存在，网络与服务失败让恢复整体可重试，不能删掉保存的队列。
import { failureOf } from "../../../shared/api";
import type { ReleaseDetailDTO, ReleaseTrackDTO } from "../../catalog";
import type { SavedQueue } from "./persistence";
import type { QueueEntry, QueueState } from "./queue";
import { playerTrackOf } from "./tracks";

export const RESTORE_CONCURRENCY = 4;

export type SavedReleases = {
  releases: ReadonlyMap<string, ReleaseDetailDTO>;
  /** 有发行因为 404 以外的原因没读到：恢复结果不可信，应整体重试。 */
  transientFailure: boolean;
};

/**
 * 读取保存的条目用到的发行（去重），最多 RESTORE_CONCURRENCY 个同时在途。每次读取之前检查 shouldStop，
 * 为真就不再发出新的读取（已在途的照常完成）。
 */
export async function loadSavedReleases(
  entries: SavedQueue["entries"],
  loadRelease: (releaseId: string) => Promise<ReleaseDetailDTO>,
  shouldStop: () => boolean,
): Promise<SavedReleases> {
  const releaseIds = [...new Set(entries.map((entry) => entry.releaseId))];
  const releases = new Map<string, ReleaseDetailDTO>();
  let next = 0;
  let transientFailure = false;
  const worker = async (): Promise<void> => {
    for (let index = next++; index < releaseIds.length; index = next++) {
      const releaseId = releaseIds[index];
      if (shouldStop()) return;
      if (releaseId === undefined) continue;
      try {
        releases.set(releaseId, await loadRelease(releaseId));
      } catch (error) {
        const failure = failureOf(error);
        // 只有明确的 404 才能证明已不存在；网络/服务失败不能删除已保存的队列。
        if (failure?.kind !== "http" || failure.status !== 404) transientFailure = true;
      }
    }
  };
  await Promise.all(Array.from({ length: Math.min(RESTORE_CONCURRENCY, releaseIds.length) }, worker));
  return { releases, transientFailure };
}

export type RestoredQueue = {
  queue: QueueState;
  /** 恢复后的开始位置：当前条目保留下来时是保存的位置，被丢弃（换成后继）或没有当前条目时从头开始。 */
  positionSeconds: number;
  dropped: number;
};

/**
 * 用读到的发行详情重建保存的队列：发行或曲目已经不存在的条目丢弃，条目 ID 与组 ID 沿用保存的。
 * 当前条目被丢弃时，取它之后第一个保留下来的条目（位置从头开始）；历史只留保留下来的条目；随机模式的袋子是历史与当前项以外的条目。
 */
export function rebuildQueue(saved: SavedQueue, releases: ReadonlyMap<string, ReleaseDetailDTO>): RestoredQueue {
  const trackMaps = new Map<string, Map<string, ReleaseTrackDTO>>();
  for (const [releaseId, release] of releases) {
    trackMaps.set(releaseId, new Map(release.media.flatMap((medium) => medium.tracks.map((track) => [track.id, track] as const))));
  }
  const entries: QueueEntry[] = [];
  let dropped = 0;
  for (const item of saved.entries) {
    const release = releases.get(item.releaseId);
    const track = trackMaps.get(item.releaseId)?.get(item.trackId);
    if (release === undefined || track === undefined) {
      dropped += 1;
      continue;
    }
    entries.push({ entryId: item.entryId, track: playerTrackOf(release, track, item.groupId) });
  }
  const survivors = new Set(entries.map((entry) => entry.entryId));
  const savedIds = saved.entries.map((entry) => entry.entryId);
  const currentSurvived = saved.currentEntryId !== null && survivors.has(saved.currentEntryId);
  const successor = saved.currentEntryId === null ? undefined : savedIds.slice(savedIds.indexOf(saved.currentEntryId) + 1).find((id) => survivors.has(id));
  const currentEntryId = currentSurvived ? saved.currentEntryId : (successor ?? null);
  const history = saved.history.filter((id) => survivors.has(id));
  const exclude = new Set([...history, ...(currentEntryId === null ? [] : [currentEntryId])]);
  return {
    queue: {
      entries,
      currentEntryId,
      mode: saved.mode,
      history,
      bag: saved.mode === "shuffle" ? entries.filter((entry) => !exclude.has(entry.entryId)).map((entry) => entry.entryId) : [],
    },
    positionSeconds: currentSurvived ? saved.positionSeconds : 0,
    dropped,
  };
}
