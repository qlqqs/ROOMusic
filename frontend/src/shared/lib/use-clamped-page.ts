import { useEffect, useEffectEvent } from "react";

type ClampedPageOptions = {
  /** URL 里的当前页，从 1 起。 */
  page: number;
  /** 当前数据的总页数；没有数据时为 0。 */
  pageCount: number;
  /**
   * 数据是否可以用来判断越界：还在加载，或显示的是翻页时保留的上一页（占位数据）时为 false。
   * 占位数据对应的是另一页的总数，不能拿来判断当前页是否越界。
   */
  ready: boolean;
  /** 页码越界时调用，参数是应该去的页码（没有数据时是 1）；调用方用“替换历史项”的方式导航，返回键不会回到越界页。 */
  onOutOfRange: (page: number) => void;
};

/**
 * 页码越界（总数缩小、手改地址）时把地址规范化到末页。返回当前页是否越界：调用方在导航生效之前不显示这一页的内容。
 * 总数为零时规范化到第 1 页，由调用方显示空状态。
 */
export function useClampedPage({ page, pageCount, ready, onOutOfRange }: ClampedPageOptions): boolean {
  const lastPage = Math.max(1, pageCount);
  const outOfRange = ready && page > lastPage;
  const clamp = useEffectEvent(onOutOfRange);
  useEffect(() => {
    if (outOfRange) clamp(lastPage);
  }, [outOfRange, lastPage]);
  return outOfRange;
}
