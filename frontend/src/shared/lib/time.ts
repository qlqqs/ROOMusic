/** 显示用的“未知”占位：时间缺失或无法解析时不写成 0 或虚构的值。 */
export const UNKNOWN_TIME = "—";

/**
 * 把 RFC 3339 时间格式化为 `YYYY-MM-DD HH:mm`，默认按浏览器所在时区显示；
 * timeZone 只用于测试或需要固定时区的场合。
 */
export function formatDateTime(value: string, timeZone?: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return UNKNOWN_TIME;
  const parts = new Intl.DateTimeFormat("zh-CN", {
    timeZone,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  }).formatToParts(date);
  const part = (type: Intl.DateTimeFormatPartTypes) => parts.find((candidate) => candidate.type === type)?.value ?? "";
  return `${part("year")}-${part("month")}-${part("day")} ${part("hour")}:${part("minute")}`;
}
