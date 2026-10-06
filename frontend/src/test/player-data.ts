import type { PlayerTrack } from "../features/player/engine/queue";

/** 测试用的队列曲目：trackId 是 track-<名字>，默认是 16 位 44.1 kHz 的 FLAC。 */
export function playerTrack(name: string, overrides: Partial<PlayerTrack> = {}): PlayerTrack {
  return {
    trackId: `track-${name}`,
    releaseId: "release-1",
    groupId: "group-1",
    title: `曲目 ${name}`,
    artist: "艺术家",
    albumTitle: "专辑",
    albumArtist: "专辑艺术家",
    durationSeconds: 60,
    artworkId: null,
    source: { sourceKind: "flac_vorbis_comment", codec: "flac", bitDepth: 16, sampleRate: 44100, channels: 2, bitrate: null },
    ...overrides,
  };
}
