import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import {
  decodeAlbumResults,
  decodeArtistResults,
  decodeSearchSuggestions,
  decodeTrackResults,
  type AlbumResultsDTO,
  type ArtistResultsDTO,
  type SearchSuggestionsDTO,
  type TrackResultsDTO,
} from "./decoders";

// 查询词由调用方先去掉首尾空白并限制在 200 字节以内；空查询不发请求（后端对空查询返回空结果，没有必要往返）。

export function fetchSearchSuggestions(params: { q: string; limit: number; signal?: AbortSignal | undefined }): Promise<ApiResponse<SearchSuggestionsDTO>> {
  const query = new URLSearchParams({ q: params.q, limit: String(params.limit) });
  return apiRequest({ method: "GET", path: `/api/v1/search/suggestions?${query}`, signal: params.signal }, decodeSearchSuggestions);
}

export type SearchPageParams = { q: string; page: number; pageSize: number; signal?: AbortSignal | undefined };

function resultsPath(type: "artists" | "albums" | "tracks", params: SearchPageParams) {
  const query = new URLSearchParams({ type, q: params.q, page: String(params.page), page_size: String(params.pageSize) });
  return `/api/v1/search?${query}` as const;
}

/** 艺人一栏的一页（M5 ⑤，艺人数据就绪时才有）。 */
export function fetchArtistResults(params: SearchPageParams): Promise<ApiResponse<ArtistResultsDTO>> {
  return apiRequest({ method: "GET", path: resultsPath("artists", params), signal: params.signal }, decodeArtistResults);
}

/** 专辑一栏的一页：每栏独立分页，不重查另一栏。 */
export function fetchAlbumResults(params: SearchPageParams): Promise<ApiResponse<AlbumResultsDTO>> {
  return apiRequest({ method: "GET", path: resultsPath("albums", params), signal: params.signal }, decodeAlbumResults);
}

/** 曲目一栏的一页。 */
export function fetchTrackResults(params: SearchPageParams): Promise<ApiResponse<TrackResultsDTO>> {
  return apiRequest({ method: "GET", path: resultsPath("tracks", params), signal: params.signal }, decodeTrackResults);
}
