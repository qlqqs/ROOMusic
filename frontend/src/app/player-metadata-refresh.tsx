import { createContext, useCallback, useContext, useEffect, useRef, type ReactNode } from "react";
import { useViewerScope } from "../features/auth";
import { fetchTrackSummaries, MAX_TRACK_SUMMARY_BATCH, useCatalogState } from "../features/catalog";
import { usePlayerEngine, type TrackDisplayMetadata } from "../features/player";
import { playerTrackOfSummary } from "./routes/track-playback";

const RefreshContext = createContext<() => void>(() => {});

/** 刷新播放器里已在队列的曲目的显示元数据（艺人治理成功之后由 app 调用）。没有范围时什么也不做。 */
export function usePlayerMetadataRefresh(): () => void {
  return useContext(RefreshContext);
}

/** 要刷新的曲目：从当前曲目开始往后，再接前面的，去重后最多 1,000 首。 */
function refreshTrackIds(entries: readonly { track: { trackId: string } }[], currentIndex: number): string[] {
  const ordered = currentIndex < 0 ? entries : [...entries.slice(currentIndex), ...entries.slice(0, currentIndex)];
  const ids: string[] = [];
  const seen = new Set<string>();
  for (const { track } of ordered) {
    if (ids.length >= MAX_TRACK_SUMMARY_BATCH) break;
    const key = track.trackId.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    ids.push(track.trackId);
  }
  return ids;
}

/**
 * 有界刷新播放器队列里的显示元数据（M5 ⑤，I4）：艺人数据变为就绪（或已就绪时 epoch 变大）、艺人治理成功之后，
 * 从当前曲目起往后再到前面，去重后最多 1,000 首，一次批量读取曲目摘要（POST /tracks/summaries），
 * 只把标题、艺人与艺人链接、专辑名与封面换成新的；不改顺序、当前位置、播放状态与进度，不重新加载音频。
 * 读不到的曲目（不在结果里、请求失败）保持原样；新一轮开始后旧一轮的结果作废。player 不导入 catalog 的界面层，所以由 app 驱动；控制器只修补 store 里的显示数据，下一次服务端状态照常覆盖。
 */
export function PlayerMetadataRefresh({ children }: { children: ReactNode }) {
  const engine = usePlayerEngine();
  const scope = useViewerScope();
  const state = useCatalogState().data;
  const runRef = useRef(0);

  const refresh = useCallback(() => {
    const snapshot = engine.getSnapshot();
    const entries = snapshot.queue.entries;
    if (entries.length === 0) return;
    const ids = refreshTrackIds(
      entries,
      entries.findIndex((entry) => entry.entryId === snapshot.queue.currentEntryId),
    );
    runRef.current += 1;
    const run = runRef.current;
    fetchTrackSummaries(ids).then(
      ({ data }) => {
        if (run !== runRef.current || !scope.isCurrent()) return;
        const patches = new Map<string, TrackDisplayMetadata>();
        for (const item of data.items) {
          const { title, artist, artistRefs, albumTitle, albumArtist, artworkId } = playerTrackOfSummary(item);
          patches.set(item.track_id, { title, artist, artistRefs: artistRefs ?? [], albumTitle, albumArtist, artworkId });
        }
        if (patches.size > 0) engine.updateTrackMetadata(patches);
      },
      () => {
        // 读不到就保持原样：这只是显示的补全，不打扰播放。
      },
    );
  }, [engine, scope]);

  // 艺人数据变为就绪，或已就绪时 epoch 变大：刷新一次。第一次读到状态时不刷新（队列是按当时的数据建立或恢复的）。
  const previous = useRef<{ artists: string; epoch: number } | null>(null);
  useEffect(() => {
    if (state === undefined) return;
    const before = previous.current;
    previous.current = { artists: state.artists_state, epoch: state.epoch };
    if (before === null || state.artists_state !== "ready") return;
    if (before.artists !== "ready" || state.epoch > before.epoch) refresh();
  }, [state, refresh]);

  return <RefreshContext value={refresh}>{children}</RefreshContext>;
}
