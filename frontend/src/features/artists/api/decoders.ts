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

// 艺人图片与简介：自动整理从外部来源补上（M8），或由管理员提供（manual）。外部来源带原页面地址；图片另有作者与许可（Commons 必有许可），
// 维基百科的简介是 CC BY-SA 4.0；管理员提供的没有地址、作者与许可。插件写入的来源是 plugin:<id>。
// 网络搜索（web_search）是模型联网整理的，citations 是它依据的网页（标题是外部的不可信文本），其余来源为空数组。
// 作者、许可是外部的不可信文本，只按纯文本显示；地址只在是 http(s) 时做成链接。
export const ARTIST_IMAGE_SOURCES = ["fanarttv", "theaudiodb", "commons", "manual", "web_search"] as const;
export const ARTIST_BIO_SOURCES = ["wikipedia", "theaudiodb", "manual", "web_search"] as const;
export type PluginSource = `plugin:${string}`;
const PLUGIN_SOURCE = /^plugin:[a-z0-9-]{3,40}$/;

/** 固定来源之一，或插件来源 plugin:<id>。 */
function sourceOf<const V extends readonly string[]>(values: V): d.Decoder<V[number] | PluginSource> {
  const fixed = d.oneOf(values);
  return (input, path) => {
    if (typeof input === "string" && PLUGIN_SOURCE.test(input)) return input as PluginSource;
    return fixed(input, path);
  };
}

const externalText = (max: number) => d.nullable(d.string({ min: 1, max }));
const citationDecoder = d.object({ url: d.string({ min: 1, max: 2048 }), title: d.string({ max: 500 }) });
const citationsDecoder = d.array(citationDecoder, { max: 5 });
export type ArtistCitationDTO = d.Infer<typeof citationDecoder>;
export const artistImageDecoder = d.object({
  resource: d.string({ min: 1, max: 255 }),
  source: sourceOf(ARTIST_IMAGE_SOURCES),
  source_url: externalText(2048),
  author: externalText(500),
  license: externalText(200),
  license_url: externalText(2048),
  citations: citationsDecoder,
});
// 简介最多 4000 个字符，按码点计（与后端按 rune 计一致），扩展区汉字算一个字。
export const MAX_BIO_CHARS = 4000;
const bioTextDecoder = checked(d.string({ min: 1, max: MAX_BIO_CHARS * 2 }), (text) => (Array.from(text).length > MAX_BIO_CHARS ? `超过 ${MAX_BIO_CHARS} 个字符` : null));
export const artistBioDecoder = d.object({
  text: bioTextDecoder,
  lang: d.oneOf(["zh", "en"] as const),
  source: sourceOf(ARTIST_BIO_SOURCES),
  source_url: externalText(2048),
  license: externalText(200),
  license_url: externalText(2048),
  citations: citationsDecoder,
});
export type ArtistImageDTO = d.Infer<typeof artistImageDecoder>;
export type ArtistBioDTO = d.Infer<typeof artistBioDecoder>;

// 艺人当前对应的外部条目（自动整理按发行写入，或管理员指定）：MusicBrainz 是 MBID，Wikidata 是 Q 号。
// judged_by_model：当前生效的对应是模型判断的同一人（ADR 0009 第 6.4 节，可撤销），operation_id 是写入它的操作，没有时为 null。
const artistLinksDecoder = d.object({
  musicbrainz: d.nullable(d.uuid()),
  wikidata: d.nullable(d.string({ pattern: /^Q[1-9][0-9]{0,11}$/ })),
  judged_by_model: d.boolean(),
  operation_id: d.nullable(d.uuid()),
});
export type ArtistLinksDTO = d.Infer<typeof artistLinksDecoder>;

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
    image: d.nullable(artistImageDecoder),
    bio: d.nullable(artistBioDecoder),
    links: artistLinksDecoder,
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

// 管理员对艺人资料的写入（上传图片、写简介、标记不对、指定对应）：成功时给出操作 ID、写入的有效身份与写入后的人工修改版本号。
const artistMediaWriteDecoder = d.object({
  operation_id: d.uuid(),
  artist_id: d.uuid(),
  revision: d.integer({ min: 1 }),
});
const artistMediaRejectDecoder = d.object({
  operation_id: d.uuid(),
  artist_id: d.uuid(),
  revision: d.integer({ min: 1 }),
  layer: d.oneOf(["manual", "auto"] as const),
});
// 指定对应前的预览：条目的名字与简介短语是外部的不可信文本；name_matches 表示与本地艺人的某个名字一致。
const artistLinkPreviewDecoder = d.object({
  source: d.oneOf(["musicbrainz", "wikidata"] as const),
  external_id: d.string({ min: 1, max: 64 }),
  name: d.string({ min: 1, max: 1000 }),
  description: externalText(1000),
  musicbrainz: d.nullable(d.uuid()),
  wikidata: d.nullable(d.string({ pattern: /^Q[1-9][0-9]{0,11}$/ })),
  name_matches: d.boolean(),
});

// 管理员看到的联网查找状态（ADR 0009 第 7 节，只在点了“联网查找”或“整理艺人”后才查）：排队、查找中、搁置（什么都没找到，本地变了再试）、等你确认。
// 等你确认时 candidates 是模型找到、但没有一张专辑结构对得上的身份候选，每个对应一条例外；名字、简短说明、依据与引用都是外部的不可信文本。
export const ARTIST_WEB_SEARCH_STATES = ["none", "queued", "running", "shelved", "waiting_human"] as const;
const artistWebSearchDecoder = d.object({
  state: d.oneOf(ARTIST_WEB_SEARCH_STATES),
  // 整理用的模型与管家的网页搜索都配好了才是 true，才显示“联网查找”；按钮能不能点看 can_start。
  configured: d.boolean(),
  can_start: d.boolean(),
  can_stop: d.boolean(),
  // 在“整理艺人”的队列里时：第几轮、排第几、共几位；不在队列里时为 null。
  queue: d.nullable(d.object({ generation: d.integer({ min: 0 }), position: d.integer({ min: 0 }), total: d.integer({ min: 0 }) })),
  // 这一位已用的工具调用与 token，以及每位的上限（10 次、5 万 token，到线就不再发）。
  usage: d.object({ tool_calls: d.integer({ min: 0 }), tokens: d.integer({ min: 0 }), tool_call_limit: d.integer({ min: 1 }), token_limit: d.integer({ min: 1 }) }),
  candidates: d.array(
    d.object({
      exception_id: d.uuid(),
      source: d.oneOf(["musicbrainz", "wikidata"] as const),
      external_id: d.string({ min: 1, max: 64 }),
      name: d.string({ min: 1, max: 1000 }),
      description: externalText(1000),
      note: d.string({ max: 2000 }),
      citations: citationsDecoder,
    }),
    { max: 20 },
  ),
});

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
export const decodeArtistMediaWrite = d.defineDecoder(artistMediaWriteDecoder);
export const decodeArtistMediaReject = d.defineDecoder(artistMediaRejectDecoder);
export const decodeArtistLinkPreview = d.defineDecoder(artistLinkPreviewDecoder);
export type ArtistMediaWriteDTO = d.Infer<typeof artistMediaWriteDecoder>;
export type ArtistMediaRejectDTO = d.Infer<typeof artistMediaRejectDecoder>;
export type ArtistLinkPreviewDTO = d.Infer<typeof artistLinkPreviewDecoder>;
export const decodeArtistWebSearch = d.defineDecoder(artistWebSearchDecoder);
export type ArtistWebSearchDTO = d.Infer<typeof artistWebSearchDecoder>;
export type ArtistCandidateDTO = ArtistWebSearchDTO["candidates"][number];
