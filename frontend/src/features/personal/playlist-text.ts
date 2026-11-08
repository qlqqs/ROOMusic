import { formatSpokenDuration } from "../../shared/lib/duration";
import { formatCount } from "../../shared/lib/number";
import { PERSONAL_TRACK_LIMIT } from "./api/decoders";

// 歌单写操作的失败文案（按错误码选，不比较后端 message）与回执的提示。

export const playlistMessages = {
  revision_conflict: "歌单刚在别处改过，已重新读取，请再试一次。",
  playlist_limit_reached: "歌单已经有 1,000 个了，先删掉一些再新建。",
  playlist_track_limit_reached: `歌单最多 ${formatCount(PERSONAL_TRACK_LIMIT)} 首，放不下这些了。`,
  idempotency_conflict: "这次操作和刚才的不一致，请刷新后再试。",
  not_found: "歌单或曲目已经不在了。",
} as const;

/** 加入的结果：一首时说加入了哪个歌单；一批时说“已加入 N 首，M 首已在歌单里”。一首都没加时说已经在里面。 */
export function addedText(name: string, counts: { added_count: number; existing_count: number }, single: boolean): string {
  if (counts.added_count === 0) return single || counts.existing_count === 1 ? `已在《${name}》里了。` : `这些都已在《${name}》里了。`;
  if (single) return `已加入《${name}》。`;
  const existing = counts.existing_count === 0 ? "" : `，${formatCount(counts.existing_count)} 首已在歌单里`;
  return `《${name}》：已加入 ${formatCount(counts.added_count)} 首${existing}。`;
}

/** 超出上限时新增入口的说明（撤销可以让歌单暂时超过上限）。 */
export function overLimitText(overLimitCount: number): string {
  return `歌单比上限多 ${formatCount(overLimitCount)} 首，先移除一些才能再添加。`;
}

type PlaylistLength = { track_count: number; duration_seconds: number; unknown_duration_count: number };

/**
 * 歌单的总时长，如“1 小时 20 分钟”。有时长未知的曲目时，已知的时长只是下限，写成“1 小时 20 分钟以上”；
 * 空歌单、全都不知道时长（或已知的加起来是 0）时是空串。
 */
export function playlistDurationText(playlist: PlaylistLength): string {
  if (playlist.track_count === 0) return "";
  if (playlist.unknown_duration_count === 0) return formatSpokenDuration(playlist.duration_seconds);
  if (playlist.unknown_duration_count >= playlist.track_count || playlist.duration_seconds <= 0) return "";
  return `${formatSpokenDuration(playlist.duration_seconds, { floor: true })}以上`;
}

/** 歌单卡片上的曲目数与总时长，如“12 首 · 1 小时 20 分钟”；没有总时长时只写曲目数。 */
export function playlistMetaText(playlist: PlaylistLength): string {
  const count = `${formatCount(playlist.track_count)} 首`;
  const duration = playlistDurationText(playlist);
  return duration === "" ? count : `${count} · ${duration}`;
}
