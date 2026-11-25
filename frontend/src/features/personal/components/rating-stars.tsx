import { Star } from "lucide-react";
import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { cx } from "../../../shared/lib/class-names";
import { useTooltip } from "../../../shared/ui";
import styles from "./rating-stars.module.css";

const STARS = [1, 2, 3, 4, 5] as const;

/** 读屏与提示里的值：“3 星”或“未评分”。 */
export function ratingText(rating: number): string {
  return rating === 0 ? "未评分" : `${rating} 星`;
}

type RatingStarsProps = {
  /** 读屏名：“评分《标题》”。 */
  label: string;
  /** 当前值：1–5，0 是没有评分；undefined 是还没读到（不可用）。 */
  value: number | undefined;
  /** 还没读到时读屏与提示说什么（“正在读取评分”“暂时读不到评分”）。 */
  unavailableText: string;
  /** 写入进行中：显示新值，不能再改。 */
  busy?: boolean;
  /** sm 固定 24px 一颗（专辑卡片两列时也放得下）；md 在触屏上放大到 34px；lg 是浮层里的大号。 */
  size?: "sm" | "md" | "lg";
  /** 写入一个值（0 是清除）；只在值确实变化时调用。 */
  onCommit: (rating: number) => void;
  autoFocus?: boolean;
  className?: string | undefined;
};

/**
 * 五颗星的评分滑块（role="slider"）：只占一个 Tab 停留点，读屏名“评分《标题》”，值读作“3 星”或“未评分”。
 * 鼠标悬停时浅色预览，点击即写入，点当前星级就清除。键盘：方向键与 Home/End 只移动预览，Enter 写入；Delete/Backspace 清除；
 * 数字 1–5 直接写入；Esc 或离开焦点放弃预览。不用 radiogroup：radio 的方向键按一下就选中一次，从 1 星调到 5 星会写四次。
 */
export function RatingStars({ label, value, unavailableText, busy = false, size = "md", onCommit, autoFocus = false, className }: RatingStarsProps) {
  const [preview, setPreview] = useState<number | null>(null);
  const unavailable = value === undefined;
  const blocked = unavailable || busy;
  const current = value ?? 0;
  const shown = blocked ? current : (preview ?? current);
  const previewing = !blocked && preview !== null && preview !== current;
  const hint = unavailable ? unavailableText : current === 0 ? "评分" : `${ratingText(current)} · 再点一次清除`;
  const tip = useTooltip(hint);
  const ref = useRef<HTMLDivElement>(null);
  // React 只替表单控件与按钮处理 autoFocus；滑块是 div，挂载后自己聚焦一次。
  const focusOnMount = useRef(autoFocus);
  useEffect(() => {
    if (focusOnMount.current) ref.current?.focus({ preventScroll: true });
  }, []);

  function commit(next: number) {
    setPreview(null);
    if (blocked || next === current) return;
    onCommit(next);
  }

  function onKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (blocked) return;
    const base = preview ?? current;
    if (event.key === "ArrowRight" || event.key === "ArrowUp") setPreview(Math.min(5, base + 1));
    else if (event.key === "ArrowLeft" || event.key === "ArrowDown") setPreview(Math.max(0, base - 1));
    else if (event.key === "Home") setPreview(0);
    else if (event.key === "End") setPreview(5);
    else if (event.key === "Enter") {
      if (!previewing) return;
      commit(base);
    } else if (event.key === "Delete" || event.key === "Backspace") commit(0);
    else if (/^[1-5]$/.test(event.key)) commit(Number(event.key));
    else if (event.key === "Escape" && previewing) {
      // 只放弃预览，不关闭所在的浮层。
      event.stopPropagation();
      setPreview(null);
    } else return;
    event.preventDefault();
  }

  return (
    <>
      <div
        ref={ref}
        role="slider"
        tabIndex={0}
        aria-label={label}
        aria-valuemin={0}
        aria-valuemax={5}
        aria-valuenow={shown}
        aria-valuetext={unavailable ? unavailableText : previewing ? `${ratingText(shown)}，按 Enter 保存` : ratingText(shown)}
        aria-disabled={unavailable ? true : undefined}
        aria-busy={busy ? true : undefined}
        className={cx(styles.stars, styles[size], className)}
        data-preview={previewing ? "" : undefined}
        onKeyDown={onKeyDown}
        onBlur={() => setPreview(null)}
        onPointerEnter={tip.triggerProps.onPointerEnter}
        onPointerLeave={() => {
          tip.triggerProps.onPointerLeave();
          setPreview(null);
        }}
        onPointerDown={tip.triggerProps.onPointerDown}
      >
        {STARS.map((star) => (
          <span
            key={star}
            className={styles.star}
            data-on={star <= shown ? "" : undefined}
            onPointerMove={() => {
              if (!blocked && preview !== star) setPreview(star);
            }}
            onClick={() => commit(star === current ? 0 : star)}
          >
            <Star size={size === "lg" ? 22 : size === "sm" ? 14 : 15} strokeWidth={1.7} aria-hidden="true" />
          </span>
        ))}
      </div>
      {tip.tooltip}
    </>
  );
}
