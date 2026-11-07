import { useCallback } from "react";
import { PlaylistDetailView, type PlaylistItemDTO, type PlaylistSummaryDTO } from "../../features/personal";
import { Page, type MenuAction } from "../../shared/ui";
import { PersonalPlayAll } from "./personal-playback";
import { TrackActions } from "./track-actions";
import { playerTrackOfSummary } from "./track-playback";

/** 歌单的一行：与各页相同的播放、心形与“更多”，歌单自己的移动与移除接在菜单后面。 */
function PlaylistTrackActions({ item, actions }: { item: PlaylistItemDTO; actions: readonly MenuAction[] }) {
  const track = playerTrackOfSummary(item.track);
  return <TrackActions track={track} present={item.track.present} playLabel={`播放 ${track.title}`} retryHint="请在“播放全部”旁重试。" extraActions={actions} />;
}

/**
 * 歌单详情：personal 给出歌单的内容、排序、移除与撤销、改名与删除；“播放全部”与每行的播放、心形、“更多”由这里经插槽组合。
 * playlistId 与 page 来自地址。
 */
export function PlaylistDetailPage({ playlistId, page }: { playlistId: string; page: number }) {
  const renderPlayAll = useCallback((playlist: PlaylistSummaryDTO) => <PersonalPlayAll source={{ kind: "playlist", playlistId: playlist.id }} />, []);
  const renderTrackActions = useCallback((item: PlaylistItemDTO, actions: readonly MenuAction[]) => <PlaylistTrackActions item={item} actions={actions} />, []);
  return (
    <Page title="我的歌单">
      <PlaylistDetailView playlistId={playlistId} page={page} renderPlayAll={renderPlayAll} renderTrackActions={renderTrackActions} />
    </Page>
  );
}
