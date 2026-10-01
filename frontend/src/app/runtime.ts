import { QueryClient } from "@tanstack/react-query";
import type { RouterHistory } from "@tanstack/react-router";
import { createBroadcastSessionChannel, createSessionController, type SessionChannel, type SessionController } from "../features/auth";
import { setSessionBoundary, shouldRetryQuery } from "../shared/api";
import { createAppRouter, type AppRouter } from "./router";

/** 页面重新可见时，距上次核对超过这么久才重新核对会话。 */
const VISIBILITY_RECHECK_MS = 30_000;

export type AppRuntime = {
  queryClient: QueryClient;
  session: SessionController;
  router: AppRouter;
  /** 开始确认会话（先查初始化状态，再用 /auth/me）。 */
  start: () => void;
  dispose: () => void;
};

export type AppRuntimeOptions = {
  history?: RouterHistory;
  /** 测试可传入 null 关闭跨标签页通道，或传入内存通道。 */
  channel?: SessionChannel | null;
  now?: () => number;
};

/**
 * 装配应用：查询缓存、会话控制器、路由，以及三者之间唯一的接线：
 * - 请求层的 401 通知会话控制器（带请求发出时的代次）；
 * - 会话状态一变就重新执行路由守卫（登录后离开登录页、会话失效后去登录）；
 * - 离开已登录状态时先取消再清空全部查询与写操作缓存，随后才渲染新状态；
 * - 页面重新可见时核对会话。
 */
export function createAppRuntime(options: AppRuntimeOptions = {}): AppRuntime {
  const queryClient = new QueryClient({
    // 读取只对网络失败与 503 重试；窗口重新聚焦时刷新已过期的数据。写操作从不自动重试（mutation 默认不重试）。
    defaultOptions: { queries: { retry: shouldRetryQuery, staleTime: 30_000 } },
  });
  const channel = options.channel === undefined ? createBroadcastSessionChannel() : options.channel;
  const session = createSessionController({ channel, ...(options.now === undefined ? {} : { now: options.now }) });
  const router = createAppRouter({ session }, options.history);

  setSessionBoundary({ currentGeneration: session.currentGeneration, onUnauthorized: session.handleUnauthorized });
  const stopRouting = session.subscribe(() => void router.invalidate());
  const stopCleanup = session.onEnded(() => {
    void queryClient.cancelQueries();
    queryClient.clear();
  });
  const onVisibilityChange = () => {
    if (document.visibilityState === "visible") void session.recheckIfStale(VISIBILITY_RECHECK_MS);
  };
  document.addEventListener("visibilitychange", onVisibilityChange);

  return {
    queryClient,
    session,
    router,
    start: () => void session.start(),
    dispose: () => {
      document.removeEventListener("visibilitychange", onVisibilityChange);
      stopRouting();
      stopCleanup();
      setSessionBoundary(null);
      session.dispose();
      void queryClient.cancelQueries();
      queryClient.clear();
    },
  };
}
