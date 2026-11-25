import { useCallback } from "react";
import { RatingsView, type RatedTrackDTO, type RatingFilter, type RatingSort, type RatingsTab } from "../../features/personal";
import { Page } from "../../shared/ui";
import { PersonalPlayAll } from "./personal-playback";
import { TrackActions } from "./track-actions";
import { playerTrackOfSummary } from "./track-playback";

/** 评过分的一首曲目的行内动作：与收藏页同样的播放、心形与“更多”。 */
function RatedTrackActions({ item }: { item: RatedTrackDTO }) {
  const track = playerTrackOfSummary(item.track);
  return <TrackActions track={track} present={item.track.present} playLabel={`播放 ${track.title}`} retryHint="请在“播放全部”旁重试。" />;
}

/**
 * 我的评分：personal 给出三栏、筛选、排序与列表；曲目栏的“播放全部”（当前筛选与排序）与每行的动作由这里经插槽组合。
 * 栏、筛选、排序与页码来自地址。
 */
export function RatingsPage({ tab, minRating, sort, page }: { tab: RatingsTab; minRating: number; sort: RatingSort; page: number }) {
  const renderPlayAll = useCallback((filter: RatingFilter) => <PersonalPlayAll source={{ kind: "ratings", ...filter }} />, []);
  const renderTrackActions = useCallback((item: RatedTrackDTO) => <RatedTrackActions item={item} />, []);
  return (
    <Page title="我的评分">
      <RatingsView tab={tab} minRating={minRating} sort={sort} page={page} renderPlayAll={renderPlayAll} renderTrackActions={renderTrackActions} />
    </Page>
  );
}
