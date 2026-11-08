import { decode as d } from "../../../shared/api";
import { artistRoles, artistSummaryDecoder } from "../../catalog/api/artist-summary";
import { releaseGroupSummaryDecoder } from "../../catalog/api/decoders";
import { trackSummaryDecoder } from "../../catalog/api/track-summary";

// 艺人浏览的接口（M5 ⑤，合同见任务 11-04-classical-m5-search 的 design.md §10 与后端 artist-contracts.md）。
// 只依赖 shared/api 与 catalog 的纯解码模块，接口样本测试与真实服务用例可以直接导入。超界、计数对不上的响应整体拒绝。

/** 每页的上限（列表、名字、专辑、曲目）。 */
export const MAX_ARTIST_PAGE_SIZE = 100;
/** 艺人详情里先给出的名字条数。 */
export const ARTIST_DETAIL_NAMES = 20;
/** 播放全部、随机播放一次最多的曲目（与队列上限一致）。 */
export const ARTIST_QUEUE_LIMIT = 10_000;

/** 只有曲目级角色可以按角色列出曲目；发行艺人与专辑艺人的参与看“专辑”。 */
export const artistTrackRoles = ["track_artist", "lyricist", "composer", "arranger", "conductor", "performer", "producer"] as const;
export type ArtistTrackRole = (typeof artistTrackRoles)[number];

const count = () => d.integer({ min: 0 });

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

const artistPageDecoder = checked(
  d.object({
    items: d.array(artistSummaryDecoder, { max: MAX_ARTIST_PAGE_SIZE }),
    pagination: paginationDecoder(MAX_ARTIST_PAGE_SIZE),
  }),
  pageProblem,
);

// 艺人的一个名字（原写法）：key_id 是规范化后的名字键，同一个键的繁简、大小写写法一起拆分；is_display_name 是当前的主显示名。
const artistNameDecoder = d.object({
  id: d.uuid(),
  key_id: d.uuid(),
  name: d.string(),
  is_display_name: d.boolean(),
  origin: d.string({ min: 1, max: 32 }),
});

// 详情：revision 是人工修改版本号（合并、拆开、改名的预期版本）；name_source 是主显示名来自最常见写法（auto）还是管理员（manual）。
// 已经被合并进别的艺人时 redirect_artist_id 是现在的艺人，页面据此跳过去。role_counts 的 total 含文件缺失的曲目，present 只算存在的。
const artistDetailDecoder = checked(
  d.object({
    artist: artistSummaryDecoder,
    revision: d.integer({ min: 1 }),
    name_source: d.oneOf(["auto", "manual"]),
    names: d.array(artistNameDecoder, { max: ARTIST_DETAIL_NAMES }),
    names_pagination: paginationDecoder(ARTIST_DETAIL_NAMES),
    role_counts: d.array(d.object({ role: d.oneOf(artistRoles), total: count(), present: count() }), { max: artistRoles.length }),
    redirect_artist_id: d.nullable(d.uuid()),
  }),
  (detail) => {
    if (detail.names.length > detail.names_pagination.page_size) return "names 多于 page_size";
    if (detail.names.length > detail.names_pagination.total) return "names 多于 total";
    // 每个角色的 present 只算存在的曲目，是 total（含文件缺失）的一部分。
    if (detail.role_counts.some((count) => count.present > count.total)) return "role_counts 的 present 多于 total";
    return null;
  },
);

const artistNamesPageDecoder = checked(
  d.object({
    artist_id: d.uuid(),
    items: d.array(artistNameDecoder, { max: MAX_ARTIST_PAGE_SIZE }),
    pagination: paginationDecoder(MAX_ARTIST_PAGE_SIZE),
  }),
  pageProblem,
);

// 作为专辑艺人的专辑：组摘要（代表版本照常）加这位艺人实际署名的那个版本，点进去打开它。
const artistAlbumsPageDecoder = checked(
  d.object({
    artist_id: d.uuid(),
    items: d.array(d.object({ group: releaseGroupSummaryDecoder, credited_release_id: d.uuid() }), { max: MAX_ARTIST_PAGE_SIZE }),
    pagination: paginationDecoder(MAX_ARTIST_PAGE_SIZE),
  }),
  pageProblem,
);

// 以任何身份参与的专辑（participation=any，管理员按专辑拆开时选择用）：每项多一个 roles，是这位艺人在这张专辑里的身份，非空。
const artistParticipationAlbumsPageDecoder = checked(
  d.object({
    artist_id: d.uuid(),
    items: d.array(
      d.object({ group: releaseGroupSummaryDecoder, credited_release_id: d.uuid(), roles: d.array(d.oneOf(artistRoles), { min: 1, max: artistRoles.length }) }),
      { max: MAX_ARTIST_PAGE_SIZE },
    ),
    pagination: paginationDecoder(MAX_ARTIST_PAGE_SIZE),
  }),
  pageProblem,
);

// 按一个角色参与的曲目：含文件缺失的（照常显示，不能播放）。
const artistTracksPageDecoder = checked(
  d.object({
    artist_id: d.uuid(),
    items: d.array(trackSummaryDecoder, { max: MAX_ARTIST_PAGE_SIZE }),
    pagination: paginationDecoder(MAX_ARTIST_PAGE_SIZE),
  }),
  pageProblem,
);

// 播放全部 / 随机播放的候选：所有角色参与的曲目去重、去掉文件缺失的，按专辑顺序或按 seed 在全部曲目里打乱，再取前 10,000 首。
// total 是存在的候选总数，missing_total 是去掉的缺失曲目数，truncated 表示超过了 10,000 首。
const artistQueueDecoder = checked(
  d.object({
    artist_id: d.uuid(),
    order: d.oneOf(["album", "random"]),
    seed: d.nullable(d.uuid()),
    items: d.array(trackSummaryDecoder, { max: ARTIST_QUEUE_LIMIT }),
    total: count(),
    returned: count(),
    limit: d.literal(ARTIST_QUEUE_LIMIT),
    truncated: d.boolean(),
    missing_total: count(),
  }),
  (queue) => {
    if (queue.returned !== queue.items.length) return "returned 与 items 的数量不一致";
    // 候选是全体存在曲目（打乱或排好后）的前 10,000 首：返回的条数恰好是 min(total, 10,000)，超过上限时 truncated。
    if (queue.returned !== Math.min(queue.total, ARTIST_QUEUE_LIMIT)) return "returned 应为 min(total, 10000)";
    if (queue.truncated !== queue.total > ARTIST_QUEUE_LIMIT) return "truncated 与计数不一致";
    if ((queue.order === "random") !== (queue.seed !== null)) return "seed 与 order 不一致";
    if (queue.items.some((item) => !item.present)) return "候选里有文件缺失的曲目";
    return null;
  },
);

export const decodeArtistPage = d.defineDecoder(artistPageDecoder);
export const decodeArtistDetail = d.defineDecoder(artistDetailDecoder);
export const decodeArtistNamesPage = d.defineDecoder(artistNamesPageDecoder);
export const decodeArtistAlbumsPage = d.defineDecoder(artistAlbumsPageDecoder);
export const decodeArtistParticipationAlbumsPage = d.defineDecoder(artistParticipationAlbumsPageDecoder);
export const decodeArtistTracksPage = d.defineDecoder(artistTracksPageDecoder);
export const decodeArtistQueue = d.defineDecoder(artistQueueDecoder);

export type ArtistPageDTO = d.Infer<typeof artistPageDecoder>;
export type ArtistNameDTO = d.Infer<typeof artistNameDecoder>;
export type ArtistDetailDTO = d.Infer<typeof artistDetailDecoder>;
export type ArtistNamesPageDTO = d.Infer<typeof artistNamesPageDecoder>;
export type ArtistAlbumsPageDTO = d.Infer<typeof artistAlbumsPageDecoder>;
export type ArtistAlbumDTO = ArtistAlbumsPageDTO["items"][number];
export type ArtistParticipationAlbumsPageDTO = d.Infer<typeof artistParticipationAlbumsPageDecoder>;
export type ArtistParticipationAlbumDTO = ArtistParticipationAlbumsPageDTO["items"][number];
export type ArtistTracksPageDTO = d.Infer<typeof artistTracksPageDecoder>;
export type ArtistQueueDTO = d.Infer<typeof artistQueueDecoder>;
