const countFormatter = new Intl.NumberFormat("zh-CN");

/** 计数按中文习惯加千位分隔符；数量来自接口，不虚构精度。 */
export function formatCount(value: number): string {
  return countFormatter.format(value);
}
