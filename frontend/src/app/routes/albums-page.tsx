import { AlbumListView, type AlbumSearch, type ReleaseGroupSummaryDTO } from "../../features/catalog";
import { useViewerScope } from "../../features/auth";
import { Page } from "../../shared/ui";
import { AlbumCardPlay } from "./album-card-play";

/** 卡片封面上的播放键：模块级的稳定引用，网格不因它重绘。 */
const renderAlbumPlay = (group: ReleaseGroupSummaryDTO) => <AlbumCardPlay group={group} />;

/** 专辑：所有登录用户都能浏览；搜索与筛选来自地址（路由已校验），管理员只在空库时多一条去扫描目录的提示。卡片的播放键由这里组合。 */
export function AlbumsPage({ search }: { search: AlbumSearch }) {
  const { viewer } = useViewerScope();
  return (
    <Page title="专辑">
      <AlbumListView search={search} isAdmin={viewer.role === "admin"} renderAlbumPlay={renderAlbumPlay} />
    </Page>
  );
}
