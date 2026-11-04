import { useQueryClient } from "@tanstack/react-query";
import { createContext, useCallback, useContext, useEffect, useRef, type ReactNode } from "react";
import { useViewerScope } from "../auth";
import { personalKeys } from "./query-keys";

// 跨标签页的刷新提示：只传 {scope}，不带用户、收藏内容或任何请求载荷；收到的标签页只让自己当前代次的查询失效，不再转发。
// 不支持 BroadcastChannel 的浏览器，以及当时没打开的标签页，靠窗口重新聚焦时的重新读取对齐。数据库的版本号仍是并发的权威。

const CHANNEL_NAME = "roomusic:personal";

export type PersonalScope = "favorites" | "playlists";

function scopeOf(data: unknown): PersonalScope | null {
  if (typeof data !== "object" || data === null || !("scope" in data)) return null;
  return data.scope === "favorites" || data.scope === "playlists" ? data.scope : null;
}

const NotifyContext = createContext<(scope: PersonalScope) => void>(() => {});

/** 本标签页改了收藏或歌单之后，告诉其他标签页重新读取；不在 PersonalSync 里（例如单独的组件测试）时什么也不做。 */
export function usePersonalNotify(): (scope: PersonalScope) => void {
  return useContext(NotifyContext);
}

/**
 * 已登录布局里挂一次：接收其他标签页的刷新提示，窗口重新聚焦或页面重新可见时重新读取本人的收藏与歌单
 * （不受 30 秒的新鲜期限制，另一个标签页刚改过的心形与歌单要马上对上）。
 */
export function PersonalSync({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  const channel = useRef<BroadcastChannel | null>(null);

  useEffect(() => {
    const invalidate = (scope: PersonalScope | "all") =>
      void queryClient.invalidateQueries({ queryKey: scope === "all" ? personalKeys.all(generation) : scope === "favorites" ? personalKeys.favorites(generation) : personalKeys.playlists(generation) });
    const onFocus = () => invalidate("all");
    const onVisibility = () => {
      if (document.visibilityState === "visible") invalidate("all");
    };
    window.addEventListener("focus", onFocus);
    document.addEventListener("visibilitychange", onVisibility);
    let opened: BroadcastChannel | null = null;
    if (typeof BroadcastChannel !== "undefined") {
      opened = new BroadcastChannel(CHANNEL_NAME);
      opened.onmessage = (event: MessageEvent<unknown>) => {
        const scope = scopeOf(event.data);
        if (scope !== null) invalidate(scope);
      };
      channel.current = opened;
    }
    return () => {
      window.removeEventListener("focus", onFocus);
      document.removeEventListener("visibilitychange", onVisibility);
      opened?.close();
      if (channel.current === opened) channel.current = null;
    };
  }, [queryClient, generation]);

  const notify = useCallback((scope: PersonalScope) => channel.current?.postMessage({ scope }), []);
  return <NotifyContext value={notify}>{children}</NotifyContext>;
}
