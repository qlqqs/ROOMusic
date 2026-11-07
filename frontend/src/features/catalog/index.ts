// catalog 的公开入口：概览用的资料库区块、专辑列表与详情，路由校验用的搜索参数，以及其他功能需要的已发布合同
// （发行详情与摘要的 DTO 类型与解码器、曲目安全摘要的解码器与显示规则、艺人引用与艺人摘要（M5 ⑤）、可点的艺人名、目录状态（搜索与艺人数据的代次）、封面组件与地址、卡片网格的布局、专辑艺人的显示规则、证据字段与原因码的中文名、按需读取发行详情）。
export { AlbumDetailView } from "./components/album-detail-view";
export { AlbumListView } from "./components/album-list-view";
export { Cover } from "./components/cover";
export { LibrarySummary } from "./components/library-summary";
export { RecentlyAddedPanel } from "./components/recently-added-panel";
export { albumArtistOf, albumMeta, formatGenres } from "./format";
export { evidenceFieldLabel, reasonCodeLabel } from "./labels";
export { albumListSearchFromState, MAX_SEARCH_BYTES, parseAlbumDetailSearch, parseAlbumSearch, parseSearchText, type AlbumDetailSearch, type AlbumSearch } from "./album-search";
export { artworkPath, fetchTrackSummaries } from "./api/endpoints";
export { releaseSummaryDecoder } from "./api/decoders";
export { albumArtistRefsOf, MAX_TRACK_SUMMARY_BATCH, trackArtistOf, trackArtistRefsOf, trackSummaryDecoder, trackTitleOf, type TrackSummaryDTO } from "./api/track-summary";
export { artistFilterRoles, artistInitialGroups, artistMetaText, artistRoleLabels, artistRoles, artistRolesText, artistSummaryDecoder, initialGroupLabel, type ArtistInitialGroup, type ArtistRole, type ArtistSummaryDTO } from "./api/artist-summary";
export { ArtistLinksScope, ArtistNames, useArtistLinksReady } from "./components/artist-names";
export { ArtistMeta } from "./components/artist-meta";
export { AlbumGridLayout, AlbumGridSkeleton } from "./components/album-grid";
export type { ArtistRefDTO, ArtworkDTO, ReleaseDetailDTO, ReleaseGroupSummaryDTO, ReleaseMediumDTO, ReleaseSummaryDTO, ReleaseTrackDTO } from "./api/decoders";
export type { CatalogStateDTO } from "./api/decoders";
export { catalogStateKeys } from "./query-keys";
export { CATALOG_STATE_POLL_MS, isCatalogPreparing, useCatalogState } from "./hooks/use-catalog-state";
export { useReleaseLoader } from "./hooks/use-release-loader";
export { useCatalogRefresh } from "./hooks/use-catalog-refresh";
export { useAlbumSearchResults } from "./hooks/use-album-search-results";
export { useAlbum } from "./hooks/use-album";
