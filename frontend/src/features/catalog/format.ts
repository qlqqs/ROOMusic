import { formatCount } from "../../shared/lib/number";
import type { ReleaseCreditDTO, ReleaseDetailDTO, ReleaseMediumDTO, ReleaseSummaryDTO } from "./api/decoders";
import { creditRoleLabel } from "./labels";

/**
 * 卡片上的一行摘要：年份、介质数（只在多于一张时写）与曲目数，用间隔号连接。年份未知就省略，不写占位的“未知”。
 * 接口只统计当前存在的曲目，所以这里的数字就是接口给的数字，不做推算。
 */
export function albumMeta(release: Pick<ReleaseSummaryDTO, "year" | "medium_count" | "track_count">): string {
  const parts: string[] = [];
  if (release.year !== null) parts.push(String(release.year));
  if (release.medium_count > 1) parts.push(`${formatCount(release.medium_count)} 张介质`);
  parts.push(`${formatCount(release.track_count)} 首曲目`);
  return parts.join(" · ");
}

/** 专辑艺人；没有专辑艺人时退回到艺人（K2）。 */
export function albumArtistOf(release: Pick<ReleaseSummaryDTO, "album_artist" | "artist">): string {
  return release.album_artist ?? release.artist;
}

/** 时长未知时的占位：不写成 0:00。 */
export const UNKNOWN_DURATION = "—";

/** 时长：不足一小时写 m:ss，否则写 h:mm:ss（小时不补零，也不设上限）；未知（null）写占位。 */
export function formatDuration(seconds: number | null): string {
  if (seconds === null) return UNKNOWN_DURATION;
  const total = Math.round(seconds);
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const rest = String(total % 60).padStart(2, "0");
  return hours > 0 ? `${hours}:${String(minutes).padStart(2, "0")}:${rest}` : `${minutes}:${rest}`;
}

/** 整张发行版本的总时长：只要有一首曲目的时长未知，或者没有曲目，就是未知（null），不拿已知部分冒充总数。 */
export function totalDuration(media: readonly ReleaseMediumDTO[]): number | null {
  let total = 0;
  let tracks = 0;
  for (const medium of media) {
    for (const track of medium.tracks) {
      if (track.duration_seconds === null) return null;
      total += track.duration_seconds;
      tracks += 1;
    }
  }
  return tracks === 0 ? null : total;
}

// 后端的介质标题列默认值（迁移 0002）：扫描不写介质标题，所以它几乎总是这个值，不是任何人起的名字。
const DEFAULT_MEDIUM_TITLE = "Medium";

/** 介质的小标题：“第 N 张”（碟号取 position，缺失时用顺序），介质有自己的标题时附在后面。 */
export function mediumHeading(medium: Pick<ReleaseMediumDTO, "position" | "title">, index: number): string {
  const number = medium.position >= 1 ? medium.position : index + 1;
  const title = medium.title.trim();
  return title === "" || title === DEFAULT_MEDIUM_TITLE ? `第 ${number} 张` : `第 ${number} 张 · ${title}`;
}

export type CreditGroup = { role: string; names: string[] };

/** 按角色归并署名，角色与姓名都保持接口给出的顺序。 */
export function groupCredits(credits: readonly ReleaseCreditDTO[]): CreditGroup[] {
  const groups = new Map<string, string[]>();
  for (const credit of credits) {
    const names = groups.get(credit.role);
    if (names === undefined) groups.set(credit.role, [credit.name]);
    else names.push(credit.name);
  }
  return Array.from(groups, ([role, names]) => ({ role, names }));
}

/** 曲目行里的署名：“作曲：甲、乙；指挥：丙”。没有署名时是空字符串。 */
export function creditsText(credits: readonly ReleaseCreditDTO[]): string {
  return groupCredits(credits)
    .map((group) => `${creditRoleLabel(group.role)}：${group.names.join("、")}`)
    .join("；");
}

export type ReleaseFact = { label: string; value: string };

/**
 * 侧栏“此版本”的条目：厂牌、编号、条码、版本说明、流派、来源类型与介质类型。没有值的条目不列出，
 * 不写“未知”：很多资料库的文件根本没有这些标签，满屏的“未知”只是噪声。
 */
export function releaseFacts(release: Pick<ReleaseDetailDTO, "label" | "catalog_number" | "barcode" | "edition" | "genre" | "source_type" | "media_type">): ReleaseFact[] {
  const candidates: [string, string | null][] = [
    ["厂牌", release.label],
    ["编号", release.catalog_number],
    ["条码", release.barcode],
    ["版本说明", release.edition],
    ["流派", release.genre],
    ["来源类型", release.source_type],
    ["介质类型", release.media_type],
  ];
  return candidates.flatMap(([label, value]) => (value === null || value.trim() === "" ? [] : [{ label, value }]));
}
