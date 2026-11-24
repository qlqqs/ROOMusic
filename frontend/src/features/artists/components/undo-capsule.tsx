import { BookOpen, Flag, Image, Undo2 } from "lucide-react";
import { useTooltip, VisuallyHidden } from "../../../shared/ui";
import { REJECT_UNDO_MS, type PendingReject } from "../hooks/use-artist-media";
import styles from "./undo-capsule.module.css";

/**
 * 标记不对之后的撤回胶囊：页面底部浮起一枚深色胶囊——旗子、被标记的那一项（图片或简介）的图标，和一个带倒计时环的撤回按钮。
 * 倒计时走完由 useArtistMedia 提交；读屏经 role="status" 读出完整的一句话。
 */
export function UndoCapsule({ pending, onUndo }: { pending: PendingReject; onUndo: () => void }) {
  const label = "撤回";
  const tip = useTooltip(label);
  const what = pending.field === "image" ? "图片" : "简介";
  const Icon = pending.field === "image" ? Image : BookOpen;
  return (
    <div className={styles.capsule} role="status">
      <VisuallyHidden>已标记{what}不对，{REJECT_UNDO_MS / 1000} 秒内可以撤回。</VisuallyHidden>
      <Flag size={15} aria-hidden="true" />
      <Icon size={15} aria-hidden="true" />
      <button type="button" className={styles.undo} aria-label={label} onClick={onUndo} {...tip.triggerProps}>
        <Undo2 size={15} aria-hidden="true" />
        <svg key={pending.since} className={styles.countdown} viewBox="0 0 28 28" aria-hidden="true" style={{ animationDuration: `${REJECT_UNDO_MS}ms` }}>
          <circle cx="14" cy="14" r="13" style={{ animationDuration: `${REJECT_UNDO_MS}ms` }} />
        </svg>
      </button>
      {tip.tooltip}
    </div>
  );
}
