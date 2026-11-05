import { keepPreviousData, useMutationState, useQuery, useQueryClient } from "@tanstack/react-query";
import { createContext, useCallback, useContext, useEffect, useMemo, type ReactNode } from "react";
import { useViewerScope } from "../../auth";
import type { FavoriteKind } from "../api/decoders";
import { lookupArtistFavorites, lookupFavorites } from "../api/favorites";
import { patchLookup, type CachedLookup } from "../hooks/use-favorite-toggle";
import { lookupIdsKey, personalKeys } from "../query-keys";
import { confirmFavorites, nextFavoriteTick, unconfirmedKey, useUnconfirmedFavorites } from "../unconfirmed-favorites";

type FavoriteStatus = {
  /** 收藏着为 true，没收藏为 false；还没读到（或读取失败）时为 undefined。进行中的切换先按目标值显示。 */
  statusOf: (kind: FavoriteKind, id: string) => boolean | undefined;
  /** 这个目标有切换正在进行：同一目标不能再切换，PUT 与 DELETE 不会交错到达。 */
  pendingOf: (kind: FavoriteKind, id: string) => boolean;
  /**
   * 这个目标上一次切换的结果不确定，还没有重新读到确定的状态：任何位置都不能再切换。
   * 与 refreshing 一起看：正在重读时等着；重读失败时给“重新读取”（reread）。
   */
  unconfirmedOf: (kind: FavoriteKind, id: string) => boolean;
  /** 重新读取所有已打开的收藏状态。 */
  reread: () => void;
  /** 正在重新读取状态（例如结果不确定之后的对账）。 */
  refreshing: boolean;
  /** 状态读取失败、也没有旧数据。 */
  failed: boolean;
};

const StatusContext = createContext<FavoriteStatus | null>(null);

const targetKey = (kind: FavoriteKind, id: string) => `${kind}:${id.toLowerCase()}`;

export function useFavoriteStatus(): FavoriteStatus {
  const status = useContext(StatusContext);
  if (status === null) throw new Error("FavoriteButton 需要放在 FavoriteStatusScope 里");
  return status;
}

type FavoriteStatusScopeProps = {
  trackIds?: readonly string[] | undefined;
  groupIds?: readonly string[] | undefined;
  /** 艺人（M5 ⑤）：另发一次请求，艺人数据还在整理时只有艺人的心形读不到状态（不可用），曲目与专辑不受影响。 */
  artistIds?: readonly string[] | undefined;
  children: ReactNode;
};

const none: readonly string[] = [];

/**
 * 一页（一个专辑版本、一页收藏、一页歌单、播放器栏的当前曲目）的收藏状态：一批 ID 一次请求，范围里的每个心形按目标读取。
 * 换一批 ID 时先沿用上一批里已知的状态，不让心形闪成不可用。所有已打开的范围共用同一个缓存前缀：一处切换，其他范围随之更新。
 */
export function FavoriteStatusScope({ trackIds = none, groupIds = none, artistIds = none, children }: FavoriteStatusScopeProps) {
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  const ids = useMemo(() => lookupIdsKey({ trackIds, groupIds }), [trackIds, groupIds]);
  const empty = ids.trackIds.length === 0 && ids.groupIds.length === 0;
  const lookup = useQuery({
    queryKey: personalKeys.favoriteLookup(generation, ids),
    // 记下读取开始时的序号：结果不确定的目标只认在标记之后才开始的读取。
    queryFn: async ({ signal }): Promise<CachedLookup> => {
      const startedTick = nextFavoriteTick();
      return { ...(await lookupFavorites(ids, signal)), startedTick };
    },
    placeholderData: keepPreviousData,
    enabled: !empty,
  });
  const artistKey = useMemo(() => personalKeys.favoriteArtistLookup(generation, artistIds), [generation, artistIds]);
  const artistLookup = useQuery({
    queryKey: artistKey,
    queryFn: async ({ signal }): Promise<CachedLookup> => {
      const startedTick = nextFavoriteTick();
      return { ...(await lookupArtistFavorites(artistIds, signal)), startedTick };
    },
    placeholderData: keepPreviousData,
    enabled: artistIds.length > 0,
  });
  const unconfirmed = useUnconfirmedFavorites();
  // 进行中的切换（任何一个组件发起的）：先按目标值显示，失败后自然回到服务端的状态。
  const pending = useMutationState({
    filters: { mutationKey: personalKeys.favoriteToggles(generation), status: "pending" },
    select: (mutation) => ({ key: mutation.options.mutationKey, next: mutation.state.variables }),
  });
  const overlay = useMemo(() => {
    const map = new Map<string, boolean>();
    for (const { key, next } of pending) {
      const [, , , , kind, id] = key ?? [];
      if ((kind === "track" || kind === "album" || kind === "artist") && typeof id === "string" && typeof next === "boolean") map.set(targetKey(kind, id), next);
    }
    return map;
  }, [pending]);
  const data = lookup.data?.data;
  const artistData = artistLookup.data?.data;
  const server = useMemo(() => {
    const map = new Map<string, boolean>();
    for (const track of data?.tracks ?? []) map.set(targetKey("track", track.track_id), track.favorited);
    for (const album of data?.albums ?? []) map.set(targetKey("album", album.group_id), album.favorited);
    // 艺人按输入的 ID 与它现在对应的艺人都能查到：合并过的旧 ID 与现在的 ID 是同一位。
    for (const artist of artistData?.artists ?? []) {
      map.set(targetKey("artist", artist.artist_id), artist.favorited);
      if (artist.resolved_artist_id !== null) map.set(targetKey("artist", artist.resolved_artist_id), artist.favorited);
    }
    return map;
  }, [data, artistData]);
  // 这次（不是翻页时沿用的上一批）成功读到、且在标记之后才开始的状态，确认了范围里结果不确定的目标：
  // 把确定的值写进每一份已缓存的批量状态（别的范围不必等自己的重读），再解除这些目标。
  const trackTick = lookup.isPlaceholderData || lookup.status !== "success" ? undefined : lookup.data?.startedTick;
  const artistTick = artistLookup.isPlaceholderData || artistLookup.status !== "success" ? undefined : artistLookup.data?.startedTick;
  // 两份读取都确认的目标才解除；只请求了其中一份时用那一份的序号（较小的序号更保守）。
  const startedTick = artistIds.length === 0 ? trackTick : empty ? artistTick : trackTick === undefined || artistTick === undefined ? undefined : Math.min(trackTick, artistTick);
  useEffect(() => {
    if (startedTick === undefined || unconfirmed.size === 0) return;
    const confirmed: { key: string; kind: FavoriteKind; id: string; favorited: boolean }[] = [];
    for (const [target, favorited] of server) {
      const [kind, id] = target.split(":") as [FavoriteKind, string];
      const key = unconfirmedKey(generation, kind, id);
      const markedAt = unconfirmed.get(key);
      if (markedAt !== undefined && startedTick > markedAt) confirmed.push({ key, kind, id, favorited });
    }
    if (confirmed.length === 0) return;
    queryClient.setQueriesData<CachedLookup>({ queryKey: personalKeys.favoriteLookups(generation) }, (previous) =>
      previous === undefined ? previous : { ...previous, data: confirmed.reduce((lookupData, target) => patchLookup(lookupData, target.kind, target.id, target.favorited), previous.data) },
    );
    confirmFavorites(queryClient, confirmed.map((target) => target.key));
  }, [startedTick, server, unconfirmed, generation, queryClient]);
  const reread = useCallback(() => void queryClient.invalidateQueries({ queryKey: personalKeys.favoriteLookups(generation) }), [queryClient, generation]);
  const refreshing = lookup.isFetching || artistLookup.isFetching;
  const failed = (lookup.isError && data === undefined) || (artistLookup.isError && artistData === undefined);
  const value = useMemo<FavoriteStatus>(
    () => ({
      statusOf: (kind, id) => overlay.get(targetKey(kind, id)) ?? server.get(targetKey(kind, id)),
      pendingOf: (kind, id) => overlay.has(targetKey(kind, id)),
      unconfirmedOf: (kind, id) => unconfirmed.has(unconfirmedKey(generation, kind, id)),
      reread,
      refreshing,
      failed,
    }),
    [overlay, server, unconfirmed, generation, reread, refreshing, failed],
  );
  return <StatusContext value={value}>{children}</StatusContext>;
}
