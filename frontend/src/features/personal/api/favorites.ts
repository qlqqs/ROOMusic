import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import {
  decodeFavoriteAlbumsPage,
  decodeFavoriteArtistsPage,
  decodeFavoriteLookup,
  decodeFavoriteQueue,
  decodeFavoriteToggle,
  decodeFavoriteTracksPage,
  type FavoriteAlbumsPageDTO,
  type FavoriteArtistsPageDTO,
  type FavoriteKind,
  type FavoriteLookupDTO,
  type FavoriteQueueDTO,
  type FavoriteToggleDTO,
  type FavoriteTracksPageDTO,
} from "./decoders";

const favoritesPath = "/api/v1/me/favorites";

type ListParams = { page: number; pageSize: number; revision: number | null; signal?: AbortSignal | undefined };

function listQuery({ page, pageSize, revision }: ListParams): URLSearchParams {
  const query = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
  if (revision !== null) query.set("revision", String(revision));
  return query;
}

/** 收藏的曲目，一页：按收藏时间倒序。带上第一页给的 revision 翻页，集合变了返回 409 favorites_changed。 */
export function fetchFavoriteTracks(params: ListParams): Promise<ApiResponse<FavoriteTracksPageDTO>> {
  return apiRequest({ method: "GET", path: `${favoritesPath}/tracks?${listQuery(params)}`, signal: params.signal }, decodeFavoriteTracksPage);
}

/** 收藏的专辑，一页：按收藏时间倒序，与曲目的页码各自独立。 */
export function fetchFavoriteAlbums(params: ListParams): Promise<ApiResponse<FavoriteAlbumsPageDTO>> {
  return apiRequest({ method: "GET", path: `${favoritesPath}/albums?${listQuery(params)}`, signal: params.signal }, decodeFavoriteAlbumsPage);
}

/** 收藏的艺人，一页：合并过的按合并后的艺人显示一次，按收藏时间倒序。艺人数据还在整理时返回 503 artist_catalog_preparing。 */
export function fetchFavoriteArtists(params: ListParams): Promise<ApiResponse<FavoriteArtistsPageDTO>> {
  return apiRequest({ method: "GET", path: `${favoritesPath}/artists?${listQuery(params)}`, signal: params.signal }, decodeFavoriteArtistsPage);
}

const kindSegment: Record<FavoriteKind, string> = { track: "tracks", album: "albums", artist: "artists" };

/** 收藏（PUT）或取消收藏（DELETE）一首曲目、一张专辑或一位艺人。两者都是幂等的，不带请求体，也不需要幂等键。艺人数据还在整理时艺人的切换返回 409 artist_catalog_not_ready。 */
export function setFavorite(kind: FavoriteKind, id: string, favorited: boolean): Promise<ApiResponse<FavoriteToggleDTO>> {
  return apiRequest({ method: favorited ? "PUT" : "DELETE", path: `${favoritesPath}/${kindSegment[kind]}/${encodeURIComponent(id)}` }, decodeFavoriteToggle);
}

/**
 * 一批曲目与专辑的收藏状态，一次请求。艺人的状态另发一次（lookupArtistFavorites）：艺人数据还在整理时带艺人的请求整体失败，
 * 不能连累曲目与专辑的心形。
 */
export function lookupFavorites(ids: { trackIds: readonly string[]; groupIds: readonly string[] }, signal?: AbortSignal): Promise<ApiResponse<FavoriteLookupDTO>> {
  return apiRequest({ method: "POST", path: `${favoritesPath}/lookup`, body: { track_ids: ids.trackIds, group_ids: ids.groupIds }, signal }, decodeFavoriteLookup);
}

/** 一批艺人的收藏状态（与曲目、专辑分开请求）。艺人数据还在整理时返回 503 artist_catalog_preparing。 */
export function lookupArtistFavorites(artistIds: readonly string[], signal?: AbortSignal): Promise<ApiResponse<FavoriteLookupDTO>> {
  return apiRequest({ method: "POST", path: `${favoritesPath}/lookup`, body: { track_ids: [], group_ids: [], artist_ids: artistIds }, signal }, decodeFavoriteLookup);
}

/** 收藏曲目的“播放全部”候选：先去掉文件缺失的，再按收藏顺序取前 10,000 首，并给出省略的数量。 */
export function fetchFavoriteTracksQueue(signal?: AbortSignal): Promise<ApiResponse<FavoriteQueueDTO>> {
  return apiRequest({ method: "GET", path: `${favoritesPath}/tracks/queue`, signal }, decodeFavoriteQueue);
}
