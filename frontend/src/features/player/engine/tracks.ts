import { albumArtistRefsOf, type ReleaseDetailDTO, type ReleaseTrackDTO } from "../../catalog";
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
