import { viewerQueryKey } from "../auth";

export const listeningKeys = {
  /** 这个查看者的全部播放历史查询（最近播放、历史页、“播放全部”的候选）：上报、移除与清除之后一起失效。 */
  all: (generation: number) => viewerQueryKey(generation, "listening"),
  /** 概览“最近播放”：最近播放的发行与是否有任何历史。 */
  recentReleases: (generation: number) => viewerQueryKey(generation, "listening", "recent-releases"),
  /** 历史页的全部分页（前缀）。 */
  tracks: (generation: number) => viewerQueryKey(generation, "listening", "tracks"),
  /** 历史页的一页。 */
  trackPage: (generation: number, params: { page: number; pageSize: number }) => viewerQueryKey(generation, "listening", "tracks", params),
  /** “播放全部”的候选（最近最多 1,000 首）。 */
  queue: (generation: number) => viewerQueryKey(generation, "listening", "queue"),
};
