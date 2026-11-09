import { albumArtistRefsOf, trackArtistOf, trackArtistRefsOf, trackTitleOf, type ReleaseDetailDTO, type ReleaseTrackDTO, type TrackSummaryDTO } from "../../catalog";
import type { PlayerTrack } from "./queue";

/** 把发行详情里的一首曲目转成队列曲目。groupId 来自路由（发行详情不带组 ID）。 */
export function playerTrackOf(release: ReleaseDetailDTO, track: ReleaseTrackDTO, groupId: string): PlayerTrack {
  const albumArtist = release.album_artist ?? release.artist;
  return {
    trackId: track.id,
    releaseId: release.id,
    groupId,
    title: track.title.trim() === "" ? "（无标题）" : track.title,
    artist: track.artist.trim() === "" ? albumArtist : track.artist,
    artistRefs: track.artist.trim() === "" ? albumArtistRefsOf(release) : track.artist_refs,
    albumTitle: release.title,
    albumArtist,
    durationSeconds: track.duration_seconds,
    artworkId: release.artwork?.resource_id ?? null,
    source: {
      sourceKind: track.source_kind,
      codec: track.codec,
      bitDepth: track.bit_depth,
      sampleRate: track.sample_rate,
      channels: track.channels,
      bitrate: track.bitrate,
    },
  };
}

/** 发行版本的全部曲目，按介质与曲目顺序。 */
export function playerTracksOf(release: ReleaseDetailDTO, groupId: string): PlayerTrack[] {
  return release.media.flatMap((medium) => medium.tracks.map((track) => playerTrackOf(release, track, groupId)));
}

/** 历史条目、队列条目与曲目安全摘要共有的字段。 */
export type SafeTrack = Pick<TrackSummaryDTO, "track_id" | "title" | "artist" | "artist_refs" | "group_id" | "present" | "duration_seconds" | "release" | "source">;

/**
 * 曲目安全摘要（收藏、歌单、历史、搜索、艺人页与服务端队列）→ 队列曲目的唯一映射，与发行详情的转换同一套规则
 * （标题空白写“（无标题）”，艺人空白退回专辑艺人）。
 */
export function playerTrackOfSummary(track: SafeTrack): PlayerTrack {
  const { release, source } = track;
  return {
    trackId: track.track_id,
    releaseId: release.id,
    groupId: track.group_id,
    title: trackTitleOf(track),
    artist: trackArtistOf(track),
    artistRefs: trackArtistRefsOf(track),
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
