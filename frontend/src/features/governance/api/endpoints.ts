import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import {
  decodeClaimDetail,
  decodeClaimHints,
  decodeClaimPage,
  decodeClaimPreview,
  decodeClaimResult,
  decodeCorrectionResult,
  decodeCorrectionState,
  decodeGroupChangeResult,
  decodeGroupDefaultResult,
  decodeIssuePage,
  decodeIssueReviewResult,
  decodeOperationPage,
  decodeReleaseIssues,
  decodeRevertResult,
  type ClaimDetailDTO,
  type ClaimHintsDTO,
  type ClaimPageDTO,
  type ClaimPreviewDTO,
  type ClaimResultDTO,
  type CorrectableField,
  type CorrectionResultDTO,
  type CorrectionStateDTO,
  type GroupChangeResultDTO,
  type GroupDefaultResultDTO,
  type IssuePageDTO,
  type IssueReviewResultDTO,
  type OperationPageDTO,
  type ReleaseIssuesDTO,
  type ReviewableIssue,
  type RevertResultDTO,
} from "./decoders";

/**
 * 对一个字段的修改：set 是改成某个值（等于扫描值也是有效修正，用来确认扫描值），null 是明确设为空，
 * clear 是删除修正、回到当前的扫描值。年份的值是整数，其余是字符串。
 */
export type CorrectionChangeBody = { mode: "set"; value: string | number } | { mode: "null" } | { mode: "clear" };

export type CorrectionChanges = Partial<Record<CorrectableField, CorrectionChangeBody>>;

// 下面所有写接口都要求幂等键（UUID）与同源请求：同一个键、同一个请求体重放会得到第一次的响应，不会重复执行；
// 发行 ID 与操作 ID 由调用方先用 isUuid 校验，不是 UUID 的值不会发请求。

/** 编辑发行信息用的修正状态（仅管理员）：当前人工修改版本号与八个字段的扫描值、修正、有效值与不确定标记。 */
export function fetchCorrections(releaseId: string, signal?: AbortSignal): Promise<ApiResponse<CorrectionStateDTO>> {
  return apiRequest({ method: "GET", path: `/api/v1/releases/${releaseId}/corrections`, signal }, decodeCorrectionState);
}

/** 修正发行的字段：必须带界面上看到的人工修改版本号，发行在此期间被修改过时返回 409 revision_conflict。 */
export function correctRelease(
  releaseId: string,
  expectedRevision: number,
  changes: CorrectionChanges,
  idempotencyKey: string,
): Promise<ApiResponse<CorrectionResultDTO>> {
  return apiRequest(
    { method: "PATCH", path: `/api/v1/releases/${releaseId}/corrections`, body: { expected_revision: expectedRevision, changes }, idempotencyKey },
    decodeCorrectionResult,
  );
}

export type GroupChangeBody = { action: "join"; group_id: string } | { action: "detach" };

/** 调整发行所在的组：join 归入另一张专辑，detach 移出为独立专辑；同样要带人工修改版本号。 */
export function changeReleaseGroup(
  releaseId: string,
  expectedRevision: number,
  change: GroupChangeBody,
  idempotencyKey: string,
): Promise<ApiResponse<GroupChangeResultDTO>> {
  return apiRequest(
    { method: "POST", path: `/api/v1/releases/${releaseId}/group`, body: { expected_revision: expectedRevision, ...change }, idempotencyKey },
    decodeGroupChangeResult,
  );
}

/**
 * 设定或清除专辑的默认版本：releaseId 是要设为默认版本的发行，null 是改回自动选择。要带界面上看到的专辑的人工修改版本号
 * （组详情的 revision），专辑在此期间被设定过时返回 409 revision_conflict；版本不在这张专辑里是 409 not_in_group。
 */
export function setGroupDefault(groupId: string, expectedRevision: number, releaseId: string | null, idempotencyKey: string): Promise<ApiResponse<GroupDefaultResultDTO>> {
  return apiRequest(
    { method: "POST", path: `/api/v1/release-groups/${groupId}/default`, body: { expected_revision: expectedRevision, release_id: releaseId }, idempotencyKey },
    decodeGroupDefaultResult,
  );
}

export type OperationListParams = {
  page: number;
  pageSize: number;
  /** 只看这个发行的记录。 */
  releaseId?: string | undefined;
  /** 只看这张专辑的记录（专辑目标的记录，如默认版本）；与 releaseId 同时给出时取两者的并集。 */
  groupId?: string | undefined;
  signal?: AbortSignal | undefined;
};

/** 操作记录（仅管理员）：按时间倒序分页，可按发行与专辑筛选（都给出时取并集）。 */
export function fetchOperations(params: OperationListParams): Promise<ApiResponse<OperationPageDTO>> {
  const query = new URLSearchParams({ page: String(params.page), page_size: String(params.pageSize) });
  if (params.releaseId !== undefined) query.set("release_id", params.releaseId);
  if (params.groupId !== undefined) query.set("group_id", params.groupId);
  return apiRequest({ method: "GET", path: `/api/v1/operations?${query}`, signal: params.signal }, decodeOperationPage);
}

/** 撤销一条操作：没有请求体；只有目标（发行或专辑）上最近一次修改可以撤销。 */
export function revertOperation(operationId: string, idempotencyKey: string): Promise<ApiResponse<RevertResultDTO>> {
  return apiRequest({ method: "POST", path: `/api/v1/operations/${operationId}/revert`, idempotencyKey }, decodeRevertResult);
}

export type IssueListParams = {
  status: "open" | "reviewed";
  /** 只看这一类问题；undefined 是全部类别。 */
  category?: "fields" | "grouping" | "missing_tracks" | undefined;
  page: number;
  pageSize: number;
  signal?: AbortSignal | undefined;
};

/** 问题列表（仅管理员）：有待处理（或已核对）问题的版本，按专辑名排序，另带筛选项上的数字。 */
export function fetchIssues(params: IssueListParams): Promise<ApiResponse<IssuePageDTO>> {
  const query = new URLSearchParams({ status: params.status, page: String(params.page), page_size: String(params.pageSize) });
  if (params.category !== undefined) query.set("category", params.category);
  return apiRequest({ method: "GET", path: `/api/v1/release-issues?${query}`, signal: params.signal }, decodeIssuePage);
}

/** 一个版本的全部问题（仅管理员）：字段、归组冲突与缺失曲目，可以核对的问题带依据令牌与核对状态。 */
export function fetchReleaseIssues(releaseId: string, signal?: AbortSignal): Promise<ApiResponse<ReleaseIssuesDTO>> {
  return apiRequest({ method: "GET", path: `/api/v1/releases/${releaseId}/issues`, signal }, decodeReleaseIssues);
}

/** 核对的请求体：review 带页面读到的依据令牌（页面打开之后问题变了时 409 issue_changed），unreview 取消核对。 */
export type IssueReviewBody = { action: "review"; basis: string } | { action: "unreview" };

/** 核对或取消核对一类问题：要带页面读到的发行的人工修改版本号。 */
export function reviewReleaseIssue(
  releaseId: string,
  expectedRevision: number,
  issue: ReviewableIssue,
  body: IssueReviewBody,
  idempotencyKey: string,
): Promise<ApiResponse<IssueReviewResultDTO>> {
  return apiRequest(
    { method: "POST", path: `/api/v1/releases/${releaseId}/issue-review`, body: { expected_revision: expectedRevision, issue, ...body }, idempotencyKey },
    decodeIssueReviewResult,
  );
}

export type ClaimListParams = { page: number; pageSize: number; signal?: AbortSignal | undefined };

/** 待认领的旧版本（仅管理员）：已不可见、但留有人工修改的版本，最近看到的在前。 */
export function fetchClaims(params: ClaimListParams): Promise<ApiResponse<ClaimPageDTO>> {
  const query = new URLSearchParams({ page: String(params.page), page_size: String(params.pageSize) });
  return apiRequest({ method: "GET", path: `/api/v1/release-claims?${query}`, signal: params.signal }, decodeClaimPage);
}

/** 一个待认领的旧版本（仅管理员）：留着的修改明细与推荐的新版本；不满足待认领的判定时 404。 */
export function fetchClaim(releaseId: string, signal?: AbortSignal): Promise<ApiResponse<ClaimDetailDTO>> {
  return apiRequest({ method: "GET", path: `/api/v1/release-claims/${releaseId}`, signal }, decodeClaimDetail);
}

/** 把这个可见版本列为推荐的待认领旧版本（仅管理员，最多 3 个），专辑详情据此提示“可能接替了已不可见的《…》”。 */
export function fetchClaimHints(releaseId: string, signal?: AbortSignal): Promise<ApiResponse<ClaimHintsDTO>> {
  return apiRequest({ method: "GET", path: `/api/v1/releases/${releaseId}/claim-hints`, signal }, decodeClaimHints);
}

/**
 * 认领的预览（仅管理员）：把旧版本 sourceId 认领给新版本 releaseId，逐项说明哪些会迁移、哪些因为新版本已有而保留、哪张专辑会改回自动选择，
 * 以及两个版本的版本号和提交时要带回的令牌。新版本不可见或旧版本不存在 404，旧版本现在不需要认领 409 not_claimable。
 */
export function fetchClaimPreview(releaseId: string, sourceId: string, signal?: AbortSignal): Promise<ApiResponse<ClaimPreviewDTO>> {
  const query = new URLSearchParams({ source: sourceId });
  return apiRequest({ method: "GET", path: `/api/v1/releases/${releaseId}/claim-preview?${query}`, signal }, decodeClaimPreview);
}

export type ClaimBody = {
  /** 新版本的人工修改版本号（预览里的 revision）。 */
  expectedRevision: number;
  sourceId: string;
  /** 旧版本的人工修改版本号（预览里的 source_revision）。 */
  sourceRevision: number;
  /** 预览令牌：提交时状态与预览不同（页面打开之后又扫描或修改过）返回 409 claim_changed。 */
  preview: string;
};

/**
 * 把旧版本上的人工修改一次迁给新版本：要带预览里的两个版本号和令牌，任一过期返回 409 revision_conflict 或 claim_changed，
 * 页面重新读取预览。同样要求幂等键与同源请求。
 */
export function claimRelease(releaseId: string, body: ClaimBody, idempotencyKey: string): Promise<ApiResponse<ClaimResultDTO>> {
  return apiRequest(
    {
      method: "POST",
      path: `/api/v1/releases/${releaseId}/claim`,
      body: { expected_revision: body.expectedRevision, source_release_id: body.sourceId, source_revision: body.sourceRevision, preview: body.preview },
      idempotencyKey,
    },
    decodeClaimResult,
  );
}
