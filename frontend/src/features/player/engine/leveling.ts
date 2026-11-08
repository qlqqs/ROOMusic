// 音量均衡的增益选择（按 Roon）：后端已按真峰值限幅，这里只挑用哪一个增益，原样施加，不截断、不压限。
import type { TrackAnalysisDTO } from "../api/decoders";
import type { LevelingMode } from "./playback-settings";
import type { QueueState } from "./queue";

/** 响度未知（没有分析结果、分析未完成或失败、读取失败、等不到，或曲目近乎静音）时固定施加的增益。 */
export const UNKNOWN_GAIN_DB = -5;

/** 实际施加的均衡：增益与依据。 */
export type Leveling = { gainDb: number; basis: "track" | "album" | "unknown" };

function trackLevel(analysis: TrackAnalysisDTO | null): Leveling {
  const gain = analysis?.status === "done" ? analysis.analysis?.trackGainDb ?? null : null;
  return gain === null ? { gainDb: UNKNOWN_GAIN_DB, basis: "unknown" } : { gainDb: gain, basis: "track" };
}

function albumLevel(analysis: TrackAnalysisDTO | null): Leveling {
  // 专辑还没全部分析完（album 为 null）时先用曲目增益，整张分析完后自动改用专辑增益。
  const album = analysis?.status === "done" ? analysis.album : null;
  return album === null ? trackLevel(analysis) : { gainDb: album.gainDb, basis: "album" };
}

/** 自动模式下是否在连续播放同一张专辑：不是随机播放，且列表里当前项的前一项或后一项是同一发行。 */
export function inAlbumContext(queue: QueueState, entryId: string): boolean {
  if (queue.mode === "shuffle") return false;
  const index = queue.entries.findIndex((entry) => entry.entryId === entryId);
  const releaseId = queue.entries[index]?.track.releaseId;
  if (index < 0 || releaseId === undefined) return false;
  return queue.entries[index - 1]?.track.releaseId === releaseId || queue.entries[index + 1]?.track.releaseId === releaseId;
}

/** 按模式选增益；“关”是 null（不施加）。 */
export function chooseLeveling(mode: LevelingMode, analysis: TrackAnalysisDTO | null, albumContext: boolean): Leveling | null {
  switch (mode) {
    case "off":
      return null;
    case "track":
      return trackLevel(analysis);
    case "album":
      return albumLevel(analysis);
    case "auto":
      return albumContext ? albumLevel(analysis) : trackLevel(analysis);
  }
}

/** 分贝 → 线性增益。 */
export function linearGain(db: number): number {
  return 10 ** (db / 20);
}
