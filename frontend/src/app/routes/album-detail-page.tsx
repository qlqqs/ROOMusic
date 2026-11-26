import { useNavigate } from "@tanstack/react-router";
import { Info, MoreHorizontal, PencilLine } from "lucide-react";
import { useCallback, useMemo, useState, type ReactNode } from "react";
import { AlbumDetailView, type AlbumSearch, type ReleaseActionsContext, type ReleaseDetailDTO, type ReleaseTrackDTO } from "../../features/catalog";
import { EditTrackDialog, ReleaseHistory, useReleaseAdminActions, useReleaseCommands, type ReleaseCommands } from "../../features/governance";
import { ReleaseMatchResults } from "../../features/curation";
import { AddToPlaylistButton, FavoriteButton, FavoriteStatusScope, RatingControl, RatingStatusScope } from "../../features/personal";
import { playerTrackOf, playerTracksOf, TrackLoudness, useAlbumPlayback, useCurrentTrackMarker } from "../../features/player";
import { useViewerScope } from "../../features/auth";
import { formatCount } from "../../shared/lib/number";
import { MenuButton, type MenuAction } from "../../shared/ui";
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

/** 这个专辑与选中版本每一首曲目的收藏状态与本人评分，各一次读出；版本详情还没到时先只读专辑。 */
function AlbumFavoritesScope({ groupId, release, children }: { groupId: string; release: ReleaseDetailDTO | undefined; children: ReactNode }) {
  const groupIds = useMemo(() => [groupId], [groupId]);
  const trackIds = useMemo(() => (release === undefined ? [] : trackIdsOf(release)), [release]);
  return (
    <FavoriteStatusScope groupIds={groupIds} trackIds={trackIds}>
      <RatingStatusScope groupIds={groupIds} trackIds={trackIds}>
        {children}
      </RatingStatusScope>
    </FavoriteStatusScope>
  );
}

const noActions: readonly MenuAction[] = [];

/**
 * 专辑头部的操作区：一条操作行——圆形“播放整张专辑”（唯一的主要操作）、随机播放、收藏这张专辑（按专辑收藏，不分版本）、五颗星的本人评分（同样按专辑，窄屏时单独一行）、
 * 把当前版本整张加入歌单、“更多操作”（下一首播放、加入队列，管理员另有“管理”分组）；下方是播放的状态与结果、管理员的线索与结果提示。
 */
function AlbumActionRow({ release, groupId, adminActions = noActions, adminPanel = null }: { release: ReleaseDetailDTO; groupId: string; adminActions?: readonly MenuAction[]; adminPanel?: ReactNode }) {
  const playback = useAlbumPlayback({ release, groupId });
  const count = release.media.reduce((total, medium) => total + medium.tracks.length, 0);
  return (
    <>
      <div className={styles.row}>
        <span className={styles.primary}>
          {playback.play}
          {playback.shuffle}
        </span>
        <FavoriteButton target={{ kind: "album", id: groupId, title: release.title }} />
        <RatingControl target={{ kind: "album", id: groupId, title: release.title }} placement="header" />
        <AddToPlaylistButton iconOnly trackIds={() => trackIdsOf(release)} subject={`《${release.title}》的 ${formatCount(count)} 首`} disabled={count === 0} />
        <MenuButton label="更多操作" iconOnly icon={<MoreHorizontal size={18} />} actions={[...playback.actions, ...adminActions]} />
      </div>
      {playback.status}
      {adminPanel}
    </>
  );
}

/** 管理员的操作区：管理员的项并入“更多操作”的“管理”分组，线索、结果提示与对话框放在操作行下方。 */
function AdminAlbumActionRow({ context, commands }: { context: ReleaseActionsContext; commands: ReleaseCommands }) {
  const { release, groupId, releaseCount, groupRevision, defaultReleaseId } = context;
  const admin = useReleaseAdminActions({
    release: { id: release.id, title: release.title, revision: release.revision, attentionCount: release.attention_count },
    mergedReleases: release.merged_releases,
    groupId,
    releaseCount,
    groupRevision,
    defaultReleaseId,
    commands,
  });
  return <AlbumActionRow release={release} groupId={groupId} adminActions={admin.actions} adminPanel={admin.panel} />;
}

/** 专辑页“曲目信息”里的响度：来自播放器的分析结果，catalog 不导入 player。 */
const renderTrackFacts = (trackId: string) => <TrackLoudness trackId={trackId} />;

/**
 * 专辑页的一行：“从这首开始播放”放整张里能播放的曲目、从这一首起；其余与各页相同（心形、“更多”），
 * 菜单（含右键）最后多一项“曲目信息”：选中这一行并把焦点移到右栏的曲目信息。
 */
function AlbumTrackActions({ release, track, groupId, showInfo, onEdit }: { release: ReleaseDetailDTO; track: ReleaseTrackDTO; groupId: string; showInfo: () => void; onEdit: ((track: ReleaseTrackDTO) => void) | undefined }) {
  const item = playerTrackOf(release, track, groupId);
  const info: MenuAction = { id: "info", label: "曲目信息", icon: <Info size={16} />, onSelect: showInfo };
  return (
    <TrackActions
      extraActions={onEdit === undefined ? [info] : [{ id: "correct", label: "修正曲目信息…", icon: <PencilLine size={16} />, onSelect: () => onEdit(track) }, info]}
      track={item}
      present
      playPlacement="lead"
      albumLink={false}
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
 * 专辑详情：所有登录用户都能读；管理员在“来源与证据”里多一个完整证据，头部“更多操作”里多一个“管理”分组（编辑发行信息、设定默认版本……）。
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
  // 换版本时用 key 重新开始：播放结果与管理员对话框的开合不带到另一个版本。
  const renderReleaseActions = useCallback(
    (context: ReleaseActionsContext) =>
      context.admin ? (
        <AdminAlbumActionRow key={`actions-${context.release.id}`} context={context} commands={commands} />
      ) : (
        <AlbumActionRow key={`actions-${context.release.id}`} release={context.release} groupId={context.groupId} />
      ),
    [commands],
  );
  // 管理员在曲目行的菜单里修正曲目标题与曲目艺人（M7 I）。
  const [editingTrack, setEditingTrack] = useState<ReleaseTrackDTO | null>(null);
  const onEditTrack = isAdmin ? setEditingTrack : undefined;
  const renderTrackActions = useCallback(
    (track: ReleaseTrackDTO, release: ReleaseDetailDTO, showInfo: () => void) => <AlbumTrackActions key={track.id} release={release} track={track} groupId={groupId} showInfo={showInfo} onEdit={onEditTrack} />,
    [groupId, onEditTrack],
  );
  const renderAdminSections = useCallback(
    ({ release, groupId: currentGroupId }: { release: ReleaseDetailDTO; groupId: string }) => (
      <>
        <ReleaseMatchResults key={`match-${release.id}`} releaseId={release.id} names={release.musicbrainz_names} refresh={refresh} />
        <ReleaseHistory key={`history-${release.id}`} release={{ id: release.id, title: release.title }} groupId={currentGroupId} commands={historyCommands} />
      </>
    ),
    [historyCommands, refresh],
  );
  return (
    <>
      <AlbumDetailView {...props} isAdmin={isAdmin}
        currentTrackId={marker.trackId} currentTrackLabel={marker.label} currentTrackPlaying={marker.playing}
        renderReleaseScope={renderReleaseScope}
        renderReleaseActions={renderReleaseActions} renderTrackActions={renderTrackActions} renderTrackFacts={renderTrackFacts}
        {...(isAdmin ? { renderAdminSections } : {})} />
      {editingTrack === null ? null : <EditTrackDialog trackId={editingTrack.id} trackTitle={editingTrack.title} refresh={refresh} onClose={() => setEditingTrack(null)} />}
    </>
  );
}
