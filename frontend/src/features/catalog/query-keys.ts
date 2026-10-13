import { viewerQueryKey } from "../auth";
import type { AlbumSearch } from "./album-search";

export const catalogKeys = {
  /** catalog 的全部查询：写操作改变了专辑数据（修正、归组、撤销）之后整体失效。 */
  all: (generation: number) => viewerQueryKey(generation, "catalog"),
  /** 概览用的专辑数与需要关注的专辑数。 */
  totals: (generation: number) => viewerQueryKey(generation, "catalog", "release-group-totals"),
  /** 专辑列表的一页：搜索词、筛选与页码都在键里，每种组合各有缓存条目。 */
  albumPage: (generation: number, search: AlbumSearch) =>
    viewerQueryKey(generation, "catalog", "release-groups", { q: search.q ?? null, attention: search.attention ?? null, page: search.page ?? 1 }),
  /** 一个专辑组的详情（成员版本摘要）。 */
  group: (generation: number, groupId: string) => viewerQueryKey(generation, "catalog", "release-group", groupId),
  /** 一个发行版本的详情。 */
  release: (generation: number, releaseId: string) => viewerQueryKey(generation, "catalog", "release", releaseId),
  /** 一个发行版本的完整证据（管理员）。 */
  evidence: (generation: number, releaseId: string) => viewerQueryKey(generation, "catalog", "release-evidence", releaseId),
};
