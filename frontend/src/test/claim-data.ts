import type { ReleaseGroupSummaryDTO } from "../features/catalog/api/decoders";
import { decodeClaimDetail, type ClaimDetailDTO, type ClaimSuggestionDTO } from "../features/governance/api/decoders";

// 待认领的旧版本的测试数据，Vitest 与 Playwright 的受控后端共用。构造出来的详情先经过真实的解码器，所以和后端响应一样合法。
// 旧版本的 ID 用 c 前缀的序号，同专辑的别的发行用 d，避开专辑数据里的 a（组）与 b（代表版本）。

const uuidOf = (prefix: string, index: number) => `00000000-0000-4000-8000-${prefix}${String(index).padStart(11, "0")}`;

/** 旧版本的 ID：第 index 个待认领的旧版本。 */
export const claimReleaseId = (index: number): string => uuidOf("c", index);

export type ClaimOverrides = {
  title?: string;
  artist?: string;
  edition?: string | null;
  year?: number | null;
  directory?: string;
  /** 最后一次被看到的时间（RFC 3339）；默认按序号递减，序号小的更新。 */
  lastSeenAt?: string;
  revision?: number;
  corrections?: ClaimDetailDTO["corrections"];
  /** 原来所在的专辑（和别的版本同在一张专辑里）；null 是没有。 */
  album?: { id: string; title: string; siblings: ClaimDetailDTO["siblings"] } | null;
  /** 是哪些专辑的默认版本。 */
  defaultOf?: ClaimDetailDTO["default_of"];
  reviews?: ClaimDetailDTO["reviews"];
  suggestions?: ClaimDetailDTO["suggestions"];
};

/** 把一个专辑组的代表版本包成推荐：reasons 与命中的文件数由调用方给。 */
export function suggestionOf(group: ReleaseGroupSummaryDTO, reasons: readonly string[], sameFiles: number, edition: string | null = null): ClaimSuggestionDTO {
  return { release: group.representative, group_id: group.id, edition, reasons: [...reasons], same_files: sameFiles };
}

/** 一个待认领的旧版本的详情（GET /release-claims/{id} 的响应体），列表项从它截取。默认带两个字段修正。 */
export function claimDetail(index: number, overrides: ClaimOverrides = {}): ClaimDetailDTO {
  const corrections = overrides.corrections ?? [
    { field: "title", correction: { mode: "set", value: `Old Edition ${index} (corrected)` }, scan_value: `Old Edition ${index}` },
    { field: "genre", correction: { mode: "set", value: "Baroque" }, scan_value: null },
  ];
  const album = overrides.album === undefined ? null : overrides.album;
  const reviews = overrides.reviews ?? [];
  const defaultOf = overrides.defaultOf ?? [];
  const title = overrides.title ?? `Old Edition ${index} (corrected)`;
  const detail = {
    release: { id: claimReleaseId(index), title, artist: overrides.artist ?? `Old Artist ${index}`, year: overrides.year === undefined ? 1999 : overrides.year, edition: overrides.edition === undefined ? null : overrides.edition },
    album: album === null ? null : { id: album.id, title: album.title, other_release_count: album.siblings.length },
    location: { root_name: "classical", directory: overrides.directory ?? `Old/Edition ${index}` },
    last_seen_at: overrides.lastSeenAt ?? new Date(Date.UTC(2026, 0, 31 - index, 8, 0, 0)).toISOString().replace(".000Z", "Z"),
    holds: {
      corrections: corrections.map((item) => item.field),
      album: album !== null,
      default: defaultOf.length > 0,
      reviews: reviews.map((item) => item.issue),
    },
    revision: overrides.revision ?? 2,
    corrections,
    siblings: album === null ? [] : album.siblings,
    default_of: defaultOf,
    reviews,
    suggestions: overrides.suggestions ?? [],
  };
  // 往返一次：构造器给出的数据必须是解码器接受的，写错形状的测试数据在这里就失败，而不是在页面里表现成莫名的空白。
  return decodeClaimDetail(detail);
}
