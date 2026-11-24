import { SquareArrowOutUpRight } from "lucide-react";
import { useEffect, useEffectEvent, useRef, type ReactNode, type RefObject } from "react";
import { cx } from "../../../shared/lib/class-names";
import styles from "./media-popover.module.css";

type MediaPopoverProps = {
  label: string;
  anchorRef: RefObject<HTMLElement | null>;
  onClose: () => void;
  className?: string | undefined;
  children: ReactNode;
};

/**
 * 艺人页的小弹层（来源、指定对应、候选）：非模态，挂在触发它的印章或图标旁边。
 * 打开时把焦点放到第一个可操作的元素；Esc 关闭并把焦点还给触发器；在弹层与触发器之外按下指针时关闭，焦点不抢回；
 * 焦点用 Tab 离开弹层时也关闭。
 */
export function MediaPopover({ label, anchorRef, onClose, className, children }: MediaPopoverProps) {
  const ref = useRef<HTMLDivElement>(null);
  const close = useEffectEvent(() => onClose());

  useEffect(() => {
    const element = ref.current;
    if (element === null) return;
    // 第一个看得见的可操作元素（折叠的 <details> 里的链接不算）。
    const first = Array.from(element.querySelectorAll<HTMLElement>("input, button, a[href], textarea, summary, [tabindex]:not([tabindex='-1'])")).find(
      (candidate) => candidate.closest("details:not([open]) > :not(summary)") === null,
    );
    (first ?? element).focus({ preventScroll: true });
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.stopPropagation();
      close();
      anchorRef.current?.focus({ preventScroll: true });
    };
    const onPointer = (event: PointerEvent) => {
      const target = event.target as Node | null;
      if (target === null || element.contains(target) || anchorRef.current?.contains(target)) return;
      close();
    };
    const onFocusOut = (event: FocusEvent) => {
      const next = event.relatedTarget as Node | null;
      if (next !== null && !element.contains(next) && !anchorRef.current?.contains(next)) close();
    };
    // Esc 挂在 document 上：焦点还在触发器上（刚点开）时也能关闭。
    document.addEventListener("keydown", onKey);
    element.addEventListener("focusout", onFocusOut);
    document.addEventListener("pointerdown", onPointer, true);
    return () => {
      document.removeEventListener("keydown", onKey);
      element.removeEventListener("focusout", onFocusOut);
      document.removeEventListener("pointerdown", onPointer, true);
    };
  }, [anchorRef]);

  return (
    <div ref={ref} className={cx(styles.popover, className)} role="dialog" aria-label={label} tabIndex={-1}>
      {children}
    </div>
  );
}

/** 弹层里的一行：前面一个图标，后面是文字；有安全的地址时整行是外链（新窗口打开）。给了 title（网页标题）时文字一行显示、过长省略。 */
export function PopoverRow({ icon, href, title, children }: { icon: ReactNode; href?: string | null | undefined; title?: string | undefined; children: ReactNode }) {
  if (href !== undefined && href !== null) {
    return (
      <a className={styles.row} href={href} target="_blank" rel="noopener noreferrer" title={title} data-truncate={title === undefined ? undefined : ""}>
        <span className={styles.rowIcon}>{icon}</span>
        <span className={styles.rowText}>{children}</span>
        <SquareArrowOutUpRight className={styles.external} size={13} aria-hidden="true" />
      </a>
    );
  }
  return (
    <div className={styles.row}>
      <span className={styles.rowIcon}>{icon}</span>
      <span className={styles.rowText}>{children}</span>
    </div>
  );
}

/** 弹层底部的一排图标按钮（管理员的操作）；左侧是标记与指定，右侧是替换与编辑。 */
export function PopoverActions({ start, end }: { start: ReactNode; end?: ReactNode }) {
  return (
    <div className={styles.actions}>
      {start}
      <span className={styles.spacer} />
      {end}
    </div>
  );
}
