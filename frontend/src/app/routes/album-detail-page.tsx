import { useCallback } from "react";
import { AlbumDetailView, type AlbumSearch, type ReleaseDetailDTO, type ReleaseTrackDTO } from "../../features/catalog";
import { AlbumPlaybackActions, TrackPlaybackActions, useCurrentTrackMarker } from "../../features/player";
import { useViewerScope } from "../../features/auth";

type AlbumDetailPageProps = {
  groupId: string;
  release: string | undefined;
  list: AlbumSearch;
};

/** 专辑详情：所有登录用户都能读；管理员在“来源与证据”里多一个完整证据。页面标题与 h1 由详情自己给出（取决于选中的版本）。 */
export function AlbumDetailPage(props: AlbumDetailPageProps) {
  const { viewer } = useViewerScope();
  // 只订阅当前曲目与播放状态：音量、队列与进度变化不重绘整张曲目表。
  const marker = useCurrentTrackMarker();
  const { groupId } = props;
  // 插槽保持同一个函数引用，曲目行才能跳过没有变化的重绘。
  const renderReleaseActions = useCallback((release: ReleaseDetailDTO) => <AlbumPlaybackActions key={release.id} release={release} groupId={groupId} />, [groupId]);
  const renderTrackActions = useCallback((track: ReleaseTrackDTO, release: ReleaseDetailDTO) => <TrackPlaybackActions key={track.id} release={release} track={track} groupId={groupId} />, [groupId]);
  return <AlbumDetailView {...props} isAdmin={viewer.role === "admin"}
    currentTrackId={marker.trackId} currentTrackLabel={marker.label}
    renderReleaseActions={renderReleaseActions} renderTrackActions={renderTrackActions} />;
}
