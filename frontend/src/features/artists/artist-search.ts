import { isUuid } from "../../shared/lib/ids";
import { parsePageParam } from "../../shared/lib/pagination";
import { artistInitialGroups, artistRoles, type ArtistInitialGroup, type ArtistRole } from "../catalog/api/artist-summary";
import { parseSearchText } from "../catalog";
import { artistTrackRoles, type ArtistTrackRole } from "./api/decoders";

// 艺人列表与艺人页的 URL 状态（纯函数）。每个字段写成 `T | undefined`：路由把校验结果合并到原始查询串上，不合法的值必须显式返回 undefined。

/** 艺人列表：搜索词、首字母、角色、排序（按名字是默认，不写）与页码（2 及以上才写）。 */
export type ArtistsSearch = {
  q?: string | undefined;
  initial?: ArtistInitialGroup | undefined;
  role?: ArtistRole | undefined;
  sort?: "album_count" | undefined;
  page?: number | undefined;
};

const oneOf = <T extends string>(values: readonly T[], value: unknown): T | undefined => values.find((candidate) => candidate === value);

/** 首字母只在按名字排序时有意义：按专辑数排序时丢掉它（列表不分组）。 */
export function parseArtistsSearch(search: Record<string, unknown>): ArtistsSearch {
  const sort = search["sort"] === "album_count" ? "album_count" : undefined;
  return {
    q: parseSearchText(search["q"]),
    initial: sort === undefined ? oneOf(artistInitialGroups, search["initial"]) : undefined,
    // release_artist 仍是合法取值（只匹配它本身）；界面的筛选只提供“专辑艺人”（album_artist，后端匹配两者任一，I6）。
    role: oneOf(artistRoles, search["role"]),
    sort,
    page: parsePageParam(search["page"]),
  };
}

/** 艺人页：专辑与曲目各自的页码，曲目栏选中的角色，名字的页码。 */
export type ArtistDetailSearch = {
  albums_page?: number | undefined;
  track_role?: ArtistTrackRole | undefined;
  tracks_page?: number | undefined;
  names_page?: number | undefined;
};

export function parseArtistDetailSearch(search: Record<string, unknown>): ArtistDetailSearch {
  return {
    albums_page: parsePageParam(search["albums_page"]),
    track_role: oneOf(artistTrackRoles, search["track_role"]),
    tracks_page: parsePageParam(search["tracks_page"]),
    names_page: parsePageParam(search["names_page"]),
  };
}

/** 路径里的艺人 ID：只认 UUID，统一成小写。 */
export function artistIdOf(value: string): string | null {
  return isUuid(value) ? value.toLowerCase() : null;
}
