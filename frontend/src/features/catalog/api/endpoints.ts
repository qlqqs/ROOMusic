import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import {
  decodeCatalogState,
  decodeReleaseComparison,
  decodeReleaseDetail,
  decodeRecentlyAdded,
  decodeReleaseEvidence,
  decodeReleaseGroupAddedPage,
  decodeReleaseGroupDetail,
  decodeReleaseGroupPage,
  type CatalogStateDTO,
  type RecentlyAddedDTO,
  type ReleaseComparisonDTO,
  type ReleaseDetailDTO,
  type ReleaseEvidenceDTO,
  type ReleaseGroupDetailDTO,
  type ReleaseGroupPageDTO,
} from "./decoders";

export type ReleaseGroupListParams = {
  page: number;
  pageSize: number;
  /** 只要需要关注的发行组（attention=required）。 */
  attentionRequired: boolean;
  /** 搜索词（标题、艺人、专辑艺人与曲目）；调用方已经去掉首尾空白并限制在 200 字节以内。 */
  search?: string | undefined;
  /** 排序：省略即按标题（后端默认）；added_at_desc 按加入时间倒序。 */
  sort?: "added_at_desc" | undefined;
  signal?: AbortSignal | undefined;
};

export function fetchReleaseGroupPage(params: ReleaseGroupListParams): Promise<ApiResponse<ReleaseGroupPageDTO>> {
  const query = new URLSearchParams({ page: String(params.page), page_size: String(params.pageSize) });
  if (params.attentionRequired) query.set("attention", "required");
  if (params.search !== undefined && params.search !== "") query.set("q", params.search);
  if (params.sort === undefined) {
    return apiRequest({ method: "GET", path: `/api/v1/release-groups?${query}`, signal: params.signal }, decodeReleaseGroupPage);
  }
  // 按加入时间排序时每项必须带 added_at：旧服务忽略 sort 按标题返回时会被拒绝，不冒充“最近新增”。
  query.set("sort", params.sort);
  return apiRequest({ method: "GET", path: `/api/v1/release-groups?${query}`, signal: params.signal }, decodeReleaseGroupAddedPage);
}

/** 最近新增的专辑（所有登录用户）：按加入时间倒序，固定最多 8 张；接口不接受分页或数量参数。 */
export function fetchRecentlyAdded(signal?: AbortSignal): Promise<ApiResponse<RecentlyAddedDTO>> {
  return apiRequest({ method: "GET", path: "/api/v1/release-groups/recently-added", signal }, decodeRecentlyAdded);
}

// 下面几个接口的 ID 由调用方先用 isUuid 校验，不是 UUID 的值不会发请求。

/** 一个专辑组：组摘要加成员版本的摘要（最多 100 个，release_count 是完整数量）。 */
export function fetchReleaseGroup(groupId: string, signal?: AbortSignal): Promise<ApiResponse<ReleaseGroupDetailDTO>> {
  return apiRequest({ method: "GET", path: `/api/v1/release-groups/${groupId}`, signal }, decodeReleaseGroupDetail);
}

/** 一张专辑各个版本的对比数据（所有登录用户可读）：发行字段的有效值、介质与曲目、总时长与音频格式汇总，成员顺序与组详情一致。 */
export function fetchReleaseComparison(groupId: string, signal?: AbortSignal): Promise<ApiResponse<ReleaseComparisonDTO>> {
  return apiRequest({ method: "GET", path: `/api/v1/release-groups/${groupId}/comparison`, signal }, decodeReleaseComparison);
}

/** 一个发行版本的详情：介质、曲目、署名与证据摘要。 */
export function fetchRelease(releaseId: string, signal?: AbortSignal): Promise<ApiResponse<ReleaseDetailDTO>> {
  return apiRequest({ method: "GET", path: `/api/v1/releases/${releaseId}`, signal }, decodeReleaseDetail);
}

/** 完整证据（仅管理员）：字段值、候选、原因码与分组证据。 */
export function fetchReleaseEvidence(releaseId: string, signal?: AbortSignal): Promise<ApiResponse<ReleaseEvidenceDTO>> {
  return apiRequest({ method: "GET", path: `/api/v1/releases/${releaseId}/evidence`, signal }, decodeReleaseEvidence);
}

/** 目录状态（所有登录用户可读）：搜索与艺人数据的代次和是否已准备好。 */
export function fetchCatalogStatus(signal?: AbortSignal): Promise<ApiResponse<CatalogStateDTO>> {
  return apiRequest({ method: "GET", path: "/api/v1/catalog/status", signal }, decodeCatalogState);
}

/**
 * 封面图片的地址。图片由浏览器直接请求（同源，带会话 Cookie），不经过 apiRequest：它不是 JSON。
 * 资源 ID 是内容哈希加扩展名，后端保证只含字母、数字、点、下划线与连字符。
 */
export function artworkPath(resourceId: string): string {
  return `/api/v1/artworks/${encodeURIComponent(resourceId)}`;
}
