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
  /** 待认领列表的全部页：认领与撤销后前缀失效。 */
  claims: (generation: number) => viewerQueryKey(generation, "governance", "claims"),
  /** 待认领列表的一页：页码与页大小在键里。 */
  claimPage: (generation: number, filter: { page: number; pageSize: number }) => viewerQueryKey(generation, "governance", "claims", filter),
  /** 一个待认领的旧版本的详情（修改明细与推荐）。 */
  claim: (generation: number, releaseId: string) => viewerQueryKey(generation, "governance", "claim", releaseId),
  /** 认领的预览：新版本与旧版本都在键里；在 all 前缀下，认领、撤销与别的修改之后随 governance 一起失效，页面重新读到最新的预览与令牌。 */
  claimPreview: (generation: number, releaseId: string, sourceId: string) => viewerQueryKey(generation, "governance", "claim-preview", releaseId, sourceId),
  /** 专辑详情里的“可能接替了……”提示。 */
  claimHints: (generation: number, releaseId: string) => viewerQueryKey(generation, "governance", "claim-hints", releaseId),
};
