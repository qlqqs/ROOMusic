// catalog 的公开入口：概览用的资料库区块、专辑列表与详情，路由校验用的搜索参数，以及其他功能需要的已发布合同
// （发行详情与摘要的 DTO 类型与解码器、封面组件与地址、专辑艺人的显示规则、按需读取发行详情）。
export { AlbumDetailView } from "./components/album-detail-view";
export { AlbumListView } from "./components/album-list-view";
export { Cover } from "./components/cover";
export { LibrarySummary } from "./components/library-summary";
export { albumArtistOf } from "./format";
export { albumListSearchFromState, parseAlbumDetailSearch, parseAlbumSearch, type AlbumDetailSearch, type AlbumSearch } from "./album-search";
export { artworkPath } from "./api/endpoints";
export { releaseSummaryDecoder } from "./api/decoders";
export type { ArtworkDTO, ReleaseDetailDTO, ReleaseMediumDTO, ReleaseSummaryDTO, ReleaseTrackDTO } from "./api/decoders";
export { useReleaseLoader } from "./hooks/use-release-loader";
