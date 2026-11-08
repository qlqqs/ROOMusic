import { formatCount } from "./number";

/** 时长未知时的占位：不写成 0:00。 */
export const UNKNOWN_DURATION = "—";

/** 时长：不足一小时写 m:ss，否则写 h:mm:ss（小时不补零，也不设上限）；未知（null）写占位。 */
export function formatDuration(seconds: number | null): string {
  if (seconds === null || !Number.isFinite(seconds) || seconds < 0) return UNKNOWN_DURATION;
  const total = Math.round(seconds);
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const rest = String(total % 60).padStart(2, "0");
  return hours > 0 ? `${hours}:${String(minutes).padStart(2, "0")}:${rest}` : `${minutes}:${rest}`;
}

/**
 * 总时长的口语写法：“1 小时 14 分钟”“45 分钟”“1 小时”，不足 1 分钟写秒数。
 * floor 为 true 时向下取整（总时长只是下限、要写“以上”时用）。
 */
export function formatSpokenDuration(seconds: number, { floor = false }: { floor?: boolean } = {}): string {
  const total = floor ? Math.floor(seconds) : Math.round(seconds);
  if (total < 60) return `${total} 秒`;
  const minutes = floor ? Math.floor(total / 60) : Math.round(total / 60);
  const hours = Math.floor(minutes / 60);
  const rest = minutes % 60;
  if (hours === 0) return `${rest} 分钟`;
  return rest === 0 ? `${formatCount(hours)} 小时` : `${formatCount(hours)} 小时 ${rest} 分钟`;
}
