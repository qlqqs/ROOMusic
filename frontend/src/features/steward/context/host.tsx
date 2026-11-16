import { createContext, useContext, type ReactNode } from "react";
import type { StewardPageContext, StewardPlaybackContext } from "./context";

// 管家从外面拿到的东西：当前页面、正在播放，以及个人数据变了之后怎么刷新。steward 不导入其他 feature，
// 这些由 app 组合（路由、查询缓存、播放器与各 feature 的查询键），经这里传进来。

/** 管家的工具改了哪类数据：收藏、歌单、播放历史；C6 起还有曲库（专辑的修正、归组、默认版本、核对、认领与撤销）与艺人（合并、拆开、改名、忽略候选）。 */
export type StewardDataScope = "favorites" | "playlists" | "history" | "catalog" | "artists";

export type StewardHost = {
  /** 正在看的页面；整页对话里是 null。 */
  page: StewardPageContext | null;
  /** 正在控制的分区与正在放的曲目；还没读到分区时是 null。 */
  playback: StewardPlaybackContext | null;
  /** 让这类数据的查询失效。 */
  refreshData: (scope: StewardDataScope) => void;
};

const noHost: StewardHost = { page: null, playback: null, refreshData: () => {} };

const HostContext = createContext<StewardHost>(noHost);

export function StewardHostProvider({ value, children }: { value: StewardHost; children: ReactNode }) {
  return <HostContext value={value}>{children}</HostContext>;
}

export function useStewardHost(): StewardHost {
  return useContext(HostContext);
}

export function usePageContext(): StewardPageContext | null {
  return useStewardHost().page;
}

export function usePlaybackContext(): StewardPlaybackContext | null {
  return useStewardHost().playback;
}
