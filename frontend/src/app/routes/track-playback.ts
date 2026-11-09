import { unplayableText, type CapabilityTable, type PlayerTrack } from "../../features/player";

// 曲目安全摘要 → 队列曲目的映射在 player（服务端队列同样用它），这里转出给各页面。
export { playerTrackOfSummary, type SafeTrack } from "../../features/player";

/** 一首曲目为什么不能播放；能播放时是 null。文件缺失优先，其次是服务端的格式表。 */
export function unplayableReason(track: Pick<PlayerTrack, "source">, present: boolean, table: CapabilityTable | undefined): string | null {
  if (!present) return "文件缺失，不能播放";
  if (table === undefined) return "正在检查播放能力";
  const verdict = table.verdictOf({ sourceKind: track.source.sourceKind, codec: track.source.codec });
  return verdict.playable ? null : unplayableText(verdict);
}
