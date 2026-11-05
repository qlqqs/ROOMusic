import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { failureOf, isHttpFailure } from "../../../shared/api";
import { useViewerScope } from "../../auth";
import { useCatalogState } from "../../catalog";
import type { ArtistQueueDTO, ArtistTrackRole } from "../api/decoders";
import { fetchArtist, fetchArtistAlbums, fetchArtistNames, fetchArtistParticipationAlbums, fetchArtistQueue, fetchArtistTracks, fetchArtists, type ArtistListParams } from "../api/endpoints";
import { artistsKeys } from "../query-keys";

/** 列表每页的艺人数。 */
export const ARTISTS_PAGE_SIZE = 50;
/** 艺人页每页的专辑（卡片）与曲目。 */
export const ARTIST_ALBUMS_PAGE_SIZE = 24;
export const ARTIST_TRACKS_PAGE_SIZE = 50;
export const ARTIST_NAMES_PAGE_SIZE = 50;

/** 艺人数据还在整理（503 artist_catalog_preparing）：页面只说一句，等目录状态变成就绪后自动重新读取。 */
export function isArtistCatalogPreparing(error: unknown): boolean {
  return isHttpFailure(failureOf(error), 503, "artist_catalog_preparing");
}

/**
 * 查询键用的 epoch：目录状态读到之前是 undefined（先不发请求）；读取失败时用 -1，照常请求。
 * 艺人数据发布后 epoch 变大，键随之变化，页面自动按新数据重新读取。
 */
function useArtistsEpoch(): number | undefined {
  const state = useCatalogState();
  return state.data?.epoch ?? (state.isError ? -1 : undefined);
}

export function useArtistsPage(params: ArtistListParams) {
  const { generation } = useViewerScope();
  const epoch = useArtistsEpoch();
  return useQuery({
    queryKey: artistsKeys.page(generation, epoch ?? -1, params),
    queryFn: ({ signal }) => fetchArtists({ ...params, signal }),
    select: (response) => response.data,
    enabled: epoch !== undefined,
    placeholderData: keepPreviousData,
  });
}

export function useArtist(artistId: string | null) {
  const { generation } = useViewerScope();
  const epoch = useArtistsEpoch();
  return useQuery({
    queryKey: artistsKeys.detail(generation, epoch ?? -1, artistId ?? ""),
    queryFn: ({ signal }) => fetchArtist(artistId ?? "", signal),
    select: (response) => response.data,
    enabled: epoch !== undefined && artistId !== null,
  });
}

export function useArtistNames(artistId: string, page: number, enabled: boolean) {
  const { generation } = useViewerScope();
  const epoch = useArtistsEpoch();
  return useQuery({
    queryKey: artistsKeys.names(generation, epoch ?? -1, artistId, page, ARTIST_NAMES_PAGE_SIZE),
    queryFn: ({ signal }) => fetchArtistNames(artistId, { page, pageSize: ARTIST_NAMES_PAGE_SIZE, signal }),
    select: (response) => response.data,
    enabled: enabled && epoch !== undefined,
    placeholderData: keepPreviousData,
  });
}

export function useArtistAlbums(artistId: string, page: number) {
  const { generation } = useViewerScope();
  const epoch = useArtistsEpoch();
  return useQuery({
    queryKey: artistsKeys.albums(generation, epoch ?? -1, artistId, page, ARTIST_ALBUMS_PAGE_SIZE),
    queryFn: ({ signal }) => fetchArtistAlbums(artistId, { page, pageSize: ARTIST_ALBUMS_PAGE_SIZE, signal }),
    select: (response) => response.data,
    enabled: epoch !== undefined,
    placeholderData: keepPreviousData,
  });
}

/** 每页的“参与的专辑”（按专辑拆开时的选择列表）。 */
export const ARTIST_PARTICIPATION_PAGE_SIZE = 20;

/** 以任何身份参与的专辑（带身份，可按专辑名筛选、分页）：管理员按专辑拆开时选择用。 */
export function useArtistParticipationAlbums(artistId: string, q: string | undefined, page: number) {
  const { generation } = useViewerScope();
  const epoch = useArtistsEpoch();
  return useQuery({
    queryKey: artistsKeys.participationAlbums(generation, epoch ?? -1, artistId, q ?? null, page, ARTIST_PARTICIPATION_PAGE_SIZE),
    queryFn: ({ signal }) => fetchArtistParticipationAlbums(artistId, { q, page, pageSize: ARTIST_PARTICIPATION_PAGE_SIZE, signal }),
    select: (response) => response.data,
    enabled: epoch !== undefined,
    placeholderData: keepPreviousData,
  });
}

export function useArtistTracks(artistId: string, role: ArtistTrackRole, page: number) {
  const { generation } = useViewerScope();
  const epoch = useArtistsEpoch();
  return useQuery({
    queryKey: artistsKeys.tracks(generation, epoch ?? -1, artistId, role, page, ARTIST_TRACKS_PAGE_SIZE),
    queryFn: ({ signal }) => fetchArtistTracks(artistId, role, { page, pageSize: ARTIST_TRACKS_PAGE_SIZE, signal }),
    select: (response) => response.data,
    enabled: epoch !== undefined,
    placeholderData: keepPreviousData,
  });
}

export type ArtistQueueOrder = { kind: "album" } | { kind: "random"; seed: string };

/**
 * 播放全部 / 随机播放的候选：每次按下都向服务端读一次（不后台预取、不留在缓存里），结果只在发起时的会话仍是当前会话时交给调用方。
 * 能否播放、怎样放进队列由 app 决定，artists 不知道播放器。
 */
export function useArtistQueue(artistId: string) {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  const { generation } = scope;
  const mutation = useMutation({
    mutationFn: async (order: ArtistQueueOrder): Promise<ArtistQueueDTO> => {
      const response = await queryClient.fetchQuery({
        queryKey: artistsKeys.queue(generation, artistId, order.kind, order.kind === "random" ? order.seed : null),
        queryFn: ({ signal }) => fetchArtistQueue(artistId, order, signal),
        staleTime: 0,
        gcTime: 0,
      });
      return response.data;
    },
  });
  return {
    pending: mutation.isPending ? mutation.variables.kind : null,
    error: mutation.isError ? mutation.error : null,
    load: (order: ArtistQueueOrder, onLoaded: (queue: ArtistQueueDTO) => void) =>
      mutation.mutate(order, {
        onSuccess: (queue) => {
          if (scope.isCurrent()) onLoaded(queue);
        },
      }),
  };
}
