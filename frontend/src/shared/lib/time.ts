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

const MINUTE_MS = 60_000;
const HOUR_MS = 60 * MINUTE_MS;
/** “N 天前”最多写到这么多天，再早就写日期。 */
const RELATIVE_MAX_DAYS = 7;

/** 按时区取出的日历日（年月日），换成 UTC 零点的毫秒数，两天相减就是相差的日历天数。 */
function calendarDayOf(value: Date, timeZone: string | undefined): number {
  const parts = new Intl.DateTimeFormat("en-US", { timeZone, year: "numeric", month: "numeric", day: "numeric" }).formatToParts(value);
  const part = (type: Intl.DateTimeFormatPartTypes) => Number(parts.find((candidate) => candidate.type === type)?.value);
  return Date.UTC(part("year"), part("month") - 1, part("day"));
}

/**
 * 相对时间：不到 1 分钟写“刚刚”，不到 1 小时写“N 分钟前”，同一天写“N 小时前”，前一天写“昨天”，两到六天写“N 天前”，
 * 七天及更早写日期（`YYYY-MM-DD`）。天数按日历日（浏览器所在时区）而不是 24 小时计。比 now 晚的时间（时钟偏差）也写“刚刚”。
 * now 与 timeZone 只用于测试或需要固定时间的场合。
 */
export function formatRelativeTime(value: string, now: number = Date.now(), timeZone?: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return UNKNOWN_TIME;
  const elapsed = now - date.getTime();
  if (elapsed < MINUTE_MS) return "刚刚";
  if (elapsed < HOUR_MS) return `${Math.floor(elapsed / MINUTE_MS)} 分钟前`;
  const days = Math.round((calendarDayOf(new Date(now), timeZone) - calendarDayOf(date, timeZone)) / (24 * HOUR_MS));
  if (days <= 0) return `${Math.floor(elapsed / HOUR_MS)} 小时前`;
  if (days === 1) return "昨天";
  if (days < RELATIVE_MAX_DAYS) return `${days} 天前`;
  return formatDate(value, timeZone);
}
