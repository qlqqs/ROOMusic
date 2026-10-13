import {
  decodeReleaseDetail,
  decodeReleaseEvidence,
  decodeReleaseGroupPage,
  type ReleaseDetailDTO,
  type ReleaseEvidenceDTO,
  type ReleaseGroupDetailDTO,
  type ReleaseGroupSummaryDTO,
  type ReleaseMediumDTO,
  type ReleaseSummaryDTO,
  type ReleaseTrackDTO,
} from "../features/catalog/api/decoders";
import { sampleBody } from "./api-samples";

// 专辑数据与接口行为（列表、组详情、版本详情、完整证据），Vitest 的替身（fake-catalog.ts）与 Playwright 的受控后端
// （e2e/support/api-mock.ts）共用。这个文件只依赖解码器与样本读取，不依赖 vitest 或应用代码，所以 Node 里的 Playwright 也能导入。

// 两个形状取自后端真实样本：一个没有封面，一个有封面；组与代表版本都经真实的 decoder。
const withoutArtwork = decodeReleaseGroupPage(sampleBody("release-groups/list-one")).items[0];
const withArtwork = decodeReleaseGroupPage(sampleBody("release-groups/list-one-with-artwork")).items[0];

// UUID 最后一段是 12 位：1 位前缀加 11 位序号。
const uuidOf = (prefix: string, index: number) => `00000000-0000-4000-8000-${prefix}${String(index).padStart(11, "0")}`;

export type GroupOverrides = {
  title?: string;
  artist?: string;
  album_artist?: string | null;
  year?: number | null;
  medium_count?: number;
  track_count?: number;
  release_count?: number;
  attention_count?: number;
  /** 默认没有封面；true 时给出一个封面（资源 ID 为 cover-<序号>.png）。 */
  artwork?: boolean;
};

/** 按序号造一个专辑组：ID 固定、标题 “Album 序号”，需要时覆盖个别字段。 */
export function albumGroup(index: number, overrides: GroupOverrides = {}): ReleaseGroupSummaryDTO {
  const base = overrides.artwork === true ? withArtwork : withoutArtwork;
  if (base === undefined) throw new Error("样本里没有组");
  const label = String(index).padStart(3, "0");
  const attention = overrides.attention_count ?? 0;
  return {
    id: uuidOf("a", index),
    release_count: overrides.release_count ?? 1,
    attention_count: attention,
    representative: {
      ...base.representative,
      id: uuidOf("b", index),
      title: overrides.title ?? `Album ${label}`,
      artist: overrides.artist ?? `Artist ${label}`,
      album_artist: overrides.album_artist === undefined ? null : overrides.album_artist,
      year: overrides.year === undefined ? 2000 + (index % 20) : overrides.year,
      medium_count: overrides.medium_count ?? 1,
      track_count: overrides.track_count ?? 10,
      attention_count: attention,
      artwork: overrides.artwork === true ? { resource_id: `cover-${label}.png`, mime: "image/png", width: 600, height: 600 } : null,
    },
  };
}

/** 一批连续序号的专辑组。 */
export function albumGroups(count: number, overrides: (index: number) => GroupOverrides = () => ({})): ReleaseGroupSummaryDTO[] {
  return Array.from({ length: count }, (_, offset) => albumGroup(offset + 1, overrides(offset + 1)));
}

const utf8Length = (value: string) => new TextEncoder().encode(value).length;

/** 接口的一次应答：状态码与响应体（成功时是 DTO，失败时是后端的错误信封）。 */
export type ApiResult = { status: number; body: unknown };

/** 后端错误信封的一次应答。 */
export function failure(status: number, code: string, message: string): ApiResult {
  return { status, body: { error: { code, message }, request_id: "sample-request" } };
}

/**
 * 专辑列表接口（GET /release-groups）的行为，依据后端：page 至少 1、page_size 1 到 100（缺省 50）、搜索词最多 200 字节、
 * attention 只接受 required，否则 400；搜索匹配代表版本的标题、艺人与专辑艺人（不区分大小写）；超出末页返回空列表而总数照常。
 * groups 已按接口应有的顺序排好：这里只做筛选与分页，不重新排序，用来证明页面按接口的顺序显示。
 */
export function respondToGroupList(groups: readonly ReleaseGroupSummaryDTO[], query: URLSearchParams): ApiResult {
  const page = Number(query.get("page") ?? "1");
  const pageSize = Number(query.get("page_size") ?? "50");
  if (!Number.isInteger(page) || page < 1 || !Number.isInteger(pageSize) || pageSize < 1 || pageSize > 100) {
    return failure(400, "invalid_pagination", "分页参数无效");
  }
  const search = query.get("q");
  if (search !== null && (search.trim() === "" || utf8Length(search) > 200)) return failure(400, "invalid_query", "搜索条件无效");
  const attention = query.get("attention");
  if (attention !== null && attention !== "required") return failure(400, "invalid_attention", "检查筛选条件无效");

  const needle = search?.toLowerCase();
  const matches = groups.filter((group) => {
    if (attention !== null && group.attention_count === 0) return false;
    if (needle === undefined) return true;
    const release = group.representative;
    return [release.title, release.artist, release.album_artist ?? ""].some((text) => text.toLowerCase().includes(needle));
  });
  const start = (page - 1) * pageSize;
  return { status: 200, body: { items: matches.slice(start, start + pageSize), pagination: { page, page_size: pageSize, total: matches.length } } };
}

// ---- 组详情、版本详情与完整证据 ----

// 形状取自后端真实样本（双碟、三首曲目、有厂牌与署名的版本，以及它的完整证据），再按需要放大到任意张数与曲目数。
const sampleRelease = decodeReleaseDetail(sampleBody("releases/get"));
const sampleEvidence = decodeReleaseEvidence(sampleBody("releases/evidence"));
const sampleTrack: ReleaseTrackDTO = (() => {
  const track = sampleRelease.media[0]?.tracks[0];
  if (track === undefined) throw new Error("样本里没有曲目");
  return track;
})();

/** 种子数：用 uuidOf 造出的 ID 最后 11 位就是序号。 */
const seqOf = (id: string) => Number(id.slice(-11));

/** 把曲目总数分到各介质：前几张各多一首。 */
function splitTracks(trackCount: number, mediumCount: number): number[] {
  const base = Math.floor(trackCount / mediumCount);
  const extra = trackCount % mediumCount;
  return Array.from({ length: mediumCount }, (_, index) => base + (index < extra ? 1 : 0));
}

/** 曲目的个别字段；参数是介质与曲目在介质内的序号，都从 1 起。 */
export type TrackOverrides = (medium: number, track: number) => Partial<ReleaseTrackDTO>;

/**
 * 按摘要里的介质数与曲目数造出介质与曲目：标题 “Track 介质-序号”（只有一张介质时是 “Track 序号”），时长已知且各不相同，
 * 每首带作曲与指挥署名；ID 由版本的序号决定，所以同一个版本每次造出的内容相同。
 */
export function mediaOf(release: Pick<ReleaseSummaryDTO, "id" | "artist" | "medium_count" | "track_count">, trackOverrides: TrackOverrides = () => ({})): ReleaseMediumDTO[] {
  const seq = seqOf(release.id);
  let ordinal = 0;
  return splitTracks(release.track_count, release.medium_count).map((count, mediumIndex) => {
    const medium = mediumIndex + 1;
    return {
      id: uuidOf("e", seq * 1000 + medium),
      position: medium,
      title: "Medium",
      tracks: Array.from({ length: count }, (_, trackIndex) => {
        const track = trackIndex + 1;
        const label = String(track).padStart(2, "0");
        ordinal += 1;
        return {
          ...sampleTrack,
          id: uuidOf("d", seq * 10_000 + ordinal),
          title: release.medium_count > 1 ? `Track ${medium}-${label}` : `Track ${label}`,
          artist: release.artist,
          position: track,
          source: `d${medium}-${label}.flac`,
          duration_seconds: 100 + track * 10 + medium,
          credits: [
            { role: "composer", name: "Sample Composer" },
            { role: "conductor", name: "Sample Conductor" },
          ],
          ...trackOverrides(medium, track),
        };
      }),
    };
  });
}

/** 版本详情里摘要之外的字段，缺省取样本里完整的取值（有厂牌、编号、条码、版本说明与流派）。 */
export type ReleaseDetailOverrides = Partial<Omit<ReleaseDetailDTO, keyof ReleaseSummaryDTO>> & Partial<Pick<ReleaseDetailDTO, "source_type" | "media_type">>;

/** 从摘要合成版本详情：介质与曲目按摘要的数量生成，署名只有专辑艺人，证据摘要取样本。 */
export function releaseDetailOf(summary: ReleaseSummaryDTO, overrides: ReleaseDetailOverrides = {}): ReleaseDetailDTO {
  return {
    ...summary,
    candidate_kind: sampleRelease.candidate_kind,
    genre: sampleRelease.genre,
    catalog_number: sampleRelease.catalog_number,
    edition: sampleRelease.edition,
    label: sampleRelease.label,
    barcode: sampleRelease.barcode,
    media: mediaOf(summary),
    credits: [{ role: "album_artist", name: summary.album_artist ?? summary.artist }],
    evidence: sampleRelease.evidence,
    revision: 1,
    corrected_fields: [],
    ...overrides,
  };
}

/** 版本的完整证据：缺省取样本（九个字段加归组证据），release_id 换成这个版本。 */
export function releaseEvidenceOf(release: Pick<ReleaseDetailDTO, "id">, overrides: Partial<ReleaseEvidenceDTO> = {}): ReleaseEvidenceDTO {
  return { ...sampleEvidence, release_id: release.id, ...overrides };
}

/** 组详情：组摘要加成员版本（缺省只有代表版本）。release_count 沿用组摘要，需要时由测试改写。 */
export function groupDetailOf(group: ReleaseGroupSummaryDTO, releases: ReleaseSummaryDTO[] = [group.representative]): ReleaseGroupDetailDTO {
  return { ...group, releases };
}

/**
 * 为一个组造 count 个版本：第一个就是组的代表版本，其余各有自己的 ID、年份（依次晚一年）与曲目数（依次多一首），
 * overrides 只改后面这些版本（参数是版本序号，从 2 起）。
 */
export function releaseVersions(group: ReleaseGroupSummaryDTO, count: number, overrides: (version: number) => Partial<ReleaseSummaryDTO> = () => ({})): ReleaseSummaryDTO[] {
  const seq = seqOf(group.id);
  const representative = group.representative;
  return Array.from({ length: count }, (_, index) =>
    index === 0
      ? representative
      : {
          ...representative,
          id: uuidOf("c", seq * 1000 + index),
          year: representative.year === null ? null : representative.year + index,
          track_count: representative.track_count + index,
          ...overrides(index + 1),
        },
  );
}

/**
 * 接口背后的数据。没有专门登记的组、版本与证据，按列表里的摘要合成（单版本组、按摘要数量生成的曲目、样本里的证据），
 * 所以列表里的任何一张卡片点进去都有完整的详情；要特别安排的（多版本、署名、空值、被重扫掉的版本）登记进对应的 Map。
 */
export type CatalogData = {
  /** 列表里的全部组，已按接口应有的顺序排好。 */
  groups: ReleaseGroupSummaryDTO[];
  groupDetails: Map<string, ReleaseGroupDetailDTO>;
  releases: Map<string, ReleaseDetailDTO>;
  evidence: Map<string, ReleaseEvidenceDTO>;
  /** 这些版本的详情与证据返回 404，而组详情仍把它们列为成员：模拟两次请求之间被重新扫描后不可见。 */
  hiddenReleases: Set<string>;
};

export function catalogDataOf(groups: ReleaseGroupSummaryDTO[] = []): CatalogData {
  return { groups, groupDetails: new Map(), releases: new Map(), evidence: new Map(), hiddenReleases: new Set() };
}

/** 组详情：登记过的取登记的，否则按列表里的摘要合成。 */
export function groupDetailFor(data: CatalogData, id: string): ReleaseGroupDetailDTO | undefined {
  const registered = data.groupDetails.get(id);
  if (registered !== undefined) return registered;
  const group = data.groups.find((candidate) => candidate.id === id);
  return group === undefined ? undefined : groupDetailOf(group);
}

/** 版本摘要：在任一组详情的成员里找，找不到再看列表里的代表版本。 */
export function releaseSummaryFor(data: CatalogData, id: string): ReleaseSummaryDTO | undefined {
  for (const detail of data.groupDetails.values()) {
    const member = detail.releases.find((release) => release.id === id);
    if (member !== undefined) return member;
  }
  return data.groups.find((group) => group.representative.id === id)?.representative;
}

/** 版本详情：被隐藏的没有；登记过的取登记的，否则按摘要合成。 */
export function releaseDetailFor(data: CatalogData, id: string): ReleaseDetailDTO | undefined {
  if (data.hiddenReleases.has(id)) return undefined;
  const registered = data.releases.get(id);
  if (registered !== undefined) return registered;
  const summary = releaseSummaryFor(data, id);
  return summary === undefined ? undefined : releaseDetailOf(summary);
}

/** GET /release-groups/{id}：不存在返回 404。 */
export function respondToGroupDetail(data: CatalogData, id: string): ApiResult {
  const group = groupDetailFor(data, id);
  return group === undefined ? failure(404, "not_found", "发行组不存在") : { status: 200, body: group };
}

/** GET /releases/{id}：不存在或已被隐藏返回 404。 */
export function respondToReleaseDetail(data: CatalogData, id: string): ApiResult {
  const release = releaseDetailFor(data, id);
  return release === undefined ? failure(404, "not_found", "发行版本不存在") : { status: 200, body: release };
}

/** GET /releases/{id}/evidence：版本不存在或已被隐藏返回 404；调用方负责管理员鉴权（403）。 */
export function respondToReleaseEvidence(data: CatalogData, id: string): ApiResult {
  const release = releaseDetailFor(data, id);
  if (release === undefined) return failure(404, "not_found", "发行版本不存在");
  return { status: 200, body: data.evidence.get(id) ?? releaseEvidenceOf(release) };
}
