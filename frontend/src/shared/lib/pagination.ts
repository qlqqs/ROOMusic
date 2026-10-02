/** URL 里页码的上限：远超任何真实的页数，只用来挡住荒谬的值，避免把它们发给后端。 */
const MAX_PAGE = 1_000_000;

/**
 * 把 URL 查询串里的页码解析成“要保留在 URL 里的页码”：2 及以上的整数。第 1 页是默认值，不写进 URL；
 * 不合法的值（缺失、0、负数、小数、非数字、过大）一律返回 undefined，调用方据此回到第 1 页。
 * 路由默认把 `?page=3` 解析成数字，也可能留下形如 "03" 的字符串，两种写法都按纯数字处理。
 */
export function parsePageParam(value: unknown): number | undefined {
  const page = typeof value === "string" && /^\d+$/.test(value) ? Number(value) : value;
  return typeof page === "number" && Number.isInteger(page) && page >= 2 && page <= MAX_PAGE ? page : undefined;
}

/** 总页数：没有数据时为 0，不虚构一个空的第 1 页。 */
export function pageCountOf(total: number, pageSize: number): number {
  return total <= 0 || pageSize <= 0 ? 0 : Math.ceil(total / pageSize);
}
