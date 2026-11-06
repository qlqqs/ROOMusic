import { createRootRouteWithContext, createRoute, createRouter, Outlet, redirect, useLocation, type RouterHistory } from "@tanstack/react-router";
import { sanitizeRedirect, type SessionController } from "../features/auth";
import { albumListSearchFromState, parseAlbumDetailSearch, parseAlbumSearch, type AlbumDetailSearch, type AlbumSearch } from "../features/catalog";
import { parseClaimsSearch, parseIssuesSearch, parseOperationsSearch, type ClaimsSearch, type IssuesSearch, type OperationsSearch } from "../features/governance";
import { ScanDetail } from "../features/library";
import { parseSearchPageSearch, type SearchPageSearch } from "../features/search";
import { parseArtistDetailSearch, parseArtistsSearch, type ArtistDetailSearch, type ArtistsSearch } from "../features/artists";
import { parsePageParam } from "../shared/lib/pagination";
import { AuthenticatedLayout } from "./shell/authenticated-layout";
import { AdminGate } from "./routes/admin-gate";
import { AlbumDetailPage } from "./routes/album-detail-page";
import { AlbumsPage } from "./routes/albums-page";
import { BackupsPage } from "./routes/backups-page";
import { FavoritesPage } from "./routes/favorites-page";
import { HistoryPage } from "./routes/history-page";
import { ClaimsPage } from "./routes/claims-page";
import { IssuesPage } from "./routes/issues-page";
import { LibraryPage } from "./routes/library-page";
import { LoginPage } from "./routes/login-page";
import { MeLayout } from "./routes/me-layout";
import { MembersPage } from "./routes/members-page";
import { NotFoundPage } from "./routes/not-found-page";
import { OperationsPage } from "./routes/operations-page";
import { OverviewPage } from "./routes/overview-page";
import { NowPlayingRoute } from "./routes/now-playing-route";
import { PlaylistDetailPage } from "./routes/playlist-detail-page";
import { PlaylistsPage } from "./routes/playlists-page";
import { SearchPage } from "./routes/search-page";
import { ArtistCandidatesPage } from "./routes/artist-candidates-page";
import { ArtistDetailPage } from "./routes/artist-detail-page";
import { ArtistsPage } from "./routes/artists-page";
import { SetupPage } from "./routes/setup-page";

export type RouterContext = { session: SessionController };

// 路由只拥有 URL、导航与守卫；服务器状态由 Query 缓存，路由不使用 loader。
// 守卫读取会话控制器的当前状态，会话变化时由应用调用 router.invalidate() 重新执行。
const rootRoute = createRootRouteWithContext<RouterContext>()({
  component: Outlet,
  notFoundComponent: NotFoundPage,
});

const setupRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/setup",
  beforeLoad: ({ context }) => {
    const state = context.session.getSnapshot();
    if (state.status === "authenticated") throw redirect({ to: "/" });
    if (state.status === "anonymous") throw redirect({ to: "/login" });
  },
  component: SetupPage,
});

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/login",
  // 路由把校验结果合并到原始查询串上：不合法的值必须显式写成 undefined，只省略键的话原值会留在 search 里。
  validateSearch: (search: Record<string, unknown>): { redirect?: string | undefined } => ({
    redirect: sanitizeRedirect(search["redirect"]),
  }),
  beforeLoad: ({ context, search }) => {
    const state = context.session.getSnapshot();
    if (state.status === "setup_required") throw redirect({ to: "/setup" });
    if (state.status === "authenticated") throw redirect({ href: search.redirect ?? "/" });
  },
  component: LoginPage,
});

const authenticatedRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: "authenticated",
  beforeLoad: ({ context, location }) => {
    const state = context.session.getSnapshot();
    if (state.status === "setup_required") throw redirect({ to: "/setup" });
    if (state.status === "authenticated") return;
    // 主动退出时不带回跳地址：下一个登录的人不应被送到上一个人停留的页面。概览是默认去向，也不必写进地址。
    const target = state.status === "anonymous" && state.notice === "signed_out" ? undefined : sanitizeRedirect(location.href);
    throw redirect(target === undefined || target === "/" ? { to: "/login" } : { to: "/login", search: { redirect: target } });
  },
  component: AuthenticatedLayout,
});

const nowPlayingRoute = createRoute({ getParentRoute: () => authenticatedRoute, path: "/now-playing", component: NowPlayingRoute });

const overviewRoute = createRoute({ getParentRoute: () => authenticatedRoute, path: "/", component: OverviewPage });

// 专辑：所有登录用户都能浏览。q、attention、page 都在地址里；不合法的值显式写成 undefined（见上面的登录路由）。
function AlbumsRoute() {
  return <AlbumsPage search={albumsRoute.useSearch()} />;
}

const albumsRoute = createRoute({
  getParentRoute: () => authenticatedRoute,
  path: "/albums",
  validateSearch: (search: Record<string, unknown>): AlbumSearch => parseAlbumSearch(search),
  component: AlbumsRoute,
});

// 详情：track 是要定位的曲目（从全局搜索的建议点进来），每次来到这条历史记录定位一次；release 只在选中非代表版本时出现，规范化（不在本组成员里的值丢弃）由详情页在组详情返回后做。
// 组 ID 不是 UUID 等情况也由详情页自己说明；“返回专辑列表”的参数来自卡片链接带进来的 history state。
function AlbumDetailRoute() {
  const { groupId } = albumDetailRoute.useParams();
  const { release, track } = albumDetailRoute.useSearch();
  const state = useLocation({ select: (location) => location.state });
  return <AlbumDetailPage groupId={groupId} release={release} track={track} list={albumListSearchFromState(state)} />;
}

const albumDetailRoute = createRoute({
  getParentRoute: () => authenticatedRoute,
  path: "/albums/$groupId",
  validateSearch: (search: Record<string, unknown>): AlbumDetailSearch => parseAlbumDetailSearch(search),
  component: AlbumDetailRoute,
});

// 全局搜索的结果页：所有登录用户都能用。q 与两栏各自的页码都在地址里（页码只在 2 及以上出现）；不合法的值显式写成 undefined。
function SearchRoute() {
  return <SearchPage search={searchRoute.useSearch()} />;
}

const searchRoute = createRoute({
  getParentRoute: () => authenticatedRoute,
  path: "/search",
  validateSearch: (search: Record<string, unknown>): SearchPageSearch => parseSearchPageSearch(search),
  component: SearchRoute,
});

// 艺人（M5 ⑤）：所有登录用户都能浏览。列表的查找词、首字母、角色、排序与页码，艺人页的专辑、曲目、名字页码与曲目栏的角色都在地址里；
// 不合法的值显式写成 undefined。艺人 ID 不是 UUID 时由页面显示“艺人不存在”，不发请求。
function ArtistsRoute() {
  return <ArtistsPage search={artistsRoute.useSearch()} />;
}

const artistsRoute = createRoute({
  getParentRoute: () => authenticatedRoute,
  path: "/artists",
  validateSearch: (search: Record<string, unknown>): ArtistsSearch => parseArtistsSearch(search),
  component: ArtistsRoute,
});

function ArtistDetailRoute() {
  const { artistId } = artistDetailRoute.useParams();
  return <ArtistDetailPage artistId={artistId} search={artistDetailRoute.useSearch()} />;
}

const artistDetailRoute = createRoute({
  getParentRoute: () => authenticatedRoute,
  path: "/artists/$artistId",
  validateSearch: (search: Record<string, unknown>): ArtistDetailSearch => parseArtistDetailSearch(search),
  component: ArtistDetailRoute,
});

// “我的音乐”：只属于当前用户的数据，所有登录用户都能用。/me 本身没有内容，转到第一个页签（替换历史项，返回键不会回到 /me）。
const meRoute = createRoute({ getParentRoute: () => authenticatedRoute, path: "/me", component: MeLayout });

const meIndexRoute = createRoute({
  getParentRoute: () => meRoute,
  path: "/",
  beforeLoad: () => {
    throw redirect({ to: "/me/favorites", replace: true });
  },
});

// page 是历史的页码，只在 2 及以上时出现；不合法的值显式写成 undefined。超出末页时页面显示空页，不改写地址。
function HistoryRoute() {
  const { page } = historyRoute.useSearch();
  return <HistoryPage page={page ?? 1} />;
}

const historyRoute = createRoute({
  getParentRoute: () => meRoute,
  path: "/history",
  validateSearch: (search: Record<string, unknown>): { page?: number | undefined } => ({ page: parsePageParam(search["page"]) }),
  component: HistoryRoute,
});

// tab 只在看“曲目”或“艺人”时出现（专辑是默认），page 只在 2 及以上时出现；三栏各自翻页。不合法的值显式写成 undefined。
// 超出末页时，页面在读到真实的总数之后替换成末页。
function FavoritesRoute() {
  const { tab, page } = favoritesRoute.useSearch();
  return <FavoritesPage tab={tab ?? "albums"} page={page ?? 1} />;
}

const favoritesRoute = createRoute({
  getParentRoute: () => meRoute,
  path: "/favorites",
  validateSearch: (search: Record<string, unknown>): { tab?: "tracks" | "artists" | undefined; page?: number | undefined } => ({
    tab: search["tab"] === "tracks" ? "tracks" : search["tab"] === "artists" ? "artists" : undefined,
    page: parsePageParam(search["page"]),
  }),
  component: FavoritesRoute,
});

// 歌单列表与歌单详情：page 只在 2 及以上时出现；歌单 ID 不是 UUID 时由页面显示“歌单不存在”，不发请求。
function PlaylistsRoute() {
  const { page } = playlistsRoute.useSearch();
  return <PlaylistsPage page={page ?? 1} />;
}

const playlistsRoute = createRoute({
  getParentRoute: () => meRoute,
  path: "/playlists",
  validateSearch: (search: Record<string, unknown>): { page?: number | undefined } => ({ page: parsePageParam(search["page"]) }),
  component: PlaylistsRoute,
});

function PlaylistDetailRoute() {
  const { playlistId } = playlistDetailRoute.useParams();
  const { page } = playlistDetailRoute.useSearch();
  return <PlaylistDetailPage playlistId={playlistId} page={page ?? 1} />;
}

const playlistDetailRoute = createRoute({
  getParentRoute: () => meRoute,
  path: "/playlists/$playlistId",
  validateSearch: (search: Record<string, unknown>): { page?: number | undefined } => ({ page: parsePageParam(search["page"]) }),
  component: PlaylistDetailRoute,
});

// “可能是同一位”的候选（M5 ⑤，管理员）：status 只在看“已忽略”时出现，page 是页码（2 及以上）；不合法的值显式写成 undefined。
function ArtistCandidatesRoute() {
  const { page, status } = artistCandidatesRoute.useSearch();
  return (
    <AdminGate>
      <ArtistCandidatesPage page={page ?? 1} status={status ?? "pending"} />
    </AdminGate>
  );
}

const artistCandidatesRoute = createRoute({
  getParentRoute: () => authenticatedRoute,
  path: "/artist-candidates",
  validateSearch: (search: Record<string, unknown>): { page?: number | undefined; status?: "ignored" | undefined } => ({
    page: parsePageParam(search["page"]),
    status: search["status"] === "ignored" ? "ignored" : undefined,
  }),
  component: ArtistCandidatesRoute,
});

// 管理页由 AdminGate 把关：非管理员显示“需要管理员权限”，不渲染页面内容，也就不会调用管理接口。
function LibraryRoute() {
  const { scan_page: scanPage } = libraryRoute.useSearch();
  return (
    <AdminGate>
      <LibraryPage scanPage={scanPage ?? 1} />
    </AdminGate>
  );
}

// scan_page 是扫描记录的页码，只在 2 及以上时出现；不合法的值显式写成 undefined（路由把校验结果合并到原始查询串上）。
const libraryRoute = createRoute({
  getParentRoute: () => authenticatedRoute,
  path: "/library",
  validateSearch: (search: Record<string, unknown>): { scan_page?: number | undefined } => ({
    scan_page: parsePageParam(search["scan_page"]),
  }),
  component: LibraryRoute,
});

function MembersRoute() {
  return (
    <AdminGate>
      <MembersPage />
    </AdminGate>
  );
}

const membersRoute = createRoute({ getParentRoute: () => authenticatedRoute, path: "/members", component: MembersRoute });

// page 是记录的页码（2 及以上），release 是只看某个版本的记录，group 是看某张专辑的记录（默认版本）；不合法的值显式写成 undefined。
function OperationsRoute() {
  const { page, release, group, artist } = operationsRoute.useSearch();
  return (
    <AdminGate>
      <OperationsPage page={page ?? 1} releaseId={release} groupId={group} artistId={artist} />
    </AdminGate>
  );
}

const operationsRoute = createRoute({
  getParentRoute: () => authenticatedRoute,
  path: "/operations",
  validateSearch: (search: Record<string, unknown>): OperationsSearch => parseOperationsSearch(search),
  component: OperationsRoute,
});

// status 只在看“已核对”时出现，category 是只看某一类，page 是页码（2 及以上），release 是选中的版本；不合法的值显式写成 undefined。
function IssuesRoute() {
  return (
    <AdminGate>
      <IssuesPage search={issuesRoute.useSearch()} />
    </AdminGate>
  );
}

const issuesRoute = createRoute({
  getParentRoute: () => authenticatedRoute,
  path: "/issues",
  validateSearch: (search: Record<string, unknown>): IssuesSearch => parseIssuesSearch(search),
  component: IssuesRoute,
});

// page 是页码（2 及以上），release 是选中的旧版本；不合法的值显式写成 undefined。
function ClaimsRoute() {
  return (
    <AdminGate>
      <ClaimsPage search={claimsRoute.useSearch()} />
    </AdminGate>
  );
}

const claimsRoute = createRoute({
  getParentRoute: () => authenticatedRoute,
  path: "/claims",
  validateSearch: (search: Record<string, unknown>): ClaimsSearch => parseClaimsSearch(search),
  component: ClaimsRoute,
});

function ScanDetailRoute() {
  const { scanId } = scanDetailRoute.useParams();
  return (
    <AdminGate>
      <ScanDetail scanId={scanId} />
    </AdminGate>
  );
}

// 参数不是 UUID 时由页面显示“扫描不存在”，不发请求。
const scanDetailRoute = createRoute({
  getParentRoute: () => authenticatedRoute,
  path: "/library/scans/$scanId",
  component: ScanDetailRoute,
});

function BackupsRoute() {
  return (
    <AdminGate>
      <BackupsPage />
    </AdminGate>
  );
}

// “系统”组目前只有这一页，不设 /system 跳转。
const backupsRoute = createRoute({ getParentRoute: () => authenticatedRoute, path: "/system/backups", component: BackupsRoute });

// 已登录用户访问不存在的路径时仍显示在外壳里；未登录用户先被守卫送去登录。
const notFoundRoute = createRoute({ getParentRoute: () => authenticatedRoute, path: "$", component: NotFoundPage });

const routeTree = rootRoute.addChildren([setupRoute, loginRoute, authenticatedRoute.addChildren([overviewRoute, nowPlayingRoute, albumsRoute, albumDetailRoute, searchRoute, artistsRoute, artistDetailRoute, meRoute.addChildren([meIndexRoute, historyRoute, favoritesRoute, playlistsRoute, playlistDetailRoute]), libraryRoute, scanDetailRoute, issuesRoute, claimsRoute, artistCandidatesRoute, membersRoute, operationsRoute, backupsRoute, notFoundRoute])]);

export function createAppRouter(context: RouterContext, history?: RouterHistory) {
  return createRouter({ routeTree, context, ...(history === undefined ? {} : { history }) });
}

export type AppRouter = ReturnType<typeof createAppRouter>;

declare module "@tanstack/react-router" {
  interface Register {
    router: AppRouter;
  }
}

declare module "@tanstack/history" {
  interface HistoryState {
    /** 从专辑列表进入详情时带上的筛选与页码，“返回专辑列表”据此恢复；直接打开详情时没有。 */
    albumList?: AlbumSearch;
  }
}
