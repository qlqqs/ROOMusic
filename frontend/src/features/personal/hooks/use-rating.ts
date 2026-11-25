import { useMutation, useQueryClient } from "@tanstack/react-query";
import { failureOf, isHttpFailure, isOutcomeUncertain, type ApiResponse } from "../../../shared/api";
import { useViewerScope } from "../../auth";
import type { FavoriteKind, RatingLookupDTO } from "../api/decoders";
import { setRating } from "../api/ratings";
import { personalKeys } from "../query-keys";
import { usePersonalNotify } from "../sync";

/** 把一份批量评分里的一个目标改成确定的值（null 是没有评分），其他目标不动。 */
export function patchRatingLookup(lookup: RatingLookupDTO, kind: FavoriteKind, id: string, rating: number | null): RatingLookupDTO {
  const target = id.toLowerCase();
  if (kind === "track") return { ...lookup, tracks: lookup.tracks.map((track) => (track.track_id.toLowerCase() === target ? { ...track, rating } : track)) };
  if (kind === "album") return { ...lookup, albums: lookup.albums.map((album) => (album.group_id.toLowerCase() === target ? { ...album, rating } : album)) };
  // 合并过的旧 ID 与现在的 ID 是同一位：写一处，等价的锚点一起变。
  const matches = (artist: RatingLookupDTO["artists"][number]) => artist.artist_id.toLowerCase() === target || artist.resolved_artist_id?.toLowerCase() === target;
  return { ...lookup, artists: lookup.artists.map((artist) => (matches(artist) ? { ...artist, rating } : artist)) };
}

export type RatingWriteFailure = { uncertain: boolean; gone: boolean; preparing: boolean };

function failureOfWrite(error: unknown): RatingWriteFailure {
  const failure = failureOf(error);
  return { uncertain: isOutcomeUncertain(failure), gone: isHttpFailure(failure, 404), preparing: isHttpFailure(failure, 409, "artist_catalog_not_ready") };
}

/** 写入失败时星级旁边的一句话。 */
export function ratingFailureText({ uncertain, gone, preparing }: RatingWriteFailure): string {
  if (uncertain) return "不确定是否保存了评分，正在重新读取。";
  if (gone) return "已经找不到它了。";
  if (preparing) return "艺人信息正在整理，请稍后再试。";
  return "评分没有保存，请稍后再试。";
}

/**
 * 设置或清除一个目标的评分（rating 为 0 是清除）。进行中的值由 RatingStatusScope 叠加显示（乐观），失败后回到服务端的值。
 * 成功后修补每一份已缓存的批量评分，再让评分的全部查询失效。评分的写入是幂等的：结果不确定时重新读取就能对上，
 * 不必像心形那样在确认之前锁住目标。
 */
export function useRatingWrite(kind: FavoriteKind, id: string) {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  const { generation } = scope;
  const notify = usePersonalNotify();
  const mutation = useMutation({
    mutationKey: personalKeys.ratingWrite(generation, kind, id),
    mutationFn: (rating: number) => setRating(kind, id, rating),
    onSuccess: (response) => {
      queryClient.setQueriesData<ApiResponse<RatingLookupDTO>>({ queryKey: personalKeys.ratingLookups(generation) }, (previous) =>
        previous === undefined ? previous : { ...previous, data: patchRatingLookup(previous.data, kind, id, response.data.rating) },
      );
      notify("ratings");
    },
    onSettled: () => void queryClient.invalidateQueries({ queryKey: personalKeys.ratings(generation) }),
  });
  return {
    /** 回调只在发起时的会话仍是当前会话时调用。 */
    write: (rating: number, callbacks?: { onSucceeded?: (() => void) | undefined; onFailed?: (failure: RatingWriteFailure) => void }) => {
      mutation.mutate(rating, {
        onSuccess: () => {
          if (scope.isCurrent()) callbacks?.onSucceeded?.();
        },
        onError: (error) => {
          if (scope.isCurrent()) callbacks?.onFailed?.(failureOfWrite(error));
        },
      });
    },
  };
}
