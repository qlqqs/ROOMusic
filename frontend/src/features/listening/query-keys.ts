import { viewerQueryKey } from "../auth";

export const listeningKeys = {
  /** 概览“最近播放”：最近播放的发行与是否有任何历史。 */
  recentReleases: (generation: number) => viewerQueryKey(generation, "listening", "recent-releases"),
};
