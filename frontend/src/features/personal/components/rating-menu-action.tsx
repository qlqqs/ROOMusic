import { Star } from "lucide-react";
import type { ReactNode } from "react";
import type { MenuAction } from "../../../shared/ui";
import { ratingText } from "./rating-stars";
import type { RatingTarget } from "./rating-control";
import { useRatingPopover } from "./rating-popover";
import { useRatingStatus } from "./rating-status-scope";

/**
 * “更多操作”里的“评分…”：提示列是当前星级，选中后在“更多操作”旁弹出评分浮层（菜单选中时焦点已回到触发按钮，浮层就挂在它旁边）。
 * 还没读到评分时不可用。popover 由调用方放在菜单旁边。
 */
export function useRatingMenuAction(target: RatingTarget): { action: MenuAction; popover: ReactNode } {
  const status = useRatingStatus();
  const rating = status.ratingOf(target.kind, target.id);
  const { open, popover } = useRatingPopover(target);
  const action: MenuAction = {
    id: "rating",
    label: "评分…",
    icon: <Star size={16} />,
    ...(rating === undefined || rating === 0 ? {} : { hint: ratingText(rating) }),
    disabled: rating === undefined,
    onSelect: () => {
      const active = document.activeElement;
      if (active instanceof HTMLElement && active !== document.body) open(active);
    },
  };
  return { action, popover };
}
