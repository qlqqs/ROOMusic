import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import { decodeReleaseGroupPage, type ReleaseGroupPageDTO } from "./decoders";

export type ReleaseGroupListParams = {
  page: number;
  pageSize: number;
  /** 只要需要关注的发行组（attention=required）。 */
  attentionRequired: boolean;
  /** 搜索词（标题、艺人、专辑艺人与曲目）；调用方已经去掉首尾空白并限制在 200 字节以内。 */
  search?: string | undefined;
  signal?: AbortSignal | undefined;
};

export function fetchReleaseGroupPage(params: ReleaseGroupListParams): Promise<ApiResponse<ReleaseGroupPageDTO>> {
  const query = new URLSearchParams({ page: String(params.page), page_size: String(params.pageSize) });
  if (params.attentionRequired) query.set("attention", "required");
  if (params.search !== undefined && params.search !== "") query.set("q", params.search);
  return apiRequest({ method: "GET", path: `/api/v1/release-groups?${query}`, signal: params.signal }, decodeReleaseGroupPage);
}

/**
 * 封面图片的地址。图片由浏览器直接请求（同源，带会话 Cookie），不经过 apiRequest：它不是 JSON。
 * 资源 ID 是内容哈希加扩展名，后端保证只含字母、数字、点、下划线与连字符。
 */
export function artworkPath(resourceId: string): string {
  return `/api/v1/artworks/${encodeURIComponent(resourceId)}`;
}
