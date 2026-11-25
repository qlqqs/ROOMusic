import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import {
  decodeRatedAlbumsPage,
  decodeRatedArtistsPage,
  decodeRatedTracksPage,
  decodeRatingLookup,
  decodeRatingQueue,
  decodeRatingResult,
  type FavoriteKind,
  type PersonalQueueDTO,
  type RatedAlbumsPageDTO,
  type RatedArtistsPageDTO,
  type RatedTracksPageDTO,
  type RatingLookupDTO,
  type RatingResultDTO,
} from "./decoders";

const ratingsPath = "/api/v1/me/ratings";

const kindSegment: Record<FavoriteKind, string> = { track: "tracks", album: "albums", artist: "artists" };

/**
 * 设置（PUT，1–5 星）或清除（rating 为 0 时 DELETE）一首曲目、一张专辑或一位艺人的评分。两者都是幂等的：结果不确定时重发同一个值是安全的。
 * 给合并过的艺人评分会改写本人对所有等价艺人的评分。艺人数据还在整理时艺人的评分返回 409 artist_catalog_not_ready。
 */
export function setRating(kind: FavoriteKind, id: string, rating: number): Promise<ApiResponse<RatingResultDTO>> {
  const path = `${ratingsPath}/${kindSegment[kind]}/${encodeURIComponent(id)}` as const;
  return apiRequest(rating === 0 ? { method: "DELETE", path } : { method: "PUT", path, body: { rating } }, decodeRatingResult);
}

/** 一批曲目与专辑的本人评分，一次请求。艺人另发一次（lookupArtistRatings），理由同收藏：艺人数据还在整理时不连累曲目与专辑。 */
export function lookupRatings(ids: { trackIds: readonly string[]; groupIds: readonly string[] }, signal?: AbortSignal): Promise<ApiResponse<RatingLookupDTO>> {
  return apiRequest({ method: "POST", path: `${ratingsPath}/lookup`, body: { track_ids: ids.trackIds, group_ids: ids.groupIds }, signal }, decodeRatingLookup);
}

/** 一批艺人的本人评分。艺人数据还在整理时返回 503 artist_catalog_preparing。 */
export function lookupArtistRatings(artistIds: readonly string[], signal?: AbortSignal): Promise<ApiResponse<RatingLookupDTO>> {
  return apiRequest({ method: "POST", path: `${ratingsPath}/lookup`, body: { artist_ids: artistIds }, signal }, decodeRatingLookup);
}

/** 评分列表的排序：rating 是评分高到低（同分按评分时间新到旧），recent 是评分时间新到旧。 */
export type RatingSort = "rating" | "recent";

/** 筛选与排序：minRating 1–5 表示“N 星及以上”。默认值（1、rating）不写进查询串。 */
export type RatingFilter = { minRating: number; sort: RatingSort };

export type RatedListParams = RatingFilter & { page: number; pageSize: number; signal?: AbortSignal | undefined };

function filterQuery({ minRating, sort }: RatingFilter): URLSearchParams {
  const query = new URLSearchParams();
  if (minRating > 1) query.set("min_rating", String(minRating));
  if (sort !== "rating") query.set("sort", sort);
  return query;
}

function listQuery(params: RatedListParams): string {
  const query = filterQuery(params);
  query.set("page", String(params.page));
  query.set("page_size", String(params.pageSize));
  return query.toString();
}

/** 评过分的曲目，一页；缺失的曲目照样列出。 */
export function fetchRatedTracks(params: RatedListParams): Promise<ApiResponse<RatedTracksPageDTO>> {
  return apiRequest({ method: "GET", path: `${ratingsPath}/tracks?${listQuery(params)}`, signal: params.signal }, decodeRatedTracksPage);
}

/** 评过分的专辑，一页；暂时不可用的用评分时记下的名字。 */
export function fetchRatedAlbums(params: RatedListParams): Promise<ApiResponse<RatedAlbumsPageDTO>> {
  return apiRequest({ method: "GET", path: `${ratingsPath}/albums?${listQuery(params)}`, signal: params.signal }, decodeRatedAlbumsPage);
}

/** 评过分的艺人，一页：合并过的按合并后的艺人一行。艺人数据还在整理时返回 503 artist_catalog_preparing。 */
export function fetchRatedArtists(params: RatedListParams): Promise<ApiResponse<RatedArtistsPageDTO>> {
  return apiRequest({ method: "GET", path: `${ratingsPath}/artists?${listQuery(params)}`, signal: params.signal }, decodeRatedArtistsPage);
}

/** 当前筛选与排序下的“播放全部”候选：先去掉文件缺失的，再按顺序取前 10,000 首。 */
export function fetchRatedTracksQueue(filter: RatingFilter, signal?: AbortSignal): Promise<ApiResponse<PersonalQueueDTO>> {
  const query = filterQuery(filter).toString();
  return apiRequest({ method: "GET", path: `${ratingsPath}/tracks/queue${query === "" ? "" : `?${query}`}`, signal }, decodeRatingQueue);
}
