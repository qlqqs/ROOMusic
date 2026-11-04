import { keepPreviousData, useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import type { ApiResponse } from "../../../shared/api";
import { useViewerScope } from "../../auth";
import type { ItemsAddedDTO, PlaylistItemsPageDTO, PlaylistStateDTO, PlaylistSummaryDTO } from "../api/decoders";
import {
  addPlaylistItems,
  createPlaylist,
  deletePlaylist,
  fetchPlaylist,
  fetchPlaylistItems,
  fetchPlaylists,
  fetchPlaylistsContainingTrack,
  movePlaylistItem,
  removePlaylistItem,
  renamePlaylist,
  restorePlaylistItem,
  type PlaylistMove,
} from "../api/playlists";
import { personalKeys } from "../query-keys";
import { usePersonalNotify } from "../sync";
import { useIdempotentCommand } from "./use-idempotent-command";

/** 歌单列表每页的卡片数（后端上限）。 */
export const PLAYLISTS_PAGE_SIZE = 20;
/** 歌单详情每页的条目数；拖动只在一页之内。 */
export const PLAYLIST_ITEMS_PAGE_SIZE = 50;

/** 歌单列表的一页：按最近修改倒序；翻页时保留上一页直到新一页到达。 */
export function usePlaylists(page: number) {
  const { generation } = useViewerScope();
  const params = { page, pageSize: PLAYLISTS_PAGE_SIZE };
  return useQuery({
    queryKey: personalKeys.playlistList(generation, params),
    queryFn: ({ signal }) => fetchPlaylists({ ...params, signal }),
    select: (response) => response.data,
    placeholderData: keepPreviousData,
  });
}

/** “添加到歌单…”对话框里的歌单：按最近修改倒序，一次一页，“显示更多”接着读。 */
export function usePlaylistPicker() {
  const { generation } = useViewerScope();
  return useInfiniteQuery({
    queryKey: personalKeys.playlistPicker(generation),
    queryFn: ({ pageParam, signal }) => fetchPlaylists({ page: pageParam, pageSize: PLAYLISTS_PAGE_SIZE, signal }),
    initialPageParam: 1,
    getNextPageParam: (last) => (last.data.pagination.page * last.data.pagination.page_size < last.data.pagination.total ? last.data.pagination.page + 1 : undefined),
    staleTime: 0,
  });
}

/** 一首曲目已经在哪些歌单里；不是单首加入时不读。 */
export function usePlaylistsContainingTrack(trackId: string | null) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: personalKeys.containingTrack(generation, trackId ?? ""),
    queryFn: ({ signal }) => fetchPlaylistsContainingTrack(trackId ?? "", signal),
    select: (response) => new Set(response.data.items.map((item) => item.playlist_id)),
    enabled: trackId !== null,
    staleTime: 0,
  });
}

/** 歌单的摘要（名字、曲目数、总时长、封面）。 */
export function usePlaylistDetail(playlistId: string) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: personalKeys.playlistDetail(generation, playlistId),
    queryFn: ({ signal }) => fetchPlaylist(playlistId, signal),
    select: (response) => response.data.playlist,
  });
}

/** 歌单的一页条目。revision 是翻页时带上的版本（直接打开某一页时为 null）。 */
export function usePlaylistItems(playlistId: string, page: number, revision: number | null) {
  const { generation } = useViewerScope();
  const params = { page, pageSize: PLAYLIST_ITEMS_PAGE_SIZE, revision };
  return useQuery({
    queryKey: personalKeys.playlistItems(generation, playlistId, params),
    queryFn: ({ signal }) => fetchPlaylistItems(playlistId, { ...params, signal }),
    select: (response) => response.data,
    placeholderData: keepPreviousData,
  });
}

/**
 * 歌单写命令之后的缓存处理：先把已缓存的条目页与摘要里的版本号换成回执里的（紧接着的下一条命令不会因自己的上一条而冲突），
 * 再让全部歌单查询失效（列表、各歌单、“在哪些歌单里”），并通知其他标签页。
 */
function usePlaylistsChanged() {
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  const notify = usePersonalNotify();
  const adopt = useCallback(
    (state: PlaylistStateDTO) => {
      queryClient.setQueriesData<ApiResponse<PlaylistItemsPageDTO>>({ queryKey: personalKeys.playlistItemPages(generation, state.id) }, (previous) =>
        previous === undefined ? previous : { ...previous, data: { ...previous.data, playlist: state } },
      );
      queryClient.setQueryData<ApiResponse<{ playlist: PlaylistSummaryDTO }>>(personalKeys.playlistDetail(generation, state.id), (previous) =>
        previous === undefined ? previous : { ...previous, data: { playlist: { ...previous.data.playlist, ...state } } },
      );
      notify("playlists");
    },
    [queryClient, generation, notify],
  );
  const invalidate = useCallback(() => void queryClient.invalidateQueries({ queryKey: personalKeys.playlists(generation) }), [queryClient, generation]);
  return { adopt, invalidate, queryClient, generation, notify };
}

/** 新建歌单（可以同时放进一批曲目）。 */
export function useCreatePlaylist() {
  const changed = usePlaylistsChanged();
  return useIdempotentCommand((input: { name: string; trackIds: readonly string[] }, key) => createPlaylist(input, key).then((response) => response.data), {
    onSucceeded: () => changed.notify("playlists"),
    onSettled: changed.invalidate,
  });
}

/** 把一批曲目加入某个歌单（对话框里选的那个）。 */
export function useAddToPlaylist() {
  const changed = usePlaylistsChanged();
  return useIdempotentCommand(
    (input: { playlistId: string; expectedRevision: number; trackIds: readonly string[] }, key) =>
      addPlaylistItems(input.playlistId, { expectedRevision: input.expectedRevision, trackIds: input.trackIds }, key).then((response): ItemsAddedDTO => response.data),
    { onSucceeded: (result) => changed.adopt(result.playlist), onSettled: changed.invalidate },
  );
}

/** 一个歌单详情页上的命令：改名、删除、移除、撤销与移动。同一时间只有一条在进行（由页面禁用其他入口）。 */
export function usePlaylistCommands(playlistId: string) {
  const changed = usePlaylistsChanged();
  const rename = useIdempotentCommand(
    (input: { expectedRevision: number; name: string }, key) => renamePlaylist(playlistId, input, key).then((response) => response.data),
    { onSucceeded: (result) => changed.adopt(result.playlist), onSettled: changed.invalidate },
  );
  const remove = useIdempotentCommand(
    (input: { expectedRevision: number; itemId: string }, key) => removePlaylistItem(playlistId, input.itemId, input, key).then((response) => response.data),
    { onSucceeded: (result) => changed.adopt(result.playlist), onSettled: changed.invalidate },
  );
  const restore = useIdempotentCommand((input: { token: string }, key) => restorePlaylistItem(playlistId, input.token, key).then((response) => response.data), {
    onSucceeded: (result) => changed.adopt(result.playlist),
    onSettled: changed.invalidate,
  });
  const move = useIdempotentCommand(
    (input: { expectedRevision: number; itemId: string; move: PlaylistMove }, key) => movePlaylistItem(playlistId, input.itemId, input, key).then((response) => response.data),
    { onSucceeded: (result) => changed.adopt(result.playlist), onSettled: changed.invalidate },
  );
  const destroy = useIdempotentCommand((input: { expectedRevision: number }, key) => deletePlaylist(playlistId, input, key).then((response) => response.data), {
    onSucceeded: () => {
      changed.queryClient.removeQueries({ queryKey: personalKeys.playlist(changed.generation, playlistId) });
      changed.notify("playlists");
    },
    onSettled: changed.invalidate,
  });
  return { rename, remove, restore, move, destroy };
}
