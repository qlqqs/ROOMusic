import { viewerQueryKey } from "../auth";
import type { AlbumSearch } from "./album-search";

export const catalogKeys = {
  /** catalog 的全部查询：写操作改变了专辑数据（修正、归组、撤销）之后整体失效。 */
  all: (generation: number) => viewerQueryKey(generation, "catalog"),
  /** 概览顶部的资料库统计；在 all 前缀下，扫描与修改专辑数据之后随目录一起失效。 */
  stats: (generation: number) => viewerQueryKey(generation, "catalog", "stats"),
  /** 概览用的专辑数与需要关注的专辑数。 */
  totals: (generation: number) => viewerQueryKey(generation, "catalog", "release-group-totals"),
  /** 专辑列表的一页：搜索词、筛选、排序与页码都在键里，每种组合各有缓存条目；省略排序与按标题排序是同一个条目。 */
  albumPage: (generation: number, search: AlbumSearch) =>
    viewerQueryKey(generation, "catalog", "release-groups", {
      q: search.q ?? null,
      attention: search.attention ?? null,
      sort: search.sort ?? "title",
      page: search.page ?? 1,
    }),
  /** 概览的“最近新增”（最多 8 张）；在 all 前缀下，扫描与写操作之后随目录一起失效。 */
  recentlyAdded: (generation: number) => viewerQueryKey(generation, "catalog", "recently-added"),
  /** 搜索专辑的结果（别的功能挑选专辑时用，例如把版本归入其他专辑）：每个搜索词一个条目，与列表页的缓存分开。 */
  albumSearch: (generation: number, search: string) => viewerQueryKey(generation, "catalog", "album-search", search),
  /** 一个专辑组的详情（成员版本摘要）。 */
  group: (generation: number, groupId: string) => viewerQueryKey(generation, "catalog", "release-group", groupId),
  /** 一个专辑组的版本对比；在 all 前缀下，写操作（修正、归组、默认版本、撤销）之后随目录一起失效。 */
  comparison: (generation: number, groupId: string) => viewerQueryKey(generation, "catalog", "release-group-comparison", groupId),
  /** 一个发行版本的详情。 */
  release: (generation: number, releaseId: string) => viewerQueryKey(generation, "catalog", "release", releaseId),
  /** 一个发行版本的完整证据（管理员）。 */
  evidence: (generation: number, releaseId: string) => viewerQueryKey(generation, "catalog", "release-evidence", releaseId),
};

export const catalogStateKeys = {
  /** 目录状态：不在 catalog 前缀下，修改专辑数据后让目录失效时不会连带重读它。 */
  current: (generation: number) => viewerQueryKey(generation, "catalog-state"),
};
