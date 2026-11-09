import type { PlayMode, PlayerTrack } from "../engine/queue";
import type { PlaybackFailureReason, PlayerStatus } from "../zones/model";

export const modeLabels: Record<PlayMode, string> = { sequential: "顺序", repeat_all: "列表循环", repeat_one: "单曲循环", shuffle: "随机" };
export const statusLabels: Record<PlayerStatus, string> = { idle: "未播放", loading: "正在加载", playing: "正在播放", paused: "已暂停", buffering: "正在缓冲" };
export const failureLabels: Record<PlaybackFailureReason, string> = { not_found: "找不到这首曲目了", root_disabled: "曲目所在的目录已停用", unsupported_format: "这种格式目前不能播放", source_unavailable: "音频文件无法读取", source_changed: "音频文件已变化，请重新扫描", decode_failed: "这首曲目解码失败" };
export function sourceFormat(track: PlayerTrack): string {
  const source = track.source;
  return `${source.codec?.toUpperCase() ?? "未知编码"}${source.bitDepth === null ? "" : ` ${source.bitDepth}`}${source.sampleRate === null ? "" : `/${source.sampleRate / 1000}`}`;
}

/** 增益：一位小数，负号用 −，正值带 +，0 写 0.0 dB。 */
export function gainLabel(db: number): string {
  const rounded = Math.round(db * 10) / 10;
  if (rounded === 0) return "0.0 dB";
  return `${rounded < 0 ? "−" : "+"}${Math.abs(rounded).toFixed(1)} dB`;
}
