import { createRootRouteWithContext, createRoute, createRouter, Outlet, redirect, useLocation, type RouterHistory } from "@tanstack/react-router";
import { sanitizeRedirect, type SessionController } from "../features/auth";
import { albumListSearchFromState, parseAlbumDetailSearch, parseAlbumSearch, type AlbumDetailSearch, type AlbumSearch } from "../features/catalog";
import { parseOperationsSearch, type OperationsSearch } from "../features/governance";
import { ScanDetail } from "../features/library";
import { parsePageParam } from "../shared/lib/pagination";
import { AuthenticatedLayout } from "./shell/authenticated-layout";
import { AdminGate } from "./routes/admin-gate";
import { AlbumDetailPage } from "./routes/album-detail-page";
import { AlbumsPage } from "./routes/albums-page";
import { LibraryPage } from "./routes/library-page";
import { LoginPage } from "./routes/login-page";
import { MembersPage } from "./routes/members-page";
import { NotFoundPage } from "./routes/not-found-page";
import { OperationsPage } from "./routes/operations-page";
import { OverviewPage } from "./routes/overview-page";
import { NowPlayingPage } from "../features/player";
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

const nowPlayingRoute = createRoute({ getParentRoute: () => authenticatedRoute, path: "/now-playing", component: NowPlayingPage });

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

// 详情：release 只在选中非代表版本时出现，规范化（不在本组成员里的值丢弃）由详情页在组详情返回后做。
// 组 ID 不是 UUID 等情况也由详情页自己说明；“返回专辑列表”的参数来自卡片链接带进来的 history state。
function AlbumDetailRoute() {
  const { groupId } = albumDetailRoute.useParams();
  const { release } = albumDetailRoute.useSearch();
  const state = useLocation({ select: (location) => location.state });
  return <AlbumDetailPage groupId={groupId} release={release} list={albumListSearchFromState(state)} />;
}

const albumDetailRoute = createRoute({
  getParentRoute: () => authenticatedRoute,
  path: "/albums/$groupId",
  validateSearch: (search: Record<string, unknown>): AlbumDetailSearch => parseAlbumDetailSearch(search),
  component: AlbumDetailRoute,
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
  const { page, release, group } = operationsRoute.useSearch();
  return (
    <AdminGate>
      <OperationsPage page={page ?? 1} releaseId={release} groupId={group} />
    </AdminGate>
  );
}

const operationsRoute = createRoute({
  getParentRoute: () => authenticatedRoute,
  path: "/operations",
  validateSearch: (search: Record<string, unknown>): OperationsSearch => parseOperationsSearch(search),
  component: OperationsRoute,
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

// 已登录用户访问不存在的路径时仍显示在外壳里；未登录用户先被守卫送去登录。
const notFoundRoute = createRoute({ getParentRoute: () => authenticatedRoute, path: "$", component: NotFoundPage });

const routeTree = rootRoute.addChildren([setupRoute, loginRoute, authenticatedRoute.addChildren([overviewRoute, nowPlayingRoute, albumsRoute, albumDetailRoute, libraryRoute, scanDetailRoute, membersRoute, operationsRoute, notFoundRoute])]);

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
