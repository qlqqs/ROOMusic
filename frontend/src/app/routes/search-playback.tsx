import type { SearchTrackItemDTO } from "../../features/search";
import { usePlaybackCapabilities } from "../../features/player";
import { Button, FailureNotice } from "../../shared/ui";
import { TrackActions } from "./track-actions";
import { playerTrackOfSummary } from "./track-playback";

// 搜索结果曲目行的动作由 app 组合：search 不导入 player 与 personal，player 也不认识搜索结果。
// 行内动作是各页共用的 TrackActions（播放、心形、“更多”里的下一首播放、加入队列、添加到歌单…），与历史、收藏、歌单页一致；
// 心形的状态来自结果页按页包住的收藏状态范围（search-page.tsx）。

/**
 * 一行的动作：“播放”替换当前队列并只播放这一首；文件缺失或不能播放时播放与入队不可用，但仍可以收藏、加入歌单。
 */
export function SearchTrackActions({ item }: { item: SearchTrackItemDTO }) {
  const track = playerTrackOfSummary(item);
  return <TrackActions track={track} present={item.present} playLabel={`播放 ${track.title}`} retryHint="请在曲目列表上方重试。" />;
}

/** 曲目栏上方：无法检查播放能力时给一次重试（不在每一行重复）；能检查时不渲染。 */
export function SearchPlaybackNotice() {
  const capability = usePlaybackCapabilities();
  if (!capability.isError) return null;
  return <FailureNotice error={capability.error} title="无法检查播放能力" action={<Button onClick={() => void capability.refetch()}>重试检查</Button>} />;
}
