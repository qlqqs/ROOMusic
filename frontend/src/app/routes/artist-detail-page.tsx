import { useNavigate } from "@tanstack/react-router";
import { MoreHorizontal } from "lucide-react";
import { useCallback, useMemo, type ReactNode } from "react";
import { useViewerScope } from "../../features/auth";
import { artistIdOf, ArtistDetailView, type ArtistDetailDTO, type ArtistDetailSearch } from "../../features/artists";
import type { TrackSummaryDTO } from "../../features/catalog";
import { useArtistAdminActions, useArtistCommands, type ArtistCommands } from "../../features/governance";
import { FavoriteButton, FavoriteStatusScope } from "../../features/personal";
import { MenuButton } from "../../shared/ui";
import { ArtistPlayActions } from "./artist-playback";
import { TrackActions } from "./track-actions";
import { TrackFavoritesScope } from "./track-favorites-scope";
import { playerTrackOfSummary } from "./track-playback";
import { useArtistDataRefresh } from "./use-artist-refresh";

/** 艺人页一行的动作：与历史、收藏、歌单、搜索结果同一套（播放、心形、“更多”）。缺失的曲目不能播放，仍可收藏、加入歌单。 */
function ArtistTrackActions({ item }: { item: TrackSummaryDTO }) {
  const track = playerTrackOfSummary(item);
  return <TrackActions track={track} present={item.present} playLabel={`播放 ${track.title}`} retryHint="请在“播放全部”旁重试。" />;
}

/** 收藏这位艺人（艺人的收藏状态单独一次读取）。 */
function ArtistFavorite({ detail }: { detail: ArtistDetailDTO }) {
  const artistIds = useMemo(() => [detail.artist.id], [detail.artist.id]);
  return (
    <FavoriteStatusScope artistIds={artistIds}>
      <FavoriteButton target={{ kind: "artist", id: detail.artist.id, title: detail.artist.name }} />
    </FavoriteStatusScope>
  );
}

/** 头部的操作行（所有成员）：播放全部、随机播放与心形；普通成员没有“更多”（里面没有别的项）。 */
function ArtistHeaderActions({ detail }: { detail: ArtistDetailDTO }) {
  return <ArtistPlayActions artistId={detail.artist.id} extra={<ArtistFavorite detail={detail} />} />;
}

/** 管理员的操作行：再加“更多”，里面是“管理”分组（合并艺人…、拆开…、修改主名…、操作记录）；结果提示与对话框在操作行下方。 */
function AdminArtistHeaderActions({ detail, commands }: { detail: ArtistDetailDTO; commands: ArtistCommands }) {
  const admin = useArtistAdminActions({ artist: { id: detail.artist.id, name: detail.artist.name, revision: detail.revision, nameSource: detail.name_source }, commands });
  return (
    <>
      <ArtistPlayActions
        artistId={detail.artist.id}
        extra={
          <>
            <ArtistFavorite detail={detail} />
            <MenuButton label="更多操作" iconOnly icon={<MoreHorizontal size={18} />} actions={admin.actions} />
          </>
        }
      />
      {admin.panel}
    </>
  );
}

/**
 * 艺人页（所有登录用户）：artists 给出页头、名字、专辑与按角色分栏的曲目；头部的播放、心形与管理员的“更多”、每行的动作由这里经插槽组合。
 * 这位艺人已经并入别的艺人时，用替换历史的方式带到现在的艺人页（地址里的页码不带过去）。
 */
export function ArtistDetailPage({ artistId, search }: { artistId: string; search: ArtistDetailSearch }) {
  const { viewer } = useViewerScope();
  const isAdmin = viewer.role === "admin";
  const navigate = useNavigate();
  const refresh = useArtistDataRefresh();
  const commands = useArtistCommands({ refresh });
  const onRedirect = useCallback((target: string) => void navigate({ to: "/artists/$artistId", params: { artistId: target }, replace: true }), [navigate]);
  // 插槽保持同一个函数引用，表格的列定义才不会每次重建。换艺人时用 key 重新开始：结果与对话框的开合不带到另一位。
  const renderHeaderActions = useCallback(
    (detail: ArtistDetailDTO) =>
      isAdmin ? <AdminArtistHeaderActions key={detail.artist.id} detail={detail} commands={commands} /> : <ArtistHeaderActions key={detail.artist.id} detail={detail} />,
    [isAdmin, commands],
  );
  const renderTrackActions = useCallback((item: TrackSummaryDTO) => <ArtistTrackActions item={item} />, []);
  const renderTrackRowsScope = useCallback((items: readonly TrackSummaryDTO[], children: ReactNode) => <TrackFavoritesScope tracks={items}>{children}</TrackFavoritesScope>, []);
  return (
    <ArtistDetailView
      artistId={artistIdOf(artistId)}
      search={search}
      onRedirect={onRedirect}
      renderHeaderActions={renderHeaderActions}
      renderTrackActions={renderTrackActions}
      renderTrackRowsScope={renderTrackRowsScope}
    />
  );
}
