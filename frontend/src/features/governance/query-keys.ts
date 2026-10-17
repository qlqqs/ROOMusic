import { viewerQueryKey } from "../auth";

// 查询键以会话代次开头（见 auth 的 viewerQueryKey）；组件与 hook 不手写查询键。
export const governanceKeys = {
  /** governance 的全部查询：修改后整体失效。 */
  all: (generation: number) => viewerQueryKey(generation, "governance"),
  /** 一个发行的修正状态（编辑发行信息的基线）。 */
  corrections: (generation: number, releaseId: string) => viewerQueryKey(generation, "governance", "corrections", releaseId),
  /** 操作记录的全部页：撤销与修改后前缀失效。 */
  operations: (generation: number) => viewerQueryKey(generation, "governance", "operations"),
  /** 操作记录的一页：发行与专辑筛选、页码与页大小都在键里。 */
  operationPage: (generation: number, filter: { releaseId: string | null; groupId: string | null; page: number; pageSize: number }) =>
    viewerQueryKey(generation, "governance", "operations", filter),
  /** 问题列表的全部页：核对与修改后前缀失效。 */
  issues: (generation: number) => viewerQueryKey(generation, "governance", "issues"),
  /** 问题列表的一页：状态、类别、页码与页大小都在键里。 */
  issuePage: (generation: number, filter: { status: "open" | "reviewed"; category: string | null; page: number; pageSize: number }) =>
    viewerQueryKey(generation, "governance", "issues", filter),
  /** 一个版本的问题（依据令牌、核对状态）。 */
  releaseIssues: (generation: number, releaseId: string) => viewerQueryKey(generation, "governance", "release-issues", releaseId),
};
