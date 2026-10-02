import { AlbumListView, type AlbumSearch } from "../../features/catalog";
import { useViewerScope } from "../../features/auth";
import { Page } from "../../shared/ui";

/** 专辑：所有登录用户都能浏览；搜索与筛选来自地址（路由已校验），管理员只在空库时多一条去扫描目录的提示。 */
export function AlbumsPage({ search }: { search: AlbumSearch }) {
  const { viewer } = useViewerScope();
  return (
    <Page title="专辑" description="浏览并搜索资料库里的专辑。">
      <AlbumListView search={search} isAdmin={viewer.role === "admin"} />
    </Page>
  );
}
