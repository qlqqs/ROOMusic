import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import { decodeRatingLookup, decodeRatingResult, type FavoriteKind, type RatingLookupDTO, type RatingResultDTO } from "./decoders";

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
