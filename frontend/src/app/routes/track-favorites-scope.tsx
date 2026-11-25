import { useMemo, type ReactNode } from "react";
import { FavoriteStatusScope, RatingStatusScope } from "../../features/personal";

/** 一页曲目的收藏状态与本人评分（各一次批量读取），供这一页每一行的心形与星级使用。曲目列表不变时 ID 也保持同一个引用。 */
export function TrackFavoritesScope({ tracks, children }: { tracks: readonly { track_id: string }[]; children: ReactNode }) {
  const trackIds = useMemo(() => tracks.map((track) => track.track_id), [tracks]);
  return (
    <FavoriteStatusScope trackIds={trackIds}>
      <RatingStatusScope trackIds={trackIds}>{children}</RatingStatusScope>
    </FavoriteStatusScope>
  );
}
