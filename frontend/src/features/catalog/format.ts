import { formatCount } from "../../shared/lib/number";
import type { ReleaseSummaryDTO } from "./api/decoders";

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
