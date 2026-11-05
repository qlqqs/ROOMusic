import { ArtistsListView, type ArtistsSearch } from "../../features/artists";
import { Page } from "../../shared/ui";

/** 艺人：所有登录用户都能浏览；查找词、首字母、角色、排序与页码来自地址（路由已校验）。 */
export function ArtistsPage({ search }: { search: ArtistsSearch }) {
  return (
    <Page title="艺人">
      <ArtistsListView search={search} />
    </Page>
  );
}
