import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import {
  decodeCurationBatch,
  decodeCurationBatchPage,
  decodeCurationClassifications,
  decodeCurationConversationPage,
  decodeCurationExceptionAccepted,
  decodeCurationExceptionDeclined,
  decodeCurationExceptionPage,
  decodeCurationModelRetry,
  decodeArtistWebQueue,
  decodeCurationProgress,
  decodeReleaseMatchResults,
  decodeCurationSettings,
  decodeCurationSource,
  decodeCurationSources,
  decodeScopeRevert,
  decodeSuppressionPage,
  type CurationBatchDTO,
  type CurationBatchPageDTO,
  type CurationClassificationsDTO,
  type CurationConversationPageDTO,
  type CurationExceptionDTO,
  type CurationExceptionPageDTO,
  type CurationModelProgressDTO,
  type ArtistWebQueueDTO,
  type CurationProgressDTO,
  type ReleaseMatchResultDTO,
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

/**
 * 改总开关、暂停、整理用的模型、同时运行数、每日 token 上限或单次最多发送（至少一项）；总开关从关到开时服务器同时解除暂停。
 * model 为空串表示用开启者的对话模型；daily_token_limit 为 null 表示不限；max_request_tokens 是正整数，不能为 null。返回改后的设置。
 */
export function updateCurationSettings(change: {
  enabled?: boolean;
  paused?: boolean;
  model?: string;
  daily_token_limit?: number | null;
  max_request_tokens?: number;
  model_concurrency?: number;
}): Promise<ApiResponse<CurationSettingsDTO>> {
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

/** 填写或更换一个源的某一把密钥（key 为 null 时清除）；返回这个源的新状态。 */
export function setCurationSourceKey(id: string, name: string, key: string | null): Promise<ApiResponse<CurationSourceDTO>> {
  const path = `/api/v1/admin/curation/sources/${encodeURIComponent(id)}/keys/${encodeURIComponent(name)}` as const;
  return apiRequest(key === null ? { method: "DELETE", path } : { method: "PUT", path, body: { key } }, decodeCurationSource);
}

/** “整理艺人”全量队列的状态。 */
export function fetchArtistWebQueue(signal?: AbortSignal): Promise<ApiResponse<ArtistWebQueueDTO>> {
  return apiRequest({ method: "GET", path: "/api/v1/admin/curation/artist-web-search", signal }, decodeArtistWebQueue);
}

/** 开始或接续“整理艺人”（无请求体，202）；停止（200）。都返回最新的队列状态；没配好模型或网页搜索时 409。 */
export function startArtistWebQueue(): Promise<ApiResponse<ArtistWebQueueDTO>> {
  return apiRequest({ method: "POST", path: "/api/v1/admin/curation/artist-web-search" }, decodeArtistWebQueue);
}

export function stopArtistWebQueue(): Promise<ApiResponse<ArtistWebQueueDTO>> {
  return apiRequest({ method: "DELETE", path: "/api/v1/admin/curation/artist-web-search" }, decodeArtistWebQueue);
}

/** 后台循环的状态与进度。 */
export function fetchCurationProgress(signal?: AbortSignal): Promise<ApiResponse<CurationProgressDTO>> {
  return apiRequest({ method: "GET", path: "/api/v1/admin/curation/progress", signal }, decodeCurationProgress);
}

/** 一张发行在各来源的识别结论（管理员），按来源排序；没有记录时为空数组。 */
export function fetchReleaseMatchResults(releaseId: string, signal?: AbortSignal): Promise<ApiResponse<{ results: ReleaseMatchResultDTO[] }>> {
  return apiRequest({ method: "GET", path: `/api/v1/admin/curation/releases/${releaseId}/match-results`, signal }, decodeReleaseMatchResults);
}

/** 手动重查一张普通未匹配的发行（只走程序，不交模型）；服务器接受后返回 202 空体，结果由后台写回。 */
export function recheckRelease(releaseId: string): Promise<ApiResponse<null>> {
  return apiRequest({ method: "POST", path: `/api/v1/admin/curation/releases/${releaseId}/recheck` }, () => null);
}

/**
 * 手动测试一次模型连接（不写库、不建整理会话）：成功时解除配置错误或空返回造成的暂停并唤醒调度；
 * 返回更新后的模型层状态。只在模型层的 can_retry 为 true 时可用。
 */
export function retryCurationModel(): Promise<ApiResponse<CurationModelProgressDTO>> {
  return apiRequest({ method: "POST", path: "/api/v1/admin/curation/model/retry" }, decodeCurationModelRetry);
}

/** 可见发行按本地分类的计数（每次实时计算，不轮询）。 */
export function fetchCurationClassifications(signal?: AbortSignal): Promise<ApiResponse<CurationClassificationsDTO>> {
  return apiRequest({ method: "GET", path: "/api/v1/admin/curation/classifications", signal }, decodeCurationClassifications);
}

/** 让所有稳定的名字重新巡检（无请求体，202 空体）；已排队或正在跑的旧巡检随之失效。 */
export function requestRepatrol(): Promise<ApiResponse<null>> {
  return apiRequest({ method: "POST", path: "/api/v1/admin/curation/repatrol" }, () => null);
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

/** 例外（M7 D）：status 为 open 时只列待处理的，all 列全部；新的在前。 */
export function fetchCurationExceptions(status: "open" | "all", page: number, pageSize: number, signal?: AbortSignal): Promise<ApiResponse<CurationExceptionPageDTO>> {
  const query = new URLSearchParams({ status, page: String(page), page_size: String(pageSize) });
  return apiRequest({ method: "GET", path: `/api/v1/admin/curation/exceptions?${query.toString()}`, signal }, decodeCurationExceptionPage);
}

/** 采纳一条例外：服务器按当时的状态重新核对后执行，记为当前管理员的人工确认；前值变了返回 409 exception_stale。 */
export function acceptCurationException(id: string): Promise<ApiResponse<{ exception: CurationExceptionDTO; operation_ids: string[] }>> {
  return apiRequest({ method: "POST", path: `/api/v1/admin/curation/exceptions/${encodeURIComponent(id)}/accept` }, decodeCurationExceptionAccepted);
}

/** 不采纳一条例外：写“不再提出”的标记，之后程序与模型都不再提出同一个值。 */
export function declineCurationException(id: string): Promise<ApiResponse<{ exception: CurationExceptionDTO }>> {
  return apiRequest({ method: "POST", path: `/api/v1/admin/curation/exceptions/${encodeURIComponent(id)}/decline` }, decodeCurationExceptionDeclined);
}

/** 当前管理员名下的自动整理会话（M7 D），新的在前；不在普通会话列表里，打开、接回与续聊用管家的会话页。 */
export function fetchCurationConversations(page: number, pageSize: number, signal?: AbortSignal): Promise<ApiResponse<CurationConversationPageDTO>> {
  return apiRequest({ method: "GET", path: `/api/v1/admin/curation/conversations?page=${page}&page_size=${pageSize}`, signal }, decodeCurationConversationPage);
}
