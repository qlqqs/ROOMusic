import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useMatchRoute, useRouterState } from "@tanstack/react-router";
import { useCallback, useMemo, useSyncExternalStore, type ReactNode } from "react";
import { type ArtistDetailDTO, artistsKeys } from "../../features/artists";
import { useViewerScope } from "../../features/auth";
import { catalogKeys, type ReleaseGroupSummaryDTO } from "../../features/catalog";
import { listeningKeys } from "../../features/listening";
import { personalKeys, type PlaylistSummaryDTO } from "../../features/personal";
import { usePlayerSelector } from "../../features/player";
import { StewardHostProvider, type PageKind, type StewardDataScope, type StewardPageContext, type StewardPlaybackContext } from "../../features/steward";
import type { ApiResponse } from "../../shared/api";
import { isUuid } from "../../shared/lib/ids";
import { navLabelOf } from "./nav-items";

// 管家的页面上下文与个人数据刷新的接线（C5）：steward 不导入其他 feature，“正在看”“正在播放”由这里从路由、查询缓存与播放器组合，
// 管家的工具改了收藏、歌单或播放历史之后，按这里给的查询键让缓存失效。

/** 列表页等没有对象的页面：侧栏里没有的几个页面的名字。 */
const extraPageNames: Readonly<Record<string, string>> = { "/me": "我的", "/search": "搜索结果" };

function pageNameOf(pathname: string): string | undefined {
  if (pathname === "/settings" || pathname.startsWith("/settings/")) return "设置";
  return navLabelOf(pathname) ?? extraPageNames[pathname];
}

/** 缓存里最近读到的那份数据取出的标题；没有读过时是 undefined。只看缓存，不发请求。 */
function cachedTitle<T>(client: QueryClient, queryKey: readonly unknown[], pick: (data: T) => string | undefined): string | undefined {
  let latest: { at: number; title: string } | null = null;
  for (const query of client.getQueryCache().findAll({ queryKey })) {
    const response = query.state.data as ApiResponse<T> | undefined;
    const title = response === undefined ? undefined : pick(response.data);
    if (title === undefined || title.trim() === "") continue;
    if (latest === null || query.state.dataUpdatedAt > latest.at) latest = { at: query.state.dataUpdatedAt, title };
  }
  return latest?.title;
}

function useCachedTitle<T>(queryKey: readonly unknown[] | null, pick: (data: T) => string | undefined): string | undefined {
  const client = useQueryClient();
  const subscribe = useCallback((onChange: () => void) => client.getQueryCache().subscribe(onChange), [client]);
  // 快照是字符串：缓存的其他变化不会让订阅者重绘。
  return useSyncExternalStore(subscribe, () => (queryKey === null ? undefined : cachedTitle(client, queryKey, pick)));
}

const albumTitleOf = (group: ReleaseGroupSummaryDTO) => group.representative.title;
const playlistNameOf = (detail: { playlist: PlaylistSummaryDTO }) => detail.playlist.name;

/** 正在看的页面：专辑、艺人、歌单页带对象 ID 与（缓存里读到的）标题，其他页面带路径与页面名；整页对话里是 null。 */
function usePageSource(): StewardPageContext | null {
  const { generation } = useViewerScope();
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  const matchRoute = useMatchRoute();
  const album = matchRoute({ to: "/albums/$groupId" });
  const artist = matchRoute({ to: "/artists/$artistId" });
  const playlist = matchRoute({ to: "/me/playlists/$playlistId" });
  const target: { kind: PageKind; id: string } | null =
    album !== false ? { kind: "album", id: album.groupId } : artist !== false ? { kind: "artist", id: artist.artistId } : playlist !== false ? { kind: "playlist", id: playlist.playlistId } : null;
  const kind = target?.kind ?? "other";
  // 地址里的 ID 不是 UUID 时页面自己显示“不存在”，上下文里不带它。
  const id = target !== null && isUuid(target.id) ? target.id : undefined;
  // 艺人详情的键里有目录的 epoch：在全部艺人详情里按 ID 找。
  const artistNameOf = useCallback((detail: ArtistDetailDTO) => (detail.artist.id === id ? detail.artist.name : undefined), [id]);
  const albumTitle = useCachedTitle(kind === "album" && id !== undefined ? catalogKeys.group(generation, id) : null, albumTitleOf);
  const artistName = useCachedTitle(kind === "artist" && id !== undefined ? artistsKeys.details(generation) : null, artistNameOf);
  const playlistName = useCachedTitle(kind === "playlist" && id !== undefined ? personalKeys.playlistDetail(generation, id) : null, playlistNameOf);
  const title = kind === "album" ? albumTitle : kind === "artist" ? artistName : kind === "playlist" ? playlistName : pageNameOf(pathname);
  const steward = pathname === "/steward" || pathname.startsWith("/steward/");
  return useMemo(
    () => (steward ? null : { path: pathname, kind, ...(id === undefined ? {} : { id }), ...(title === undefined ? {} : { title }) }),
    [steward, pathname, kind, id, title],
  );
}

/** 正在控制的分区（设备名就是分区名）与正在放的曲目；还没读到分区时是 null。 */
function usePlaybackSource(): StewardPlaybackContext | null {
  const zoneId = usePlayerSelector((snapshot) => snapshot.zone?.zoneId ?? null);
  const zoneName = usePlayerSelector((snapshot) => snapshot.device?.name ?? "");
  const trackId = usePlayerSelector((snapshot) => snapshot.current?.track.trackId ?? null);
  const title = usePlayerSelector((snapshot) => snapshot.current?.track.title ?? "");
  const artist = usePlayerSelector((snapshot) => snapshot.current?.track.artist ?? "");
  const status = usePlayerSelector((snapshot) => snapshot.status);
  return useMemo(() => {
    if (zoneId === null) return null;
    return {
      zone_id: zoneId,
      ...(zoneName === "" ? {} : { zone_name: zoneName }),
      ...(trackId === null ? {} : { track_id: trackId }),
      ...(trackId === null || title === "" ? {} : { title }),
      ...(trackId === null || artist === "" ? {} : { artist }),
      status,
    };
  }, [zoneId, zoneName, trackId, title, artist, status]);
}

const dataKeys: Readonly<Record<StewardDataScope, (generation: number) => readonly unknown[]>> = {
  favorites: personalKeys.favorites,
  playlists: personalKeys.playlists,
  history: listeningKeys.all,
};

/** 已登录外壳里挂一次：把页面上下文、播放上下文与个人数据的刷新交给管家（面板与整页对话）。 */
export function StewardHostScope({ children }: { children: ReactNode }) {
  const client = useQueryClient();
  const { generation } = useViewerScope();
  const page = usePageSource();
  const playback = usePlaybackSource();
  const refreshData = useCallback((scope: StewardDataScope) => void client.invalidateQueries({ queryKey: dataKeys[scope](generation) }), [client, generation]);
  const host = useMemo(() => ({ page, playback, refreshData }), [page, playback, refreshData]);
  return <StewardHostProvider value={host}>{children}</StewardHostProvider>;
}
