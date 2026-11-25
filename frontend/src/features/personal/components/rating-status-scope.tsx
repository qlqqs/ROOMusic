import { keepPreviousData, useMutationState, useQuery, useQueryClient } from "@tanstack/react-query";
import { createContext, useCallback, useContext, useMemo, type ReactNode } from "react";
import { useViewerScope } from "../../auth";
import type { FavoriteKind } from "../api/decoders";
import { lookupArtistRatings, lookupRatings } from "../api/ratings";
import { lookupIdsKey, personalKeys } from "../query-keys";

type RatingStatus = {
  /** 本人的评分：1–5，没有评分是 0；还没读到（或读取失败）时为 undefined。进行中的写入先按新值显示。 */
  ratingOf: (kind: FavoriteKind, id: string) => number | undefined;
  /** 这个目标有写入正在进行：同一目标不能再改，PUT 与 DELETE 不会交错到达。 */
  pendingOf: (kind: FavoriteKind, id: string) => boolean;
  /** 重新读取所有已打开的评分。 */
  reread: () => void;
  /** 读取失败、也没有旧数据。 */
  failed: boolean;
};

const StatusContext = createContext<RatingStatus | null>(null);

const targetKey = (kind: FavoriteKind, id: string) => `${kind}:${id.toLowerCase()}`;

export function useRatingStatus(): RatingStatus {
  const status = useContext(StatusContext);
  if (status === null) throw new Error("星级需要放在 RatingStatusScope 里");
  return status;
}

type RatingStatusScopeProps = {
  trackIds?: readonly string[] | undefined;
  groupIds?: readonly string[] | undefined;
  /** 艺人另发一次请求：艺人数据还在整理时只有艺人的星级读不到，曲目与专辑不受影响。 */
  artistIds?: readonly string[] | undefined;
  children: ReactNode;
};

const none: readonly string[] = [];

/**
 * 一页（一个专辑版本、一页歌单、播放器栏的当前曲目）的本人评分：一批 ID 一次请求，范围里的每个星级按目标读取。做法同 FavoriteStatusScope：
 * 换一批 ID 时先沿用上一批里已知的值；所有已打开的范围共用同一个缓存前缀，一处写入，其他范围随之更新。
 */
export function RatingStatusScope({ trackIds = none, groupIds = none, artistIds = none, children }: RatingStatusScopeProps) {
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  const ids = useMemo(() => lookupIdsKey({ trackIds, groupIds }), [trackIds, groupIds]);
  const lookup = useQuery({
    queryKey: personalKeys.ratingLookup(generation, ids),
    queryFn: ({ signal }) => lookupRatings(ids, signal),
    placeholderData: keepPreviousData,
    enabled: ids.trackIds.length > 0 || ids.groupIds.length > 0,
  });
  const artistLookup = useQuery({
    queryKey: personalKeys.ratingArtistLookup(generation, artistIds),
    queryFn: ({ signal }) => lookupArtistRatings(artistIds, signal),
    placeholderData: keepPreviousData,
    enabled: artistIds.length > 0,
  });
  const pending = useMutationState({
    filters: { mutationKey: personalKeys.ratingWrites(generation), status: "pending" },
    select: (mutation) => ({ key: mutation.options.mutationKey, next: mutation.state.variables }),
  });
  const overlay = useMemo(() => {
    const map = new Map<string, number>();
    for (const { key, next } of pending) {
      const [, , , , kind, id] = key ?? [];
      if ((kind === "track" || kind === "album" || kind === "artist") && typeof id === "string" && typeof next === "number") map.set(targetKey(kind, id), next);
    }
    return map;
  }, [pending]);
  const data = lookup.data?.data;
  const artistData = artistLookup.data?.data;
  const server = useMemo(() => {
    const map = new Map<string, number>();
    for (const track of data?.tracks ?? []) map.set(targetKey("track", track.track_id), track.rating ?? 0);
    for (const album of data?.albums ?? []) map.set(targetKey("album", album.group_id), album.rating ?? 0);
    for (const artist of artistData?.artists ?? []) {
      map.set(targetKey("artist", artist.artist_id), artist.rating ?? 0);
      if (artist.resolved_artist_id !== null) map.set(targetKey("artist", artist.resolved_artist_id), artist.rating ?? 0);
    }
    return map;
  }, [data, artistData]);
  const reread = useCallback(() => void queryClient.invalidateQueries({ queryKey: personalKeys.ratingLookups(generation) }), [queryClient, generation]);
  const failed = (lookup.isError && data === undefined) || (artistLookup.isError && artistData === undefined);
  const value = useMemo<RatingStatus>(
    () => ({
      ratingOf: (kind, id) => overlay.get(targetKey(kind, id)) ?? server.get(targetKey(kind, id)),
      pendingOf: (kind, id) => overlay.has(targetKey(kind, id)),
      reread,
      failed,
    }),
    [overlay, server, reread, failed],
  );
  return <StatusContext value={value}>{children}</StatusContext>;
}
