import { useNavigate } from "@tanstack/react-router";
import { useCallback, useMemo, type ReactNode } from "react";
import { AlbumDetailView, type AlbumSearch, type ReleaseDetailDTO, type ReleaseTrackDTO } from "../../features/catalog";
import { ReleaseAdminActions, ReleaseHistory, useReleaseCommands } from "../../features/governance";
import { AddToPlaylistButton, FavoriteButton, FavoriteStatusScope } from "../../features/personal";
import { AlbumPlaybackActions, playerTrackOf, playerTracksOf, useCurrentTrackMarker } from "../../features/player";
import { useViewerScope } from "../../features/auth";
import { formatCount } from "../../shared/lib/number";
import { QUEUE_FULL_TEXT, TrackActions } from "./track-actions";
import { unplayableReason } from "./track-playback";
import { useReleaseDataRefresh } from "./use-release-refresh";
import styles from "./album-detail-page.module.css";

type AlbumDetailPageProps = {
  groupId: string;
  release: string | undefined;
  /** 要定位的曲目（地址里的 track）；没有就不定位。 */
  track: string | undefined;
  list: AlbumSearch;
};

/** 选中版本的全部曲目 ID，按碟序与轨序（“添加到歌单…”整张加入的就是当前看的这个版本）。 */
function trackIdsOf(release: ReleaseDetailDTO): string[] {
  return release.media.flatMap((medium) => medium.tracks.map((track) => track.id));
}

/** 这个专辑与选中版本每一首曲目的收藏状态，一次读出；版本详情还没到时先只读专辑。 */
function AlbumFavoritesScope({ groupId, release, children }: { groupId: string; release: ReleaseDetailDTO | undefined; children: ReactNode }) {
  const groupIds = useMemo(() => [groupId], [groupId]);
  const trackIds = useMemo(() => (release === undefined ? [] : trackIdsOf(release)), [release]);
  return (
    <FavoriteStatusScope groupIds={groupIds} trackIds={trackIds}>
      {children}
    </FavoriteStatusScope>
  );
}

/** 专辑头部：播放整张、加入队列，以及收藏这张专辑（按专辑收藏，不分版本）与把当前版本整张加入歌单。 */
function AlbumHeaderActions({ release, groupId }: { release: ReleaseDetailDTO; groupId: string }) {
  const count = release.media.reduce((total, medium) => total + medium.tracks.length, 0);
  return (
    <div className={styles.headerActions}>
      <AlbumPlaybackActions release={release} groupId={groupId} />
      <div className={styles.personal}>
        <FavoriteButton target={{ kind: "album", id: groupId, title: release.title }} />
        <AddToPlaylistButton trackIds={() => trackIdsOf(release)} subject={`《${release.title}》的 ${formatCount(count)} 首`} disabled={count === 0} />
      </div>
    </div>
  );
}

/** 专辑页的一行：“从这首开始播放”放整张里能播放的曲目、从这一首起；其余与各页相同（心形、“更多”）。 */
function AlbumTrackActions({ release, track, groupId }: { release: ReleaseDetailDTO; track: ReleaseTrackDTO; groupId: string }) {
  const item = playerTrackOf(release, track, groupId);
  return (
    <TrackActions
      track={item}
      present
      playLabel="从这首开始播放"
      retryHint="请在专辑头部重试。"
      onPlay={({ engine, table }) => {
        const playable = playerTracksOf(release, groupId).filter((entry) => unplayableReason(entry, true, table) === null);
        if (engine.playTracks(playable, playable.findIndex((entry) => entry.trackId === track.id)) === "queue_full") return QUEUE_FULL_TEXT;
        const skipped = release.media.reduce((total, medium) => total + medium.tracks.length, 0) - playable.length;
        return skipped > 0 ? `跳过 ${skipped} 首不可播放的曲目。` : "";
      }}
    />
  );
}

/**
 * 专辑详情：所有登录用户都能读；管理员在“来源与证据”里多一个完整证据，在头部多一个操作区（编辑发行信息、设定默认版本……）。
 * 页面标题与 h1 由详情自己给出（取决于选中的版本）。管理员写操作的命令状态由这里持有：写操作之后路由可能换到另一个专辑组，
 * 详情组件会重新挂载，结果提示仍要留在页面上。头部的操作与“修改记录”里的撤销各有一个命令状态，结果提示各自显示在自己的区域。
 * 头部与每一行的心形共用一个收藏状态范围（整个版本一次读取）。
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
  const renderReleaseScope = useCallback(
    (context: { groupId: string; release: ReleaseDetailDTO | undefined }, children: ReactNode) => (
      <AlbumFavoritesScope groupId={context.groupId} release={context.release}>
        {children}
      </AlbumFavoritesScope>
    ),
    [],
  );
  const renderReleaseActions = useCallback((release: ReleaseDetailDTO) => <AlbumHeaderActions key={`playback-${release.id}`} release={release} groupId={groupId} />, [groupId]);
  const renderTrackActions = useCallback((track: ReleaseTrackDTO, release: ReleaseDetailDTO) => <AlbumTrackActions key={track.id} release={release} track={track} groupId={groupId} />, [groupId]);
  const renderAdminActions = useCallback(
    ({ release, groupId: currentGroupId, releaseCount, groupRevision, defaultReleaseId }: { release: ReleaseDetailDTO; groupId: string; releaseCount: number; groupRevision: number; defaultReleaseId: string | null }) => (
      <ReleaseAdminActions
        key={`admin-${release.id}`}
        release={{ id: release.id, title: release.title, revision: release.revision, attentionCount: release.attention_count }}
        groupId={currentGroupId}
        releaseCount={releaseCount}
        groupRevision={groupRevision}
        defaultReleaseId={defaultReleaseId}
        commands={commands}
      />
    ),
    [commands],
  );
  const renderAdminSections = useCallback(
    ({ release, groupId: currentGroupId }: { release: ReleaseDetailDTO; groupId: string }) => (
      <ReleaseHistory key={`history-${release.id}`} release={{ id: release.id, title: release.title }} groupId={currentGroupId} commands={historyCommands} />
    ),
    [historyCommands],
  );
  return <AlbumDetailView {...props} isAdmin={isAdmin}
    currentTrackId={marker.trackId} currentTrackLabel={marker.label}
    renderReleaseScope={renderReleaseScope}
    renderReleaseActions={renderReleaseActions} renderTrackActions={renderTrackActions}
    {...(isAdmin ? { renderAdminActions, renderAdminSections } : {})} />;
}
