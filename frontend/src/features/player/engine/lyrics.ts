// 歌词的纯函数：把后端已解析、已排序的同步歌词按时间分组，并按播放位置找出当前组。
// 不依赖 React 与媒体元素；LRC 的解析规则只在后端有一份，这里只做分组与定位。

import type { SyncedLyricsLine } from "../api/decoders";

/**
 * 同一开始时间的行作为一组：第一行是主行，其余是对照行（译文）。lines 只含非空文本；
 * 没有任何文本的组是间奏（后端的空文本行）：它让上一句在这一刻之后不再是“当前句”，但不显示文字。
 */
export type LyricsGroup = { startMs: number; lines: readonly string[] };

/** 后端保证行按开始时间非递减，所以相同开始时间的行相邻；相邻且相同的并成一组，保持原顺序。 */
export function groupSyncedLines(lines: readonly SyncedLyricsLine[]): LyricsGroup[] {
  const groups: { startMs: number; lines: string[] }[] = [];
  for (const line of lines) {
    let group = groups[groups.length - 1];
    if (group === undefined || group.startMs !== line.start_ms) {
      group = { startMs: line.start_ms, lines: [] };
      groups.push(group);
    }
    if (line.text !== "") group.lines.push(line.text);
  }
  return groups;
}

/**
 * 点击一行后媒体报告的位置可能比该行时间早几毫秒（定位按采样或帧对齐、浮点取整），不留余量时高亮会停在上一句；
 * 提前这么多毫秒就算轮到该行，远小于任何可感知的差别。
 */
export const ACTIVE_TOLERANCE_MS = 40;

/** 最后一个“开始时间不晚于播放位置（含容差）”的组的下标；位置在第一组之前是 -1。groups 按开始时间非递减。 */
export function activeGroupIndex(groups: readonly Pick<LyricsGroup, "startMs">[], positionMs: number): number {
  const reached = positionMs + ACTIVE_TOLERANCE_MS;
  let low = 0;
  let high = groups.length;
  // 二分：找到第一个开始时间晚于 reached 的组，它前面那个就是当前组。
  while (low < high) {
    const middle = (low + high) >>> 1;
    if ((groups[middle]?.startMs ?? Number.POSITIVE_INFINITY) <= reached) low = middle + 1;
    else high = middle;
  }
  return low - 1;
}

/** 跟随时要滚到中部的组：当前组有文字就是它，当前组是间奏则停在最近的上一句，没有则是 -1。 */
export function followIndex(groups: readonly Pick<LyricsGroup, "lines">[], active: number): number {
  for (let index = Math.min(active, groups.length - 1); index >= 0; index -= 1) {
    if ((groups[index]?.lines.length ?? 0) > 0) return index;
  }
  return -1;
}
