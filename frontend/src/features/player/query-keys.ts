import { viewerQueryKey } from "../auth";

export const playerKeys = {
  formats: (generation: number) => viewerQueryKey(generation, "player", "formats"),
  capabilities: (generation: number) => viewerQueryKey(generation, "player", "capabilities"),
  /** 一首曲目的歌词：会话内不过期；会话变化随代次一起清空。 */
  lyrics: (generation: number, trackId: string) => viewerQueryKey(generation, "player", "lyrics", trackId),
};
