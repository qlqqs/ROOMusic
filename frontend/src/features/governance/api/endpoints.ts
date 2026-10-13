import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import {
  decodeCorrectionResult,
  decodeCorrectionState,
  decodeGroupChangeResult,
  decodeOperationPage,
  decodeRevertResult,
  type CorrectableField,
  type CorrectionResultDTO,
  type CorrectionStateDTO,
  type GroupChangeResultDTO,
  type OperationPageDTO,
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

export type OperationListParams = {
  page: number;
  pageSize: number;
  /** 只看这个发行的记录。 */
  releaseId?: string | undefined;
  signal?: AbortSignal | undefined;
};

/** 操作记录（仅管理员）：按时间倒序分页，可按发行筛选。 */
export function fetchOperations(params: OperationListParams): Promise<ApiResponse<OperationPageDTO>> {
  const query = new URLSearchParams({ page: String(params.page), page_size: String(params.pageSize) });
  if (params.releaseId !== undefined) query.set("release_id", params.releaseId);
  return apiRequest({ method: "GET", path: `/api/v1/operations?${query}`, signal: params.signal }, decodeOperationPage);
}

/** 撤销一条操作：没有请求体；只有发行上最近一次修改可以撤销。 */
export function revertOperation(operationId: string, idempotencyKey: string): Promise<ApiResponse<RevertResultDTO>> {
  return apiRequest({ method: "POST", path: `/api/v1/operations/${operationId}/revert`, idempotencyKey }, decodeRevertResult);
}
