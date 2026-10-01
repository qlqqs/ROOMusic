import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import { decodeReleaseGroupPage, type ReleaseGroupPageDTO } from "./decoders";

export type ReleaseGroupListParams = {
  page: number;
  pageSize: number;
  /** 只要需要关注的发行组（attention=required）。 */
  attentionRequired: boolean;
  signal?: AbortSignal | undefined;
};

export function fetchReleaseGroupPage(params: ReleaseGroupListParams): Promise<ApiResponse<ReleaseGroupPageDTO>> {
  const query = new URLSearchParams({ page: String(params.page), page_size: String(params.pageSize) });
  if (params.attentionRequired) query.set("attention", "required");
  return apiRequest({ method: "GET", path: `/api/v1/release-groups?${query}`, signal: params.signal }, decodeReleaseGroupPage);
}
