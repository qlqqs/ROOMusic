import { useCallback } from "react";
import { FavoritesView, LikedTracksView, type FavoriteTrackDTO, type FavoritesTab } from "../../features/personal";
import { Page } from "../../shared/ui";
import { PersonalPlayAll } from "./personal-playback";
import { TrackActions } from "./track-actions";
import { playerTrackOfSummary } from "./track-playback";

const favoritesSource = { kind: "favorites" } as const;

/** 收藏的一首曲目的行内动作：与历史页同样的播放、心形与“更多”。 */
function FavoriteTrackActions({ item }: { item: FavoriteTrackDTO }) {
  const track = playerTrackOfSummary(item.track);
  return <TrackActions track={track} present={item.track.present} playLabel={`播放 ${track.title}`} retryHint="请在“播放全部”旁重试。" />;
}

/**
 * 我喜欢的音乐：personal 给出收藏的曲目、翻页与保留行；“播放全部”与每行的播放、心形、“更多”由这里经插槽组合。
 * page 来自地址。
 */
export function LikedPage({ page }: { page: number }) {
  // 插槽保持同一个函数引用，表格的列定义才不会每次重建。
  const renderPlayAll = useCallback(() => <PersonalPlayAll source={favoritesSource} />, []);
  const renderTrackActions = useCallback((item: FavoriteTrackDTO) => <FavoriteTrackActions item={item} />, []);
  return (
    <Page title="我喜欢的音乐">
      <LikedTracksView page={page} renderPlayAll={renderPlayAll} renderTrackActions={renderTrackActions} />
    </Page>
  );
}

/** 我的收藏：收藏的专辑与艺人两栏；tab 与 page 来自地址。 */
export function FavoritesPage({ tab, page }: { tab: FavoritesTab; page: number }) {
  return (
    <Page title="我的收藏">
      <FavoritesView tab={tab} page={page} />
    </Page>
  );
}
