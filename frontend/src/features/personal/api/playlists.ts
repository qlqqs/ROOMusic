import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import {
  decodeContainingTrack,
  decodeItemMoved,
  decodeItemRemoved,
  decodeItemRestored,
  decodeItemsAdded,
  decodePlaylistCreated,
  decodePlaylistDeleted,
  decodePlaylistDetail,
  decodePlaylistItemsPage,
  decodePlaylistQueue,
  decodePlaylistRenamed,
  decodePlaylistsPage,
  type ContainingTrackDTO,
  type ItemMovedDTO,
  type ItemRemovedDTO,
  type ItemRestoredDTO,
  type ItemsAddedDTO,
  type PlaylistCreatedDTO,
  type PlaylistDeletedDTO,
  type PlaylistDetailDTO,
  type PlaylistItemsPageDTO,
  type PlaylistQueueDTO,
  type PlaylistRenamedDTO,
  type PlaylistsPageDTO,
} from "./decoders";

// 歌单的写请求都带幂等键：一次用户意图一个键，结果不确定时用同一个键、同一个请求重试。
// 除新建与撤销外都带读取时的 expected_revision，歌单在别处改过时返回 409 revision_conflict。

const playlistsPath = "/api/v1/me/playlists";

function playlistPath(playlistId: string): `/api/v1/${string}` {
  return `${playlistsPath}/${encodeURIComponent(playlistId)}`;
}

/** 本人的歌单，一页：按最近修改倒序。 */
export function fetchPlaylists(params: { page: number; pageSize: number; signal?: AbortSignal | undefined }): Promise<ApiResponse<PlaylistsPageDTO>> {
  const query = new URLSearchParams({ page: String(params.page), page_size: String(params.pageSize) });
  return apiRequest({ method: "GET", path: `${playlistsPath}?${query}`, signal: params.signal }, decodePlaylistsPage);
}

/** 新建歌单，可以同时放进一批曲目（同一个事务，不会建成空歌单后第二步失败）。成功是 201。 */
export function createPlaylist(input: { name: string; trackIds: readonly string[] }, idempotencyKey: string): Promise<ApiResponse<PlaylistCreatedDTO>> {
  const body = input.trackIds.length === 0 ? { name: input.name } : { name: input.name, track_ids: input.trackIds };
  return apiRequest({ method: "POST", path: playlistsPath, body, idempotencyKey }, decodePlaylistCreated);
}

/** 歌单的摘要（名字、曲目数、总时长、封面）。 */
export function fetchPlaylist(playlistId: string, signal?: AbortSignal): Promise<ApiResponse<PlaylistDetailDTO>> {
  return apiRequest({ method: "GET", path: playlistPath(playlistId), signal }, decodePlaylistDetail);
}

export function renamePlaylist(playlistId: string, input: { expectedRevision: number; name: string }, idempotencyKey: string): Promise<ApiResponse<PlaylistRenamedDTO>> {
  return apiRequest({ method: "PATCH", path: playlistPath(playlistId), body: { expected_revision: input.expectedRevision, name: input.name }, idempotencyKey }, decodePlaylistRenamed);
}

export function deletePlaylist(playlistId: string, input: { expectedRevision: number }, idempotencyKey: string): Promise<ApiResponse<PlaylistDeletedDTO>> {
  return apiRequest({ method: "DELETE", path: playlistPath(playlistId), body: { expected_revision: input.expectedRevision }, idempotencyKey }, decodePlaylistDeleted);
}

/** 歌单的一页条目，按歌单顺序；带上第一页给的 revision 翻页，歌单变了返回 409 revision_conflict。 */
export function fetchPlaylistItems(
  playlistId: string,
  params: { page: number; pageSize: number; revision: number | null; signal?: AbortSignal | undefined },
): Promise<ApiResponse<PlaylistItemsPageDTO>> {
  const query = new URLSearchParams({ page: String(params.page), page_size: String(params.pageSize) });
  if (params.revision !== null) query.set("revision", String(params.revision));
  return apiRequest({ method: "GET", path: `${playlistPath(playlistId)}/items?${query}`, signal: params.signal }, decodePlaylistItemsPage);
}

/** 把一批曲目接到歌单末尾：已在歌单里的与同一批里重复的不再加，回执给出各自的数量。 */
export function addPlaylistItems(playlistId: string, input: { expectedRevision: number; trackIds: readonly string[] }, idempotencyKey: string): Promise<ApiResponse<ItemsAddedDTO>> {
  return apiRequest(
    { method: "POST", path: `${playlistPath(playlistId)}/items`, body: { expected_revision: input.expectedRevision, track_ids: input.trackIds }, idempotencyKey },
    decodeItemsAdded,
  );
}

/** 从歌单移除一个条目；回执带撤销用的 token。 */
export function removePlaylistItem(playlistId: string, itemId: string, input: { expectedRevision: number }, idempotencyKey: string): Promise<ApiResponse<ItemRemovedDTO>> {
  return apiRequest(
    { method: "DELETE", path: `${playlistPath(playlistId)}/items/${encodeURIComponent(itemId)}`, body: { expected_revision: input.expectedRevision }, idempotencyKey },
    decodeItemRemoved,
  );
}

/** 撤销一次移除：只带 token，不带 expected_revision；服务端按歌单当前的样子决定放回哪里。 */
export function restorePlaylistItem(playlistId: string, token: string, idempotencyKey: string): Promise<ApiResponse<ItemRestoredDTO>> {
  return apiRequest({ method: "POST", path: `${playlistPath(playlistId)}/restore`, body: { token }, idempotencyKey }, decodeItemRestored);
}

/** 移动一个条目：菜单的四项是 up/down/first/last（全歌单范围）；拖动是 before，锚点为 null 表示放到全歌单最后。 */
export type PlaylistMove = { action: "up" | "down" | "first" | "last" } | { action: "before"; beforeItemId: string | null };

export function movePlaylistItem(playlistId: string, itemId: string, input: { expectedRevision: number; move: PlaylistMove }, idempotencyKey: string): Promise<ApiResponse<ItemMovedDTO>> {
  const { move } = input;
  const body = move.action === "before" ? { expected_revision: input.expectedRevision, action: "before", before_item_id: move.beforeItemId } : { expected_revision: input.expectedRevision, action: move.action };
  return apiRequest({ method: "POST", path: `${playlistPath(playlistId)}/items/${encodeURIComponent(itemId)}/move`, body, idempotencyKey }, decodeItemMoved);
}

/** 歌单的“播放全部”候选：先去掉文件缺失的，再按歌单顺序取前 10,000 首。 */
export function fetchPlaylistQueue(playlistId: string, signal?: AbortSignal): Promise<ApiResponse<PlaylistQueueDTO>> {
  return apiRequest({ method: "GET", path: `${playlistPath(playlistId)}/queue`, signal }, decodePlaylistQueue);
}

/** 一首曲目已经在本人的哪些歌单里。 */
export function fetchPlaylistsContainingTrack(trackId: string, signal?: AbortSignal): Promise<ApiResponse<ContainingTrackDTO>> {
  return apiRequest({ method: "GET", path: `${playlistsPath}/containing-track/${encodeURIComponent(trackId)}`, signal }, decodeContainingTrack);
}
