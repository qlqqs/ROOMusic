import { decodeReleaseGroupPage, type ReleaseGroupSummaryDTO } from "../features/catalog/api/decoders";
import { sampleBody } from "./api-samples";

// 专辑数据与列表接口的行为，Vitest 的替身（fake-catalog.ts）与 Playwright 的受控后端（e2e/support/api-mock.ts）共用。
// 这个文件只依赖解码器与样本读取，不依赖 vitest 或应用代码，所以 Node 里的 Playwright 也能导入。

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

function failure(status: number, code: string, message: string): GroupListResult {
  return { status, body: { error: { code, message }, request_id: "sample-request" } };
}

export type GroupListResult = { status: number; body: unknown };

/**
 * 专辑列表接口（GET /release-groups）的行为，依据后端：page 至少 1、page_size 1 到 100（缺省 50）、搜索词最多 200 字节、
 * attention 只接受 required，否则 400；搜索匹配代表版本的标题、艺人与专辑艺人（不区分大小写）；超出末页返回空列表而总数照常。
 * groups 已按接口应有的顺序排好：这里只做筛选与分页，不重新排序，用来证明页面按接口的顺序显示。
 */
export function respondToGroupList(groups: readonly ReleaseGroupSummaryDTO[], query: URLSearchParams): GroupListResult {
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
