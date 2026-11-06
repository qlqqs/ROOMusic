import { UNKNOWN_TIME } from "./time";

const units = ["B", "KB", "MB", "GB", "TB"] as const;
const precise = new Intl.NumberFormat("zh-CN", { minimumFractionDigits: 1, maximumFractionDigits: 1 });
const whole = new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 0 });

/**
 * 文件大小按 1024 进制换算到最合适的单位：小于 10 保留一位小数，否则取整（如“1.5 MB”“320 KB”）；字节数本身是整数，不带小数。
 * 不是有限非负数时显示“—”，不虚构大小。
 */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return UNKNOWN_TIME;
  let value = bytes;
  let unit = 0;
  // 取整后到 1024 的（如 1023.6 KB）进到下一个单位，不显示成“1,024 KB”。
  while (Math.round(value) >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  // 9.96 之类按一位小数会显示成“10.0”：进位到 10 的改用整数格式。
  const formatter = unit === 0 || Math.round(value * 10) / 10 >= 10 ? whole : precise;
  return `${formatter.format(value)} ${units[unit]}`;
}
