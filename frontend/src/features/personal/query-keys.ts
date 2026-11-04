import { viewerQueryKey } from "../auth";
import type { FavoriteKind } from "./api/decoders";

type ListParams = { page: number; pageSize: number; revision: number | null };

/** 批量状态的键：ID 小写、去重并排序，同一批 ID 不管传入顺序都命中同一个缓存条目。 */
export function lookupIdsKey(ids: { trackIds: readonly string[]; groupIds: readonly string[] }): { trackIds: string[]; groupIds: string[] } {
  const normalize = (values: readonly string[]) => [...new Set(values.map((value) => value.toLowerCase()))].sort();
  return { trackIds: normalize(ids.trackIds), groupIds: normalize(ids.groupIds) };
}

// 全部以 ["viewer", 代次, "personal", …] 开头：换用户或重新登录后旧缓存不会被命中。revision 没有时写 null。
export const personalKeys = {
  /** 本人的全部收藏与歌单查询：窗口重新聚焦、其他标签页通知变化时一起重新读取。 */
  all: (generation: number) => viewerQueryKey(generation, "personal"),
  /** 收藏的全部查询（两栏、批量状态、“播放全部”候选）：任何一个心形切换之后失效。 */
  favorites: (generation: number) => viewerQueryKey(generation, "personal", "favorites"),
  favoriteList: (generation: number, kind: FavoriteKind, params: ListParams) => viewerQueryKey(generation, "personal", "favorites", kind, "list", params),
  /** 全部批量心形状态（前缀）：心形切换成功后按目标修补每一份。 */
  favoriteLookups: (generation: number) => viewerQueryKey(generation, "personal", "favorites", "lookup"),
  favoriteLookup: (generation: number, ids: { trackIds: readonly string[]; groupIds: readonly string[] }) =>
    viewerQueryKey(generation, "personal", "favorites", "lookup", lookupIdsKey(ids)),
  favoriteQueue: (generation: number) => viewerQueryKey(generation, "personal", "favorites", "track", "queue"),
  /** 心形切换的写操作（前缀）：批量状态据此叠加进行中的切换。 */
  favoriteToggles: (generation: number) => viewerQueryKey(generation, "personal", "favorite-toggle"),
  favoriteToggle: (generation: number, kind: FavoriteKind, id: string) => viewerQueryKey(generation, "personal", "favorite-toggle", kind, id.toLowerCase()),
  /** 歌单的全部查询：列表、各歌单的详情、条目、候选与“在哪些歌单里”。 */
  playlists: (generation: number) => viewerQueryKey(generation, "personal", "playlists"),
  playlistList: (generation: number, params: { page: number; pageSize: number }) => viewerQueryKey(generation, "personal", "playlists", "list", params),
  /** “添加到歌单…”对话框里逐页加载的歌单（与列表页的分页缓存分开）。 */
  playlistPicker: (generation: number) => viewerQueryKey(generation, "personal", "playlists", "list", "picker"),
  /** 一个歌单的全部查询（前缀）。 */
  playlist: (generation: number, playlistId: string) => viewerQueryKey(generation, "personal", "playlists", "id", playlistId),
  playlistDetail: (generation: number, playlistId: string) => viewerQueryKey(generation, "personal", "playlists", "id", playlistId, "detail"),
  /** 一个歌单的全部条目页（前缀）。 */
  playlistItemPages: (generation: number, playlistId: string) => viewerQueryKey(generation, "personal", "playlists", "id", playlistId, "items"),
  playlistItems: (generation: number, playlistId: string, params: ListParams) => viewerQueryKey(generation, "personal", "playlists", "id", playlistId, "items", params),
  playlistQueue: (generation: number, playlistId: string) => viewerQueryKey(generation, "personal", "playlists", "id", playlistId, "queue"),
  containingTrack: (generation: number, trackId: string) => viewerQueryKey(generation, "personal", "playlists", "containing-track", trackId.toLowerCase()),
};
