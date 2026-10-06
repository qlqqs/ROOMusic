import { createContext, useContext, useEffect, useMemo, useSyncExternalStore, type ReactNode } from "react";
import { newIdempotencyKey } from "../../shared/lib/ids";
import { useSessionController, useViewerScope } from "../auth";
import { artworkPath, useReleaseLoader } from "../catalog";
import { fetchTrackPlayback } from "./api/endpoints";
import { browserMediaSession } from "./engine/media-session";
import { PlayerEngine, type PlayerProgress, type PlayerSnapshot } from "./engine/player-engine";
import type { StorageLike } from "./engine/persistence";

const PlayerContext = createContext<PlayerEngine | null>(null);

/** 隐私设置可能让读取 Storage 属性本身就抛错，不能只保护 getItem/setItem。 */
function browserStorage(kind: "sessionStorage" | "localStorage"): StorageLike | null {
  try {
    return window[kind];
  } catch {
    return null;
  }
}

/** 放在按会话代次挂载的已登录子树内。普通卸载保留刷新恢复，会话结束同步销毁用户范围状态。 */
export function PlayerProvider({ userId, children, engine: suppliedEngine }: { userId: string; children: ReactNode; engine?: PlayerEngine }) {
  const controller = useSessionController();
  const { isCurrent } = useViewerScope();
  const loadRelease = useReleaseLoader();
  const engine = useMemo(
    () => suppliedEngine ?? new PlayerEngine({
      userId,
      audio: new Audio(),
      loadPlayback: async (trackId, signal) => (await fetchTrackPlayback(trackId, signal)).data,
      loadRelease,
      sessionStorage: browserStorage("sessionStorage"),
      localStorage: browserStorage("localStorage"),
      mediaSession: browserMediaSession(),
      artworkUrl: (id) => new URL(artworkPath(id), window.location.origin).href,
      newId: newIdempotencyKey,
      random: Math.random,
      now: Date.now,
    }),
    [suppliedEngine, userId, loadRelease],
  );
  useEffect(() => {
    // 请求层的 401、跨标签页注销和换用户都走同一个同步结束事件，不等下一次 React 渲染。
    const unsubscribe = controller.onEnded(() => engine.endSession());
    if (isCurrent()) engine.attach();
    else engine.endSession();
    return () => {
      unsubscribe();
      if (isCurrent()) engine.detach();
      else engine.endSession();
    };
  }, [controller, engine, isCurrent]);
  return <PlayerContext value={engine}>{children}</PlayerContext>;
}

/** 只取引擎发命令，不订阅任何状态：大量重复的组件（例如万首曲目的每一行动作）用它，引擎变化时不重绘。 */
export function usePlayerEngine(): PlayerEngine {
  const engine = useContext(PlayerContext);
  if (engine === null) throw new Error("缺少 PlayerProvider");
  return engine;
}

/**
 * 只订阅快照里的一个派生值：select 的结果不变（按 Object.is 比较）就不重绘，音量、队列等其他变化不影响调用方。
 * select 必须返回基本值或快照里已有的引用，不能每次新建对象或数组，否则每次读取都被当作变化。
 */
export function usePlayerSelector<T>(select: (snapshot: PlayerSnapshot) => T): T {
  const engine = usePlayerEngine();
  return useSyncExternalStore(engine.subscribe, () => select(engine.getSnapshot()));
}

/** 离散状态订阅：进度单独订阅，避免每次 timeupdate 重绘整个队列。 */
export function usePlayer(): { engine: PlayerEngine; snapshot: PlayerSnapshot } {
  const engine = usePlayerEngine();
  const snapshot = useSyncExternalStore(engine.subscribe, engine.getSnapshot);
  return { engine, snapshot };
}

export function usePlayerProgress(): PlayerProgress {
  const engine = usePlayerEngine();
  return useSyncExternalStore(engine.subscribeProgress, engine.getProgress);
}
