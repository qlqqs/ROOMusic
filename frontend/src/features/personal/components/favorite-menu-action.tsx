import { Heart } from "lucide-react";
import { useEffect, useEffectEvent, type ReactNode } from "react";
import { Button, type MenuAction } from "../../../shared/ui";
import { favoriteFailureText, uncertainFavoriteText, useFavoriteToggle } from "../hooks/use-favorite-toggle";
import type { FavoriteTarget } from "./favorite-button";
import { useFavoriteStatus } from "./favorite-status-scope";

/**
 * 心形的菜单项版本（窄屏的曲目行把心形收进“更多”）：“收藏”或“取消收藏”，状态与心形相同（来自所在的 FavoriteStatusScope）。
 * 还没读到状态、切换进行中或结果不确定时不可用；失败的那句话交给 report（开始新的切换时先收到空字符串）。
 * 结果不确定且重读也失败时，与心形一样给出“重新读取”（retry，由调用方放在菜单旁），否则菜单项会一直不可用。
 */
export function useFavoriteMenuAction(target: FavoriteTarget, report: (text: string) => void): { action: MenuAction; retry: ReactNode } {
  const subject = target.kind === "artist" ? `“${target.title}”` : `《${target.title}》`;
  const status = useFavoriteStatus();
  const { toggle, failure } = useFavoriteToggle(target.kind, target.id);
  const pressed = status.statusOf(target.kind, target.id);
  const unconfirmed = status.unconfirmedOf(target.kind, target.id);
  const busy = status.pendingOf(target.kind, target.id) || unconfirmed;
  const waiting = unconfirmed && !status.refreshing;
  const uncertainText = uncertainFavoriteText(unconfirmed, waiting, failure);
  const reportUncertain = useEffectEvent((value: string) => report(value));
  useEffect(() => {
    if (uncertainText !== null) reportUncertain(uncertainText);
  }, [uncertainText]);
  const action: MenuAction = {
    id: "favorite",
    label: pressed === true ? "取消收藏" : "收藏",
    icon: <Heart size={16} fill={pressed === true ? "currentColor" : "none"} />,
    disabled: pressed === undefined || busy,
    onSelect: () => {
      if (pressed === undefined) return;
      report("");
      toggle(!pressed, { onFailed: (result) => report(favoriteFailureText(result)) });
    },
  };
  const retry = waiting ? (
    <Button aria-label={`重新读取${subject}的收藏状态`} data-favorite-retry="" onClick={status.reread}>
      重新读取
    </Button>
  ) : null;
  return { action, retry };
}
