// catalog 的公开入口：概览用的资料库区块、专辑列表与详情，路由校验用的搜索参数，以及播放器需要的已发布合同
// （发行详情的 DTO 类型、封面地址、按需读取发行详情）。
export { AlbumDetailView } from "./components/album-detail-view";
export { AlbumListView } from "./components/album-list-view";
export { LibrarySummary } from "./components/library-summary";
export { albumListSearchFromState, parseAlbumDetailSearch, parseAlbumSearch, type AlbumDetailSearch, type AlbumSearch } from "./album-search";
export { artworkPath } from "./api/endpoints";
export type { ArtworkDTO, ReleaseDetailDTO, ReleaseMediumDTO, ReleaseTrackDTO } from "./api/decoders";
export { useReleaseLoader } from "./hooks/use-release-loader";
