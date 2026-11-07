// personal 的公开入口：“我的音乐”各页（我喜欢的音乐、我的收藏、我的歌单）的内容、侧栏的歌单列表，各处共用的心形、批量收藏状态范围、“添加到歌单…”与新建歌单对话框，
// “播放全部”候选的读取，跨标签页的刷新与查询键。播放、曲目行的组合与页面路由由 app 负责；personal 不导入 player、listening。
export { FavoriteButton, type FavoriteTarget } from "./components/favorite-button";
export { FavoriteStatusScope } from "./components/favorite-status-scope";
export { AddToPlaylistButton } from "./components/add-to-playlist-button";
export { AddToPlaylistDialog } from "./components/add-to-playlist-dialog";
export { CreatePlaylistDialog } from "./components/create-playlist-dialog";
export { FavoritesView, type FavoritesTab } from "./components/favorites-view";
export { LikedTracksView } from "./components/liked-tracks-view";
export { PlaylistsView } from "./components/playlists-view";
export { PlaylistDetailView } from "./components/playlist-detail-view";
export { usePersonalQueue, type PersonalQueueSource } from "./hooks/use-favorites";
export { usePlaylists } from "./hooks/use-playlists";
export { PERSONAL_TRACK_LIMIT, type FavoriteTrackDTO, type PersonalQueueDTO, type PlaylistItemDTO, type PlaylistSummaryDTO } from "./api/decoders";
export { queuePlaylistName } from "./playlist-name";
export { PersonalSync } from "./sync";
export { personalKeys } from "./query-keys";
