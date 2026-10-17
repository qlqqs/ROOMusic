import type { IssueCountsDTO } from "./api/decoders";
import type { IssueCategory } from "./issues-search";

/** 问题类别的中文名：列表的筛选、每行的数目与详情的分节共用。 */
export const issueCategoryLabels: Readonly<Record<IssueCategory, string>> = {
  fields: "字段",
  grouping: "归组",
  missing_tracks: "缺失曲目",
};

/** 一行的数目说明，如“2 个字段 · 归组冲突 · 3 首缺失曲目”；没有的类别不写。全为 0 时是空串（由调用方说明“没有待处理的问题”）。 */
export function issueCountsText(counts: IssueCountsDTO): string {
  const parts: string[] = [];
  if (counts.fields > 0) parts.push(`${counts.fields} 个字段`);
  if (counts.grouping > 0) parts.push("归组冲突");
  if (counts.missing_tracks > 0) parts.push(`${counts.missing_tracks} 首缺失曲目`);
  return parts.join(" · ");
}
