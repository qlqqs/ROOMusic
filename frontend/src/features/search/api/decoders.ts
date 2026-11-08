import { decode as d } from "../../../shared/api";
import { utf8ByteLength } from "../../auth/account-rules";
import { artistSummaryDecoder } from "../../catalog/api/artist-summary";
import { catalogStateDecoder, releaseGroupSummaryDecoder } from "../../catalog/api/decoders";
import { trackSummaryDecoder, type TrackSummaryDTO } from "../../catalog/api/track-summary";

// 全局搜索（GET /search/suggestions 与 GET /search）的响应。上限与后端合同一致，超界的响应整体拒绝，而不是截断后显示。
// 合同见任务 11-04-classical-m5-search 的 design.md §9。专辑与曲目两类总在；艺人一类（M5 ⑤）只在艺人数据就绪时出现。

/** 每类建议最多几条（limit 的上限）。 */
export const MAX_SUGGESTION_LIMIT = 8;
/** 结果每页最多几条（page_size 的上限）。 */
export const MAX_RESULT_PAGE_SIZE = 100;
/** 计数探测的阈值：超过时 total 固定为 1,000，total_relation 为 gte，页面显示“1,000+”。 */
export const SEARCH_COUNT_LIMIT = 1000;
/** 回显的查询词：去掉首尾空白后不超过 200 个 UTF-8 字节（与后端的上限一样按字节，不按字符）。 */
const MAX_QUERY_BYTES = 200;
/** 命中说明里的原文与别名：不超过 4,096 个 UTF-8 字节。 */
const MAX_MATCH_ORIGINAL_BYTES = 4096;

/** 按 UTF-8 字节限制长度的字符串：先按字符数挡掉明显超长的值，再算字节数。 */
function utf8String(maxBytes: number): d.Decoder<string> {
  const text = d.string({ max: maxBytes });
  return (input, path) => {
    const value = text(input, path);
    if (utf8ByteLength(value) > maxBytes) throw new d.DecodeError(path, `不应超过 ${maxBytes} 个 UTF-8 字节`);
    return value;
  };
}

export const searchTypes = ["artists", "albums", "tracks"] as const;
export type SearchType = (typeof searchTypes)[number];

export const matchFields = [
  "release_title",
  "release_artist",
  "album_artist",
  "track_title",
  "track_artist",
  "credit",
  "label",
  "catalog_number",
  "artist_name",
  "artist_alias",
] as const;
export type MatchField = (typeof matchFields)[number];

export const matchRoles = [
  "release_artist",
  "album_artist",
  "track_artist",
  "lyricist",
  "composer",
  "arranger",
  "conductor",
  "performer",
  "producer",
] as const;
export type MatchRole = (typeof matchRoles)[number];

// 一条结果为什么被找到：实际命中的字段与它的原文（不是查询词，也不是规范化后的内部值）。所有字段都必传，允许 null 的不省略。
// ④ 的 artist_id 与 alias 总是 null；⑤ 的别名命中带上有效艺人与匹配到的别名。
const matchDecoder = d.object({
  kind: d.oneOf(["exact", "prefix", "contains"]),
  field: d.oneOf(matchFields),
  role: d.nullable(d.oneOf(matchRoles)),
  original: utf8String(MAX_MATCH_ORIGINAL_BYTES),
  artist_id: d.nullable(d.uuid()),
  alias: d.nullable(utf8String(MAX_MATCH_ORIGINAL_BYTES)),
  release_id: d.nullable(d.uuid()),
  track_id: d.nullable(d.uuid()),
});

// 专辑结果：组摘要（代表版本照旧）加实际命中的版本；点进去打开那个版本。
const albumItemDecoder = d.object({
  group: releaseGroupSummaryDecoder,
  matched_release_id: d.uuid(),
  match: matchDecoder,
});

// 曲目结果：与历史同一个可播放的曲目摘要，加命中说明。全局曲目结果只有当前存在的曲目。
const trackMatchDecoder = d.object({ match: matchDecoder });
const trackItemDecoder: d.Decoder<TrackSummaryDTO & { match: d.Infer<typeof matchDecoder> }> = (input, path) => ({
  ...trackSummaryDecoder(input, path),
  match: trackMatchDecoder(input, path).match,
});

// 艺人结果（M5 ⑤）：艺人摘要加命中说明；按别名找到时 match.field 是 artist_alias，alias 是匹配到的别名。
const artistItemDecoder = d.object({
  artist: artistSummaryDecoder,
  match: matchDecoder,
});

// 搜索专用的分页：total 不超过 1,000；total_relation 为 gte 时 total 是下界（固定为 1,000），不能据此算末页，翻页只看 has_more。
// 计数与页查询来自同一快照，三者不能互相矛盾：
// - eq：has_more 恰好等于“本页之后还有结果”（page × page_size < total）；
// - gte：实际至少 1,001 条，本页结束位置不到 1,001 时一定还有下一页，has_more 不能是 false。
const searchPaginationShape = d.object({
  page: d.integer({ min: 1 }),
  page_size: d.integer({ min: 1, max: MAX_RESULT_PAGE_SIZE }),
  total: d.integer({ min: 0, max: SEARCH_COUNT_LIMIT }),
  total_relation: d.oneOf(["eq", "gte"]),
  has_more: d.boolean(),
});
const searchPaginationDecoder: d.Decoder<d.Infer<typeof searchPaginationShape>> = (input, path) => {
  const pagination = searchPaginationShape(input, path);
  const pageEnd = pagination.page * pagination.page_size;
  if (pagination.total_relation === "gte") {
    if (pagination.total !== SEARCH_COUNT_LIMIT) throw new d.DecodeError(`${path}.total`, `total_relation 为 gte 时 total 应为 ${SEARCH_COUNT_LIMIT}`);
    if (!pagination.has_more && pageEnd <= SEARCH_COUNT_LIMIT) throw new d.DecodeError(`${path}.has_more`, "结果超过 1,000 条时，这一页之后一定还有结果");
  } else if (pagination.has_more !== pageEnd < pagination.total) {
    throw new d.DecodeError(`${path}.has_more`, "与精确总数矛盾");
  }
  return pagination;
};

const suggestionGroup = <T>(item: d.Decoder<T>) =>
  d.object({
    items: d.array(item, { max: MAX_SUGGESTION_LIMIT }),
    has_more: d.boolean(),
  });

// 建议：每类只取前几条，has_more 表示还有更多，不给总数。
const suggestionsShape = d.object({
  query: utf8String(MAX_QUERY_BYTES),
  catalog_state: catalogStateDecoder,
  available_types: d.array(d.oneOf(searchTypes), { max: searchTypes.length }),
  artists: suggestionGroup(artistItemDecoder),
  albums: suggestionGroup(albumItemDecoder),
  tracks: suggestionGroup(trackItemDecoder),
});
// 可搜索的类别不重复，并与同一快照的能力状态一致：专辑与曲目总在；艺人只在艺人数据就绪（artists_state=ready）时出现。
const suggestionsDecoder: d.Decoder<d.Infer<typeof suggestionsShape>> = (input, path) => {
  const suggestions = suggestionsShape(input, path);
  const types = suggestions.available_types;
  const typesPath = `${path}.available_types`;
  if (new Set(types).size !== types.length) throw new d.DecodeError(typesPath, "类别不能重复");
  if (!types.includes("albums") || !types.includes("tracks")) throw new d.DecodeError(typesPath, "应包含专辑与曲目");
  if (types.includes("artists") !== (suggestions.catalog_state.artists_state === "ready")) throw new d.DecodeError(typesPath, "与艺人数据的状态不一致");
  if (!types.includes("artists") && suggestions.artists.items.length > 0) throw new d.DecodeError(`${path}.artists`, "艺人不可搜索时不应有艺人建议");
  return suggestions;
};

const resultsDecoder = <const T extends SearchType, I>(type: T, item: d.Decoder<I>) =>
  d.object({
    query: utf8String(MAX_QUERY_BYTES),
    type: d.literal(type),
    catalog_state: catalogStateDecoder,
    items: d.array(item, { max: MAX_RESULT_PAGE_SIZE }),
    pagination: searchPaginationDecoder,
  });

const artistResultsDecoder = resultsDecoder("artists", artistItemDecoder);
const albumResultsDecoder = resultsDecoder("albums", albumItemDecoder);
const trackResultsDecoder = resultsDecoder("tracks", trackItemDecoder);

export const decodeSearchSuggestions = d.defineDecoder(suggestionsDecoder);
export const decodeArtistResults = d.defineDecoder(artistResultsDecoder);
export const decodeAlbumResults = d.defineDecoder(albumResultsDecoder);
export const decodeTrackResults = d.defineDecoder(trackResultsDecoder);
export const decodeSearchPagination = d.defineDecoder(searchPaginationDecoder);

export type SearchMatchDTO = d.Infer<typeof matchDecoder>;
export type SearchArtistItemDTO = d.Infer<typeof artistItemDecoder>;
export type SearchAlbumItemDTO = d.Infer<typeof albumItemDecoder>;
export type SearchTrackItemDTO = d.Infer<typeof trackItemDecoder>;
export type SearchPaginationDTO = d.Infer<typeof searchPaginationDecoder>;
export type SearchSuggestionsDTO = d.Infer<typeof suggestionsDecoder>;
export type ArtistResultsDTO = d.Infer<typeof artistResultsDecoder>;
export type AlbumResultsDTO = d.Infer<typeof albumResultsDecoder>;
export type TrackResultsDTO = d.Infer<typeof trackResultsDecoder>;
