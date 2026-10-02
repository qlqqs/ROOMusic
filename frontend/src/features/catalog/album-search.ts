import { parsePageParam } from "../../shared/lib/pagination";
import { utf8ByteLength } from "../auth";

/** 搜索词的上限与后端一致：200 个 UTF-8 字节（超过时后端返回 invalid_query）。 */
export const MAX_SEARCH_BYTES = 200;

/**
 * 专辑列表的 URL 状态：搜索词、只看需要关注、页码。第 1 页是默认值，不写进 URL。
 * 每个字段写成 `T | undefined`：路由把校验结果合并到原始查询串上，不合法的值必须显式返回 undefined。
 */
export type AlbumSearch = {
  q?: string | undefined;
  attention?: "required" | undefined;
  page?: number | undefined;
};

/**
 * 搜索词：去掉首尾空白后非空且不超过 200 字节才保留，否则丢弃（不把任意长的值发给后端）。
 * 路由会把形如数字的查询值解析成数字（?q=1984），所以数字按原文本处理；应用自己写出的地址里字符串带引号，不受影响。
 */
export function parseSearchText(value: unknown): string | undefined {
  const text = typeof value === "string" ? value : typeof value === "number" && Number.isFinite(value) ? String(value) : undefined;
  if (text === undefined) return undefined;
  const trimmed = text.trim();
  return trimmed === "" || utf8ByteLength(trimmed) > MAX_SEARCH_BYTES ? undefined : trimmed;
}

/** 路由的 validateSearch：只有 attention=required 保留；页码只保留 2 及以上的整数。 */
export function parseAlbumSearch(search: Record<string, unknown>): AlbumSearch {
  return {
    q: parseSearchText(search["q"]),
    attention: search["attention"] === "required" ? "required" : undefined,
    page: parsePageParam(search["page"]),
  };
}

/** 是否带了搜索词或筛选：决定“没有结果”说的是资料库为空，还是没有符合条件的专辑。 */
export function isFiltered(search: AlbumSearch): boolean {
  return search.q !== undefined || search.attention !== undefined;
}
