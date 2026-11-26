import { ArtistsListView, type ArtistsSearch } from "../../features/artists";
import { useViewerScope } from "../../features/auth";
import type { ArtistSummaryDTO } from "../../features/catalog";
import { ArtistWebQueueControl } from "../../features/curation";
import { Page } from "../../shared/ui";
import { ArtistRowPlay } from "./artist-row-play";

/** 每行的播放键：模块级的稳定引用。 */
const renderArtistPlay = (artist: ArtistSummaryDTO) => <ArtistRowPlay artistId={artist.id} name={artist.name} />;

/** 艺人：所有登录用户都能浏览；筛选词、首字母、角色、排序与页码来自地址（路由已校验）。每行的播放键由这里组合；管理员在标题旁有“整理艺人”。 */
export function ArtistsPage({ search }: { search: ArtistsSearch }) {
  const { viewer } = useViewerScope();
  return (
    <Page title="艺人" {...(viewer.role === "admin" ? { actions: <ArtistWebQueueControl /> } : {})}>
      <ArtistsListView search={search} renderArtistPlay={renderArtistPlay} />
    </Page>
  );
}
