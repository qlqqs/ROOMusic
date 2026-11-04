import { useLayoutEffect, useState } from "react";

/** 浏览器为网格解析出的列数：grid-template-columns 的计算值把 auto-fill 展开成逐列的长度（含空列），数一数就是列数。 */
function columnCountOf(element: HTMLElement): number | null {
  const tracks = getComputedStyle(element).gridTemplateColumns.trim();
  if (tracks === "" || tracks === "none") return null;
  return tracks.split(/\s+/).length;
}

/**
 * 网格当前的列数，随容器宽度变化。列宽规则只写在 CSS 里（auto-fill 加最小列宽），这里读浏览器排好的结果，不在 JS 里重复断点。
 * element 用回调 ref 交给 useState：网格在数据到达后才挂载，挂载与量取都在绘制前完成，第一帧就是正确的张数。
 * 量不出来（没有元素、jsdom 没有布局或没有 ResizeObserver）时为 null，调用方按“不知道列数”处理。
 */
export function useGridColumns(element: HTMLElement | null): number | null {
  const [columns, setColumns] = useState<number | null>(null);
  useLayoutEffect(() => {
    if (element === null) return;
    const measure = () => setColumns(columnCountOf(element));
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, [element]);
  return element === null ? null : columns;
}
