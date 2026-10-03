import { AlbumDetailView, type AlbumSearch } from "../../features/catalog";
import { useViewerScope } from "../../features/auth";

type AlbumDetailPageProps = {
  groupId: string;
  release: string | undefined;
  list: AlbumSearch;
};

/** 专辑详情：所有登录用户都能读；管理员在“来源与证据”里多一个完整证据。页面标题与 h1 由详情自己给出（取决于选中的版本）。 */
export function AlbumDetailPage(props: AlbumDetailPageProps) {
  const { viewer } = useViewerScope();
  return <AlbumDetailView {...props} isAdmin={viewer.role === "admin"} />;
}
