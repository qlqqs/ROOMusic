import { isUuid } from "../../shared/lib/ids";
import { parsePageParam } from "../../shared/lib/pagination";

/** 问题的类别：拿不准的字段、同一目录的归组冲突、缺失的曲目。 */
export const ISSUE_CATEGORIES = ["fields", "grouping", "missing_tracks"] as const;
export type IssueCategory = (typeof ISSUE_CATEGORIES)[number];

/**
 * 问题页的地址参数：status 只在看“已核对”时出现（默认是待处理），category 是只看某一类，page 只在 2 及以上时出现，
 * release 是选中的版本（宽屏在右栏显示它的问题，窄屏只显示它）。
 */
export type IssuesSearch = {
  status?: "reviewed" | undefined;
  category?: IssueCategory | undefined;
  page?: number | undefined;
  release?: string | undefined;
};

/**
 * 路由的 validateSearch：不合法的值显式写成 undefined（路由把校验结果合并到原始查询串上，只省略键的话原值会留在 search 里）。
 * release 只保留 UUID 形状的值并统一成小写；默认值（待处理、全部类别、第 1 页）不写进地址。
 */
export function parseIssuesSearch(search: Record<string, unknown>): IssuesSearch {
  const category = search["category"];
  const release = search["release"];
  return {
    status: search["status"] === "reviewed" ? "reviewed" : undefined,
    category: ISSUE_CATEGORIES.find((known) => known === category),
    page: parsePageParam(search["page"]),
    release: isUuid(release) ? release.toLowerCase() : undefined,
  };
}
