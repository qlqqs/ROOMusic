import { viewerQueryKey } from "../auth";
import type { AlbumSearch } from "./album-search";

export const catalogKeys = {
  /** 概览用的专辑数与需要关注的专辑数。 */
  totals: (generation: number) => viewerQueryKey(generation, "catalog", "release-group-totals"),
  /** 专辑列表的一页：搜索词、筛选与页码都在键里，每种组合各有缓存条目。 */
  albumPage: (generation: number, search: AlbumSearch) =>
    viewerQueryKey(generation, "catalog", "release-groups", { q: search.q ?? null, attention: search.attention ?? null, page: search.page ?? 1 }),
};
