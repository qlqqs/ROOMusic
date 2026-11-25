import { Star } from "lucide-react";
import { useRef, type KeyboardEvent } from "react";
import { useTooltip } from "../../../shared/ui";
import styles from "./rating-filter.module.css";

const STARS = [1, 2, 3, 4, 5] as const;

/** 筛选的文字：“全部”或“4 星及以上”。 */
export function minRatingText(minRating: number): string {
  return minRating <= 1 ? "全部" : `${minRating} 星及以上`;
}

function FilterStar({ star, value, onSelect, onKeyDown, refOf }: { star: number; value: number; onSelect: (star: number) => void; onKeyDown: (event: KeyboardEvent<HTMLButtonElement>) => void; refOf: (element: HTMLButtonElement | null) => void }) {
  const label = star === 1 ? "全部评过分的" : `${star} 星及以上`;
  const tip = useTooltip(label);
  return (
    <>
      <button
        ref={refOf}
        type="button"
        role="radio"
        aria-checked={star === value}
        aria-label={label}
        tabIndex={star === value ? 0 : -1}
        className={styles.star}
        data-on={star <= value ? "" : undefined}
        onClick={() => onSelect(star === value ? 1 : star)}
        onKeyDown={onKeyDown}
        {...tip.triggerProps}
      >
        <Star size={15} strokeWidth={1.7} aria-hidden="true" />
      </button>
      {tip.tooltip}
    </>
  );
}

/**
 * “我的评分”的星级筛选：五颗星的单选组（读屏名“筛选”），选第 N 颗表示“N 星及以上”，再点一次当前的那颗回到全部。
 * 形状同评分的星级，但是独立的控件：不读写评分。方向键按单选组的惯例移动并选中（筛选只是读取，逐级切换没有副作用）。
 */
export function RatingFilter({ value, onChange }: { value: number; onChange: (minRating: number) => void }) {
  const buttons = useRef<(HTMLButtonElement | null)[]>([]);
  function select(star: number) {
    onChange(star);
    buttons.current[star - 1]?.focus();
  }
  function onKeyDown(event: KeyboardEvent<HTMLButtonElement>) {
    const step = { ArrowRight: 1, ArrowUp: 1, ArrowLeft: -1, ArrowDown: -1 }[event.key];
    const next = event.key === "Home" ? 1 : event.key === "End" ? 5 : step === undefined ? null : Math.min(5, Math.max(1, value + step));
    if (next === null) return;
    event.preventDefault();
    select(next);
  }
  return (
    <span className={styles.filter}>
      <span role="radiogroup" aria-label="筛选" className={styles.group}>
        {STARS.map((star) => (
          <FilterStar key={star} star={star} value={value} onSelect={select} onKeyDown={onKeyDown} refOf={(element) => void (buttons.current[star - 1] = element)} />
        ))}
      </span>
      <span className={styles.text} aria-hidden="true">
        {minRatingText(value)}
      </span>
    </span>
  );
}
