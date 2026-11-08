/** 显示用的“未知”占位：时间缺失或无法解析时不写成 0 或虚构的值。 */
export const UNKNOWN_TIME = "—";

/**
 * 把 RFC 3339 时间格式化为 `YYYY-MM-DD HH:mm`，默认按浏览器所在时区显示；
 * timeZone 只用于测试或需要固定时区的场合。
 */
export function formatDateTime(value: string, timeZone?: string): string {
  const part = partsOf(value, timeZone);
  return part === null ? UNKNOWN_TIME : `${part("year")}-${part("month")}-${part("day")} ${part("hour")}:${part("minute")}`;
}

/** 把 RFC 3339 时间格式化为 `YYYY-MM-DD`，时区规则与 formatDateTime 相同。 */
export function formatDate(value: string, timeZone?: string): string {
  const part = partsOf(value, timeZone);
  return part === null ? UNKNOWN_TIME : `${part("year")}-${part("month")}-${part("day")}`;
}

/** 按时区拆出年月日时分；无法解析时是 null。 */
function partsOf(value: string, timeZone: string | undefined) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return null;
  const parts = new Intl.DateTimeFormat("zh-CN", {
    timeZone,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  }).formatToParts(date);
  return (type: Intl.DateTimeFormatPartTypes) => parts.find((candidate) => candidate.type === type)?.value ?? "";
}
