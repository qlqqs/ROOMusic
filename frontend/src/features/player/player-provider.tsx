import { useQueryClient } from "@tanstack/react-query";
import { createContext, useContext, useEffect, useMemo, useSyncExternalStore, type ReactNode } from "react";
import { useSessionController, useViewerScope } from "../auth";
import { artworkPath } from "../catalog";
import { listeningKeys } from "../listening";
import { browserMediaSession } from "./engine/media-session";
import type { StorageLike } from "./engine/persistence";
import { browserZoneApi } from "./zones/api";
import { PlayerController, type PlayerProgress, type PlayerSnapshot } from "./zones/controller";
import { browserLocks } from "./zones/device";
import { documentVisibility } from "./zones/event-stream";

const PlayerContext = createContext<PlayerController | null>(null);

/** 隐私设置可能让读取 Storage 属性本身就抛错，不能只保护 getItem/setItem。 */
function browserStorage(kind: "sessionStorage" | "localStorage"): StorageLike | null {
  try {
    return window[kind];
  } catch {
    return null;
  }
}

/**
 * 放在按会话代次挂载的已登录子树内：装配 zone store、事件流、控制器与本标签页的输出端（拿到输出锁时）；切页不重建。
 * 普通卸载与会话结束都停下事件流、释放输出权；队列与状态在服务端，不受影响。
 */
export function PlayerProvider({ userId, children, controller: supplied }: { userId: string; children: ReactNode; controller?: PlayerController }) {
  const session = useSessionController();
  const { generation, isCurrent } = useViewerScope();
  const queryClient = useQueryClient();
  const controller = useMemo(() => {
    if (supplied !== undefined) return supplied;
    return new PlayerController({
      userId,
      api: browserZoneApi,
      localStorage: browserStorage("localStorage"),
      sessionStorage: browserStorage("sessionStorage"),
      locks: browserLocks(),
      createAudio: () => new Audio(),
      mediaSession: browserMediaSession(),
      artworkUrl: (id) => new URL(artworkPath(id), window.location.origin).href,
      userAgent: navigator.userAgent,
      maxTouchPoints: navigator.maxTouchPoints,
      visibility: documentVisibility(),
      now: Date.now,
      // 服务端记下一次播放：本人的播放历史（最近播放、历史页、“播放全部”的候选）一起失效。
      onPlayRecorded: () => void queryClient.invalidateQueries({ queryKey: listeningKeys.all(generation) }),
    });
  }, [supplied, userId, queryClient, generation]);
  useEffect(() => {
    // 请求层的 401、跨标签页注销和换用户都走同一个同步结束事件，不等下一次 React 渲染。
    const unsubscribe = session.onEnded(() => controller.endSession());
    if (isCurrent()) controller.attach();
    else controller.endSession();
    return () => {
      unsubscribe();
      if (isCurrent()) controller.detach();
      else controller.endSession();
    };
  }, [session, controller, isCurrent]);
  return <PlayerContext value={controller}>{children}</PlayerContext>;
}

/** 只取控制器发命令，不订阅任何状态：大量重复的组件（例如万首曲目的每一行动作）用它，状态变化时不重绘。 */
export function usePlayerEngine(): PlayerController {
  const controller = useContext(PlayerContext);
  if (controller === null) throw new Error("缺少 PlayerProvider");
  return controller;
}

/**
 * 只订阅快照里的一个派生值：select 的结果不变（按 Object.is 比较）就不重绘，音量、队列等其他变化不影响调用方。
 * select 必须返回基本值或快照里已有的引用，不能每次新建对象或数组，否则每次读取都被当作变化。
 */
export function usePlayerSelector<T>(select: (snapshot: PlayerSnapshot) => T): T {
  const controller = usePlayerEngine();
  return useSyncExternalStore(controller.subscribe, () => select(controller.getSnapshot()));
}

/** 离散状态订阅：进度单独订阅，避免每次进度刷新重绘整个队列。 */
export function usePlayer(): { engine: PlayerController; snapshot: PlayerSnapshot } {
  const engine = usePlayerEngine();
  const snapshot = useSyncExternalStore(engine.subscribe, engine.getSnapshot);
  return { engine, snapshot };
}

export function usePlayerProgress(): PlayerProgress {
  const controller = usePlayerEngine();
  return useSyncExternalStore(controller.subscribeProgress, controller.getProgress);
}

/**
 * 只订阅进度里的一个派生值（例如歌词的“当前组下标”）：select 的结果不变（按 Object.is 比较）就不重绘，
 * 约每秒 4 次的进度刷新不会让大列表跟着重绘。select 必须返回基本值或进度里已有的引用。
 */
export function usePlayerProgressSelector<T>(select: (progress: PlayerProgress) => T): T {
  const controller = usePlayerEngine();
  return useSyncExternalStore(controller.subscribeProgress, () => select(controller.getProgress()));
}
