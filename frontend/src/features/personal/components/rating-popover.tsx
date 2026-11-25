import { X } from "lucide-react";
import { useEffect, useEffectEvent, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { Button } from "../../../shared/ui";
import { ratingText } from "./rating-stars";
import { RatingControl, ratingSubject, type RatingTarget } from "./rating-control";
import { useRatingStatus } from "./rating-status-scope";
import { useRatingWrite } from "../hooks/use-rating";
import styles from "./rating-popover.module.css";

const EDGE = 8;
const GAP = 4;
/** 写入成功后停一下再关：让人看到星级变了。 */
const CLOSE_DELAY_MS = 600;

type Opened = { opener: HTMLElement; placement: "below" | "above" };

/**
 * 评分浮层（曲目行的“评分…”、播放器栏的星）：标题、大号星级、当前值与清除按钮。非模态，fixed 定位在打开它的元素旁边，
 * 渲染到它所在的模态 <dialog> 里（没有时是 body），不被表格或播放器栏裁掉。
 * 打开时焦点在星级上；Esc、在浮层外按下指针、Tab 离开、滚动或窗口缩放时关闭；写入成功后稍停再关，失败时留着，提示在浮层里。
 * 关闭后焦点还给打开它的元素（菜单项选中时焦点已经回到“更多操作”）。
 */
export function useRatingPopover(target: RatingTarget): { open: (opener: HTMLElement, placement?: "below" | "above") => void; isOpen: boolean; popover: ReactNode } {
  const [opened, setOpened] = useState<Opened | null>(null);
  const popover = opened === null ? null : <RatingPopover target={target} opened={opened} onClose={() => setOpened(null)} />;
  return { open: (opener, placement = "below") => setOpened({ opener, placement }), isOpen: opened !== null, popover };
}

function RatingPopover({ target, opened, onClose }: { target: RatingTarget; opened: Opened; onClose: () => void }) {
  const ref = useRef<HTMLDivElement>(null);
  const status = useRatingStatus();
  const { write } = useRatingWrite(target.kind, target.id);
  const value = status.ratingOf(target.kind, target.id);
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);
  function closeNow(restore: boolean) {
    onClose();
    if (restore && opened.opener.isConnected) opened.opener.focus({ preventScroll: true });
  }
  const close = useEffectEvent(closeNow);

  useLayoutEffect(() => {
    const element = ref.current;
    if (element === null) return;
    const rect = opened.opener.getBoundingClientRect();
    const { width, height } = element.getBoundingClientRect();
    const left = Math.max(EDGE, Math.min(rect.right - width, window.innerWidth - width - EDGE));
    const below = rect.bottom + GAP;
    const above = rect.top - GAP - height;
    const top = opened.placement === "above" ? (above < EDGE ? below : above) : below + height > window.innerHeight - EDGE && above >= EDGE ? above : below;
    element.style.left = `${left}px`;
    element.style.top = `${top}px`;
  }, [opened]);

  useEffect(() => {
    const element = ref.current;
    if (element === null) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.stopPropagation();
      close(true);
    };
    const onPointer = (event: PointerEvent) => {
      const node = event.target as Node | null;
      if (node === null || element.contains(node) || opened.opener.contains(node)) return;
      close(false);
    };
    const onFocusOut = (event: FocusEvent) => {
      const next = event.relatedTarget as Node | null;
      if (next !== null && !element.contains(next)) close(false);
    };
    const onScroll = (event: Event) => {
      if (event.target instanceof Node && element.contains(event.target)) return;
      close(false);
    };
    const onResize = () => close(false);
    document.addEventListener("keydown", onKey);
    document.addEventListener("pointerdown", onPointer, true);
    document.addEventListener("scroll", onScroll, true);
    window.addEventListener("resize", onResize);
    element.addEventListener("focusout", onFocusOut);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("pointerdown", onPointer, true);
      document.removeEventListener("scroll", onScroll, true);
      window.removeEventListener("resize", onResize);
      element.removeEventListener("focusout", onFocusOut);
    };
  }, [opened]);

  const saved = () => {
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => closeNow(true), CLOSE_DELAY_MS);
  };

  const subject = ratingSubject(target);
  return createPortal(
    <div ref={ref} className={styles.popover} role="dialog" aria-label={`评分${subject}`}>
      <span className={styles.title}>{subject}</span>
      <RatingControl target={target} size="lg" autoFocus onSaved={saved} />
      <span className={styles.footer}>
        <span className={styles.value}>{value === undefined ? "" : ratingText(value)}</span>
        {value !== undefined && value > 0 ? (
          <Button
            variant="icon"
            icon={<X size={16} />}
            label="清除评分"
            busy={status.pendingOf(target.kind, target.id)}
            onClick={() => {
              write(0, { onSucceeded: saved });
              ref.current?.querySelector<HTMLElement>("[role='slider']")?.focus();
            }}
          />
        ) : null}
      </span>
    </div>,
    opened.opener.closest("dialog") ?? document.body,
  );
}
