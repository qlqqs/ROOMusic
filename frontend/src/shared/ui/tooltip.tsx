import { useEffect, useLayoutEffect, useRef, useState, type PointerEvent, type ReactNode } from "react";
import { createPortal } from "react-dom";
import styles from "./tooltip.module.css";

const DELAY_MS = 300;
const GAP = 6;
const EDGE = 8;

type TooltipTriggerProps = {
  onPointerEnter: (event: PointerEvent<HTMLElement>) => void;
  onPointerLeave: () => void;
  onPointerDown: () => void;
};

/**
 * 图标按钮与图标链接的悬停提示：只响应鼠标，停留 300ms 后显示，移开、按下、Esc、滚动或窗口缩放时立即消失；
 * 按下后要等指针移出再移入才会再出现，所以点击后不残留。键盘焦点不弹（读屏靠 aria-label），
 * 提示 aria-hidden，文字与读屏名相同，不再读一遍。
 * 浮层 fixed 定位，渲染到触发器所在的模态 <dialog> 里（没有时是 body），不会被表格、播放器栏等容器裁掉；
 * 默认在触发器上方，上方放不下翻到下方，水平方向夹在视口边距内。text 为空时不显示。
 */
export function useTooltip(text: string | undefined): { triggerProps: TooltipTriggerProps; tooltip: ReactNode } {
  const trigger = useRef<HTMLElement | null>(null);
  const timer = useRef<number | undefined>(undefined);
  const tip = useRef<HTMLDivElement>(null);
  const [container, setContainer] = useState<Element | null>(null);

  function hide() {
    window.clearTimeout(timer.current);
    timer.current = undefined;
    setContainer(null);
  }

  useEffect(() => () => window.clearTimeout(timer.current), []);

  useLayoutEffect(() => {
    const element = tip.current;
    const anchor = trigger.current;
    if (container === null || element === null || anchor === null) return;
    const rect = anchor.getBoundingClientRect();
    const { width, height } = element.getBoundingClientRect();
    const above = rect.top - GAP - height;
    const top = above < EDGE ? rect.bottom + GAP : above;
    const left = Math.max(EDGE, Math.min(rect.left + rect.width / 2 - width / 2, window.innerWidth - width - EDGE));
    element.style.top = `${top}px`;
    element.style.left = `${left}px`;
  }, [container, text]);

  useEffect(() => {
    if (container === null) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") setContainer(null);
    };
    const dismiss = () => setContainer(null);
    document.addEventListener("keydown", onKey);
    document.addEventListener("scroll", dismiss, true);
    window.addEventListener("resize", dismiss);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("scroll", dismiss, true);
      window.removeEventListener("resize", dismiss);
    };
  }, [container]);

  const triggerProps: TooltipTriggerProps = {
    onPointerEnter: (event) => {
      if (event.pointerType !== "mouse" || text === undefined || text === "") return;
      const element = event.currentTarget;
      trigger.current = element;
      window.clearTimeout(timer.current);
      timer.current = window.setTimeout(() => {
        timer.current = undefined;
        setContainer(element.closest("dialog") ?? document.body);
      }, DELAY_MS);
    },
    onPointerLeave: hide,
    onPointerDown: hide,
  };
  const tooltip =
    container === null || text === undefined || text === ""
      ? null
      : createPortal(
          <div ref={tip} className={styles.tooltip} aria-hidden="true">
            {text}
          </div>,
          container,
        );
  return { triggerProps, tooltip };
}
