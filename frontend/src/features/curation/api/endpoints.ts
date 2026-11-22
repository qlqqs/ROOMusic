import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import {
  decodeCurationBatch,
  decodeCurationBatchPage,
  decodeCurationProgress,
  decodeCurationSettings,
  decodeCurationSource,
  decodeCurationSources,
  decodeScopeRevert,
  decodeSuppressionPage,
  type CurationBatchDTO,
  type CurationBatchPageDTO,
  type CurationProgressDTO,
  type CurationSettingsDTO,
  type CurationSourceDTO,
  type CurationSourcesDTO,
  type ScopeRevertDTO,
  type SuppressionPageDTO,
} from "./decoders";

// 自动整理的管理员接口。写接口都要求同源；范围撤销另要求幂等键（一次撤销的各段用同一个键）。

export const CURATION_PAGE_SIZE = 20;

/** 总开关、开启者与暂停。 */
export function fetchCurationSettings(signal?: AbortSignal): Promise<ApiResponse<CurationSettingsDTO>> {
  return apiRequest({ method: "GET", path: "/api/v1/admin/curation/settings", signal }, decodeCurationSettings);
}

/** 改总开关或暂停（至少一项）；总开关从关到开时服务器同时解除暂停。返回改后的设置。 */
export function updateCurationSettings(change: { enabled?: boolean; paused?: boolean }): Promise<ApiResponse<CurationSettingsDTO>> {
  return apiRequest({ method: "PUT", path: "/api/v1/admin/curation/settings", body: change }, decodeCurationSettings);
}

/** 各外部源的开关与能不能用，按注册顺序。 */
export function fetchCurationSources(signal?: AbortSignal): Promise<ApiResponse<CurationSourcesDTO>> {
  return apiRequest({ method: "GET", path: "/api/v1/admin/curation/sources", signal }, decodeCurationSources);
}

/** 改一个源的开关；返回这一项。 */
export function setCurationSource(id: string, enabled: boolean): Promise<ApiResponse<CurationSourceDTO>> {
  return apiRequest({ method: "PUT", path: `/api/v1/admin/curation/sources/${encodeURIComponent(id)}`, body: { enabled } }, decodeCurationSource);
}

/** 后台循环的状态与进度。 */
export function fetchCurationProgress(signal?: AbortSignal): Promise<ApiResponse<CurationProgressDTO>> {
  return apiRequest({ method: "GET", path: "/api/v1/admin/curation/progress", signal }, decodeCurationProgress);
}

/** 批次，最新的在前。 */
export function fetchCurationBatches(page: number, signal?: AbortSignal): Promise<ApiResponse<CurationBatchPageDTO>> {
  const query = new URLSearchParams({ page: String(page), page_size: String(CURATION_PAGE_SIZE) });
  return apiRequest({ method: "GET", path: `/api/v1/admin/curation/batches?${query.toString()}`, signal }, decodeCurationBatchPage);
}

/** 单个批次（撤销这一批之后读权威的总数）。 */
export function fetchCurationBatch(id: string, signal?: AbortSignal): Promise<ApiResponse<CurationBatchDTO>> {
  return apiRequest({ method: "GET", path: `/api/v1/admin/curation/batches/${encodeURIComponent(id)}`, signal }, decodeCurationBatch);
}

export type RevertScope = { kind: "batch"; id: string } | { kind: "provider"; provider: string };

/**
 * 按批或按来源撤销的一段（至多 100 条）：第一次不带游标，之后带上一段的 next_cursor，同一次撤销的各段用同一个幂等键。
 * 同键重试不会重复撤销（已撤销的不再是候选）。
 */
export function revertCurationScope(scope: RevertScope, cursor: string | null, idempotencyKey: string): Promise<ApiResponse<ScopeRevertDTO>> {
  const body = cursor === null ? { scope } : { scope, cursor };
  return apiRequest({ method: "POST", path: "/api/v1/admin/curation/reverts", body, idempotencyKey }, decodeScopeRevert);
}

/** “不再提出”的标记，最新的在前。 */
export function fetchSuppressions(page: number, signal?: AbortSignal): Promise<ApiResponse<SuppressionPageDTO>> {
  const query = new URLSearchParams({ page: String(page), page_size: String(CURATION_PAGE_SIZE) });
  return apiRequest({ method: "GET", path: `/api/v1/admin/curation/suppressions?${query.toString()}`, signal }, decodeSuppressionPage);
}

/** 清除一条标记（封面与歌词的一对一起清除）；成功是 204。 */
export function clearSuppression(id: string): Promise<ApiResponse<null>> {
  return apiRequest({ method: "DELETE", path: `/api/v1/admin/curation/suppressions/${encodeURIComponent(id)}` }, () => null);
}
