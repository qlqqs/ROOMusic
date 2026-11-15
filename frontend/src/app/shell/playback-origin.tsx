import { createContext, useContext, useMemo, useState, type ReactNode } from "react";
import { usePlayerSelector, type PlayerSnapshot } from "../../features/player";

/**
 * 侧栏“正在播放”标记的依据：播放区快照里没有“从哪个歌单放的”，只能由本页记下最近一次“播放全部”的来源与放进去的曲目。
 * key 是 "favorites"（我喜欢的音乐）或 "playlist:<歌单 ID>"。
 */
export type PlaybackOrigin = { key: string; trackIds: readonly string[] };

type OriginContextValue = { origin: PlaybackOrigin | null; record: (origin: PlaybackOrigin) => void };

// 没有外壳时（单独渲染页面的测试）不记录，标记也不出现。
const OriginContext = createContext<OriginContextValue>({ origin: null, record: () => undefined });

/** 外壳挂一次；按会话代次重新挂载，换用户时随之清空。 */
export function PlaybackOriginProvider({ children }: { children: ReactNode }) {
  const [origin, setOrigin] = useState<PlaybackOrigin | null>(null);
  const value = useMemo(() => ({ origin, record: setOrigin }), [origin]);
  return <OriginContext.Provider value={value}>{children}</OriginContext.Provider>;
}

/** “播放全部”替换队列之后调用，记下来源与放进去的曲目（按顺序）。 */
export function useRecordPlaybackOrigin(): (origin: PlaybackOrigin) => void {
  return useContext(OriginContext).record;
}

/**
 * 当前曲目是否来自记下的来源：队列开头仍是当时放进去的那些曲目（顺序相同），当前条目落在这一段里。
 * 之后放了别的专辑、在这一段里插入或删除了曲目，或在别的设备上换了队列，都不再算，不会错标。
 */
function originMatches(snapshot: PlayerSnapshot, origin: PlaybackOrigin): boolean {
  const { entries, currentEntryId } = snapshot.queue;
  const count = origin.trackIds.length;
  if (currentEntryId === null || entries.length < count) return false;
  for (let index = 0; index < count; index += 1) {
    if (entries[index]?.track.trackId !== origin.trackIds[index]) return false;
  }
  const current = entries.findIndex((entry) => entry.entryId === currentEntryId);
  return current >= 0 && current < count;
}

/** 正在播放的来源（key）与是否真的在播放（暂停、加载时音柱静止）；不是记下的来源时 key 为 null。 */
export function useNowPlayingOrigin(): { key: string | null; playing: boolean } {
  const { origin } = useContext(OriginContext);
  const key = usePlayerSelector((snapshot) => (origin !== null && originMatches(snapshot, origin) ? origin.key : null));
  const playing = usePlayerSelector((snapshot) => snapshot.status === "playing");
  return { key, playing };
}
