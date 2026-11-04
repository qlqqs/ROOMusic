import { useMutation, useQueryClient } from "@tanstack/react-query";
import { failureOf, isHttpFailure, isOutcomeUncertain, type ApiResponse } from "../../../shared/api";
import { useViewerScope } from "../../auth";
import type { FavoriteKind, FavoriteLookupDTO } from "../api/decoders";
import { setFavorite } from "../api/favorites";
import { personalKeys } from "../query-keys";
import { usePersonalNotify } from "../sync";
import { markUnconfirmed, unconfirmedKey } from "../unconfirmed-favorites";

/** 已缓存的一份批量状态：响应加上这次读取开始时的序号（见 unconfirmed-favorites）。 */
export type CachedLookup = ApiResponse<FavoriteLookupDTO> & { startedTick: number };

/** 把一份批量状态里的一个目标改成确定的值，其他目标不动。 */
export function patchLookup(lookup: FavoriteLookupDTO, kind: FavoriteKind, id: string, favorited: boolean): FavoriteLookupDTO {
  const target = id.toLowerCase();
  if (kind === "track") {
    return { ...lookup, tracks: lookup.tracks.map((track) => (track.track_id.toLowerCase() === target ? { ...track, favorited } : track)) };
  }
  return { ...lookup, albums: lookup.albums.map((album) => (album.group_id.toLowerCase() === target ? { ...album, favorited } : album)) };
}

export type FavoriteToggleFailure = { favorited: boolean; uncertain: boolean; gone: boolean };

function failureOfToggle(error: unknown, favorited: boolean): FavoriteToggleFailure {
  return { favorited, uncertain: isOutcomeUncertain(failureOf(error)), gone: isHttpFailure(failureOf(error), 404) };
}

/** 切换失败时心形旁边的一句话。 */
export function favoriteFailureText({ favorited, uncertain, gone }: FavoriteToggleFailure): string {
  if (uncertain) return "不确定是否成功，正在重新读取。";
  if (gone) return "已经找不到它了。";
  return favorited ? "没能收藏，请稍后再试。" : "没能取消收藏，请稍后再试。";
}

/**
 * 切换一个目标的收藏。进行中的目标值由 FavoriteStatusScope 叠加显示（乐观），失败后自然回到服务端的状态；
 * 成功后只确认这一个目标（修补每一份已缓存的批量状态），再让收藏的全部查询失效，由服务端的状态收敛。
 * 同一目标的切换共用一个写操作键，范围据此在进行中禁止再次切换。
 */
export function useFavoriteToggle(kind: FavoriteKind, id: string) {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  const { generation } = scope;
  const notify = usePersonalNotify();
  const mutation = useMutation({
    mutationKey: personalKeys.favoriteToggle(generation, kind, id),
    mutationFn: (favorited: boolean) => setFavorite(kind, id, favorited),
    onSuccess: (response) => {
      queryClient.setQueriesData<CachedLookup>({ queryKey: personalKeys.favoriteLookups(generation) }, (previous) =>
        previous === undefined ? previous : { ...previous, data: patchLookup(previous.data, kind, id, response.data.favorited) },
      );
      notify("favorites");
    },
    onError: (error) => {
      // 结果不确定：在重新读到确定的状态之前，所有位置的这个目标都不能再切换。先标记，再由 onSettled 让批量状态重新读取。
      if (isOutcomeUncertain(failureOf(error))) markUnconfirmed(queryClient, unconfirmedKey(generation, kind, id));
    },
    onSettled: () => void queryClient.invalidateQueries({ queryKey: personalKeys.favorites(generation) }),
  });
  const failure: FavoriteToggleFailure | null = mutation.status === "error" && typeof mutation.variables === "boolean" ? failureOfToggle(mutation.error, mutation.variables) : null;
  return {
    /** onFailed 只在发起时的会话仍是当前会话时调用。 */
    toggle: (favorited: boolean, callbacks?: { onFailed?: (failure: FavoriteToggleFailure) => void }) =>
      mutation.mutate(favorited, {
        onError: (error) => {
          if (scope.isCurrent()) callbacks?.onFailed?.(failureOfToggle(error, favorited));
        },
      }),
    failure,
  };
}
