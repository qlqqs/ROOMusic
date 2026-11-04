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

/** 总时长的口语写法：“1 小时 20 分”“45 分”，不足 1 分写秒数。floor 为 true 时向下取整（“以上”只能是下限）。 */
function spokenDuration(seconds: number, floor: boolean): string {
  const total = floor ? Math.floor(seconds) : Math.round(seconds);
  if (total < 60) return `${total} 秒`;
  const minutes = floor ? Math.floor(total / 60) : Math.round(total / 60);
  const hours = Math.floor(minutes / 60);
  const rest = minutes % 60;
  if (hours === 0) return `${rest} 分`;
  return rest === 0 ? `${formatCount(hours)} 小时` : `${formatCount(hours)} 小时 ${rest} 分`;
}

/**
 * 歌单的曲目数与总时长，如“12 首 · 1 小时 20 分”。有时长未知的曲目时，已知的时长只是下限，写成“1 小时 20 分以上”；
 * 全都不知道时长（或已知的加起来是 0）就只写曲目数。
 */
export function playlistMetaText(playlist: { track_count: number; duration_seconds: number; unknown_duration_count: number }): string {
  const count = `${formatCount(playlist.track_count)} 首`;
  if (playlist.track_count === 0) return count;
  if (playlist.unknown_duration_count === 0) return `${count} · ${spokenDuration(playlist.duration_seconds, false)}`;
  if (playlist.unknown_duration_count >= playlist.track_count || playlist.duration_seconds <= 0) return count;
  return `${count} · ${spokenDuration(playlist.duration_seconds, true)}以上`;
}
