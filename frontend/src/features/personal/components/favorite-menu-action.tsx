import { Heart } from "lucide-react";
import type { MenuAction } from "../../../shared/ui";
import { favoriteFailureText, useFavoriteToggle } from "../hooks/use-favorite-toggle";
import type { FavoriteTarget } from "./favorite-button";
import { useFavoriteStatus } from "./favorite-status-scope";

/**
 * 心形的菜单项版本（窄屏的曲目行把心形收进“更多”）：“收藏”或“取消收藏”，状态与心形相同（来自所在的 FavoriteStatusScope）。
 * 还没读到状态、切换进行中或结果不确定时不可用；失败的那句话交给 report（开始新的切换时先收到空字符串）。
 */
export function useFavoriteMenuAction(target: FavoriteTarget, report: (text: string) => void): MenuAction {
  const status = useFavoriteStatus();
  const { toggle } = useFavoriteToggle(target.kind, target.id);
  const pressed = status.statusOf(target.kind, target.id);
  const busy = status.pendingOf(target.kind, target.id) || status.unconfirmedOf(target.kind, target.id);
  return {
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
}
