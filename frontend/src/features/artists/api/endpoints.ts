import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import type { ArtistInitialGroup, ArtistRole } from "../../catalog/api/artist-summary";
import {
  decodeArtistAlbumsPage,
  decodeArtistDetail,
  decodeArtistNamesPage,
  decodeArtistParticipationAlbumsPage,
  decodeArtistPage,
  decodeArtistQueue,
  decodeArtistTracksPage,
  type ArtistAlbumsPageDTO,
  type ArtistDetailDTO,
  type ArtistNamesPageDTO,
  type ArtistParticipationAlbumsPageDTO,
  type ArtistPageDTO,
  type ArtistQueueDTO,
  type ArtistTrackRole,
  type ArtistTracksPageDTO,
} from "./decoders";

// 艺人 ID 由调用方先用 isUuid 校验，不是 UUID 的值不发请求。艺人数据还在整理时这些接口返回 503 artist_catalog_preparing。

export type ArtistListParams = {
  /** 搜索词（主名与别名）；调用方已经去掉首尾空白并限制在 200 字节以内。 */
  q?: string | undefined;
  initial?: ArtistInitialGroup | undefined;
  role?: ArtistRole | undefined;
  sort: "name" | "album_count";
  page: number;
  pageSize: number;
};

type PageParams = { page: number; pageSize: number; signal?: AbortSignal | undefined };

const pageQuery = ({ page, pageSize }: PageParams) => new URLSearchParams({ page: String(page), page_size: String(pageSize) });

export function fetchArtists(params: ArtistListParams & { signal?: AbortSignal | undefined }): Promise<ApiResponse<ArtistPageDTO>> {
  const query = pageQuery(params);
  query.set("sort", params.sort);
  if (params.q !== undefined && params.q !== "") query.set("q", params.q);
  if (params.initial !== undefined) query.set("initial", params.initial);
  if (params.role !== undefined) query.set("role", params.role);
  return apiRequest({ method: "GET", path: `/api/v1/artists?${query}`, signal: params.signal }, decodeArtistPage);
}

export function fetchArtist(artistId: string, signal?: AbortSignal): Promise<ApiResponse<ArtistDetailDTO>> {
  return apiRequest({ method: "GET", path: `/api/v1/artists/${artistId}`, signal }, decodeArtistDetail);
}

export function fetchArtistNames(artistId: string, params: PageParams): Promise<ApiResponse<ArtistNamesPageDTO>> {
  return apiRequest({ method: "GET", path: `/api/v1/artists/${artistId}/names?${pageQuery(params)}`, signal: params.signal }, decodeArtistNamesPage);
}

export function fetchArtistAlbums(artistId: string, params: PageParams): Promise<ApiResponse<ArtistAlbumsPageDTO>> {
  return apiRequest({ method: "GET", path: `/api/v1/artists/${artistId}/albums?${pageQuery(params)}`, signal: params.signal }, decodeArtistAlbumsPage);
}

/**
 * 这位艺人以任何身份参与的专辑（participation=any）：每项带身份；q 按专辑名筛选（调用方已经去掉首尾空白并限制在 200 字节以内）。
 */
export function fetchArtistParticipationAlbums(artistId: string, params: PageParams & { q?: string | undefined }): Promise<ApiResponse<ArtistParticipationAlbumsPageDTO>> {
  const query = pageQuery(params);
  query.set("participation", "any");
  if (params.q !== undefined && params.q !== "") query.set("q", params.q);
  return apiRequest({ method: "GET", path: `/api/v1/artists/${artistId}/albums?${query}`, signal: params.signal }, decodeArtistParticipationAlbumsPage);
}

export function fetchArtistTracks(artistId: string, role: ArtistTrackRole, params: PageParams): Promise<ApiResponse<ArtistTracksPageDTO>> {
  const query = pageQuery(params);
  query.set("role", role);
  return apiRequest({ method: "GET", path: `/api/v1/artists/${artistId}/tracks?${query}`, signal: params.signal }, decodeArtistTracksPage);
}

/** 播放全部（order=album）或随机播放（order=random，带一个随机的 seed）的候选。 */
export function fetchArtistQueue(artistId: string, order: { kind: "album" } | { kind: "random"; seed: string }, signal?: AbortSignal): Promise<ApiResponse<ArtistQueueDTO>> {
  const query = new URLSearchParams({ order: order.kind });
  if (order.kind === "random") query.set("seed", order.seed);
  return apiRequest({ method: "GET", path: `/api/v1/artists/${artistId}/queue?${query}`, signal }, decodeArtistQueue);
}
