import { useRouter, useRouterState } from "@tanstack/react-router";
import { useCallback } from "react";
import { newRequestId } from "../../../shared/lib/ids";

/**
 * 本次页面加载的标记：全屏开合记在历史条目的 state 里（roomusicPlayer 等于它即打开），返回键自然收起。
 * 刷新后标记不同，旧条目不会自动重开全屏。
 */
const PLAYER_LOAD_ID = newRequestId();

/** 最近一次打开前量出的封面中心（视口坐标）：全屏从这里放大，收起时缩回这里。 */
let lastOrigin: { x: number; y: number } | null = null;

export function fullscreenOrigin(): { x: number; y: number } | null {
  return lastOrigin;
}

/**
 * 全屏播放器的开合：打开是在当前地址上压入一条带标记的历史记录（地址不变，不触发换页），收起是返回上一条。
 * playerFrom 记下原条目的 key，专辑页据此知道这不是一次新的到达，不重新定位曲目。
 */
export function useFullscreenPlayer() {
  const router = useRouter();
  const open = useRouterState({ select: (state) => state.location.state.roomusicPlayer === PLAYER_LOAD_ID });
  const openPlayer = useCallback((from?: HTMLElement | null) => {
    const location = router.state.location;
    if (location.state.roomusicPlayer === PLAYER_LOAD_ID) return;
    const rect = from?.getBoundingClientRect();
    lastOrigin = rect === undefined ? null : { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 };
    router.history.push(location.href, { ...location.state, roomusicPlayer: PLAYER_LOAD_ID, playerFrom: location.state.playerFrom ?? location.state.__TSR_key });
  }, [router]);
  const closePlayer = useCallback(() => {
    if (router.state.location.state.roomusicPlayer === PLAYER_LOAD_ID) router.history.back();
  }, [router]);
  return { open, openPlayer, closePlayer };
}
