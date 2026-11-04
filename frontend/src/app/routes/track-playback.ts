import type { TrackSummaryDTO } from "../../features/catalog";
import { trackArtistOf, trackTitleOf } from "../../features/catalog";
import { unplayableText, type CapabilityTable, type PlayerTrack } from "../../features/player";

// 曲目安全摘要（收藏、歌单、历史，以后还有搜索与艺人页）→ 队列曲目的唯一映射，与发行详情的转换同一套规则
// （标题空白写“（无标题）”，艺人空白退回专辑艺人）。player 只经 catalog 公开合同读发行，不认识这些条目，所以映射在 app。

/** 历史条目与曲目安全摘要共有的字段。 */
export type SafeTrack = Pick<TrackSummaryDTO, "track_id" | "title" | "artist" | "group_id" | "present" | "duration_seconds" | "release" | "source">;

export function playerTrackOfSummary(track: SafeTrack): PlayerTrack {
  const { release, source } = track;
  return {
    trackId: track.track_id,
    releaseId: release.id,
    groupId: track.group_id,
    title: trackTitleOf(track),
    artist: trackArtistOf(track),
    albumTitle: release.title,
    albumArtist: release.album_artist ?? release.artist,
    durationSeconds: track.duration_seconds,
    artworkId: release.artwork?.resource_id ?? null,
    source: {
      sourceKind: source.source_kind,
      codec: source.codec,
      bitDepth: source.bit_depth,
      sampleRate: source.sample_rate,
      channels: source.channels,
      bitrate: source.bitrate,
    },
  };
}

/** 一首曲目为什么不能播放；能播放时是 null。文件缺失优先，其次是浏览器与服务端的能力表（含需要切分的 CUE 分轨）。 */
export function unplayableReason(track: Pick<PlayerTrack, "source">, present: boolean, table: CapabilityTable | undefined): string | null {
  if (!present) return "文件缺失，不能播放";
  if (table === undefined) return "正在检查播放能力";
  const verdict = table.verdictOf({ sourceKind: track.source.sourceKind, codec: track.source.codec });
  return verdict.playable ? null : unplayableText(verdict, track.source.codec);
}
