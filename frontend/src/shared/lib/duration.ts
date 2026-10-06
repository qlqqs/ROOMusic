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
