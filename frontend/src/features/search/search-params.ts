import { parsePageParam } from "../../shared/lib/pagination";
import { MAX_SEARCH_BYTES, parseSearchText } from "../catalog";
import { utf8ByteLength } from "../auth";

/**
 * 搜索结果页的 URL 状态：查询词与艺人、专辑、曲目三栏各自的页码（第 1 页不写进地址）；艺人栏只在艺人数据就绪时出现。
 * 每个字段写成 `T | undefined`：路由把校验结果合并到原始查询串上，不合法的值必须显式返回 undefined。
 */
export type SearchPageSearch = {
  q?: string | undefined;
  artists_page?: number | undefined;
  albums_page?: number | undefined;
  tracks_page?: number | undefined;
};

/** 路由的 validateSearch：查询词去掉首尾空白后非空且不超过 200 字节才保留，页码只保留 2 及以上的整数。 */
export function parseSearchPageSearch(search: Record<string, unknown>): SearchPageSearch {
  return {
    q: parseSearchText(search["q"]),
    artists_page: parsePageParam(search["artists_page"]),
    albums_page: parsePageParam(search["albums_page"]),
    tracks_page: parsePageParam(search["tracks_page"]),
  };
}

/** 换查询词时各栏都回到第 1 页。 */
export function searchPageFor(q: string): SearchPageSearch {
  return { q, artists_page: undefined, albums_page: undefined, tracks_page: undefined };
}

export type DraftQuery = { kind: "empty" } | { kind: "too_long" } | { kind: "ready"; q: string };

/** 输入框里的草稿能不能拿去搜：去掉首尾空白后为空、超过 200 字节，或可以搜的文字。 */
export function draftQueryOf(draft: string): DraftQuery {
  const q = draft.trim();
  if (q === "") return { kind: "empty" };
  return utf8ByteLength(q) > MAX_SEARCH_BYTES ? { kind: "too_long" } : { kind: "ready", q };
}
