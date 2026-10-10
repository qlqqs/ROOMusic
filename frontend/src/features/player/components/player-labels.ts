import type { PlaybackFailureReason, PlayerStatus } from "../engine/player-engine";
import type { PlayMode, PlayerTrack } from "../engine/queue";

export const modeLabels: Record<PlayMode, string> = { sequential: "顺序", repeat_all: "列表循环", repeat_one: "单曲循环", shuffle: "随机" };
// preparing：服务端正在把无损曲目转换成浏览器能解码的格式，转换完成后自动开始加载。
export const statusLabels: Record<PlayerStatus, string> = { idle: "未播放", loading: "正在加载", preparing: "正在准备", playing: "正在播放", paused: "已暂停", buffering: "正在缓冲" };
export const failureLabels: Record<PlaybackFailureReason, string> = { not_found: "曲目已不可见", root_disabled: "曲目所在的目录已停用", not_directly_playable: "这首曲目不能直接播放", source_unavailable: "音频文件无法读取", source_changed: "音频文件已变化，请重新扫描", decode_failed: "浏览器无法解码这首曲目", derivation_failed: "无损转换失败，无法播放这首曲目" };
/** 转换的并发、排队或磁盘空间已满：不是曲目的问题，稍后重试。 */
export const derivationBusyText = "服务器正忙于转换，请稍后重试";
export function sourceFormat(track: PlayerTrack): string {
  const source = track.source;
  return `${source.codec?.toUpperCase() ?? "未知编码"}${source.bitDepth === null ? "" : ` ${source.bitDepth}`}${source.sampleRate === null ? "" : `/${source.sampleRate / 1000}`}`;
}
