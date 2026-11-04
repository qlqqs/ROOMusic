import { decode as d } from "../../../shared/api";
import { artworkDecoder, releaseSummaryDecoder } from "../../catalog/api/decoders";
import { trackSummaryDecoder } from "../../catalog/api/track-summary";

// 收藏与歌单接口的解码器（合同见任务 11-04-classical-m5-favorites 的 design.md §4–§6）。只依赖 shared/api 与 catalog 的纯解码模块，
// 接口样本测试与真实服务用例可以直接导入。超界、计数对不上的响应整体拒绝，而不是截断后显示。

/** 一次播放全部、一个歌单的普通上限（与队列上限一致）。 */
export const PERSONAL_TRACK_LIMIT = 10_000;
/** 收藏列表每页的上限。 */
export const MAX_FAVORITES_PAGE_SIZE = 100;
/** 歌单列表每页的上限。 */
export const MAX_PLAYLISTS_PAGE_SIZE = 20;
/** 歌单条目每页的上限。 */
export const MAX_PLAYLIST_ITEMS_PAGE_SIZE = 100;
/** 批量心形状态一次最多查的曲目与专辑。 */
export const MAX_LOOKUP_TRACKS = 10_000;
export const MAX_LOOKUP_ALBUMS = 1_000;
/** 每人最多的歌单数；也是“一首曲目在哪些歌单里”的上限。 */
export const MAX_PLAYLISTS = 1_000;
/** 歌单名字的上限（按 Unicode 码点）。 */
export const MAX_PLAYLIST_NAME = 100;

const count = () => d.integer({ min: 0 });

/** 在结构解码之后核对字段之间的关系；不成立时与结构错误一样抛出 DecodeError。 */
function checked<T>(decoder: d.Decoder<T>, check: (value: T) => string | null): d.Decoder<T> {
  return (input, path) => {
    const value = decoder(input, path);
    const problem = check(value);
    if (problem !== null) throw new d.DecodeError(path, problem);
    return value;
  };
}

const paginationDecoder = (maxPageSize: number) =>
  d.object({
    page: d.integer({ min: 1 }),
    page_size: d.integer({ min: 1, max: maxPageSize }),
    total: count(),
  });

/** 一页的条目数不超过页大小，也不超过总数（越界页是空数组）。 */
function pageProblem(page: { items: readonly unknown[]; pagination: { page_size: number; total: number } }): string | null {
  if (page.items.length > page.pagination.page_size) return "items 多于 page_size";
  if (page.items.length > page.pagination.total) return "items 多于 total";
  return null;
}

/** 同一批里的 ID 不重复（后端对输入去重、只输出一次；一个歌单里同一首只出现一次）。 */
function duplicateProblem(ids: readonly string[], field: string): string | null {
  return new Set(ids.map((id) => id.toLowerCase())).size === ids.length ? null : `${field} 有重复的 ID`;
}

// ---- 收藏 ----

const favoriteTrackDecoder = d.object({
  track: trackSummaryDecoder,
  created_at: d.timestamp(),
});

// 收藏的专辑：可见时有代表版本；暂时没有可见版本时 available 为 false、representative 为 null、release_count 为 0，
// 标题与艺人是收藏时记下的显示值。
const personalAlbumDecoder = checked(
  d.object({
    group_id: d.uuid(),
    available: d.boolean(),
    title: d.string(),
    artist: d.string(),
    release_count: count(),
    representative: d.nullable(releaseSummaryDecoder),
  }),
  (album) => {
    if (album.available !== (album.representative !== null)) return "available 与 representative 不一致";
    // 可见的专辑至少有一个可见版本；暂时不可用的专辑没有可见版本，release_count 必须是 0。
    if (album.available ? album.release_count < 1 : album.release_count !== 0) return "release_count 与 available 不一致";
    return null;
  },
);

const favoriteAlbumDecoder = d.object({
  album: personalAlbumDecoder,
  created_at: d.timestamp(),
});

// 收藏的一页：revision 是本人收藏集合的版本，翻页时带上它，集合变了服务端返回 409 favorites_changed。
const favoriteTracksPageDecoder = checked(
  d.object({
    items: d.array(favoriteTrackDecoder, { max: MAX_FAVORITES_PAGE_SIZE }),
    pagination: paginationDecoder(MAX_FAVORITES_PAGE_SIZE),
    revision: count(),
  }),
  (page) => pageProblem(page) ?? duplicateProblem(page.items.map((item) => item.track.track_id), "items"),
);

const favoriteAlbumsPageDecoder = checked(
  d.object({
    items: d.array(favoriteAlbumDecoder, { max: MAX_FAVORITES_PAGE_SIZE }),
    pagination: paginationDecoder(MAX_FAVORITES_PAGE_SIZE),
    revision: count(),
  }),
  (page) => pageProblem(page) ?? duplicateProblem(page.items.map((item) => item.album.group_id), "items"),
);

export const favoriteKinds = ["track", "album"] as const;
export type FavoriteKind = (typeof favoriteKinds)[number];

// 收藏或取消一个目标的回执：收藏着时有收藏时间，取消后是 null。
const favoriteToggleDecoder = checked(
  d.object({
    target_kind: d.oneOf(favoriteKinds),
    target_id: d.uuid(),
    favorited: d.boolean(),
    created_at: d.nullable(d.timestamp()),
    revision: count(),
  }),
  (result) => (result.favorited === (result.created_at !== null) ? null : "favorited 与 created_at 不一致"),
);

// 一批曲目与专辑的收藏状态（未知的 ID 是 false）；后端以后可能多给艺人一栏，这里忽略。
const favoriteLookupDecoder = checked(
  d.object({
    revision: count(),
    tracks: d.array(d.object({ track_id: d.uuid(), favorited: d.boolean() }), { max: MAX_LOOKUP_TRACKS }),
    albums: d.array(d.object({ group_id: d.uuid(), favorited: d.boolean() }), { max: MAX_LOOKUP_ALBUMS }),
  }),
  (lookup) => duplicateProblem(lookup.tracks.map((track) => track.track_id), "tracks") ?? duplicateProblem(lookup.albums.map((album) => album.group_id), "albums"),
);

// “播放全部”的候选窗口：先去掉文件缺失的，再按顺序取前 10,000 首。计数之间的关系见 queueCountsProblem。
const queueCountsShape = {
  items: d.array(trackSummaryDecoder, { max: PERSONAL_TRACK_LIMIT }),
  total: count(),
  missing_count: count(),
  eligible_count: count(),
  returned_count: count(),
  omitted_count: count(),
  limit: d.literal(PERSONAL_TRACK_LIMIT),
  truncated: d.boolean(),
};

type QueueCounts = d.ObjectOf<typeof queueCountsShape>;

function queueCountsProblem(queue: QueueCounts): string | null {
  if (queue.returned_count !== queue.items.length) return "returned_count 与 items 的长度不一致";
  if (queue.total !== queue.missing_count + queue.eligible_count) return "total 不等于 missing_count 加 eligible_count";
  if (queue.returned_count !== Math.min(queue.eligible_count, queue.limit)) return "returned_count 不是 eligible_count 与 limit 的较小值";
  if (queue.omitted_count !== queue.eligible_count - queue.returned_count) return "omitted_count 与其他计数不一致";
  if (queue.truncated !== queue.omitted_count > 0) return "truncated 与 omitted_count 不一致";
  if (queue.items.some((item) => !item.present)) return "items 里有文件缺失的曲目";
  return duplicateProblem(queue.items.map((item) => item.track_id), "items");
}

const favoriteQueueDecoder = checked(d.object({ ...queueCountsShape, revision: count() }), queueCountsProblem);

// ---- 歌单 ----

// 歌单的状态：撤销可以让曲目数暂时超过上限，over_limit_count 就是超出的数量。
const playlistStateShape = {
  id: d.uuid(),
  name: d.string({ min: 1, max: MAX_PLAYLIST_NAME * 2 }),
  revision: d.integer({ min: 1 }),
  track_count: count(),
  track_limit: d.literal(PERSONAL_TRACK_LIMIT),
  over_limit_count: count(),
  created_at: d.timestamp(),
  updated_at: d.timestamp(),
};

function overLimitProblem(playlist: { track_count: number; track_limit: number; over_limit_count: number }): string | null {
  return playlist.over_limit_count === Math.max(playlist.track_count - playlist.track_limit, 0) ? null : "over_limit_count 与 track_count 不一致";
}

const playlistStateDecoder = checked(d.object(playlistStateShape), overLimitProblem);

// 列表卡片与详情头部：总时长只算已知的（含文件缺失的曲目上次记下的时长），未知的单独计数；封面取前四个不同的专辑。
// 缺失与时长未知的都是曲目的一部分，不会超过曲目数；封面的专辑互不相同，个数也不会超过曲目数（空歌单没有封面）。
const playlistSummaryDecoder = checked(
  d.object({
    ...playlistStateShape,
    duration_seconds: d.number({ min: 0 }),
    unknown_duration_count: count(),
    missing_count: count(),
    cover_groups: d.array(d.object({ group_id: d.uuid(), artwork: d.nullable(artworkDecoder) }), { max: 4 }),
  }),
  (playlist) => {
    const problem = overLimitProblem(playlist);
    if (problem !== null) return problem;
    if (playlist.missing_count > playlist.track_count) return "missing_count 超过 track_count";
    if (playlist.unknown_duration_count > playlist.track_count) return "unknown_duration_count 超过 track_count";
    if (playlist.cover_groups.length > playlist.track_count) return "cover_groups 多于曲目数";
    return duplicateProblem(playlist.cover_groups.map((group) => group.group_id), "cover_groups");
  },
);

const playlistsPageDecoder = checked(
  d.object({
    items: d.array(playlistSummaryDecoder, { max: MAX_PLAYLISTS_PAGE_SIZE }),
    pagination: paginationDecoder(MAX_PLAYLISTS_PAGE_SIZE),
  }),
  (page) => pageProblem(page) ?? duplicateProblem(page.items.map((item) => item.id), "items"),
);

const addCountsShape = {
  added_count: count(),
  existing_count: count(),
  duplicate_count: count(),
};

const playlistCreatedDecoder = d.object({ playlist: playlistStateDecoder, ...addCountsShape });
const playlistDetailDecoder = d.object({ playlist: playlistSummaryDecoder });
const playlistRenamedDecoder = d.object({ playlist: playlistStateDecoder, changed: d.boolean() });
const playlistDeletedDecoder = d.object({ playlist_id: d.uuid(), deleted: d.literal(true) });

// 条目的小回执：position 是这次动作之后（移除时是移除之前）从 1 起的序号。
const itemRefDecoder = d.object({
  id: d.uuid(),
  track_id: d.uuid(),
  position: d.integer({ min: 1 }),
  added_at: d.timestamp(),
});

const playlistItemDecoder = d.object({
  id: d.uuid(),
  position: d.integer({ min: 1 }),
  added_at: d.timestamp(),
  track: trackSummaryDecoder,
});

// 歌单的一页条目：page_after_item_id 是下一页的第一项（拖到本页末尾时的锚点），已经是最后一页时为 null。
// 同一个快照：total 就是歌单的曲目数，position 是从这一页的起点开始的连续序号，条目与曲目都不重复。
const playlistItemsPageDecoder = checked(
  d.object({
    playlist: playlistStateDecoder,
    items: d.array(playlistItemDecoder, { max: MAX_PLAYLIST_ITEMS_PAGE_SIZE }),
    pagination: paginationDecoder(MAX_PLAYLIST_ITEMS_PAGE_SIZE),
    page_after_item_id: d.nullable(d.uuid()),
  }),
  (page) => {
    const problem = pageProblem(page);
    if (problem !== null) return problem;
    if (page.pagination.total !== page.playlist.track_count) return "pagination.total 与 track_count 不一致";
    const start = (page.pagination.page - 1) * page.pagination.page_size + 1;
    if (page.items.some((item, index) => item.position !== start + index)) return "position 不是这一页的连续序号";
    if (page.items.some((item) => item.position > page.playlist.track_count)) return "position 超过曲目数";
    if (page.page_after_item_id !== null && page.items.some((item) => item.id.toLowerCase() === page.page_after_item_id?.toLowerCase())) return "page_after_item_id 指向了这一页里的条目";
    const last = page.items.at(-1);
    if (page.page_after_item_id !== null && (last === undefined || last.position >= page.playlist.track_count)) return "最后一页不应有 page_after_item_id";
    if (page.page_after_item_id === null && last !== undefined && last.position < page.playlist.track_count) return "后面还有条目却没有 page_after_item_id";
    return duplicateProblem(page.items.map((item) => item.id), "items") ?? duplicateProblem(page.items.map((item) => item.track.track_id), "items.track");
  },
);

const itemsAddedDecoder = d.object({ playlist: playlistStateDecoder, ...addCountsShape });

// 移除回执里的 position 是移除之前的序号，所以最多比现在的曲目数多 1；removal_revision 就是移除后的歌单版本。
const itemRemovedDecoder = checked(
  d.object({
    playlist: playlistStateDecoder,
    removed: itemRefDecoder,
    undo: d.object({ token: d.uuid(), removal_revision: d.integer({ min: 1 }) }),
  }),
  (result) => {
    if (result.removed.position > result.playlist.track_count + 1) return "removed.position 超过移除前的曲目数";
    return result.undo.removal_revision === result.playlist.revision ? null : "removal_revision 与歌单版本不一致";
  },
);

const itemRestoredDecoder = checked(
  d.object({
    playlist: playlistStateDecoder,
    outcome: d.oneOf(["restored", "already_present"] as const),
    item: itemRefDecoder,
    placement: d.oneOf(["original", "after_previous", "original_index", "end", "unchanged"] as const),
  }),
  (result) => {
    if ((result.outcome === "already_present") !== (result.placement === "unchanged")) return "outcome 与 placement 不一致";
    return result.item.position > result.playlist.track_count ? "item.position 超过曲目数" : null;
  },
);

const itemMovedDecoder = checked(d.object({ playlist: playlistStateDecoder, moved: itemRefDecoder, changed: d.boolean() }), (result) =>
  result.moved.position > result.playlist.track_count ? "moved.position 超过曲目数" : null,
);

// 歌单的候选还要与歌单本身对上：total 就是曲目数。
const playlistQueueDecoder = checked(d.object({ ...queueCountsShape, playlist: playlistStateDecoder }), (queue) =>
  queueCountsProblem(queue) ?? (queue.total === queue.playlist.track_count ? null : "total 与 track_count 不一致"),
);

// 一首曲目已经在本人的哪些歌单里（对话框据此标“已在歌单里”）。
const containingTrackDecoder = checked(
  d.object({
    track_id: d.uuid(),
    items: d.array(d.object({ playlist_id: d.uuid(), revision: d.integer({ min: 1 }) }), { max: MAX_PLAYLISTS }),
  }),
  (result) => duplicateProblem(result.items.map((item) => item.playlist_id), "items"),
);

export const decodeFavoriteTracksPage = d.defineDecoder(favoriteTracksPageDecoder);
export const decodeFavoriteAlbumsPage = d.defineDecoder(favoriteAlbumsPageDecoder);
export const decodeFavoriteToggle = d.defineDecoder(favoriteToggleDecoder);
export const decodeFavoriteLookup = d.defineDecoder(favoriteLookupDecoder);
export const decodeFavoriteQueue = d.defineDecoder(favoriteQueueDecoder);
export const decodePlaylistsPage = d.defineDecoder(playlistsPageDecoder);
export const decodePlaylistCreated = d.defineDecoder(playlistCreatedDecoder);
export const decodePlaylistDetail = d.defineDecoder(playlistDetailDecoder);
export const decodePlaylistRenamed = d.defineDecoder(playlistRenamedDecoder);
export const decodePlaylistDeleted = d.defineDecoder(playlistDeletedDecoder);
export const decodePlaylistItemsPage = d.defineDecoder(playlistItemsPageDecoder);
export const decodeItemsAdded = d.defineDecoder(itemsAddedDecoder);
export const decodeItemRemoved = d.defineDecoder(itemRemovedDecoder);
export const decodeItemRestored = d.defineDecoder(itemRestoredDecoder);
export const decodeItemMoved = d.defineDecoder(itemMovedDecoder);
export const decodePlaylistQueue = d.defineDecoder(playlistQueueDecoder);
export const decodeContainingTrack = d.defineDecoder(containingTrackDecoder);

export type FavoriteTrackDTO = d.Infer<typeof favoriteTrackDecoder>;
export type PersonalAlbumDTO = d.Infer<typeof personalAlbumDecoder>;
export type FavoriteAlbumDTO = d.Infer<typeof favoriteAlbumDecoder>;
export type FavoriteTracksPageDTO = d.Infer<typeof favoriteTracksPageDecoder>;
export type FavoriteAlbumsPageDTO = d.Infer<typeof favoriteAlbumsPageDecoder>;
export type FavoriteToggleDTO = d.Infer<typeof favoriteToggleDecoder>;
export type FavoriteLookupDTO = d.Infer<typeof favoriteLookupDecoder>;
export type FavoriteQueueDTO = d.Infer<typeof favoriteQueueDecoder>;
export type PlaylistStateDTO = d.Infer<typeof playlistStateDecoder>;
export type PlaylistSummaryDTO = d.Infer<typeof playlistSummaryDecoder>;
export type PlaylistsPageDTO = d.Infer<typeof playlistsPageDecoder>;
export type PlaylistCreatedDTO = d.Infer<typeof playlistCreatedDecoder>;
export type PlaylistDetailDTO = d.Infer<typeof playlistDetailDecoder>;
export type PlaylistRenamedDTO = d.Infer<typeof playlistRenamedDecoder>;
export type PlaylistDeletedDTO = d.Infer<typeof playlistDeletedDecoder>;
export type ItemRefDTO = d.Infer<typeof itemRefDecoder>;
export type PlaylistItemDTO = d.Infer<typeof playlistItemDecoder>;
export type PlaylistItemsPageDTO = d.Infer<typeof playlistItemsPageDecoder>;
export type ItemsAddedDTO = d.Infer<typeof itemsAddedDecoder>;
export type ItemRemovedDTO = d.Infer<typeof itemRemovedDecoder>;
export type ItemRestoredDTO = d.Infer<typeof itemRestoredDecoder>;
export type ItemMovedDTO = d.Infer<typeof itemMovedDecoder>;
export type PlaylistQueueDTO = d.Infer<typeof playlistQueueDecoder>;
export type ContainingTrackDTO = d.Infer<typeof containingTrackDecoder>;
/** 两种“播放全部”候选共有的部分：app 只按这些字段放进队列与写提示。 */
export type PersonalQueueDTO = QueueCounts;
