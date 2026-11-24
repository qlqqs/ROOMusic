import type { ApiResponse } from "../../../shared/api";
import { apiRequest, apiUpload } from "../../../shared/api";
import type { ArtistInitialGroup, ArtistRole } from "../../catalog/api/artist-summary";
import {
  decodeArtistAlbumsPage,
  decodeArtistDetail,
  decodeArtistLinkPreview,
  decodeArtistMediaReject,
  decodeArtistMediaWrite,
  decodeArtistWebSearch,
  decodeArtistNamesPage,
  decodeArtistParticipationAlbumsPage,
  decodeArtistPage,
  decodeArtistQueue,
  decodeArtistTracksPage,
  type ArtistAlbumsPageDTO,
  type ArtistDetailDTO,
  type ArtistLinkPreviewDTO,
  type ArtistMediaRejectDTO,
  type ArtistMediaWriteDTO,
  type ArtistWebSearchDTO,
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

// 管理员对艺人资料的写入：都带页面读到的人工修改版本号与一个幂等键（结果不确定后的重试沿用同一个键）。

/** 上传图片（替换管理员提供的那张）；onProgress 是已发出的比例。 */
export function uploadArtistImage(
  artistId: string,
  params: { expectedRevision: number; file: File; onProgress?: (fraction: number) => void; signal?: AbortSignal },
  idempotencyKey: string,
): Promise<ApiResponse<ArtistMediaWriteDTO>> {
  const form = new FormData();
  form.set("expected_revision", String(params.expectedRevision));
  form.set("image", params.file);
  return apiUpload(
    { method: "PUT", path: `/api/v1/artists/${artistId}/image`, form, idempotencyKey, signal: params.signal, ...(params.onProgress === undefined ? {} : { onProgress: params.onProgress }) },
    decodeArtistMediaWrite,
  );
}

export function putArtistBio(artistId: string, params: { expectedRevision: number; text: string; lang: "zh" | "en" }, idempotencyKey: string): Promise<ApiResponse<ArtistMediaWriteDTO>> {
  return apiRequest(
    { method: "PUT", path: `/api/v1/artists/${artistId}/bio`, body: { expected_revision: params.expectedRevision, text: params.text, lang: params.lang }, idempotencyKey },
    decodeArtistMediaWrite,
  );
}

/**
 * 标记当前显示的图片或简介不对：管理员提供的就清除，自动补上的就撤销并且以后不再写回同一条目。
 * keepalive：离开页面时仍要发出（撤回的等待期间关掉了页面）。
 */
export function rejectArtistMedia(
  artistId: string,
  params: { expectedRevision: number; field: "image" | "bio" },
  idempotencyKey: string,
  keepalive = false,
): Promise<ApiResponse<ArtistMediaRejectDTO>> {
  return apiRequest(
    { method: "POST", path: `/api/v1/artists/${artistId}/media/reject`, body: { expected_revision: params.expectedRevision, field: params.field }, idempotencyKey, keepalive },
    decodeArtistMediaReject,
  );
}

/** 指定对应前预览一个 MusicBrainz 或 Wikidata 链接（也接受裸 MBID 与 Q 号）。 */
export function previewArtistLink(artistId: string, url: string, signal?: AbortSignal): Promise<ApiResponse<ArtistLinkPreviewDTO>> {
  return apiRequest({ method: "POST", path: `/api/v1/artists/${artistId}/link-preview`, body: { url }, signal }, decodeArtistLinkPreview);
}

export function putArtistLink(
  artistId: string,
  params: { expectedRevision: number; source: "musicbrainz" | "wikidata"; externalId: string },
  idempotencyKey: string,
): Promise<ApiResponse<ArtistMediaWriteDTO>> {
  return apiRequest(
    { method: "PUT", path: `/api/v1/artists/${artistId}/links`, body: { expected_revision: params.expectedRevision, source: params.source, external_id: params.externalId }, idempotencyKey },
    decodeArtistMediaWrite,
  );
}

/** 管理员：这位艺人的网络搜索状态与待确认的身份候选。 */
export function fetchArtistWebSearch(artistId: string, signal?: AbortSignal): Promise<ApiResponse<ArtistWebSearchDTO>> {
  return apiRequest({ method: "GET", path: `/api/v1/artists/${artistId}/web-search`, signal }, decodeArtistWebSearch);
}

/**
 * 采纳或不采纳一个身份候选：走自动整理的例外接口（完整的响应由 curation 解码，这里只需要成功与否）。
 * 采纳后同一位艺人的其他候选由后端关闭。
 */
export function decideArtistCandidate(exceptionId: string, decision: "accept" | "decline"): Promise<ApiResponse<null>> {
  return apiRequest({ method: "POST", path: `/api/v1/admin/curation/exceptions/${encodeURIComponent(exceptionId)}/${decision}` }, () => null);
}
