import { useNavigate } from "@tanstack/react-router";
import { useCallback, useMemo, type ReactNode } from "react";
import { useViewerScope } from "../../features/auth";
import { artistIdOf, ArtistDetailView, type ArtistDetailDTO, type ArtistDetailSearch } from "../../features/artists";
import type { TrackSummaryDTO } from "../../features/catalog";
import { ArtistAdminActions, useArtistCommands } from "../../features/governance";
import { FavoriteButton, FavoriteStatusScope } from "../../features/personal";
import { ArtistPlayActions } from "./artist-playback";
import { TrackActions } from "./track-actions";
import { TrackFavoritesScope } from "./track-favorites-scope";
import { playerTrackOfSummary } from "./track-playback";
import { useArtistDataRefresh } from "./use-artist-refresh";
import styles from "./artist-detail-page.module.css";

/** 艺人页一行的动作：与历史、收藏、歌单、搜索结果同一套（播放、心形、“更多”）。缺失的曲目不能播放，仍可收藏、加入歌单。 */
function ArtistTrackActions({ item }: { item: TrackSummaryDTO }) {
  const track = playerTrackOfSummary(item);
  return <TrackActions track={track} present={item.present} playName={`播放 ${track.title}`} retryHint="请在“播放全部”旁重试。" />;
}

/** 头部：播放全部、随机播放与收藏这位艺人（艺人的收藏状态单独一次读取）。 */
function ArtistHeaderActions({ detail }: { detail: ArtistDetailDTO }) {
  const artistIds = useMemo(() => [detail.artist.id], [detail.artist.id]);
  return (
    <>
      <ArtistPlayActions artistId={detail.artist.id} />
      <FavoriteStatusScope artistIds={artistIds}>
        <FavoriteButton target={{ kind: "artist", id: detail.artist.id, title: detail.artist.name }} />
      </FavoriteStatusScope>
    </>
  );
}

/**
 * 艺人页（所有登录用户）：artists 给出名字、专辑与按角色分栏的曲目；头部的播放与心形、每行的动作、管理员的合并/拆开/改名由这里经插槽组合。
 * 这位艺人已经并入别的艺人时，用替换历史的方式带到现在的艺人页（地址里的页码不带过去）。
 */
export function ArtistDetailPage({ artistId, search }: { artistId: string; search: ArtistDetailSearch }) {
  const { viewer } = useViewerScope();
  const navigate = useNavigate();
  const refresh = useArtistDataRefresh();
  const commands = useArtistCommands({ refresh });
  const onRedirect = useCallback((target: string) => void navigate({ to: "/artists/$artistId", params: { artistId: target }, replace: true }), [navigate]);
  // 插槽保持同一个函数引用，表格的列定义才不会每次重建。
  const renderHeaderActions = useCallback((detail: ArtistDetailDTO) => <ArtistHeaderActions detail={detail} />, []);
  const renderTrackActions = useCallback((item: TrackSummaryDTO) => <ArtistTrackActions item={item} />, []);
  const renderTrackRowsScope = useCallback((items: readonly TrackSummaryDTO[], children: ReactNode) => <TrackFavoritesScope tracks={items}>{children}</TrackFavoritesScope>, []);
  const renderAdminActions = useCallback(
    (detail: ArtistDetailDTO) => (
      <div className={styles.admin}>
        <ArtistAdminActions artist={{ id: detail.artist.id, name: detail.artist.name, revision: detail.revision, nameSource: detail.name_source }} commands={commands} />
      </div>
    ),
    [commands],
  );
  return (
    <ArtistDetailView
      artistId={artistIdOf(artistId)}
      search={search}
      onRedirect={onRedirect}
      renderHeaderActions={renderHeaderActions}
      renderTrackActions={renderTrackActions}
      renderTrackRowsScope={renderTrackRowsScope}
      {...(viewer.role === "admin" ? { renderAdminActions } : {})}
    />
  );
}
