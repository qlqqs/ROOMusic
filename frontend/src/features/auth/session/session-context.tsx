import { createContext, useContext, useMemo, useSyncExternalStore, type ReactNode } from "react";
import type { ViewerDTO } from "../api/decoders";
import type { SessionController, SessionState } from "./session-controller";

const ControllerContext = createContext<SessionController | null>(null);

export function SessionProvider({ controller, children }: { controller: SessionController; children: ReactNode }) {
  return <ControllerContext value={controller}>{children}</ControllerContext>;
}

export function useSessionController(): SessionController {
  const controller = useContext(ControllerContext);
  if (controller === null) throw new Error("缺少 SessionProvider");
  return controller;
}

/** 当前会话状态；控制器是普通对象，经 useSyncExternalStore 订阅。 */
export function useSession(): SessionState {
  const controller = useSessionController();
  return useSyncExternalStore(controller.subscribe, controller.getSnapshot);
}

/** 已登录界面内的会话范围：当前查看者、会话代次，以及“发起操作时的会话是否仍是当前会话”。 */
export type ViewerScope = {
  viewer: ViewerDTO;
  generation: number;
  /**
   * 写操作发起时的会话已被结束或替换（退出、失效、换用户）时返回 false：
   * 迟到的成功或失败回调不能更新界面，也不能导航。
   */
  isCurrent: () => boolean;
};

const ScopeContext = createContext<ViewerScope | null>(null);

export function ViewerScopeProvider({ viewer, generation, children }: { viewer: ViewerDTO; generation: number; children: ReactNode }) {
  const controller = useSessionController();
  const scope = useMemo<ViewerScope>(
    () => ({ viewer, generation, isCurrent: () => controller.currentGeneration() === generation }),
    [viewer, generation, controller],
  );
  return <ScopeContext value={scope}>{children}</ScopeContext>;
}

/** 只能在已登录布局内使用；已登录布局在会话结束的同一次渲染里卸载子树，所以这里不会看到未登录状态。 */
export function useViewerScope(): ViewerScope {
  const scope = useContext(ScopeContext);
  if (scope === null) throw new Error("useViewerScope 只能在已登录布局内使用");
  return scope;
}

/**
 * 查询键以会话代次开头：换用户或重新登录后，旧用户的缓存条目不会被新会话命中。
 * 会话结束时缓存也会被整体清空，这是第二重保护。
 */
export function viewerQueryKey(generation: number, ...parts: readonly unknown[]): readonly unknown[] {
  return ["viewer", generation, ...parts];
}
