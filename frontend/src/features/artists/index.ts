// artists 的公开入口（M5 ⑤）：艺人列表与艺人页的内容（播放、心形、管理员动作与曲目行动作经插槽由 app 组合）、地址参数的校验、
// 艺人详情与播放候选的类型与读取，以及查询键（app 在治理之后、epoch 变化后让艺人数据失效）。外部组件不导入 feature 内部文件。
export { ArtistsListView } from "./components/artists-list-view";
export { ArtistDetailView } from "./components/artist-detail-view";
export { ArtistPreparing } from "./components/artist-preparing";
export { artistIdOf, parseArtistDetailSearch, parseArtistsSearch, type ArtistDetailSearch, type ArtistsSearch } from "./artist-search";
export { ARTIST_QUEUE_LIMIT, artistTrackRoles, type ArtistDetailDTO, type ArtistNameDTO, type ArtistQueueDTO, type ArtistTrackRole } from "./api/decoders";
export { fetchArtist, fetchArtists } from "./api/endpoints";
export { isArtistCatalogPreparing, useArtist, useArtistAlbums, useArtistNames, useArtistParticipationAlbums, useArtistQueue, useArtistsPage, type ArtistQueueOrder } from "./hooks/use-artists";
export type { ArtistParticipationAlbumDTO } from "./api/decoders";
export { artistsKeys } from "./query-keys";
