import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import type { FavoriteAlbumsPageDTO, FavoriteArtistsPageDTO, FavoriteTracksPageDTO, PersonalQueueDTO } from "../api/decoders";
import { fetchFavoriteAlbums, fetchFavoriteArtists, fetchFavoriteTracks, fetchFavoriteTracksQueue } from "../api/favorites";
import { fetchPlaylistQueue } from "../api/playlists";
import { personalKeys } from "../query-keys";

/** 收藏每页的条目数（后端默认 50，上限 100）；专辑与曲目两栏相同。 */
export const FAVORITES_PAGE_SIZE = 48;

/** 收藏曲目的一页。revision 是翻页时带上的集合版本（直接打开某一页时为 null）。 */
export function useFavoriteTracks(page: number, revision: number | null) {
  const { generation } = useViewerScope();
  const params = { page, pageSize: FAVORITES_PAGE_SIZE, revision };
  return useQuery({
    queryKey: personalKeys.favoriteList(generation, "track", params),
    queryFn: ({ signal }) => fetchFavoriteTracks({ ...params, signal }),
    select: (response): FavoriteTracksPageDTO => response.data,
    placeholderData: keepPreviousData,
  });
}

/** 收藏专辑的一页（与曲目的页码各自独立）。 */
export function useFavoriteAlbums(page: number, revision: number | null) {
  const { generation } = useViewerScope();
  const params = { page, pageSize: FAVORITES_PAGE_SIZE, revision };
  return useQuery({
    queryKey: personalKeys.favoriteList(generation, "album", params),
    queryFn: ({ signal }) => fetchFavoriteAlbums({ ...params, signal }),
    select: (response): FavoriteAlbumsPageDTO => response.data,
    placeholderData: keepPreviousData,
  });
}

/** 收藏艺人的一页（与专辑、曲目的页码各自独立）。艺人数据还在整理时读取失败为 503 artist_catalog_preparing。 */
export function useFavoriteArtists(page: number, revision: number | null) {
  const { generation } = useViewerScope();
  const params = { page, pageSize: FAVORITES_PAGE_SIZE, revision };
  return useQuery({
    queryKey: personalKeys.favoriteList(generation, "artist", params),
    queryFn: ({ signal }) => fetchFavoriteArtists({ ...params, signal }),
    select: (response): FavoriteArtistsPageDTO => response.data,
    placeholderData: keepPreviousData,
  });
}

export type PersonalQueueCallbacks = { onLoaded: (queue: PersonalQueueDTO) => void };

export type PersonalQueueSource = { kind: "favorites" } | { kind: "playlist"; playlistId: string };

/**
 * “播放全部”的候选（收藏的曲目，或一个歌单）：每次按下都向服务端读一次最新的，不沿用旧缓存，也不在后台预取；
 * 结果只在发起时的会话仍是当前会话时交给调用方。能否播放、怎样放进队列由 app 决定，personal 不知道播放器。
 */
export function usePersonalQueue(source: PersonalQueueSource) {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  const { generation } = scope;
  const mutation = useMutation({
    mutationFn: async (): Promise<PersonalQueueDTO> => {
      if (source.kind === "favorites") {
        const response = await queryClient.fetchQuery({ queryKey: personalKeys.favoriteQueue(generation), queryFn: ({ signal }) => fetchFavoriteTracksQueue(signal), staleTime: 0, gcTime: 0 });
        return response.data;
      }
      const response = await queryClient.fetchQuery({
        queryKey: personalKeys.playlistQueue(generation, source.playlistId),
        queryFn: ({ signal }) => fetchPlaylistQueue(source.playlistId, signal),
        staleTime: 0,
        gcTime: 0,
      });
      return response.data;
    },
  });
  return {
    pending: mutation.isPending,
    error: mutation.isError ? mutation.error : null,
    load: (callbacks: PersonalQueueCallbacks) =>
      mutation.mutate(undefined, {
        onSuccess: (queue) => {
          if (scope.isCurrent()) callbacks.onLoaded(queue);
        },
      }),
  };
}
