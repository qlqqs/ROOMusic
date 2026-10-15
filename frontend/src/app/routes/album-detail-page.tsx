import { useNavigate } from "@tanstack/react-router";
import { useCallback } from "react";
import { AlbumDetailView, type AlbumSearch, type ReleaseDetailDTO, type ReleaseTrackDTO } from "../../features/catalog";
import { ReleaseAdminActions, ReleaseHistory, useReleaseCommands } from "../../features/governance";
import { AlbumPlaybackActions, TrackPlaybackActions, useCurrentTrackMarker } from "../../features/player";
import { useViewerScope } from "../../features/auth";
import { useReleaseDataRefresh } from "./use-release-refresh";

type AlbumDetailPageProps = {
  groupId: string;
  release: string | undefined;
  list: AlbumSearch;
};

/**
 * 专辑详情：所有登录用户都能读；管理员在“来源与证据”里多一个完整证据，在头部多一个操作区（编辑发行信息……）。
 * 页面标题与 h1 由详情自己给出（取决于选中的版本）。管理员写操作的命令状态由这里持有：写操作之后路由可能换到另一个专辑组，
 * 详情组件会重新挂载，结果提示仍要留在页面上。头部的操作与“修改记录”里的撤销各有一个命令状态，结果提示各自显示在自己的区域。
 */
export function AlbumDetailPage(props: AlbumDetailPageProps) {
  const { viewer } = useViewerScope();
  const isAdmin = viewer.role === "admin";
  // 只订阅当前曲目与播放状态：音量、队列与进度变化不重绘整张曲目表。
  const marker = useCurrentTrackMarker();
  const { groupId } = props;
  // 修改专辑数据之后，别处缓存的专辑（目录、版本详情）与概览的“最近播放”都要重新读取。
  const refresh = useReleaseDataRefresh();
  // 归组之后版本在另一个专辑组里：导航到那个组并选中这个版本（代表版本时详情页会把 release 参数规范掉），“返回专辑列表”的参数随之保留。
  const navigate = useNavigate();
  const { list } = props;
  const showRelease = useCallback(
    (targetGroupId: string, releaseId: string) => void navigate({ to: "/albums/$groupId", params: { groupId: targetGroupId }, search: { release: releaseId }, state: { albumList: list } }),
    [navigate, list],
  );
  const commands = useReleaseCommands({ refresh, onMoved: showRelease });
  // “修改记录”里的撤销另有一个命令状态：结果提示就近显示在记录区，不与头部的操作结果混在一起。
  const historyCommands = useReleaseCommands({ refresh, onMoved: showRelease });
  // 插槽保持同一个函数引用，曲目行才能跳过没有变化的重绘。
  const renderReleaseActions = useCallback((release: ReleaseDetailDTO) => <AlbumPlaybackActions key={`playback-${release.id}`} release={release} groupId={groupId} />, [groupId]);
  const renderTrackActions = useCallback((track: ReleaseTrackDTO, release: ReleaseDetailDTO) => <TrackPlaybackActions key={track.id} release={release} track={track} groupId={groupId} />, [groupId]);
  const renderAdminActions = useCallback(
    ({ release, groupId: currentGroupId, releaseCount }: { release: ReleaseDetailDTO; groupId: string; releaseCount: number }) => (
      <ReleaseAdminActions
        key={`admin-${release.id}`}
        release={{ id: release.id, title: release.title, revision: release.revision }}
        groupId={currentGroupId}
        releaseCount={releaseCount}
        commands={commands}
      />
    ),
    [commands],
  );
  const renderAdminSections = useCallback(
    ({ release }: { release: ReleaseDetailDTO }) => <ReleaseHistory key={`history-${release.id}`} release={{ id: release.id, title: release.title }} commands={historyCommands} />,
    [historyCommands],
  );
  return <AlbumDetailView {...props} isAdmin={isAdmin}
    currentTrackId={marker.trackId} currentTrackLabel={marker.label}
    renderReleaseActions={renderReleaseActions} renderTrackActions={renderTrackActions}
    {...(isAdmin ? { renderAdminActions, renderAdminSections } : {})} />;
}
