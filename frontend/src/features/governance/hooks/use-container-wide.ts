import { useLayoutEffect, useState, type RefObject } from "react";

/**
 * 容器是否至少 minWidth 宽（CSS 像素）：问题页按它在两栏与顺序导航之间切换。看容器而不是视口，因为带侧栏时 1024px 视口里内容区只有约 800px。
 * 两种布局渲染的元素不同（窄屏只渲染列表或详情之一），只靠 CSS 容器查询做不到，所以用 ResizeObserver 量宽度。
 * 没有 ResizeObserver（jsdom）时按宽屏处理；测试要窄屏时自己提供 ResizeObserver。
 */
export function useContainerWide(ref: RefObject<HTMLElement | null>, minWidth: number): boolean {
  const [wide, setWide] = useState(true);
  useLayoutEffect(() => {
    const element = ref.current;
    if (element === null || typeof ResizeObserver === "undefined") return;
    const measure = () => setWide(element.getBoundingClientRect().width >= minWidth);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, [ref, minWidth]);
  return wide;
}
