import { ChevronLeft, ChevronRight } from "lucide-react";
import { useLayoutEffect, useState, type ReactNode } from "react";
import { cx } from "../lib/class-names";
import { Button } from "./button";
import styles from "./shelf.module.css";

const EDGE_SLACK = 8;

type Shelf = {
  /** 放在横排列表（自己渲染的 ul）上的类名。 */
  className: string;
  /** 放在包裹元素上的类名：它的直接子元素 ul 变成横排（列表由别的组件渲染时用）。 */
  hostClassName: string;
  /** 横排滚动元素的回调 ref。 */
  ref: (element: HTMLElement | null) => void;
  /** 标题行里的“向前”“向后”两个图标按钮；内容放得下、不需要翻页时为 null。 */
  controls: ReactNode;
};

/**
 * 一行横排的卡片（书架）：卡片宽度固定，放不下时横向滚动，不换行也不落单；滚动条隐藏，标题行给出“向前”“向后”两个按钮，
 * 每次翻过大约一屏，到头的方向变淡、按下不做任何事（不设 disabled，焦点不丢）。触屏直接横向滑动。减少动态效果时不做平滑滚动。
 * name 是区块名（“最近播放”），写进按钮的读屏名，同一页有几排时能分辨。
 */
export function useShelf(name: string): Shelf {
  const [element, setElement] = useState<HTMLElement | null>(null);
  const [edges, setEdges] = useState({ start: true, end: true });
  useLayoutEffect(() => {
    if (element === null) return;
    // 吸附可能停在离两端几像素的地方（两侧各有 4px 内边距），差不到 8px 就算到头。
    const measure = () => {
      const max = element.scrollWidth - element.clientWidth;
      setEdges({ start: element.scrollLeft <= EDGE_SLACK, end: element.scrollLeft >= max - EDGE_SLACK });
    };
    measure();
    element.addEventListener("scroll", measure, { passive: true });
    const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(measure);
    observer?.observe(element);
    return () => {
      element.removeEventListener("scroll", measure);
      observer?.disconnect();
    };
  }, [element]);
  function page(direction: -1 | 1) {
    if (element === null || (direction < 0 ? edges.start : edges.end)) return;
    const reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    element.scrollBy({ left: direction * element.clientWidth * 0.9, behavior: reduced ? "auto" : "smooth" });
  }
  const controls =
    edges.start && edges.end ? null : (
      <span className={styles.controls}>
        <Button variant="icon" icon={<ChevronLeft size={18} />} label={`${name}：向前`} data-edge={edges.start ? "" : undefined} onClick={() => page(-1)} />
        <Button variant="icon" icon={<ChevronRight size={18} />} label={`${name}：向后`} data-edge={edges.end ? "" : undefined} onClick={() => page(1)} />
      </span>
    );
  return { className: cx(styles.shelf), hostClassName: cx(styles.host), ref: setElement, controls };
}
